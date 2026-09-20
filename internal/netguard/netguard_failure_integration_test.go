package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

type netguardFailureIntegrationResolver struct {
	lookup func(
		context.Context,
		string,
		string,
	) ([]netip.Addr, error)
}

func (resolver netguardFailureIntegrationResolver) LookupNetIP(
	ctx context.Context,
	network string,
	host string,
) ([]netip.Addr, error) {
	return resolver.lookup(ctx, network, host)
}

type netguardFailureIntegrationDialer struct {
	calls []string
	dial  func(
		context.Context,
		string,
		string,
	) (net.Conn, error)
}

func (dialer *netguardFailureIntegrationDialer) DialContext(
	ctx context.Context,
	network string,
	address string,
) (net.Conn, error) {
	dialer.calls = append(
		dialer.calls,
		address,
	)

	return dialer.dial(ctx, network, address)
}

func TestNetguardFailureIntegrationClassifiesSafetyAndFailures(
	t *testing.T,
) {
	categories := []struct {
		name string
		err  error
		want retry.Category
	}{
		{
			name: "deadline",
			err:  context.DeadlineExceeded,
			want: retry.CategoryTimeout,
		},
		{
			name: "resolution failure",
			err:  errResolutionFailed,
			want: retry.CategoryDNS,
		},
		{
			name: "no addresses",
			err:  errNoAddresses,
			want: retry.CategoryDNS,
		},
		{
			name: "unsafe address",
			err:  errUnsafeAddress,
			want: retry.CategoryUnsafeAddress,
		},
		{
			name: "dial failure",
			err:  errDialFailed,
			want: retry.CategoryTransport,
		},
		{
			name: "invalid origin",
			err:  errInvalidOrigin,
			want: retry.CategoryMalformedOrigin,
		},
		{
			name: "unclassified",
			err:  errors.New("integration unknown failure"),
			want: retry.CategoryUnsupportedOrigin,
		},
	}

	for _, test := range categories {
		t.Run(test.name, func(t *testing.T) {
			if got := FailureCategory(test.err); got != test.want {
				t.Errorf(
					"FailureCategory(%v) = %q, want %q",
					test.err,
					got,
					test.want,
				)
			}
		})
	}

	addresses := []struct {
		name string
		addr netip.Addr
		want bool
	}{
		{
			name: "invalid",
			addr: netip.Addr{},
			want: false,
		},
		{
			name: "zoned IPv6",
			addr: netip.MustParseAddr("fe80::1%eth0"),
			want: false,
		},
		{
			name: "private IPv4",
			addr: netip.MustParseAddr("10.0.0.1"),
			want: false,
		},
		{
			name: "mapped private IPv4",
			addr: netip.MustParseAddr("::ffff:127.0.0.1"),
			want: false,
		},
		{
			name: "public IPv4",
			addr: netip.MustParseAddr("8.8.8.8"),
			want: true,
		},
		{
			name: "documentation IPv6",
			addr: netip.MustParseAddr("2001:db8::1"),
			want: false,
		},
		{
			name: "private IPv6",
			addr: netip.MustParseAddr("fc00::1"),
			want: false,
		},
		{
			name: "blocked allocated IPv6",
			addr: netip.MustParseAddr("3fff::1"),
			want: false,
		},
		{
			name: "public IPv6",
			addr: netip.MustParseAddr("2606:4700:4700::1111"),
			want: true,
		},
	}

	for _, test := range addresses {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateAddr(test.addr)

			if test.want && err != nil {
				t.Errorf(
					"ValidateAddr(%v) error = %v, want nil",
					test.addr,
					err,
				)
			}

			if !test.want && !errors.Is(err, errUnsafeAddress) {
				t.Errorf(
					"ValidateAddr(%v) error = %v, want %v",
					test.addr,
					err,
					errUnsafeAddress,
				)
			}
		})
	}
}

func TestNetguardFailureIntegrationResolveBoundaries(t *testing.T) {
	hostOrigin := netguardFailureIntegrationOrigin(
		t,
		"https://example.com:8443",
	)

	if _, err := Resolve(nil, hostOrigin, nil); !errors.Is(
		err,
		errInvalidContext,
	) {
		t.Errorf(
			"Resolve(nil) error = %v, want %v",
			err,
			errInvalidContext,
		)
	}

	canceledContext, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if _, err := Resolve(
		canceledContext,
		hostOrigin,
		nil,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Resolve(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	if _, err := Resolve(
		context.Background(),
		origin.Origin{},
		nil,
	); !errors.Is(err, errInvalidOrigin) {
		t.Errorf(
			"Resolve(zero origin) error = %v, want %v",
			err,
			errInvalidOrigin,
		)
	}

	t.Run("safe IP literal bypasses resolver", func(t *testing.T) {
		candidate := netguardFailureIntegrationOrigin(
			t,
			"https://8.8.8.8:8443",
		)

		destination, err := Resolve(
			context.Background(),
			candidate,
			nil,
		)
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}

		if len(destination.addresses) != 1 ||
			destination.addresses[0] != netip.MustParseAddr("8.8.8.8") ||
			destination.port != 8443 {
			t.Errorf(
				"destination = %#v",
				destination,
			)
		}
	})

	t.Run("unsafe IP literal fails closed", func(t *testing.T) {
		candidate := netguardFailureIntegrationOrigin(
			t,
			"https://127.0.0.1",
		)

		_, err := Resolve(
			context.Background(),
			candidate,
			nil,
		)
		if !errors.Is(err, errUnsafeAddress) {
			t.Errorf(
				"Resolve() error = %v, want %v",
				err,
				errUnsafeAddress,
			)
		}
	})

	if _, err := Resolve(
		context.Background(),
		hostOrigin,
		nil,
	); !errors.Is(err, errResolverUnavailable) {
		t.Errorf(
			"Resolve(nil resolver) error = %v, want %v",
			err,
			errResolverUnavailable,
		)
	}

	t.Run("resolver failure", func(t *testing.T) {
		resolver := netguardFailureIntegrationResolver{
			lookup: func(
				context.Context,
				string,
				string,
			) ([]netip.Addr, error) {
				return nil, errors.New("integration DNS failure")
			},
		}

		_, err := Resolve(
			context.Background(),
			hostOrigin,
			resolver,
		)
		if !errors.Is(err, errResolutionFailed) {
			t.Errorf(
				"Resolve() error = %v, want %v",
				err,
				errResolutionFailed,
			)
		}
	})

	t.Run("parent cancellation wins resolver failure", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)

		resolver := netguardFailureIntegrationResolver{
			lookup: func(
				context.Context,
				string,
				string,
			) ([]netip.Addr, error) {
				cancel()
				return nil, errors.New("integration DNS failure")
			},
		}

		_, err := Resolve(ctx, hostOrigin, resolver)
		if !errors.Is(err, context.Canceled) {
			t.Errorf(
				"Resolve() error = %v, want context.Canceled",
				err,
			)
		}
	})

	t.Run("empty result", func(t *testing.T) {
		resolver := netguardFailureIntegrationResolver{
			lookup: func(
				context.Context,
				string,
				string,
			) ([]netip.Addr, error) {
				return nil, nil
			},
		}

		_, err := Resolve(
			context.Background(),
			hostOrigin,
			resolver,
		)
		if !errors.Is(err, errNoAddresses) {
			t.Errorf(
				"Resolve() error = %v, want %v",
				err,
				errNoAddresses,
			)
		}
	})

	t.Run("one unsafe DNS address rejects destination", func(t *testing.T) {
		resolver := netguardFailureIntegrationResolver{
			lookup: func(
				context.Context,
				string,
				string,
			) ([]netip.Addr, error) {
				return []netip.Addr{
					netip.MustParseAddr("8.8.8.8"),
					netip.MustParseAddr("10.0.0.1"),
				}, nil
			},
		}

		_, err := Resolve(
			context.Background(),
			hostOrigin,
			resolver,
		)
		if !errors.Is(err, errUnsafeAddress) {
			t.Errorf(
				"Resolve() error = %v, want %v",
				err,
				errUnsafeAddress,
			)
		}
	})

	t.Run("deduplicates and sorts addresses", func(t *testing.T) {
		resolver := netguardFailureIntegrationResolver{
			lookup: func(
				_ context.Context,
				network string,
				host string,
			) ([]netip.Addr, error) {
				if network != "ip" || host != "example.com" {
					t.Fatalf(
						"LookupNetIP(%q, %q), want ip, example.com",
						network,
						host,
					)
				}

				return []netip.Addr{
					netip.MustParseAddr("8.8.8.8"),
					netip.MustParseAddr("1.1.1.1"),
					netip.MustParseAddr("8.8.8.8"),
				}, nil
			},
		}

		destination, err := Resolve(
			context.Background(),
			hostOrigin,
			resolver,
		)
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}

		want := []netip.Addr{
			netip.MustParseAddr("1.1.1.1"),
			netip.MustParseAddr("8.8.8.8"),
		}

		if len(destination.addresses) != len(want) {
			t.Fatalf(
				"destination addresses = %#v, want %#v",
				destination.addresses,
				want,
			)
		}

		for index := range want {
			if destination.addresses[index] != want[index] {
				t.Errorf(
					"destination addresses = %#v, want %#v",
					destination.addresses,
					want,
				)
				break
			}
		}
	})
}

func TestNetguardFailureIntegrationDialBoundaries(t *testing.T) {
	validDestination := Destination{
		addresses: []netip.Addr{
			netip.MustParseAddr("1.1.1.1"),
			netip.MustParseAddr("8.8.8.8"),
		},
		port: 443,
	}

	if _, err := validDestination.DialContext(
		nil,
		nil,
	); !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"DialContext(nil) error = %v, want %v",
			err,
			errInvalidContext,
		)
	}

	canceledContext, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if _, err := validDestination.DialContext(
		canceledContext,
		nil,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"DialContext(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	if _, err := validDestination.DialContext(
		context.Background(),
		nil,
	); !errors.Is(err, errDialerUnavailable) {
		t.Errorf(
			"DialContext(nil dialer) error = %v, want %v",
			err,
			errDialerUnavailable,
		)
	}

	if _, err := (Destination{}).DialContext(
		context.Background(),
		&netguardFailureIntegrationDialer{
			dial: func(
				context.Context,
				string,
				string,
			) (net.Conn, error) {
				t.Fatal("dialer called for empty destination")
				return nil, nil
			},
		},
	); !errors.Is(err, errInvalidDestination) {
		t.Errorf(
			"empty destination error = %v, want %v",
			err,
			errInvalidDestination,
		)
	}

	t.Run("revalidates destination before dialing", func(t *testing.T) {
		destination := Destination{
			addresses: []netip.Addr{
				netip.MustParseAddr("127.0.0.1"),
			},
			port: 443,
		}

		dialer := &netguardFailureIntegrationDialer{
			dial: func(
				context.Context,
				string,
				string,
			) (net.Conn, error) {
				t.Fatal("dialer called for unsafe destination")
				return nil, nil
			},
		}

		_, err := destination.DialContext(
			context.Background(),
			dialer,
		)
		if !errors.Is(err, errUnsafeAddress) {
			t.Errorf(
				"DialContext() error = %v, want %v",
				err,
				errUnsafeAddress,
			)
		}
	})

	t.Run("nil successful connection fails closed", func(t *testing.T) {
		dialer := &netguardFailureIntegrationDialer{
			dial: func(
				context.Context,
				string,
				string,
			) (net.Conn, error) {
				return nil, nil
			},
		}

		_, err := validDestination.DialContext(
			context.Background(),
			dialer,
		)
		if !errors.Is(err, errDialFailed) {
			t.Errorf(
				"DialContext() error = %v, want %v",
				err,
				errDialFailed,
			)
		}
	})

	t.Run("tries validated addresses in deterministic order", func(t *testing.T) {
		dialer := &netguardFailureIntegrationDialer{}
		var peer net.Conn

		dialer.dial = func(
			_ context.Context,
			network string,
			address string,
		) (net.Conn, error) {
			if network != "tcp" {
				t.Fatalf(
					"DialContext network = %q, want tcp",
					network,
				)
			}

			if address == "1.1.1.1:443" {
				return nil, errors.New(
					"integration first dial failure",
				)
			}

			connection, other := net.Pipe()
			peer = other
			return connection, nil
		}

		connection, err := validDestination.DialContext(
			context.Background(),
			dialer,
		)
		if err != nil {
			t.Fatalf("DialContext() error = %v", err)
		}
		defer connection.Close()
		defer peer.Close()

		wantCalls := []string{
			"1.1.1.1:443",
			"8.8.8.8:443",
		}

		if len(dialer.calls) != len(wantCalls) {
			t.Fatalf(
				"dial calls = %#v, want %#v",
				dialer.calls,
				wantCalls,
			)
		}

		for index := range wantCalls {
			if dialer.calls[index] != wantCalls[index] {
				t.Errorf(
					"dial calls = %#v, want %#v",
					dialer.calls,
					wantCalls,
				)
				break
			}
		}
	})

	t.Run("parent cancellation wins dial failure", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)

		dialer := &netguardFailureIntegrationDialer{
			dial: func(
				context.Context,
				string,
				string,
			) (net.Conn, error) {
				cancel()
				return nil, errors.New(
					"integration dial failure",
				)
			},
		}

		_, err := validDestination.DialContext(ctx, dialer)
		if !errors.Is(err, context.Canceled) {
			t.Errorf(
				"DialContext() error = %v, want context.Canceled",
				err,
			)
		}
	})

	t.Run("all addresses fail", func(t *testing.T) {
		dialer := &netguardFailureIntegrationDialer{
			dial: func(
				context.Context,
				string,
				string,
			) (net.Conn, error) {
				return nil, errors.New(
					"integration dial failure",
				)
			},
		}

		_, err := validDestination.DialContext(
			context.Background(),
			dialer,
		)
		if !errors.Is(err, errDialFailed) {
			t.Errorf(
				"DialContext() error = %v, want %v",
				err,
				errDialFailed,
			)
		}

		if len(dialer.calls) != 2 {
			t.Errorf(
				"dial calls = %d, want 2",
				len(dialer.calls),
			)
		}
	})
}

func netguardFailureIntegrationOrigin(
	t *testing.T,
	raw string,
) origin.Origin {
	t.Helper()

	candidate, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			raw,
			err,
		)
	}

	return candidate
}

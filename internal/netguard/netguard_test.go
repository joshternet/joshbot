package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"testing"

	"github.com/joshternet/joshbot/internal/origin"
)

func TestValidateAddrRejectsUnsafe(t *testing.T) {
	tests := []struct {
		name string
		addr netip.Addr
	}{
		{
			name: "invalid address",
			addr: netip.Addr{},
		},
		{
			name: "IPv4 unspecified",
			addr: netip.MustParseAddr("0.0.0.0"),
		},
		{
			name: "IPv4 this network",
			addr: netip.MustParseAddr("0.0.0.1"),
		},
		{
			name: "IPv4 private 10",
			addr: netip.MustParseAddr("10.0.0.1"),
		},
		{
			name: "IPv4 shared CGNAT",
			addr: netip.MustParseAddr("100.64.0.1"),
		},
		{
			name: "IPv4 loopback",
			addr: netip.MustParseAddr("127.0.0.1"),
		},
		{
			name: "IPv4 link-local metadata",
			addr: netip.MustParseAddr("169.254.169.254"),
		},
		{
			name: "IPv4 private 172",
			addr: netip.MustParseAddr("172.16.0.1"),
		},
		{
			name: "IPv4 protocol assignment",
			addr: netip.MustParseAddr("192.0.0.1"),
		},
		{
			name: "IPv4 protocol anycast",
			addr: netip.MustParseAddr("192.0.0.9"),
		},
		{
			name: "IPv4 documentation TEST-NET-1",
			addr: netip.MustParseAddr("192.0.2.1"),
		},
		{
			name: "IPv4 AS112",
			addr: netip.MustParseAddr("192.31.196.1"),
		},
		{
			name: "IPv4 AMT",
			addr: netip.MustParseAddr("192.52.193.1"),
		},
		{
			name: "IPv4 deprecated 6to4 relay",
			addr: netip.MustParseAddr("192.88.99.1"),
		},
		{
			name: "IPv4 private 192",
			addr: netip.MustParseAddr("192.168.0.1"),
		},
		{
			name: "IPv4 direct delegation AS112",
			addr: netip.MustParseAddr("192.175.48.1"),
		},
		{
			name: "IPv4 benchmarking",
			addr: netip.MustParseAddr("198.18.0.1"),
		},
		{
			name: "IPv4 documentation TEST-NET-2",
			addr: netip.MustParseAddr("198.51.100.1"),
		},
		{
			name: "IPv4 documentation TEST-NET-3",
			addr: netip.MustParseAddr("203.0.113.1"),
		},
		{
			name: "IPv4 multicast",
			addr: netip.MustParseAddr("224.0.0.1"),
		},
		{
			name: "IPv4 reserved future use",
			addr: netip.MustParseAddr("240.0.0.1"),
		},
		{
			name: "IPv4 limited broadcast",
			addr: netip.MustParseAddr("255.255.255.255"),
		},
		{
			name: "IPv6 unspecified",
			addr: netip.MustParseAddr("::"),
		},
		{
			name: "IPv6 loopback",
			addr: netip.MustParseAddr("::1"),
		},
		{
			name: "IPv4-mapped IPv6 loopback",
			addr: netip.MustParseAddr("::ffff:127.0.0.1"),
		},
		{
			name: "IPv6 translation",
			addr: netip.MustParseAddr("64:ff9b::808:808"),
		},
		{
			name: "IPv6 local-use translation",
			addr: netip.MustParseAddr("64:ff9b:1::1"),
		},
		{
			name: "IPv6 discard-only",
			addr: netip.MustParseAddr("100::1"),
		},
		{
			name: "IPv6 dummy prefix",
			addr: netip.MustParseAddr("100:0:0:1::1"),
		},
		{
			name: "IPv6 IETF protocol assignment",
			addr: netip.MustParseAddr("2001::1"),
		},
		{
			name: "IPv6 benchmarking",
			addr: netip.MustParseAddr("2001:2::1"),
		},
		{
			name: "IPv6 documentation",
			addr: netip.MustParseAddr("2001:db8::1"),
		},
		{
			name: "IPv6 6to4",
			addr: netip.MustParseAddr("2002::1"),
		},
		{
			name: "IPv6 AS112",
			addr: netip.MustParseAddr("2620:4f:8000::1"),
		},
		{
			name: "IPv6 former 6bone",
			addr: netip.MustParseAddr("3ffe::1"),
		},
		{
			name: "IPv6 documentation 3fff",
			addr: netip.MustParseAddr("3fff::1"),
		},
		{
			name: "IPv6 segment routing SIDs",
			addr: netip.MustParseAddr("5f00::1"),
		},
		{
			name: "IPv6 unique-local",
			addr: netip.MustParseAddr("fd00::1"),
		},
		{
			name: "IPv6 link-local",
			addr: netip.MustParseAddr("fe80::1"),
		},
		{
			name: "IPv6 multicast",
			addr: netip.MustParseAddr("ff02::1"),
		},
		{
			name: "IPv6 reserved space",
			addr: netip.MustParseAddr("4000::1"),
		},
		{
			name: "IPv6 scoped address",
			addr: netip.MustParseAddr("fe80::1%eth0"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateAddr(test.addr)
			if err == nil {
				t.Fatal("ValidateAddr() error = nil, want non-nil")
			}

			if got := err.Error(); got != "netguard: unsafe address" {
				t.Errorf(
					"ValidateAddr() error = %q, want %q",
					got,
					"netguard: unsafe address",
				)
			}
		})
	}
}

func TestValidateAddrAllowsPublic(t *testing.T) {
	// These are static classification fixtures. Tests never connect to them.
	tests := []struct {
		name string
		addr string
	}{
		{
			name: "public IPv4",
			addr: "8.8.8.8",
		},
		{
			name: "second public IPv4",
			addr: "1.1.1.1",
		},
		{
			name: "public IPv6",
			addr: "2606:4700:4700::1111",
		},
		{
			name: "second public IPv6",
			addr: "2001:4860:4860::8888",
		},
		{
			name: "IPv4-mapped public address",
			addr: "::ffff:8.8.8.8",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			addr := netip.MustParseAddr(test.addr)

			if err := ValidateAddr(addr); err != nil {
				t.Fatalf("ValidateAddr() error = %v, want nil", err)
			}
		})
	}
}

type fakeResolver struct {
	calls     int
	network   string
	host      string
	addresses []netip.Addr
	err       error
	lookup    func(context.Context) ([]netip.Addr, error)
}

func (r *fakeResolver) LookupNetIP(
	ctx context.Context,
	network string,
	host string,
) ([]netip.Addr, error) {
	r.calls++
	r.network = network
	r.host = host

	if r.lookup != nil {
		return r.lookup(ctx)
	}

	return slices.Clone(r.addresses), r.err
}

type dialCall struct {
	network string
	address string
}

type fakeDialer struct {
	calls []dialCall
	dial  func(context.Context, string, string) (net.Conn, error)
}

func (d *fakeDialer) DialContext(
	ctx context.Context,
	network string,
	address string,
) (net.Conn, error) {
	d.calls = append(d.calls, dialCall{
		network: network,
		address: address,
	})

	return d.dial(ctx, network, address)
}

func mustOrigin(t *testing.T, rawURL string) origin.Origin {
	t.Helper()

	got, err := origin.Parse(rawURL)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v, want nil",
			rawURL,
			err,
		)
	}

	return got
}

func pipeConnection(t *testing.T) net.Conn {
	t.Helper()

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	return client
}

func TestResolveLiteralBypassesDNS(t *testing.T) {
	resolver := &fakeResolver{
		err: errors.New("DNS must not be called for an IP literal"),
	}

	unsafe := mustOrigin(t, "https://127.0.0.1")
	if _, err := Resolve(
		context.Background(),
		unsafe,
		resolver,
	); !errors.Is(err, errUnsafeAddress) {
		t.Fatalf(
			"Resolve() error = %v, want errUnsafeAddress",
			err,
		)
	}

	safe := mustOrigin(t, "https://8.8.8.8:8443")
	destination, err := Resolve(
		context.Background(),
		safe,
		resolver,
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}

	wantAddresses := []netip.Addr{
		netip.MustParseAddr("8.8.8.8"),
	}
	if !slices.Equal(destination.addresses, wantAddresses) {
		t.Errorf(
			"Resolve() addresses = %v, want %v",
			destination.addresses,
			wantAddresses,
		)
	}

	if destination.port != 8443 {
		t.Errorf("Resolve() port = %d, want 8443", destination.port)
	}

	if resolver.calls != 0 {
		t.Errorf("resolver calls = %d, want 0", resolver.calls)
	}
}

func TestResolveHostnameValidatesDeduplicatesAndSorts(t *testing.T) {
	resolver := &fakeResolver{
		addresses: []netip.Addr{
			netip.MustParseAddr("8.8.8.8"),
			netip.MustParseAddr("1.1.1.1"),
			netip.MustParseAddr("::ffff:8.8.8.8"),
		},
	}
	candidate := mustOrigin(
		t,
		"https://Example.COM:8443/path",
	)

	destination, err := Resolve(
		context.Background(),
		candidate,
		resolver,
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}

	wantAddresses := []netip.Addr{
		netip.MustParseAddr("1.1.1.1"),
		netip.MustParseAddr("8.8.8.8"),
	}
	if !slices.Equal(destination.addresses, wantAddresses) {
		t.Errorf(
			"Resolve() addresses = %v, want %v",
			destination.addresses,
			wantAddresses,
		)
	}

	if destination.port != 8443 {
		t.Errorf("Resolve() port = %d, want 8443", destination.port)
	}

	if resolver.calls != 1 {
		t.Errorf("resolver calls = %d, want 1", resolver.calls)
	}

	if resolver.network != "ip" {
		t.Errorf(
			"resolver network = %q, want %q",
			resolver.network,
			"ip",
		)
	}

	if resolver.host != "example.com" {
		t.Errorf(
			"resolver host = %q, want %q",
			resolver.host,
			"example.com",
		)
	}
}

func TestResolveRejectsUnsafeOrIncompleteResults(t *testing.T) {
	tests := []struct {
		name      string
		addresses []netip.Addr
		err       error
		wantErr   error
	}{
		{
			name: "mixed safe and unsafe",
			addresses: []netip.Addr{
				netip.MustParseAddr("8.8.8.8"),
				netip.MustParseAddr("127.0.0.1"),
			},
			wantErr: errUnsafeAddress,
		},
		{
			name: "only unsafe",
			addresses: []netip.Addr{
				netip.MustParseAddr("10.0.0.1"),
			},
			wantErr: errUnsafeAddress,
		},
		{
			name: "invalid address",
			addresses: []netip.Addr{
				{},
			},
			wantErr: errUnsafeAddress,
		},
		{
			name:    "empty result",
			wantErr: errNoAddresses,
		},
		{
			name: "partial result with error",
			addresses: []netip.Addr{
				netip.MustParseAddr("8.8.8.8"),
			},
			err: errors.New(
				"partial resolver failure with internal details",
			),
			wantErr: errResolutionFailed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &fakeResolver{
				addresses: test.addresses,
				err:       test.err,
			}
			candidate := mustOrigin(t, "https://example.com")

			_, err := Resolve(
				context.Background(),
				candidate,
				resolver,
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf(
					"Resolve() error = %v, want %v",
					err,
					test.wantErr,
				)
			}

			if err.Error() != test.wantErr.Error() {
				t.Errorf(
					"Resolve() error = %q, want %q",
					err,
					test.wantErr,
				)
			}
		})
	}
}

func TestResolveRejectsInvalidInputsAndCancellation(t *testing.T) {
	candidate := mustOrigin(t, "https://example.com")

	if _, err := Resolve(
		nil,
		candidate,
		nil,
	); !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"Resolve(nil) error = %v, want errInvalidContext",
			err,
		)
	}

	if _, err := Resolve(
		context.Background(),
		origin.Origin{},
		nil,
	); !errors.Is(err, errInvalidOrigin) {
		t.Errorf(
			"Resolve(zero origin) error = %v, want errInvalidOrigin",
			err,
		)
	}

	if _, err := Resolve(
		context.Background(),
		candidate,
		nil,
	); !errors.Is(err, errResolverUnavailable) {
		t.Errorf(
			"Resolve(nil resolver) error = %v, want errResolverUnavailable",
			err,
		)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	resolver := &fakeResolver{}
	if _, err := Resolve(
		canceled,
		candidate,
		resolver,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Resolve(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	if resolver.calls != 0 {
		t.Errorf(
			"resolver calls after pre-cancellation = %d, want 0",
			resolver.calls,
		)
	}

	duringLookup, cancelLookup := context.WithCancel(
		context.Background(),
	)
	resolver.lookup = func(
		context.Context,
	) ([]netip.Addr, error) {
		cancelLookup()
		return nil, errors.New("resolver canceled")
	}

	if _, err := Resolve(
		duringLookup,
		candidate,
		resolver,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Resolve(canceled lookup) error = %v, want context.Canceled",
			err,
		)
	}
}

func TestDialContextUsesExactResolvedIPv4(t *testing.T) {
	resolver := &fakeResolver{
		addresses: []netip.Addr{
			netip.MustParseAddr("8.8.8.8"),
		},
	}
	candidate := mustOrigin(t, "https://example.com")

	destination, err := Resolve(
		context.Background(),
		candidate,
		resolver,
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}

	dialer := &fakeDialer{
		dial: func(
			context.Context,
			string,
			string,
		) (net.Conn, error) {
			return pipeConnection(t), nil
		},
	}

	connection, err := destination.DialContext(
		context.Background(),
		dialer,
	)
	if err != nil {
		t.Fatalf("DialContext() error = %v, want nil", err)
	}
	_ = connection.Close()

	wantCalls := []dialCall{
		{
			network: "tcp",
			address: "8.8.8.8:443",
		},
	}
	if !slices.Equal(dialer.calls, wantCalls) {
		t.Errorf(
			"dial calls = %v, want %v",
			dialer.calls,
			wantCalls,
		)
	}

	if resolver.calls != 1 {
		t.Errorf("resolver calls = %d, want 1", resolver.calls)
	}
}

func TestDialContextFormatsExactIPv6Literal(t *testing.T) {
	resolver := &fakeResolver{}
	candidate := mustOrigin(
		t,
		"https://[2001:4860:4860::8888]:8443",
	)

	destination, err := Resolve(
		context.Background(),
		candidate,
		resolver,
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}

	dialer := &fakeDialer{
		dial: func(
			context.Context,
			string,
			string,
		) (net.Conn, error) {
			return pipeConnection(t), nil
		},
	}

	connection, err := destination.DialContext(
		context.Background(),
		dialer,
	)
	if err != nil {
		t.Fatalf("DialContext() error = %v, want nil", err)
	}
	_ = connection.Close()

	wantCalls := []dialCall{
		{
			network: "tcp",
			address: "[2001:4860:4860::8888]:8443",
		},
	}
	if !slices.Equal(dialer.calls, wantCalls) {
		t.Errorf(
			"dial calls = %v, want %v",
			dialer.calls,
			wantCalls,
		)
	}

	if resolver.calls != 0 {
		t.Errorf("resolver calls = %d, want 0", resolver.calls)
	}
}

func TestDialContextRetriesOnlyValidatedAddresses(t *testing.T) {
	resolver := &fakeResolver{
		addresses: []netip.Addr{
			netip.MustParseAddr("8.8.8.8"),
			netip.MustParseAddr("1.1.1.1"),
		},
	}
	candidate := mustOrigin(t, "http://example.com")

	destination, err := Resolve(
		context.Background(),
		candidate,
		resolver,
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}

	dialer := &fakeDialer{}
	dialer.dial = func(
		context.Context,
		string,
		string,
	) (net.Conn, error) {
		if len(dialer.calls) == 1 {
			return nil, errors.New(
				"first validated address failed",
			)
		}

		return pipeConnection(t), nil
	}

	connection, err := destination.DialContext(
		context.Background(),
		dialer,
	)
	if err != nil {
		t.Fatalf("DialContext() error = %v, want nil", err)
	}
	_ = connection.Close()

	wantCalls := []dialCall{
		{
			network: "tcp",
			address: "1.1.1.1:80",
		},
		{
			network: "tcp",
			address: "8.8.8.8:80",
		},
	}
	if !slices.Equal(dialer.calls, wantCalls) {
		t.Errorf(
			"dial calls = %v, want %v",
			dialer.calls,
			wantCalls,
		)
	}

	if resolver.calls != 1 {
		t.Errorf("resolver calls = %d, want 1", resolver.calls)
	}
}

func TestDialContextRejectsInvalidInputsAndFailures(t *testing.T) {
	safeDestination := Destination{
		addresses: []netip.Addr{
			netip.MustParseAddr("8.8.8.8"),
		},
		port: 443,
	}

	if _, err := safeDestination.DialContext(
		nil,
		nil,
	); !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"DialContext(nil) error = %v, want errInvalidContext",
			err,
		)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := safeDestination.DialContext(
		canceled,
		nil,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"DialContext(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	if _, err := safeDestination.DialContext(
		context.Background(),
		nil,
	); !errors.Is(err, errDialerUnavailable) {
		t.Errorf(
			"DialContext(nil dialer) error = %v, want errDialerUnavailable",
			err,
		)
	}

	dialer := &fakeDialer{
		dial: func(
			context.Context,
			string,
			string,
		) (net.Conn, error) {
			return nil, errors.New("unexpected dial")
		},
	}

	if _, err := (Destination{}).DialContext(
		context.Background(),
		dialer,
	); !errors.Is(err, errInvalidDestination) {
		t.Errorf(
			"zero Destination.DialContext() error = %v, want errInvalidDestination",
			err,
		)
	}

	unsafeDestination := Destination{
		addresses: []netip.Addr{
			netip.MustParseAddr("127.0.0.1"),
		},
		port: 443,
	}

	if _, err := unsafeDestination.DialContext(
		context.Background(),
		dialer,
	); !errors.Is(err, errUnsafeAddress) {
		t.Errorf(
			"unsafe Destination.DialContext() error = %v, want errUnsafeAddress",
			err,
		)
	}

	if len(dialer.calls) != 0 {
		t.Errorf(
			"dial calls for invalid destinations = %d, want 0",
			len(dialer.calls),
		)
	}

	nilConnectionDialer := &fakeDialer{
		dial: func(
			context.Context,
			string,
			string,
		) (net.Conn, error) {
			return nil, nil
		},
	}

	if _, err := safeDestination.DialContext(
		context.Background(),
		nilConnectionDialer,
	); !errors.Is(err, errDialFailed) {
		t.Errorf(
			"DialContext(nil connection) error = %v, want errDialFailed",
			err,
		)
	}

	failingDialer := &fakeDialer{
		dial: func(
			context.Context,
			string,
			string,
		) (net.Conn, error) {
			return nil, errors.New("internal dial failure")
		},
	}

	if _, err := safeDestination.DialContext(
		context.Background(),
		failingDialer,
	); !errors.Is(err, errDialFailed) {
		t.Errorf(
			"DialContext(failure) error = %v, want errDialFailed",
			err,
		)
	}
}

func TestDialContextStopsAfterCancellation(t *testing.T) {
	destination := Destination{
		addresses: []netip.Addr{
			netip.MustParseAddr("1.1.1.1"),
			netip.MustParseAddr("8.8.8.8"),
		},
		port: 443,
	}

	ctx, cancel := context.WithCancel(context.Background())
	dialer := &fakeDialer{
		dial: func(
			context.Context,
			string,
			string,
		) (net.Conn, error) {
			cancel()
			return nil, errors.New("dial canceled")
		},
	}

	if _, err := destination.DialContext(
		ctx,
		dialer,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf(
			"DialContext() error = %v, want context.Canceled",
			err,
		)
	}

	if len(dialer.calls) != 1 {
		t.Errorf("dial calls = %d, want 1", len(dialer.calls))
	}
}

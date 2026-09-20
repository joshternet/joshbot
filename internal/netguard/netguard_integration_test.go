package netguard_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"

	"github.com/joshternet/joshbot/internal/netguard"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

type netguardIntegrationResolver struct {
	addresses []netip.Addr
	err       error
}

func (resolver netguardIntegrationResolver) LookupNetIP(
	context.Context,
	string,
	string,
) ([]netip.Addr, error) {
	return append(
		[]netip.Addr(nil),
		resolver.addresses...,
	), resolver.err
}

type netguardIntegrationDialer struct {
	target string

	mu    sync.Mutex
	calls []string
}

func (dialer *netguardIntegrationDialer) DialContext(
	ctx context.Context,
	network string,
	address string,
) (net.Conn, error) {
	dialer.mu.Lock()
	dialer.calls = append(
		dialer.calls,
		network+" "+address,
	)
	dialer.mu.Unlock()

	var system net.Dialer

	return system.DialContext(
		ctx,
		network,
		dialer.target,
	)
}

type netguardIntegrationFailingDialer struct{}

func (netguardIntegrationFailingDialer) DialContext(
	context.Context,
	string,
	string,
) (net.Conn, error) {
	return nil, errors.New(
		"integration dial failure",
	)
}

func TestNetguardIntegrationResolvesAndDialsValidatedDestination(
	t *testing.T,
) {
	listener, err := net.Listen(
		"tcp",
		"127.0.0.1:0",
	)
	if err != nil {
		t.Fatalf(
			"net.Listen() error = %v",
			err,
		)
	}
	defer listener.Close()

	source, err := origin.Parse(
		"https://example.com:8443",
	)
	if err != nil {
		t.Fatalf(
			"origin.Parse() error = %v",
			err,
		)
	}

	destination, err := netguard.Resolve(
		context.Background(),
		source,
		netguardIntegrationResolver{
			addresses: []netip.Addr{
				netip.MustParseAddr(
					"8.8.8.8",
				),
				netip.MustParseAddr(
					"::ffff:8.8.8.8",
				),
			},
		},
	)
	if err != nil {
		t.Fatalf(
			"netguard.Resolve() error = %v",
			err,
		)
	}

	dialer := &netguardIntegrationDialer{
		target: listener.Addr().String(),
	}

	client, err := destination.DialContext(
		context.Background(),
		dialer,
	)
	if err != nil {
		t.Fatalf(
			"Destination.DialContext() error = %v",
			err,
		)
	}
	defer client.Close()

	server, err := listener.Accept()
	if err != nil {
		t.Fatalf(
			"listener.Accept() error = %v",
			err,
		)
	}
	defer server.Close()

	const payload = "JoshBot guarded connection"

	writeErr := make(chan error, 1)
	go func() {
		_, err := io.WriteString(
			server,
			payload,
		)
		writeErr <- err
	}()

	body := make(
		[]byte,
		len(payload),
	)
	if _, err := io.ReadFull(
		client,
		body,
	); err != nil {
		t.Fatalf(
			"io.ReadFull() error = %v",
			err,
		)
	}

	if err := <-writeErr; err != nil {
		t.Fatalf(
			"server write error = %v",
			err,
		)
	}

	if string(body) != payload {
		t.Errorf(
			"payload = %q, want %q",
			body,
			payload,
		)
	}

	dialer.mu.Lock()
	calls := append(
		[]string(nil),
		dialer.calls...,
	)
	dialer.mu.Unlock()

	if len(calls) != 1 {
		t.Fatalf(
			"dial call count = %d, want 1",
			len(calls),
		)
	}

	if calls[0] != "tcp 8.8.8.8:8443" {
		t.Errorf(
			"dial target = %q, want %q",
			calls[0],
			"tcp 8.8.8.8:8443",
		)
	}
}

func TestNetguardIntegrationClassifiesUnsafeResolutionAndDialFailure(
	t *testing.T,
) {
	source, err := origin.Parse(
		"https://example.com",
	)
	if err != nil {
		t.Fatalf(
			"origin.Parse() error = %v",
			err,
		)
	}

	_, err = netguard.Resolve(
		context.Background(),
		source,
		netguardIntegrationResolver{
			addresses: []netip.Addr{
				netip.MustParseAddr(
					"127.0.0.1",
				),
			},
		},
	)
	if err == nil {
		t.Fatal(
			"netguard.Resolve() error = nil, want unsafe-address failure",
		)
	}

	if got := netguard.FailureCategory(err); got !=
		retry.CategoryUnsafeAddress {
		t.Errorf(
			"unsafe resolution category = %q, want %q",
			got,
			retry.CategoryUnsafeAddress,
		)
	}

	destination, err := netguard.Resolve(
		context.Background(),
		source,
		netguardIntegrationResolver{
			addresses: []netip.Addr{
				netip.MustParseAddr(
					"8.8.8.8",
				),
			},
		},
	)
	if err != nil {
		t.Fatalf(
			"netguard.Resolve() error = %v",
			err,
		)
	}

	_, err = destination.DialContext(
		context.Background(),
		netguardIntegrationFailingDialer{},
	)
	if err == nil {
		t.Fatal(
			"Destination.DialContext() error = nil, want dial failure",
		)
	}

	if got := netguard.FailureCategory(err); got !=
		retry.CategoryTransport {
		t.Errorf(
			"dial failure category = %q, want %q",
			got,
			retry.CategoryTransport,
		)
	}
}

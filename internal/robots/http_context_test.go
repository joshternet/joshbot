package robots

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"testing"
)

func TestGuardedHTTPReturnsContextErrorFromFailedRequest(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	resolver := &robotsResolver{
		addresses: []netip.Addr{
			netip.MustParseAddr("93.184.216.34"),
		},
	}
	dialer := &recordingDialer{
		dial: func(
			context.Context,
			string,
			string,
		) (net.Conn, error) {
			cancel()

			return nil, errors.New("test dial failure")
		},
	}
	getter := &guardedHTTP{
		resolver: resolver,
		dialer:   dialer,
	}

	response, err := getter.get(
		ctx,
		mustTarget(t, "http://example.com/robots.txt"),
	)
	if response != nil {
		_ = response.Body.Close()
		t.Fatal("guardedHTTP.get() response is non-nil")
	}

	if !errors.Is(err, context.Canceled) {
		t.Fatalf(
			"guardedHTTP.get() error = %v, want context.Canceled",
			err,
		)
	}
}

func TestObtainPolicyReturnsContextErrorFromFailedFetch(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	getter := &cancelingHopGetter{
		cancel: cancel,
	}

	policy, err := obtainPolicy(
		ctx,
		mustWebOrigin(t, "https://example.com"),
		getter,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf(
			"obtainPolicy() error = %v, want context.Canceled",
			err,
		)
	}

	target := mustTarget(t, "https://example.com/public")
	if policy.Allowed(target) {
		t.Fatal("Policy.Allowed() = true, want false")
	}
}

type cancelingHopGetter struct {
	cancel context.CancelFunc
}

func (g *cancelingHopGetter) get(
	_ context.Context,
	_ *url.URL,
) (*http.Response, error) {
	g.cancel()

	return nil, errors.New("test fetch failure")
}

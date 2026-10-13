package robots

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/netguard"
)

func TestConfiguredCrawlDelayLimit(t *testing.T) {
	for _, test := range []struct {
		value   string
		want    time.Duration
		invalid bool
	}{
		{"0", 0, false}, {"1.25", 1250 * time.Millisecond, false}, {"2", 2 * time.Second, false},
		{"3", 2 * time.Second, false}, {"9223372036.854775807", 2 * time.Second, false},
		{"9223372036.854775808", 0, true}, {"99\nCrawl-delay: garbage", 0, true},
	} {
		t.Run(test.value, func(t *testing.T) {
			p := Parse([]byte("User-agent: *\nCrawl-delay: " + test.value + "\n"))
			got, err := p.crawlDelay(2 * time.Second)
			if got != test.want || (err != nil) != test.invalid {
				t.Fatalf("delay=%v error=%v", got, err)
			}
		})
	}
}

func TestCheckerStoresBoundedCrawlDelay(t *testing.T) {
	for _, minimum := range []time.Duration{time.Second, 4 * time.Second} {
		clock := &requestDelayTestClock{current: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)}
		waiter := &requestDelayRecordingWaiter{clock: clock}
		getter := &requestDelayTestGetter{clock: clock, respond: func(target *url.URL) (*http.Response, error) {
			body := "page"
			if target.Path == "/robots.txt" {
				body = "User-agent: *\nCrawl-delay: 9223372036.854775807\n"
			}
			return requestDelayResponse(http.StatusOK, body), nil
		}}
		checker := newCheckerWithRequestDelay(getter, clock.Now, minimum, waiter)
		checker.maxCrawlDelay = 2 * time.Second
		for _, path := range []string{"/first", "/second"} {
			response, err := checker.Get(context.Background(), requestDelayTarget(t, "https://source.example"+path))
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
		}
		want := max(minimum, 2*time.Second)
		for i := 1; i < len(getter.requests); i++ {
			if got := getter.requests[i].at.Sub(getter.requests[i-1].at); got != want {
				t.Fatalf("spacing=%v want %v", got, want)
			}
		}
	}
}

func TestHTTPConfigValidationAndTransport(t *testing.T) {
	config := DefaultHTTPConfig()
	for _, field := range []*time.Duration{&config.MaxCrawlDelay, &config.DialTimeout, &config.TLSHandshakeTimeout, &config.ResponseHeaderTimeout} {
		original := *field
		for _, invalid := range []time.Duration{0, -time.Second} {
			*field = invalid
			if _, err := NewCheckerWithHTTPConfig(nil, nil, 0, nil, config); !errors.Is(err, ErrInvalidHTTPConfig) {
				t.Fatalf("error=%v", err)
			}
		}
		*field = original
	}
	checker, err := NewCheckerWithHTTPConfig(nil, nil, 0, nil, config)
	if err != nil || checker.maxCrawlDelay != config.MaxCrawlDelay {
		t.Fatalf("checker=%v error=%v", checker, err)
	}
	if _, err := NewCheckerWithHTTPConfig(nil, nil, -time.Second, nil, config); !errors.Is(err, errInvalidRequestDelay) {
		t.Fatalf("error=%v", err)
	}
}

func TestGuardedHTTPStopsStalledHandshakeAndHeaders(t *testing.T) {
	for _, scheme := range []string{"https", "http"} {
		t.Run(scheme, func(t *testing.T) {
			config := DefaultHTTPConfig()
			config.TLSHandshakeTimeout = 10 * time.Millisecond
			config.ResponseHeaderTimeout = 10 * time.Millisecond
			config.DialTimeout = 100 * time.Millisecond
			var done chan struct{}
			dialer := &recordingDialer{dial: func(context.Context, string, string) (net.Conn, error) {
				client, server := net.Pipe()
				done = make(chan struct{})
				go func() { defer close(done); defer server.Close(); io.Copy(io.Discard, server) }()
				return client, nil
			}}
			getter := &guardedHTTP{resolver: &robotsResolver{addresses: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}, dialer: dialer, config: config}
			response, err := getter.get(context.Background(), mustTarget(t, scheme+"://example.com/page"))
			if response != nil {
				response.Body.Close()
				t.Fatal("unexpected response")
			}
			var timeout net.Error
			if !errors.As(err, &timeout) || !timeout.Timeout() {
				t.Fatalf("error=%v want timeout", err)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("timed-out connection leaked")
			}
		})
	}
}

func TestConfiguredTransportFields(t *testing.T) {
	config := DefaultHTTPConfig()
	config.TLSHandshakeTimeout = 2 * time.Second
	config.ResponseHeaderTimeout = 3 * time.Second
	config.DialTimeout = 4 * time.Second
	resolver := &robotsResolver{addresses: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}
	destination, err := netguard.Resolve(context.Background(), mustWebOrigin(t, "https://example.com"), resolver)
	if err != nil {
		t.Fatal(err)
	}
	var attempt context.Context
	dialer := &recordingDialer{dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		attempt = ctx
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > config.DialTimeout {
			t.Fatal("missing dial deadline")
		}
		return nil, errors.New("failed")
	}}
	transport := newGuardedTransport(destination, dialer, nil, config)
	defer transport.CloseIdleConnections()
	if transport.TLSHandshakeTimeout != config.TLSHandshakeTimeout || transport.ResponseHeaderTimeout != config.ResponseHeaderTimeout || transport.Proxy != nil || transport.DialTLSContext != nil || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("invalid transport configuration")
	}
	transport.DialContext(context.Background(), "tcp", "example.com:443")
	if !errors.Is(attempt.Err(), context.Canceled) {
		t.Fatal("dial context was not canceled")
	}
}

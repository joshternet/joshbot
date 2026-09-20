package robots_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync"
	"testing"

	"github.com/joshternet/joshbot/internal/robots"
)

type robotsIntegrationResolver struct{}

func (robotsIntegrationResolver) LookupNetIP(
	context.Context,
	string,
	string,
) ([]netip.Addr, error) {
	return []netip.Addr{
		netip.MustParseAddr(
			"93.184.216.34",
		),
	}, nil
}

type robotsIntegrationDialer struct {
	target string
}

func (dialer robotsIntegrationDialer) DialContext(
	ctx context.Context,
	network string,
	_ string,
) (net.Conn, error) {
	var system net.Dialer

	return system.DialContext(
		ctx,
		network,
		dialer.target,
	)
}

func TestRobotsIntegrationGuardsRequestsAndCachesPolicy(
	t *testing.T,
) {
	var mu sync.Mutex
	robotsRequests := 0
	publicRequests := 0
	privateRequests := 0

	server := httptest.NewServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			if request.UserAgent() !=
				robots.UserAgent {
				t.Errorf(
					"User-Agent = %q, want %q",
					request.UserAgent(),
					robots.UserAgent,
				)
			}

			mu.Lock()
			switch request.URL.Path {
			case "/robots.txt":
				robotsRequests++
			case "/public":
				publicRequests++
			case "/private":
				privateRequests++
			}
			mu.Unlock()

			switch request.URL.Path {
			case "/robots.txt":
				writer.Header().Set(
					"Content-Type",
					"text/plain",
				)
				_, _ = io.WriteString(
					writer,
					"User-agent: Joshternet-Joshbot\n"+
						"Allow: /public\n"+
						"Disallow: /private\n",
				)

			case "/public":
				_, _ = io.WriteString(
					writer,
					"public",
				)

			case "/private":
				_, _ = io.WriteString(
					writer,
					"private",
				)

			default:
				http.NotFound(
					writer,
					request,
				)
			}
		}),
	)
	defer server.Close()

	checker := robots.NewChecker(
		robotsIntegrationResolver{},
		robotsIntegrationDialer{
			target: server.Listener.Addr().String(),
		},
	)

	publicURL := mustRobotsIntegrationURL(
		t,
		"http://example.com/public",
	)

	for attempt := 0; attempt < 2; attempt++ {
		response, err := checker.Get(
			context.Background(),
			publicURL,
		)
		if err != nil {
			t.Fatalf(
				"Checker.Get(public) error = %v",
				err,
			)
		}

		body, err := io.ReadAll(
			response.Body,
		)
		if err != nil {
			_ = response.Body.Close()
			t.Fatalf(
				"read public response: %v",
				err,
			)
		}

		if err := response.Body.Close(); err != nil {
			t.Fatalf(
				"close public response: %v",
				err,
			)
		}

		if string(body) != "public" {
			t.Errorf(
				"public response = %q, want %q",
				body,
				"public",
			)
		}
	}

	privateURL := mustRobotsIntegrationURL(
		t,
		"http://example.com/private",
	)

	response, err := checker.Get(
		context.Background(),
		privateURL,
	)
	if response != nil {
		_ = response.Body.Close()
		t.Errorf(
			"Checker.Get(private) response = %#v, want nil",
			response,
		)
	}

	if !errors.Is(
		err,
		robots.ErrDisallowed,
	) {
		t.Fatalf(
			"Checker.Get(private) error = %v, want %v",
			err,
			robots.ErrDisallowed,
		)
	}

	mu.Lock()
	gotRobotsRequests := robotsRequests
	gotPublicRequests := publicRequests
	gotPrivateRequests := privateRequests
	mu.Unlock()

	if gotRobotsRequests != 1 {
		t.Errorf(
			"robots request count = %d, want 1",
			gotRobotsRequests,
		)
	}

	if gotPublicRequests != 2 {
		t.Errorf(
			"public request count = %d, want 2",
			gotPublicRequests,
		)
	}

	if gotPrivateRequests != 0 {
		t.Errorf(
			"private request count = %d, want 0",
			gotPrivateRequests,
		)
	}
}

func TestRobotsIntegrationFailsClosedOnTemporaryPolicyFailure(
	t *testing.T,
) {
	protectedRequests := 0

	server := httptest.NewServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			switch request.URL.Path {
			case "/robots.txt":
				http.Error(
					writer,
					"temporarily unavailable",
					http.StatusServiceUnavailable,
				)

			case "/page":
				protectedRequests++
				_, _ = io.WriteString(
					writer,
					"must not be fetched",
				)

			default:
				http.NotFound(
					writer,
					request,
				)
			}
		}),
	)
	defer server.Close()

	checker := robots.NewChecker(
		robotsIntegrationResolver{},
		robotsIntegrationDialer{
			target: server.Listener.Addr().String(),
		},
	)

	response, err := checker.Get(
		context.Background(),
		mustRobotsIntegrationURL(
			t,
			"http://example.com/page",
		),
	)
	if response != nil {
		_ = response.Body.Close()
		t.Errorf(
			"Checker.Get() response = %#v, want nil",
			response,
		)
	}

	if !errors.Is(
		err,
		robots.ErrTemporary,
	) {
		t.Fatalf(
			"Checker.Get() error = %v, want %v",
			err,
			robots.ErrTemporary,
		)
	}

	if protectedRequests != 0 {
		t.Errorf(
			"protected requests = %d, want 0",
			protectedRequests,
		)
	}
}

func mustRobotsIntegrationURL(
	t *testing.T,
	raw string,
) *url.URL {
	t.Helper()

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf(
			"url.Parse(%q) error = %v",
			raw,
			err,
		)
	}

	return parsed
}

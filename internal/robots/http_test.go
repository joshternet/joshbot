package robots

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/joshternet/joshbot/internal/netguard"
	"github.com/joshternet/joshbot/internal/origin"
)

func TestObtainPolicyInterpretsHTTPResults(t *testing.T) {
	networkFailure := errors.New("test network failure")
	bodyFailure := errors.New("test body failure")

	tests := []struct {
		name        string
		step        hopStep
		wantAllowed bool
		wantError   bool
	}{
		{
			name: "successful robots body",
			step: hopStep{
				response: robotsResponse(
					http.StatusOK,
					"User-agent: Joshternet-Joshbot\n"+
						"Disallow: /private\n",
				),
			},
			wantAllowed: false,
		},
		{
			name: "incorrect content type is still parsed",
			step: hopStep{
				response: responseWithHeader(
					http.StatusOK,
					"User-agent: Joshternet-Joshbot\n"+
						"Disallow: /private\n",
					"Content-Type",
					"text/html",
				),
			},
			wantAllowed: false,
		},
		{
			name: "empty successful body allows",
			step: hopStep{
				response: robotsResponse(http.StatusNoContent, ""),
			},
			wantAllowed: true,
		},
		{
			name: "401 allows",
			step: hopStep{
				response: robotsResponse(
					http.StatusUnauthorized,
					"",
				),
			},
			wantAllowed: true,
		},
		{
			name: "403 allows",
			step: hopStep{
				response: robotsResponse(
					http.StatusForbidden,
					"",
				),
			},
			wantAllowed: true,
		},
		{
			name: "404 allows",
			step: hopStep{
				response: robotsResponse(
					http.StatusNotFound,
					"",
				),
			},
			wantAllowed: true,
		},
		{
			name: "410 allows",
			step: hopStep{
				response: robotsResponse(
					http.StatusGone,
					"",
				),
			},
			wantAllowed: true,
		},
		{
			name: "500 fails closed",
			step: hopStep{
				response: robotsResponse(
					http.StatusInternalServerError,
					"",
				),
			},
			wantAllowed: false,
			wantError:   true,
		},
		{
			name: "503 fails closed",
			step: hopStep{
				response: robotsResponse(
					http.StatusServiceUnavailable,
					"",
				),
			},
			wantAllowed: false,
			wantError:   true,
		},
		{
			name: "network failure fails closed",
			step: hopStep{
				err: networkFailure,
			},
			wantAllowed: false,
			wantError:   true,
		},
		{
			name: "body failure fails closed",
			step: hopStep{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body: &failingBody{
						data: []byte(
							"User-agent: Joshternet-Joshbot\n" +
								"Allow: /\n",
						),
						err: bodyFailure,
					},
				},
			},
			wantAllowed: false,
			wantError:   true,
		},
		{
			name: "oversized body fails closed",
			step: hopStep{
				response: robotsResponse(
					http.StatusOK,
					strings.Repeat("x", MaxBodySize+1),
				),
			},
			wantAllowed: false,
			wantError:   true,
		},
		{
			name: "unexpected 300 fails closed",
			step: hopStep{
				response: robotsResponse(
					http.StatusMultipleChoices,
					"",
				),
			},
			wantAllowed: false,
			wantError:   true,
		},
		{
			name: "unexpected 304 fails closed",
			step: hopStep{
				response: robotsResponse(
					http.StatusNotModified,
					"",
				),
			},
			wantAllowed: false,
			wantError:   true,
		},
		{
			name: "unexpected 600 fails closed",
			step: hopStep{
				response: robotsResponse(600, ""),
			},
			wantAllowed: false,
			wantError:   true,
		},
		{
			name:        "nil response fails closed",
			step:        hopStep{},
			wantAllowed: false,
			wantError:   true,
		},
		{
			name: "nil response body fails closed",
			step: hopStep{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
				},
			},
			wantAllowed: false,
			wantError:   true,
		},
	}

	initial := mustWebOrigin(t, "https://example.com")
	target := mustTarget(t, "https://example.com/private/page")

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getter := &fakeHopGetter{
				steps: []hopStep{test.step},
			}

			policy, err := obtainPolicy(
				context.Background(),
				initial,
				getter,
			)
			if (err != nil) != test.wantError {
				t.Errorf(
					"obtainPolicy() error = %v, wantError %t",
					err,
					test.wantError,
				)
			}

			if got := policy.Allowed(target); got != test.wantAllowed {
				t.Errorf(
					"Policy.Allowed() = %t, want %t",
					got,
					test.wantAllowed,
				)
			}

			if len(getter.targets) != 1 {
				t.Errorf(
					"HTTP attempts = %d, want 1",
					len(getter.targets),
				)
			}

			if got := getter.targets[0].String(); got !=
				"https://example.com/robots.txt" {
				t.Errorf(
					"robots target = %q, want %q",
					got,
					"https://example.com/robots.txt",
				)
			}
		})
	}
}

func TestObtainPolicyFollowsRedirects(t *testing.T) {
	initial := mustWebOrigin(t, "https://example.com")
	protected := mustTarget(
		t,
		"https://example.com/private/page",
	)

	tests := []struct {
		name        string
		steps       []hopStep
		wantTargets []string
	}{
		{
			name: "same-origin absolute redirect",
			steps: []hopStep{
				{
					response: redirectResponse(
						http.StatusMovedPermanently,
						"https://example.com/policy",
					),
				},
				{
					response: disallowingResponse(),
				},
			},
			wantTargets: []string{
				"https://example.com/robots.txt",
				"https://example.com/policy",
			},
		},
		{
			name: "relative redirect",
			steps: []hopStep{
				{
					response: redirectResponse(
						http.StatusFound,
						"/policy",
					),
				},
				{
					response: disallowingResponse(),
				},
			},
			wantTargets: []string{
				"https://example.com/robots.txt",
				"https://example.com/policy",
			},
		},
		{
			name: "cross-origin redirect",
			steps: []hopStep{
				{
					response: redirectResponse(
						http.StatusTemporaryRedirect,
						"https://other.example/robots.txt",
					),
				},
				{
					response: disallowingResponse(),
				},
			},
			wantTargets: []string{
				"https://example.com/robots.txt",
				"https://other.example/robots.txt",
			},
		},
		{
			name:        "five redirects succeed",
			steps:       successfulFiveRedirectChain(),
			wantTargets: redirectTargets(5, true),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getter := &fakeHopGetter{
				steps: test.steps,
			}

			policy, err := obtainPolicy(
				context.Background(),
				initial,
				getter,
			)
			if err != nil {
				t.Fatalf(
					"obtainPolicy() error = %v, want nil",
					err,
				)
			}

			if policy.Allowed(protected) {
				t.Fatal("Policy.Allowed() = true, want false")
			}

			gotTargets := getter.targetStrings()
			if len(gotTargets) != len(test.wantTargets) {
				t.Fatalf(
					"redirect targets = %v, want %v",
					gotTargets,
					test.wantTargets,
				)
			}

			for index := range test.wantTargets {
				if gotTargets[index] != test.wantTargets[index] {
					t.Errorf(
						"redirect target %d = %q, want %q",
						index,
						gotTargets[index],
						test.wantTargets[index],
					)
				}
			}
		})
	}
}

func TestObtainPolicyRejectsRedirectFailures(t *testing.T) {
	initial := mustWebOrigin(t, "https://example.com")
	protected := mustTarget(
		t,
		"https://example.com/private/page",
	)

	tests := []struct {
		name      string
		steps     []hopStep
		wantCalls int
	}{
		{
			name:      "sixth redirect",
			steps:     redirectChain(6),
			wantCalls: 6,
		},
		{
			name: "missing location",
			steps: []hopStep{
				{
					response: robotsResponse(
						http.StatusFound,
						"",
					),
				},
			},
			wantCalls: 1,
		},
		{
			name: "malformed location",
			steps: []hopStep{
				{
					response: redirectResponse(
						http.StatusFound,
						"http://[::1",
					),
				},
			},
			wantCalls: 1,
		},
		{
			name: "unsupported redirect scheme",
			steps: []hopStep{
				{
					response: redirectResponse(
						http.StatusFound,
						"file:///private",
					),
				},
			},
			wantCalls: 1,
		},
		{
			name: "redirect cycle",
			steps: []hopStep{
				{
					response: redirectResponse(
						http.StatusPermanentRedirect,
						"/one",
					),
				},
				{
					response: redirectResponse(
						http.StatusPermanentRedirect,
						"/two",
					),
				},
				{
					response: redirectResponse(
						http.StatusPermanentRedirect,
						"/one",
					),
				},
				{
					response: redirectResponse(
						http.StatusPermanentRedirect,
						"/two",
					),
				},
				{
					response: redirectResponse(
						http.StatusPermanentRedirect,
						"/one",
					),
				},
				{
					response: redirectResponse(
						http.StatusPermanentRedirect,
						"/two",
					),
				},
			},
			wantCalls: 6,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getter := &fakeHopGetter{
				steps: test.steps,
			}

			policy, err := obtainPolicy(
				context.Background(),
				initial,
				getter,
			)
			if err == nil {
				t.Fatal("obtainPolicy() error = nil, want non-nil")
			}

			if policy.Allowed(protected) {
				t.Fatal("Policy.Allowed() = true, want false")
			}

			if len(getter.targets) != test.wantCalls {
				t.Errorf(
					"HTTP attempts = %d, want %d",
					len(getter.targets),
					test.wantCalls,
				)
			}
		})
	}
}

func TestObtainPolicyRejectsInvalidInputs(t *testing.T) {
	initial := mustWebOrigin(t, "https://example.com")
	protected := mustTarget(
		t,
		"https://example.com/private/page",
	)
	getter := &fakeHopGetter{}

	t.Run("nil context", func(t *testing.T) {
		policy, err := obtainPolicy(nil, initial, getter)
		if !errors.Is(err, errInvalidContext) {
			t.Fatalf(
				"obtainPolicy() error = %v, want errInvalidContext",
				err,
			)
		}

		if policy.Allowed(protected) {
			t.Fatal("Policy.Allowed() = true, want false")
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		policy, err := obtainPolicy(ctx, initial, getter)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf(
				"obtainPolicy() error = %v, want context.Canceled",
				err,
			)
		}

		if policy.Allowed(protected) {
			t.Fatal("Policy.Allowed() = true, want false")
		}
	})

	t.Run("zero origin", func(t *testing.T) {
		policy, err := obtainPolicy(
			context.Background(),
			origin.Origin{},
			getter,
		)
		if !errors.Is(err, errInvalidInitialOrigin) {
			t.Fatalf(
				"obtainPolicy() error = %v, want invalid origin",
				err,
			)
		}

		if policy.Allowed(protected) {
			t.Fatal("Policy.Allowed() = true, want false")
		}
	})

	t.Run("nil getter", func(t *testing.T) {
		policy, err := obtainPolicy(
			context.Background(),
			initial,
			nil,
		)
		if !errors.Is(err, errHTTPGetterUnavailable) {
			t.Fatalf(
				"obtainPolicy() error = %v, want unavailable getter",
				err,
			)
		}

		if policy.Allowed(protected) {
			t.Fatal("Policy.Allowed() = true, want false")
		}
	})
}

func TestGuardedHTTPPreservesIdentityAndTLSAuthority(t *testing.T) {
	observations := make(chan requestObservation, 1)
	server := httptest.NewUnstartedServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			observation := requestObservation{
				host:      request.Host,
				path:      request.URL.Path,
				userAgent: request.UserAgent(),
			}
			if request.TLS != nil {
				observation.serverName = request.TLS.ServerName
			}

			observations <- observation
			_, _ = writer.Write([]byte("ok"))
		}),
	)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)

	serverTransport, ok := server.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatal("test server transport is not *http.Transport")
	}

	resolver := &robotsResolver{
		addresses: []netip.Addr{
			netip.MustParseAddr("93.184.216.34"),
		},
	}
	dialer := &recordingDialer{
		dial: func(
			ctx context.Context,
			network string,
			_ string,
		) (net.Conn, error) {
			var local net.Dialer
			return local.DialContext(
				ctx,
				network,
				server.Listener.Addr().String(),
			)
		},
	}

	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:65535")
	t.Setenv("https_proxy", "http://127.0.0.1:65535")

	getter := &guardedHTTP{
		resolver: resolver,
		dialer:   dialer,
		rootCAs:  serverTransport.TLSClientConfig.RootCAs,
	}
	target := mustTarget(
		t,
		"https://example.com/robots.txt",
	)

	response, err := getter.get(context.Background(), target)
	if err != nil {
		t.Fatalf("guardedHTTP.get() error = %v, want nil", err)
	}

	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	if err := response.Body.Close(); err != nil {
		t.Fatalf("closing response body: %v", err)
	}

	observation := <-observations
	if observation.host != "example.com" {
		t.Errorf(
			"HTTP Host = %q, want %q",
			observation.host,
			"example.com",
		)
	}

	if observation.path != "/robots.txt" {
		t.Errorf(
			"request path = %q, want %q",
			observation.path,
			"/robots.txt",
		)
	}

	if observation.userAgent != UserAgent {
		t.Errorf(
			"User-Agent = %q, want %q",
			observation.userAgent,
			UserAgent,
		)
	}

	if observation.serverName != "example.com" {
		t.Errorf(
			"TLS ServerName = %q, want %q",
			observation.serverName,
			"example.com",
		)
	}

	addresses := dialer.addressSnapshot()
	if len(addresses) != 1 ||
		addresses[0] != "93.184.216.34:443" {
		t.Errorf(
			"dial addresses = %v, want [%q]",
			addresses,
			"93.184.216.34:443",
		)
	}
}

func TestGuardedHTTPRejectsUntrustedTLS(t *testing.T) {
	server := httptest.NewUnstartedServer(
		http.HandlerFunc(func(
			http.ResponseWriter,
			*http.Request,
		) {
		}),
	)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)

	resolver := &robotsResolver{
		addresses: []netip.Addr{
			netip.MustParseAddr("93.184.216.34"),
		},
	}
	dialer := &recordingDialer{
		dial: func(
			ctx context.Context,
			network string,
			_ string,
		) (net.Conn, error) {
			var local net.Dialer
			return local.DialContext(
				ctx,
				network,
				server.Listener.Addr().String(),
			)
		},
	}
	getter := &guardedHTTP{
		resolver: resolver,
		dialer:   dialer,
	}

	response, err := getter.get(
		context.Background(),
		mustTarget(t, "https://example.com/robots.txt"),
	)
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("guardedHTTP.get() error = nil, want TLS error")
	}
}

func TestGuardedHTTPRejectsUnsafeAddressBeforeDial(t *testing.T) {
	resolver := &robotsResolver{
		addresses: []netip.Addr{
			netip.MustParseAddr("127.0.0.1"),
		},
	}
	dialer := &recordingDialer{}
	getter := &guardedHTTP{
		resolver: resolver,
		dialer:   dialer,
	}

	response, err := getter.get(
		context.Background(),
		mustTarget(t, "http://unsafe.example/robots.txt"),
	)
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("guardedHTTP.get() error = nil, want non-nil")
	}

	if addresses := dialer.addressSnapshot(); len(addresses) != 0 {
		t.Errorf(
			"dial addresses = %v, want none",
			addresses,
		)
	}
}

func TestGuardedHTTPRejectsUnsafeRedirectBeforeDial(t *testing.T) {
	server := httptest.NewUnstartedServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			_ *http.Request,
		) {
			writer.Header().Set(
				"Location",
				"http://127.0.0.1/private",
			)
			writer.WriteHeader(http.StatusFound)
		}),
	)
	server.Start()
	t.Cleanup(server.Close)

	resolver := &robotsResolver{
		addresses: []netip.Addr{
			netip.MustParseAddr("93.184.216.34"),
		},
	}
	dialer := &recordingDialer{
		dial: func(
			ctx context.Context,
			network string,
			_ string,
		) (net.Conn, error) {
			var local net.Dialer
			return local.DialContext(
				ctx,
				network,
				server.Listener.Addr().String(),
			)
		},
	}
	getter := &guardedHTTP{
		resolver: resolver,
		dialer:   dialer,
	}

	policy, err := obtainPolicy(
		context.Background(),
		mustWebOrigin(t, "http://example.com"),
		getter,
	)
	if err == nil {
		t.Fatal("obtainPolicy() error = nil, want non-nil")
	}

	protected := mustTarget(
		t,
		"http://example.com/private",
	)
	if policy.Allowed(protected) {
		t.Fatal("Policy.Allowed() = true, want false")
	}

	if addresses := dialer.addressSnapshot(); len(addresses) != 1 {
		t.Errorf(
			"dial addresses = %v, want one initial-origin dial",
			addresses,
		)
	}
}

func TestGuardedHTTPRejectsInvalidInputs(t *testing.T) {
	target := mustTarget(t, "https://example.com/robots.txt")
	getter := &guardedHTTP{}

	t.Run("nil receiver", func(t *testing.T) {
		var absent *guardedHTTP

		if _, err := absent.get(
			context.Background(),
			target,
		); !errors.Is(err, errHTTPGetterUnavailable) {
			t.Fatalf(
				"guardedHTTP.get() error = %v, want unavailable",
				err,
			)
		}
	})

	t.Run("nil context", func(t *testing.T) {
		if _, err := getter.get(
			nil,
			target,
		); !errors.Is(err, errInvalidContext) {
			t.Fatalf(
				"guardedHTTP.get() error = %v, want invalid context",
				err,
			)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if _, err := getter.get(
			ctx,
			target,
		); !errors.Is(err, context.Canceled) {
			t.Fatalf(
				"guardedHTTP.get() error = %v, want canceled",
				err,
			)
		}
	})

	t.Run("nil target", func(t *testing.T) {
		if _, err := getter.get(
			context.Background(),
			nil,
		); !errors.Is(err, errInvalidHTTPTarget) {
			t.Fatalf(
				"guardedHTTP.get() error = %v, want invalid target",
				err,
			)
		}
	})

	t.Run("invalid target origin", func(t *testing.T) {
		if _, err := getter.get(
			context.Background(),
			&url.URL{
				Scheme: "file",
				Path:   "/private",
			},
		); !errors.Is(err, errInvalidHTTPTarget) {
			t.Fatalf(
				"guardedHTTP.get() error = %v, want invalid target",
				err,
			)
		}
	})
}

func TestGuardedTransportDisablesProxyAndTLSBypass(t *testing.T) {
	resolver := &robotsResolver{
		addresses: []netip.Addr{
			netip.MustParseAddr("93.184.216.34"),
		},
	}
	candidate := mustWebOrigin(t, "https://example.com")

	destination, err := netguard.Resolve(
		context.Background(),
		candidate,
		resolver,
	)
	if err != nil {
		t.Fatalf("netguard.Resolve() error = %v, want nil", err)
	}

	transport := newGuardedTransport(
		destination,
		&recordingDialer{},
		nil,
	)
	t.Cleanup(transport.CloseIdleConnections)

	if transport.Proxy != nil {
		t.Fatal("Transport.Proxy is non-nil, want nil")
	}

	if transport.DialTLSContext != nil {
		t.Fatal("Transport.DialTLSContext is non-nil, want nil")
	}

	if transport.TLSClientConfig == nil {
		t.Fatal("Transport.TLSClientConfig is nil")
	}

	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("Transport uses InsecureSkipVerify")
	}
}

type hopStep struct {
	response *http.Response
	err      error
}

type fakeHopGetter struct {
	steps   []hopStep
	targets []*url.URL
}

func (g *fakeHopGetter) get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	copiedTarget := *target
	g.targets = append(g.targets, &copiedTarget)

	if len(g.steps) == 0 {
		return nil, errors.New("unexpected HTTP attempt")
	}

	step := g.steps[0]
	g.steps = g.steps[1:]

	if step.response != nil {
		step.response.Request = &http.Request{
			Method: http.MethodGet,
			URL:    &copiedTarget,
		}
	}

	return step.response, step.err
}

func (g *fakeHopGetter) targetStrings() []string {
	targets := make([]string, 0, len(g.targets))
	for _, target := range g.targets {
		targets = append(targets, target.String())
	}

	return targets
}

func robotsResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func responseWithHeader(
	status int,
	body string,
	name string,
	value string,
) *http.Response {
	response := robotsResponse(status, body)
	response.Header.Set(name, value)

	return response
}

func redirectResponse(status int, location string) *http.Response {
	return responseWithHeader(
		status,
		"",
		"Location",
		location,
	)
}

func disallowingResponse() *http.Response {
	return robotsResponse(
		http.StatusOK,
		"User-agent: Joshternet-Joshbot\n"+
			"Disallow: /private\n",
	)
}

func successfulFiveRedirectChain() []hopStep {
	steps := redirectChain(5)
	steps = append(
		steps,
		hopStep{
			response: disallowingResponse(),
		},
	)

	return steps
}

func redirectChain(count int) []hopStep {
	statuses := []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	}

	steps := make([]hopStep, 0, count)
	for index := 1; index <= count; index++ {
		steps = append(
			steps,
			hopStep{
				response: redirectResponse(
					statuses[(index-1)%len(statuses)],
					fmt.Sprintf("/redirect-%d", index),
				),
			},
		)
	}

	return steps
}

func redirectTargets(count int, includeFinal bool) []string {
	targets := []string{
		"https://example.com/robots.txt",
	}

	for index := 1; index <= count; index++ {
		targets = append(
			targets,
			fmt.Sprintf(
				"https://example.com/redirect-%d",
				index,
			),
		)
	}

	if !includeFinal {
		return targets[:len(targets)-1]
	}

	return targets
}

type requestObservation struct {
	host       string
	path       string
	userAgent  string
	serverName string
}

type robotsResolver struct {
	addresses []netip.Addr
	calls     []string
}

func (r *robotsResolver) LookupNetIP(
	_ context.Context,
	_ string,
	host string,
) ([]netip.Addr, error) {
	r.calls = append(r.calls, host)

	return append([]netip.Addr(nil), r.addresses...), nil
}

type recordingDialer struct {
	mu        sync.Mutex
	addresses []string
	dial      func(
		context.Context,
		string,
		string,
	) (net.Conn, error)
}

func (d *recordingDialer) DialContext(
	ctx context.Context,
	network string,
	address string,
) (net.Conn, error) {
	d.mu.Lock()
	d.addresses = append(d.addresses, address)
	d.mu.Unlock()

	if d.dial == nil {
		return nil, errors.New("unexpected dial")
	}

	return d.dial(ctx, network, address)
}

func (d *recordingDialer) addressSnapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	return append([]string(nil), d.addresses...)
}

func mustWebOrigin(
	t *testing.T,
	rawURL string,
) origin.Origin {
	t.Helper()

	parsed, err := origin.Parse(rawURL)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v, want nil",
			rawURL,
			err,
		)
	}

	return parsed
}

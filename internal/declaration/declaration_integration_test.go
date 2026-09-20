package declaration_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/robots"
)

type declarationIntegrationResolver struct{}

func (declarationIntegrationResolver) LookupNetIP(
	context.Context,
	string,
	string,
) ([]netip.Addr, error) {
	return []netip.Addr{
		netip.MustParseAddr("93.184.216.34"),
	}, nil
}

type declarationIntegrationDialer struct {
	target string
}

func (dialer declarationIntegrationDialer) DialContext(
	ctx context.Context,
	network string,
	_ string,
) (net.Conn, error) {
	var system net.Dialer
	return system.DialContext(ctx, network, dialer.target)
}

func TestDeclarationIntegrationVerifiesThroughRobotsAndGuardedHTTP(
	t *testing.T,
) {
	var paths []string
	var pathsMu sync.Mutex

	server := httptest.NewServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			pathsMu.Lock()
			paths = append(paths, request.URL.Path)
			pathsMu.Unlock()

			if request.UserAgent() != robots.UserAgent {
				t.Errorf(
					"User-Agent = %q, want %q",
					request.UserAgent(),
					robots.UserAgent,
				)
			}

			switch request.URL.Path {
			case "/robots.txt":
				_, _ = writer.Write([]byte(
					"User-agent: Joshternet-Joshbot\nAllow: /\n",
				))
			case declaration.WellKnownPath:
				writer.Header().Set("Location", "/josh.json")
				writer.WriteHeader(http.StatusFound)
			case "/josh.json":
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(`{"version":1,"josh":true}`))
			default:
				http.NotFound(writer, request)
			}
		}),
	)
	defer server.Close()

	verifier, source := newDeclarationIntegrationVerifier(t, server)

	result, err := verifier.Verify(context.Background(), source)
	if err != nil {
		t.Fatalf("Verify() error = %v, want nil", err)
	}

	if result.Outcome != declaration.OutcomeValid {
		t.Fatalf(
			"Verify() outcome = %v, want %v",
			result.Outcome,
			declaration.OutcomeValid,
		)
	}
	if result.Origin != source {
		t.Errorf(
			"Verify() origin = %q, want %q",
			result.Origin.String(),
			source.String(),
		)
	}
	if result.Declaration != (declaration.Declaration{
		Version:  1,
		Identity: declaration.IdentityAffirmed,
	}) {
		t.Errorf(
			"Verify() declaration = %#v",
			result.Declaration,
		)
	}
	if result.FailureCategory != retry.CategoryNone {
		t.Errorf(
			"Verify() failure category = %q, want empty",
			result.FailureCategory,
		)
	}
	if result.RetryAfter != 0 {
		t.Errorf(
			"Verify() RetryAfter = %v, want 0",
			result.RetryAfter,
		)
	}

	wantPaths := []string{
		"/robots.txt",
		declaration.WellKnownPath,
		"/josh.json",
	}

	pathsMu.Lock()
	gotPaths := append([]string(nil), paths...)
	pathsMu.Unlock()

	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Errorf(
			"request paths = %#v, want %#v",
			gotPaths,
			wantPaths,
		)
	}
}

func TestDeclarationIntegrationHonorsRobotsDenial(
	t *testing.T,
) {
	declarationRequested := false

	server := httptest.NewServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			switch request.URL.Path {
			case "/robots.txt":
				_, _ = writer.Write([]byte(
					"User-agent: Joshternet-Joshbot\nDisallow: /.well-known/josh\n",
				))
			case declaration.WellKnownPath:
				declarationRequested = true
				_, _ = writer.Write([]byte(`{"version":1}`))
			default:
				http.NotFound(writer, request)
			}
		}),
	)
	defer server.Close()

	verifier, source := newDeclarationIntegrationVerifier(t, server)

	result, err := verifier.Verify(context.Background(), source)
	if err != nil {
		t.Fatalf("Verify() error = %v, want nil", err)
	}

	if result.Outcome != declaration.OutcomeRobotsDenied {
		t.Fatalf(
			"Verify() outcome = %v, want %v",
			result.Outcome,
			declaration.OutcomeRobotsDenied,
		)
	}
	if declarationRequested {
		t.Fatal(
			"declaration resource was requested despite robots denial",
		)
	}
}

func TestDeclarationIntegrationClassifiesRemoteResponses(
	t *testing.T,
) {
	tests := []struct {
		name           string
		status         int
		body           string
		retryAfter     string
		wantOutcome    declaration.Outcome
		wantFailure    retry.Category
		wantRetryAfter time.Duration
	}{
		{
			name:        "absent",
			status:      http.StatusNotFound,
			wantOutcome: declaration.OutcomeAbsent,
		},
		{
			name:        "invalid declaration",
			status:      http.StatusOK,
			body:        `{"version":`,
			wantOutcome: declaration.OutcomeInvalid,
		},
		{
			name:        "unsupported declaration",
			status:      http.StatusOK,
			body:        `{"version":2}`,
			wantOutcome: declaration.OutcomeUnsupportedVersion,
		},
		{
			name:           "temporary service failure",
			status:         http.StatusServiceUnavailable,
			retryAfter:     "120",
			wantOutcome:    declaration.OutcomeUnavailable,
			wantFailure:    retry.CategoryHTTP5xx,
			wantRetryAfter: 2 * time.Minute,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(
					writer http.ResponseWriter,
					request *http.Request,
				) {
					if request.URL.Path == "/robots.txt" {
						_, _ = writer.Write([]byte(
							"User-agent: Joshternet-Joshbot\nAllow: /\n",
						))
						return
					}

					if request.URL.Path != declaration.WellKnownPath {
						http.NotFound(writer, request)
						return
					}

					if test.retryAfter != "" {
						writer.Header().Set(
							"Retry-After",
							test.retryAfter,
						)
					}
					writer.WriteHeader(test.status)
					_, _ = writer.Write(
						[]byte(test.body),
					)
				}),
			)
			defer server.Close()

			verifier, source :=
				newDeclarationIntegrationVerifier(
					t,
					server,
				)

			result, err := verifier.Verify(
				context.Background(),
				source,
			)
			if err != nil {
				t.Fatalf(
					"Verify() error = %v, want nil",
					err,
				)
			}

			if result.Outcome != test.wantOutcome {
				t.Errorf(
					"outcome = %v, want %v",
					result.Outcome,
					test.wantOutcome,
				)
			}
			if result.FailureCategory !=
				test.wantFailure {
				t.Errorf(
					"failure category = %q, want %q",
					result.FailureCategory,
					test.wantFailure,
				)
			}
			if result.RetryAfter !=
				test.wantRetryAfter {
				t.Errorf(
					"RetryAfter = %v, want %v",
					result.RetryAfter,
					test.wantRetryAfter,
				)
			}
		})
	}
}

func TestDeclarationIntegrationRejectsCrossOriginRedirect(
	t *testing.T,
) {
	server := httptest.NewServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			if request.URL.Path == "/robots.txt" {
				_, _ = writer.Write([]byte(
					"User-agent: Joshternet-Joshbot\nAllow: /\n",
				))
				return
			}

			writer.Header().Set(
				"Location",
				"http://other.example/.well-known/josh",
			)
			writer.WriteHeader(http.StatusFound)
		}),
	)
	defer server.Close()

	verifier, source := newDeclarationIntegrationVerifier(
		t,
		server,
	)

	result, err := verifier.Verify(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Verify() error = %v, want nil",
			err,
		)
	}

	if result.Outcome !=
		declaration.OutcomeCrossOriginRedirect {
		t.Fatalf(
			"outcome = %v, want %v",
			result.Outcome,
			declaration.OutcomeCrossOriginRedirect,
		)
	}
}

func newDeclarationIntegrationVerifier(
	t *testing.T,
	server *httptest.Server,
) (*declaration.Verifier, origin.Origin) {
	t.Helper()

	source, err := origin.Parse(
		"http://example.com",
	)
	if err != nil {
		t.Fatalf(
			"origin.Parse() error = %v",
			err,
		)
	}

	checker := robots.NewCheckerWithRequestDelayAndSigner(
		declarationIntegrationResolver{},
		declarationIntegrationDialer{
			target: server.Listener.Addr().String(),
		},
		0,
		nil,
	)

	return declaration.NewVerifier(checker), source
}

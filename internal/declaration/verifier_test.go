package declaration

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/robots"
)

func TestVerifierRetrievesCanonicalDeclarationTarget(
	t *testing.T,
) {
	tests := []struct {
		name       string
		source     string
		wantTarget string
	}{
		{
			name:       "default HTTPS port",
			source:     "https://example.com/ignored",
			wantTarget: "https://example.com/.well-known/josh",
		},
		{
			name:       "nondefault HTTPS port",
			source:     "https://example.org:8443/ignored",
			wantTarget: "https://example.org:8443/.well-known/josh",
		},
		{
			name:       "default HTTP port",
			source:     "http://example.net:80/ignored",
			wantTarget: "http://example.net/.well-known/josh",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := mustDeclarationOrigin(t, test.source)
			getter := newScriptedDeclarationGetter(
				t,
				declarationGetterStep{
					response: declarationTextResponse(
						http.StatusOK,
						`{"version":1}`,
					),
				},
			)

			got, err := NewVerifier(getter).Verify(
				context.Background(),
				source,
			)
			if err != nil {
				t.Fatalf("Verify() error = %v, want nil", err)
			}

			assertDeclarationResult(
				t,
				got,
				Result{
					Outcome: OutcomeValid,
					Origin:  source,
					Declaration: Declaration{
						Version:  1,
						Identity: IdentityUndeclared,
					},
				},
			)

			wantTargets := []string{test.wantTarget}
			if !slices.Equal(getter.targets, wantTargets) {
				t.Errorf(
					"getter targets = %q, want %q",
					getter.targets,
					wantTargets,
				)
			}

			assertDeclarationBodiesClosed(t, getter)
		})
	}
}

func TestVerifierClassifiesHTTPOutcomes(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		contentType string
		wantOutcome Outcome
		want        Declaration
	}{
		{
			name:        "200 valid without Content-Type",
			status:      http.StatusOK,
			body:        `{"version":1}`,
			wantOutcome: OutcomeValid,
			want: Declaration{
				Version:  1,
				Identity: IdentityUndeclared,
			},
		},
		{
			name:        "201 valid",
			status:      http.StatusCreated,
			body:        `{"version":1,"josh":true}`,
			contentType: "application/json",
			wantOutcome: OutcomeValid,
			want: Declaration{
				Version:  1,
				Identity: IdentityAffirmed,
			},
		},
		{
			name:        "226 valid with text Content-Type",
			status:      http.StatusIMUsed,
			body:        `{"version":1,"josh":false}`,
			contentType: "text/plain",
			wantOutcome: OutcomeValid,
			want: Declaration{
				Version:  1,
				Identity: IdentityDeclined,
			},
		},
		{
			name:        "204 has no declaration",
			status:      http.StatusNoContent,
			wantOutcome: OutcomeInvalid,
		},
		{
			name:        "invalid successful body",
			status:      http.StatusOK,
			body:        `{"version":`,
			wantOutcome: OutcomeInvalid,
		},
		{
			name:        "unsupported version",
			status:      http.StatusOK,
			body:        `{"version":2,"josh":true}`,
			wantOutcome: OutcomeUnsupportedVersion,
		},
		{
			name:        "404 absent",
			status:      http.StatusNotFound,
			body:        "not found",
			wantOutcome: OutcomeAbsent,
		},
		{
			name:        "410 absent",
			status:      http.StatusGone,
			body:        "gone",
			wantOutcome: OutcomeAbsent,
		},
		{
			name:        "400 unavailable",
			status:      http.StatusBadRequest,
			body:        "bad request",
			wantOutcome: OutcomeUnavailable,
		},
		{
			name:        "401 unavailable",
			status:      http.StatusUnauthorized,
			body:        "authentication required",
			wantOutcome: OutcomeUnavailable,
		},
		{
			name:        "403 unavailable",
			status:      http.StatusForbidden,
			body:        "forbidden",
			wantOutcome: OutcomeUnavailable,
		},
		{
			name:        "429 unavailable",
			status:      http.StatusTooManyRequests,
			body:        "try later",
			wantOutcome: OutcomeUnavailable,
		},
		{
			name:        "500 unavailable",
			status:      http.StatusInternalServerError,
			body:        "server failure",
			wantOutcome: OutcomeUnavailable,
		},
		{
			name:        "503 unavailable",
			status:      http.StatusServiceUnavailable,
			body:        "unavailable",
			wantOutcome: OutcomeUnavailable,
		},
		{
			name:        "300 unavailable",
			status:      http.StatusMultipleChoices,
			body:        "multiple choices",
			wantOutcome: OutcomeUnavailable,
		},
		{
			name:        "304 unavailable",
			status:      http.StatusNotModified,
			wantOutcome: OutcomeUnavailable,
		},
		{
			name:        "unexpected status unavailable",
			status:      600,
			body:        "unexpected",
			wantOutcome: OutcomeUnavailable,
		},
	}

	source := mustDeclarationOrigin(t, "https://example.com")

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := declarationTextResponse(
				test.status,
				test.body,
			)
			if test.contentType != "" {
				response.Header.Set(
					"Content-Type",
					test.contentType,
				)
			}

			getter := newScriptedDeclarationGetter(
				t,
				declarationGetterStep{
					response: response,
				},
			)

			got, err := NewVerifier(getter).Verify(
				context.Background(),
				source,
			)
			if err != nil {
				t.Fatalf("Verify() error = %v, want nil", err)
			}

			assertDeclarationResult(
				t,
				got,
				Result{
					Outcome:     test.wantOutcome,
					Origin:      source,
					Declaration: test.want,
				},
			)
			assertDeclarationBodiesClosed(t, getter)
		})
	}
}

func TestVerifierHandlesDeclarationBodyFailures(t *testing.T) {
	source := mustDeclarationOrigin(t, "https://example.com")

	t.Run("exact body limit", func(t *testing.T) {
		minimum := `{"version":1}`
		body := minimum + strings.Repeat(
			" ",
			MaxBodySize-len(minimum),
		)
		getter := newScriptedDeclarationGetter(
			t,
			declarationGetterStep{
				response: declarationTextResponse(
					http.StatusOK,
					body,
				),
			},
		)

		got, err := NewVerifier(getter).Verify(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf("Verify() error = %v, want nil", err)
		}

		assertDeclarationResult(
			t,
			got,
			Result{
				Outcome: OutcomeValid,
				Origin:  source,
				Declaration: Declaration{
					Version:  1,
					Identity: IdentityUndeclared,
				},
			},
		)
		assertDeclarationBodiesClosed(t, getter)
	})

	t.Run("oversized body", func(t *testing.T) {
		reader := &countingDeclarationReader{
			remaining: MaxBodySize + 100,
		}
		getter := newScriptedDeclarationGetter(
			t,
			declarationGetterStep{
				response: declarationReaderResponse(
					http.StatusOK,
					reader,
				),
			},
		)

		got, err := NewVerifier(getter).Verify(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf("Verify() error = %v, want nil", err)
		}

		assertDeclarationResult(
			t,
			got,
			Result{
				Outcome: OutcomeInvalid,
				Origin:  source,
			},
		)

		if reader.read != MaxBodySize+1 {
			t.Errorf(
				"body bytes read = %d, want %d",
				reader.read,
				MaxBodySize+1,
			)
		}

		assertDeclarationBodiesClosed(t, getter)
	})

	t.Run("partial read failure", func(t *testing.T) {
		readFailure := errors.New(
			"test declaration read failure",
		)
		getter := newScriptedDeclarationGetter(
			t,
			declarationGetterStep{
				response: declarationReaderResponse(
					http.StatusOK,
					&failingDeclarationReader{
						data: []byte(`{"version":1}`),
						err:  readFailure,
					},
				),
			},
		)

		got, err := NewVerifier(getter).Verify(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf("Verify() error = %v, want nil", err)
		}

		assertDeclarationResult(
			t,
			got,
			Result{
				Outcome: OutcomeUnavailable,
				Origin:  source,
			},
		)
		assertDeclarationBodiesClosed(t, getter)
	})

	t.Run("nil response", func(t *testing.T) {
		getter := newScriptedDeclarationGetter(
			t,
			declarationGetterStep{},
		)

		got, err := NewVerifier(getter).Verify(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf("Verify() error = %v, want nil", err)
		}

		assertDeclarationResult(
			t,
			got,
			Result{
				Outcome: OutcomeUnavailable,
				Origin:  source,
			},
		)
	})

	t.Run("nil response body", func(t *testing.T) {
		getter := newScriptedDeclarationGetter(
			t,
			declarationGetterStep{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
				},
			},
		)

		got, err := NewVerifier(getter).Verify(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf("Verify() error = %v, want nil", err)
		}

		assertDeclarationResult(
			t,
			got,
			Result{
				Outcome: OutcomeUnavailable,
				Origin:  source,
			},
		)
	})

	t.Run("network failure", func(t *testing.T) {
		getter := newScriptedDeclarationGetter(
			t,
			declarationGetterStep{
				err: errors.New("test network failure"),
			},
		)

		got, err := NewVerifier(getter).Verify(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf("Verify() error = %v, want nil", err)
		}

		assertDeclarationResult(
			t,
			got,
			Result{
				Outcome: OutcomeUnavailable,
				Origin:  source,
			},
		)
	})

	t.Run("response returned with getter failure", func(t *testing.T) {
		getter := newScriptedDeclarationGetter(
			t,
			declarationGetterStep{
				response: declarationTextResponse(
					http.StatusOK,
					`{"version":1}`,
				),
				err: errors.New("test getter failure"),
			},
		)

		got, err := NewVerifier(getter).Verify(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf("Verify() error = %v, want nil", err)
		}

		assertDeclarationResult(
			t,
			got,
			Result{
				Outcome: OutcomeUnavailable,
				Origin:  source,
			},
		)
		assertDeclarationBodiesClosed(t, getter)
	})
}

func TestVerifierDistinguishesRobotsDenial(t *testing.T) {
	source := mustDeclarationOrigin(t, "https://example.com")
	getter := &robotsBlockingDeclarationGetter{}

	got, err := NewVerifier(getter).Verify(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf("Verify() error = %v, want nil", err)
	}

	assertDeclarationResult(
		t,
		got,
		Result{
			Outcome: OutcomeRobotsDenied,
			Origin:  source,
		},
	)

	if getter.robotsChecks != 1 {
		t.Errorf(
			"robots checks = %d, want 1",
			getter.robotsChecks,
		)
	}

	if getter.declarationRequests != 0 {
		t.Errorf(
			"declaration requests = %d, want 0",
			getter.declarationRequests,
		)
	}
}

func TestVerifierRejectsInvalidInputs(t *testing.T) {
	source := mustDeclarationOrigin(t, "https://example.com")
	getter := newScriptedDeclarationGetter(t)

	tests := []struct {
		name     string
		verifier *Verifier
		ctx      context.Context
		source   origin.Origin
		wantErr  error
	}{
		{
			name:     "nil verifier",
			verifier: nil,
			ctx:      context.Background(),
			source:   source,
			wantErr:  errVerifierUnavailable,
		},
		{
			name:     "nil context",
			verifier: NewVerifier(getter),
			ctx:      nil,
			source:   source,
			wantErr:  errInvalidDeclarationContext,
		},
		{
			name:     "zero origin",
			verifier: NewVerifier(getter),
			ctx:      context.Background(),
			source:   origin.Origin{},
			wantErr:  errInvalidDeclarationOrigin,
		},
		{
			name:     "nil getter",
			verifier: NewVerifier(nil),
			ctx:      context.Background(),
			source:   source,
			wantErr:  errDeclarationGetterUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.verifier.Verify(
				test.ctx,
				test.source,
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf(
					"Verify() error = %v, want %v",
					err,
					test.wantErr,
				)
			}

			if got != (Result{}) {
				t.Errorf(
					"Verify() result = %#v, want zero value",
					got,
				)
			}
		})
	}
}

func TestVerifierPreservesContextCancellation(t *testing.T) {
	source := mustDeclarationOrigin(t, "https://example.com")

	t.Run("before fetch", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		cancel()

		getter := newScriptedDeclarationGetter(t)
		got, err := NewVerifier(getter).Verify(ctx, source)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf(
				"Verify() error = %v, want context.Canceled",
				err,
			)
		}

		if got != (Result{}) {
			t.Errorf(
				"Verify() result = %#v, want zero value",
				got,
			)
		}

		if len(getter.targets) != 0 {
			t.Errorf(
				"getter calls = %d, want 0",
				len(getter.targets),
			)
		}
	})

	t.Run("during fetch", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		body := declarationTextResponse(
			http.StatusOK,
			`{"version":1}`,
		)
		getter := &cancelingDeclarationGetter{
			cancel:   cancel,
			response: body,
			err:      errors.New("test fetch failure"),
		}

		got, err := NewVerifier(getter).Verify(ctx, source)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf(
				"Verify() error = %v, want context.Canceled",
				err,
			)
		}

		if got != (Result{}) {
			t.Errorf(
				"Verify() result = %#v, want zero value",
				got,
			)
		}

		tracked := body.Body.(*trackingDeclarationBody)
		if tracked.closeCount != 1 {
			t.Errorf(
				"body close count = %d, want 1",
				tracked.closeCount,
			)
		}
	})

	t.Run("while reading body", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		getter := newScriptedDeclarationGetter(
			t,
			declarationGetterStep{
				response: declarationReaderResponse(
					http.StatusOK,
					&cancelingDeclarationReader{
						cancel: cancel,
					},
				),
			},
		)

		got, err := NewVerifier(getter).Verify(ctx, source)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf(
				"Verify() error = %v, want context.Canceled",
				err,
			)
		}

		if got != (Result{}) {
			t.Errorf(
				"Verify() result = %#v, want zero value",
				got,
			)
		}

		assertDeclarationBodiesClosed(t, getter)
	})
}

func TestVerifierFollowsSameOriginRedirects(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		location   string
		wantTarget string
	}{
		{
			name:       "relative",
			status:     http.StatusMovedPermanently,
			location:   "/moved",
			wantTarget: "https://example.com/moved",
		},
		{
			name:       "absolute",
			status:     http.StatusFound,
			location:   "https://example.com/moved",
			wantTarget: "https://example.com/moved",
		},
		{
			name:       "explicit default port",
			status:     http.StatusSeeOther,
			location:   "https://example.com:443/moved",
			wantTarget: "https://example.com:443/moved",
		},
		{
			name:       "fragment removed",
			status:     http.StatusTemporaryRedirect,
			location:   "/moved#section",
			wantTarget: "https://example.com/moved",
		},
		{
			name:       "query preserved",
			status:     http.StatusPermanentRedirect,
			location:   "/moved?revision=1",
			wantTarget: "https://example.com/moved?revision=1",
		},
	}

	source := mustDeclarationOrigin(t, "https://example.com")

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getter := newScriptedDeclarationGetter(
				t,
				declarationGetterStep{
					response: declarationRedirectResponse(
						test.status,
						test.location,
					),
				},
				declarationGetterStep{
					response: declarationTextResponse(
						http.StatusOK,
						`{"version":1}`,
					),
				},
			)

			got, err := NewVerifier(getter).Verify(
				context.Background(),
				source,
			)
			if err != nil {
				t.Fatalf("Verify() error = %v, want nil", err)
			}

			assertDeclarationResult(
				t,
				got,
				Result{
					Outcome: OutcomeValid,
					Origin:  source,
					Declaration: Declaration{
						Version:  1,
						Identity: IdentityUndeclared,
					},
				},
			)

			wantTargets := []string{
				"https://example.com/.well-known/josh",
				test.wantTarget,
			}
			if !slices.Equal(getter.targets, wantTargets) {
				t.Errorf(
					"getter targets = %q, want %q",
					getter.targets,
					wantTargets,
				)
			}

			assertDeclarationBodiesClosed(t, getter)
		})
	}
}

func TestVerifierFollowsFiveSameOriginRedirects(
	t *testing.T,
) {
	source := mustDeclarationOrigin(t, "https://example.com")
	getter := newScriptedDeclarationGetter(
		t,
		declarationGetterStep{
			response: declarationRedirectResponse(
				http.StatusMovedPermanently,
				"/one",
			),
		},
		declarationGetterStep{
			response: declarationRedirectResponse(
				http.StatusFound,
				"/two",
			),
		},
		declarationGetterStep{
			response: declarationRedirectResponse(
				http.StatusSeeOther,
				"/three",
			),
		},
		declarationGetterStep{
			response: declarationRedirectResponse(
				http.StatusTemporaryRedirect,
				"/four",
			),
		},
		declarationGetterStep{
			response: declarationRedirectResponse(
				http.StatusPermanentRedirect,
				"/five",
			),
		},
		declarationGetterStep{
			response: declarationTextResponse(
				http.StatusOK,
				`{"version":1,"josh":true}`,
			),
		},
	)

	got, err := NewVerifier(getter).Verify(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf("Verify() error = %v, want nil", err)
	}

	assertDeclarationResult(
		t,
		got,
		Result{
			Outcome: OutcomeValid,
			Origin:  source,
			Declaration: Declaration{
				Version:  1,
				Identity: IdentityAffirmed,
			},
		},
	)

	wantTargets := []string{
		"https://example.com/.well-known/josh",
		"https://example.com/one",
		"https://example.com/two",
		"https://example.com/three",
		"https://example.com/four",
		"https://example.com/five",
	}
	if !slices.Equal(getter.targets, wantTargets) {
		t.Errorf(
			"getter targets = %q, want %q",
			getter.targets,
			wantTargets,
		)
	}

	assertDeclarationBodiesClosed(t, getter)
}

func TestVerifierRejectsRedirectFailures(t *testing.T) {
	source := mustDeclarationOrigin(t, "https://example.com")

	tests := []struct {
		name      string
		steps     []declarationGetterStep
		wantCalls int
	}{
		{
			name: "required sixth redirect",
			steps: []declarationGetterStep{
				{
					response: declarationRedirectResponse(
						http.StatusFound,
						"/one",
					),
				},
				{
					response: declarationRedirectResponse(
						http.StatusFound,
						"/two",
					),
				},
				{
					response: declarationRedirectResponse(
						http.StatusFound,
						"/three",
					),
				},
				{
					response: declarationRedirectResponse(
						http.StatusFound,
						"/four",
					),
				},
				{
					response: declarationRedirectResponse(
						http.StatusFound,
						"/five",
					),
				},
				{
					response: declarationRedirectResponse(
						http.StatusFound,
						"/six",
					),
				},
			},
			wantCalls: 6,
		},
		{
			name: "missing Location",
			steps: []declarationGetterStep{
				{
					response: declarationTextResponse(
						http.StatusFound,
						"",
					),
				},
			},
			wantCalls: 1,
		},
		{
			name: "malformed Location",
			steps: []declarationGetterStep{
				{
					response: declarationRedirectResponse(
						http.StatusFound,
						"http://[::1",
					),
				},
			},
			wantCalls: 1,
		},
		{
			name: "unsupported redirect scheme",
			steps: []declarationGetterStep{
				{
					response: declarationRedirectResponse(
						http.StatusFound,
						"file:///private",
					),
				},
			},
			wantCalls: 1,
		},
		{
			name: "credential-bearing redirect",
			steps: []declarationGetterStep{
				{
					response: declarationRedirectResponse(
						http.StatusFound,
						"https://user:secret@example.com/private",
					),
				},
			},
			wantCalls: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getter := newScriptedDeclarationGetter(
				t,
				test.steps...,
			)

			got, err := NewVerifier(getter).Verify(
				context.Background(),
				source,
			)
			if err != nil {
				t.Fatalf("Verify() error = %v, want nil", err)
			}

			assertDeclarationResult(
				t,
				got,
				Result{
					Outcome: OutcomeUnavailable,
					Origin:  source,
				},
			)

			if len(getter.targets) != test.wantCalls {
				t.Errorf(
					"getter calls = %d, want %d",
					len(getter.targets),
					test.wantCalls,
				)
			}

			assertDeclarationBodiesClosed(t, getter)
		})
	}
}

func TestVerifierRejectsCrossOriginRedirects(t *testing.T) {
	tests := []struct {
		name     string
		location string
	}{
		{
			name:     "different hostname",
			location: "https://example.net/declaration",
		},
		{
			name:     "subdomain",
			location: "https://www.example.com/declaration",
		},
		{
			name:     "scheme change",
			location: "http://example.com/declaration",
		},
		{
			name:     "port change",
			location: "https://example.com:8443/declaration",
		},
	}

	source := mustDeclarationOrigin(t, "https://example.com")

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getter := newScriptedDeclarationGetter(
				t,
				declarationGetterStep{
					response: declarationRedirectResponse(
						http.StatusFound,
						test.location,
					),
				},
			)

			got, err := NewVerifier(getter).Verify(
				context.Background(),
				source,
			)
			if err != nil {
				t.Fatalf("Verify() error = %v, want nil", err)
			}

			assertDeclarationResult(
				t,
				got,
				Result{
					Outcome: OutcomeCrossOriginRedirect,
					Origin:  source,
				},
			)

			if len(getter.targets) != 1 {
				t.Errorf(
					"getter calls = %d, want 1",
					len(getter.targets),
				)
			}

			assertDeclarationBodiesClosed(t, getter)
		})
	}
}

type declarationGetterStep struct {
	response *http.Response
	err      error
}

type scriptedDeclarationGetter struct {
	t       *testing.T
	steps   []declarationGetterStep
	targets []string
	bodies  []*trackingDeclarationBody
}

func newScriptedDeclarationGetter(
	t *testing.T,
	steps ...declarationGetterStep,
) *scriptedDeclarationGetter {
	t.Helper()

	getter := &scriptedDeclarationGetter{
		t:     t,
		steps: append([]declarationGetterStep(nil), steps...),
	}

	for _, step := range steps {
		if step.response == nil || step.response.Body == nil {
			continue
		}

		body, ok := step.response.Body.(*trackingDeclarationBody)
		if ok {
			getter.bodies = append(getter.bodies, body)
		}
	}

	return getter
}

func (g *scriptedDeclarationGetter) Get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	g.t.Helper()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	g.targets = append(g.targets, target.String())
	if len(g.steps) == 0 {
		g.t.Fatalf(
			"unexpected declaration getter call for %q",
			target,
		)
		return nil, nil
	}

	step := g.steps[0]
	g.steps = g.steps[1:]

	return step.response, step.err
}

type robotsBlockingDeclarationGetter struct {
	robotsChecks        int
	declarationRequests int
	allowed             bool
}

func (g *robotsBlockingDeclarationGetter) Get(
	context.Context,
	*url.URL,
) (*http.Response, error) {
	g.robotsChecks++

	if !g.allowed {
		return nil, robots.ErrDisallowed
	}

	g.declarationRequests++

	return declarationTextResponse(
		http.StatusOK,
		`{"version":1}`,
	), nil
}

type cancelingDeclarationGetter struct {
	cancel   context.CancelFunc
	response *http.Response
	err      error
}

func (g *cancelingDeclarationGetter) Get(
	context.Context,
	*url.URL,
) (*http.Response, error) {
	g.cancel()

	return g.response, g.err
}

type cancelingDeclarationReader struct {
	cancel context.CancelFunc
}

func (r *cancelingDeclarationReader) Read(
	[]byte,
) (int, error) {
	r.cancel()

	return 0, errors.New("test canceled body read")
}

type trackingDeclarationBody struct {
	reader     io.Reader
	closeCount int
}

func (b *trackingDeclarationBody) Read(
	destination []byte,
) (int, error) {
	return b.reader.Read(destination)
}

func (b *trackingDeclarationBody) Close() error {
	b.closeCount++

	return nil
}

func declarationTextResponse(
	status int,
	body string,
) *http.Response {
	return declarationReaderResponse(
		status,
		strings.NewReader(body),
	)
}

func declarationReaderResponse(
	status int,
	body io.Reader,
) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body: &trackingDeclarationBody{
			reader: body,
		},
	}
}

func declarationRedirectResponse(
	status int,
	location string,
) *http.Response {
	response := declarationTextResponse(status, "")
	response.Header.Set("Location", location)

	return response
}

func mustDeclarationOrigin(
	t *testing.T,
	rawURL string,
) origin.Origin {
	t.Helper()

	got, err := origin.Parse(rawURL)
	if err != nil {
		t.Fatalf("origin.Parse(%q) error = %v", rawURL, err)
	}

	return got
}

func assertDeclarationResult(
	t *testing.T,
	got Result,
	want Result,
) {
	t.Helper()

	if got != want {
		t.Errorf(
			"Verify() result = %#v, want %#v",
			got,
			want,
		)
	}
}

func assertDeclarationBodiesClosed(
	t *testing.T,
	getter *scriptedDeclarationGetter,
) {
	t.Helper()

	for index, body := range getter.bodies {
		if body.closeCount != 1 {
			t.Errorf(
				"response body %d close count = %d, want 1",
				index,
				body.closeCount,
			)
		}
	}
}

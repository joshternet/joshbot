package declaration_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/robots"
)

type declarationFailureIntegrationClock struct {
	now time.Time
}

func (clock declarationFailureIntegrationClock) Now() time.Time {
	return clock.now
}

type declarationFailureIntegrationGetter struct {
	get func(context.Context, *url.URL) (*http.Response, error)
}

func (getter declarationFailureIntegrationGetter) Get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	return getter.get(ctx, target)
}

type declarationFailureIntegrationBody struct {
	reader io.Reader
	closed bool
}

func (body *declarationFailureIntegrationBody) Read(
	buffer []byte,
) (int, error) {
	return body.reader.Read(buffer)
}

func (body *declarationFailureIntegrationBody) Close() error {
	body.closed = true
	return errors.New("integration close failure")
}

type declarationFailureIntegrationErrorReader struct {
	err error
}

func (reader declarationFailureIntegrationErrorReader) Read(
	[]byte,
) (int, error) {
	return 0, reader.err
}

func TestDeclarationFailureIntegrationRejectsMalformedDeclarations(
	t *testing.T,
) {
	tests := []struct {
		name string
		data []byte
		want declaration.ParseStatus
	}{
		{
			name: "oversized",
			data: bytes.Repeat(
				[]byte("x"),
				declaration.MaxBodySize+1,
			),
			want: declaration.ParseInvalid,
		},
		{
			name: "invalid UTF-8",
			data: []byte{0xff},
			want: declaration.ParseInvalid,
		},
		{
			name: "malformed JSON",
			data: []byte(`{"version":`),
			want: declaration.ParseInvalid,
		},
		{
			name: "missing version",
			data: []byte(`{"josh":true}`),
			want: declaration.ParseInvalid,
		},
		{
			name: "fractional version",
			data: []byte(`{"version":1.5}`),
			want: declaration.ParseInvalid,
		},
		{
			name: "negative integer version",
			data: []byte(`{"version":-2}`),
			want: declaration.ParseUnsupportedVersion,
		},
		{
			name: "invalid josh value",
			data: []byte(`{"version":1,"josh":"yes"}`),
			want: declaration.ParseInvalid,
		},
		{
			name: "declined identity",
			data: []byte(`{"version":1,"josh":false}`),
			want: declaration.ParseValid,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := declaration.Parse(test.data)
			if result.Status != test.want {
				t.Errorf(
					"Parse() status = %v, want %v",
					result.Status,
					test.want,
				)
			}
		})
	}
}

func TestDeclarationFailureIntegrationValidatesVerifierInputs(
	t *testing.T,
) {
	source := declarationFailureIntegrationOrigin(
		t,
		"https://example.com",
	)

	getter := declarationFailureIntegrationGetter{
		get: func(
			context.Context,
			*url.URL,
		) (*http.Response, error) {
			return declarationFailureIntegrationResponse(
				http.StatusNotFound,
				"",
			), nil
		},
	}

	var missing *declaration.Verifier
	if _, err := missing.Verify(
		context.Background(),
		source,
	); err == nil {
		t.Fatal("nil verifier Verify() error = nil")
	}

	verifier := declaration.NewVerifier(getter)

	if _, err := verifier.Verify(nil, source); err == nil {
		t.Fatal("Verify(nil context) error = nil")
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if _, err := verifier.Verify(ctx, source); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Errorf(
			"Verify(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	if _, err := verifier.Verify(
		context.Background(),
		origin.Origin{},
	); err == nil {
		t.Fatal("Verify(zero origin) error = nil")
	}

	if _, err := declaration.NewVerifier(nil).Verify(
		context.Background(),
		source,
	); err == nil {
		t.Fatal("Verify(nil getter) error = nil")
	}

	if _, err := declaration.NewVerifierWithClock(
		getter,
		nil,
	).Verify(
		context.Background(),
		source,
	); err == nil {
		t.Fatal("Verify(nil clock) error = nil")
	}
}

func TestDeclarationFailureIntegrationClassifiesGetterAndResponseFailures(
	t *testing.T,
) {
	source := declarationFailureIntegrationOrigin(
		t,
		"https://example.com",
	)

	t.Run("getter error closes response and maps transport", func(t *testing.T) {
		body := &declarationFailureIntegrationBody{
			reader: strings.NewReader("unused"),
		}

		verifier := declaration.NewVerifier(
			declarationFailureIntegrationGetter{
				get: func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					return &http.Response{
						Body: body,
					}, errors.New("integration getter failure")
				},
			},
		)

		result, err := verifier.Verify(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf("Verify() error = %v", err)
		}

		if !body.closed {
			t.Error("getter failure response body was not closed")
		}

		if result.Outcome != declaration.OutcomeUnavailable ||
			result.FailureCategory != retry.CategoryTransport {
			t.Errorf("Verify() result = %#v", result)
		}
	})

	t.Run("robots denied", func(t *testing.T) {
		result := declarationFailureIntegrationVerifyGetterError(
			t,
			source,
			robots.ErrDisallowed,
		)

		if result.Outcome != declaration.OutcomeRobotsDenied {
			t.Errorf("Verify() result = %#v", result)
		}
	})

	t.Run("robots temporary", func(t *testing.T) {
		result := declarationFailureIntegrationVerifyGetterError(
			t,
			source,
			robots.ErrTemporary,
		)

		if result.Outcome != declaration.OutcomeUnavailable ||
			result.FailureCategory != retry.CategoryRobotsTemporary {
			t.Errorf("Verify() result = %#v", result)
		}
	})

	t.Run("parent cancellation wins getter error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		verifier := declaration.NewVerifier(
			declarationFailureIntegrationGetter{
				get: func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					cancel()
					return nil, errors.New("integration getter failure")
				},
			},
		)

		_, err := verifier.Verify(ctx, source)
		if !errors.Is(err, context.Canceled) {
			t.Errorf(
				"Verify() error = %v, want context.Canceled",
				err,
			)
		}
	})

	t.Run("nil response", func(t *testing.T) {
		verifier := declaration.NewVerifier(
			declarationFailureIntegrationGetter{
				get: func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					return nil, nil
				},
			},
		)

		result, err := verifier.Verify(context.Background(), source)
		if err != nil {
			t.Fatalf("Verify() error = %v", err)
		}

		if result.Outcome != declaration.OutcomeUnavailable ||
			result.FailureCategory != retry.CategoryTransport {
			t.Errorf("Verify() result = %#v", result)
		}
	})

	t.Run("nil response body", func(t *testing.T) {
		verifier := declaration.NewVerifier(
			declarationFailureIntegrationGetter{
				get: func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
					}, nil
				},
			},
		)

		result, err := verifier.Verify(context.Background(), source)
		if err != nil {
			t.Fatalf("Verify() error = %v", err)
		}

		if result.Outcome != declaration.OutcomeUnavailable ||
			result.FailureCategory != retry.CategoryTransport {
			t.Errorf("Verify() result = %#v", result)
		}
	})
}

func TestDeclarationFailureIntegrationHandlesBodiesAndStatuses(
	t *testing.T,
) {
	source := declarationFailureIntegrationOrigin(
		t,
		"https://example.com",
	)

	tests := []struct {
		name       string
		status     int
		body       io.ReadCloser
		header     http.Header
		want       declaration.Outcome
		category   retry.Category
		retryAfter time.Duration
	}{
		{
			name:   "oversized success body is invalid",
			status: http.StatusOK,
			body: io.NopCloser(
				bytes.NewReader(
					bytes.Repeat(
						[]byte("x"),
						declaration.MaxBodySize+1,
					),
				),
			),
			want: declaration.OutcomeInvalid,
		},
		{
			name:   "body read failure is unavailable",
			status: http.StatusOK,
			body: io.NopCloser(
				declarationFailureIntegrationErrorReader{
					err: errors.New("integration read failure"),
				},
			),
			want:     declaration.OutcomeUnavailable,
			category: retry.CategoryTransport,
		},
		{
			name:   "invalid declaration",
			status: http.StatusOK,
			body: io.NopCloser(
				strings.NewReader(`{"version":1,"josh":7}`),
			),
			want: declaration.OutcomeInvalid,
		},
		{
			name:   "unsupported declaration",
			status: http.StatusOK,
			body: io.NopCloser(
				strings.NewReader(`{"version":2}`),
			),
			want: declaration.OutcomeUnsupportedVersion,
		},
		{
			name:   "valid declaration",
			status: http.StatusOK,
			body: io.NopCloser(
				strings.NewReader(`{"version":1,"josh":true}`),
			),
			want: declaration.OutcomeValid,
		},
		{
			name:   "not found",
			status: http.StatusNotFound,
			body:   io.NopCloser(strings.NewReader("missing")),
			want:   declaration.OutcomeAbsent,
		},
		{
			name:   "gone",
			status: http.StatusGone,
			body:   io.NopCloser(strings.NewReader("gone")),
			want:   declaration.OutcomeAbsent,
		},
		{
			name:   "too many requests",
			status: http.StatusTooManyRequests,
			body:   io.NopCloser(strings.NewReader("slow down")),
			header: http.Header{
				"Retry-After": []string{"60"},
			},
			want:       declaration.OutcomeUnavailable,
			category:   retry.CategoryHTTP429,
			retryAfter: time.Minute,
		},
		{
			name:     "non transient response",
			status:   http.StatusBadRequest,
			body:     io.NopCloser(strings.NewReader("bad request")),
			want:     declaration.OutcomeUnavailable,
			category: retry.CategoryUnsupportedOrigin,
		},
	}

	clock := declarationFailureIntegrationClock{
		now: time.Date(2026, 9, 19, 20, 0, 0, 0, time.UTC),
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := test.header
			if header == nil {
				header = make(http.Header)
			}

			verifier := declaration.NewVerifierWithClock(
				declarationFailureIntegrationGetter{
					get: func(
						context.Context,
						*url.URL,
					) (*http.Response, error) {
						return &http.Response{
							StatusCode: test.status,
							Header:     header.Clone(),
							Body:       test.body,
						}, nil
					},
				},
				clock,
			)

			result, err := verifier.Verify(
				context.Background(),
				source,
			)
			if err != nil {
				t.Fatalf("Verify() error = %v", err)
			}

			if result.Outcome != test.want ||
				result.FailureCategory != test.category ||
				result.RetryAfter != test.retryAfter {
				t.Errorf(
					"Verify() result = %#v, want outcome=%v category=%q retryAfter=%v",
					result,
					test.want,
					test.category,
					test.retryAfter,
				)
			}
		})
	}
}

func TestDeclarationFailureIntegrationEnforcesRedirectAuthority(
	t *testing.T,
) {
	source := declarationFailureIntegrationOrigin(
		t,
		"https://example.com",
	)

	t.Run("same-origin redirect succeeds", func(t *testing.T) {
		calls := 0

		verifier := declaration.NewVerifier(
			declarationFailureIntegrationGetter{
				get: func(
					_ context.Context,
					target *url.URL,
				) (*http.Response, error) {
					calls++

					if calls == 1 {
						return declarationFailureIntegrationRedirect(
							http.StatusFound,
							"/redirected#fragment",
						), nil
					}

					if target.Path != "/redirected" ||
						target.Fragment != "" {
						t.Fatalf(
							"redirect target = %q",
							target.String(),
						)
					}

					return declarationFailureIntegrationResponse(
						http.StatusOK,
						`{"version":1}`,
					), nil
				},
			},
		)

		result, err := verifier.Verify(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf("Verify() error = %v", err)
		}

		if result.Outcome != declaration.OutcomeValid || calls != 2 {
			t.Errorf(
				"Verify() result = %#v, calls = %d",
				result,
				calls,
			)
		}
	})

	tests := []struct {
		name     string
		location string
		want     declaration.Outcome
		category retry.Category
	}{
		{
			name:     "missing location",
			location: "",
			want:     declaration.OutcomeUnavailable,
			category: retry.CategoryMalformedOrigin,
		},
		{
			name:     "malformed location",
			location: "%zz",
			want:     declaration.OutcomeUnavailable,
			category: retry.CategoryMalformedOrigin,
		},
		{
			name:     "cross-origin location",
			location: "https://other.example/.well-known/josh",
			want:     declaration.OutcomeCrossOriginRedirect,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier := declaration.NewVerifier(
				declarationFailureIntegrationGetter{
					get: func(
						context.Context,
						*url.URL,
					) (*http.Response, error) {
						return declarationFailureIntegrationRedirect(
							http.StatusTemporaryRedirect,
							test.location,
						), nil
					},
				},
			)

			result, err := verifier.Verify(
				context.Background(),
				source,
			)
			if err != nil {
				t.Fatalf("Verify() error = %v", err)
			}

			if result.Outcome != test.want ||
				result.FailureCategory != test.category {
				t.Errorf(
					"Verify() result = %#v",
					result,
				)
			}
		})
	}

	t.Run("redirect limit", func(t *testing.T) {
		calls := 0

		verifier := declaration.NewVerifier(
			declarationFailureIntegrationGetter{
				get: func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					calls++
					return declarationFailureIntegrationRedirect(
						http.StatusTemporaryRedirect,
						"/.well-known/josh?redirected=1",
					), nil
				},
			},
		)

		result, err := verifier.Verify(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf("Verify() error = %v", err)
		}

		if calls != 6 {
			t.Errorf(
				"redirect calls = %d, want 6",
				calls,
			)
		}

		if result.Outcome != declaration.OutcomeUnavailable ||
			result.FailureCategory != retry.CategoryUnsupportedOrigin {
			t.Errorf("Verify() result = %#v", result)
		}
	})
}

func declarationFailureIntegrationVerifyGetterError(
	t *testing.T,
	source origin.Origin,
	getterError error,
) declaration.Result {
	t.Helper()

	verifier := declaration.NewVerifier(
		declarationFailureIntegrationGetter{
			get: func(
				context.Context,
				*url.URL,
			) (*http.Response, error) {
				return nil, getterError
			},
		},
	)

	result, err := verifier.Verify(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	return result
}

func declarationFailureIntegrationResponse(
	status int,
	body string,
) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body: io.NopCloser(
			strings.NewReader(body),
		),
	}
}

func declarationFailureIntegrationRedirect(
	status int,
	location string,
) *http.Response {
	response := declarationFailureIntegrationResponse(
		status,
		"redirect",
	)

	if location != "" {
		response.Header.Set(
			"Location",
			location,
		)
	}

	return response
}

func declarationFailureIntegrationOrigin(
	t *testing.T,
	raw string,
) origin.Origin {
	t.Helper()

	source, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			raw,
			err,
		)
	}

	return source
}

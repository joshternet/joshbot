package discovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/netguard"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/personalsite"
	"github.com/joshternet/joshbot/internal/robots"
)

func TestQualifyPersonalSiteNilResponseAndReadErrors(t *testing.T) {
	t.Parallel()

	candidate := mustOrigin(t, "https://example.com")

	t.Run("empty_origin", func(t *testing.T) {
		t.Parallel()
		_, err := QualifyPersonalSite(
			context.Background(),
			qualifyGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, errors.New("unused")
			}),
			origin.Origin{},
		)
		if !errors.Is(err, errInvalidSource) {
			t.Fatalf("error = %v, want invalid source", err)
		}
	})

	t.Run("nil_response", func(t *testing.T) {
		t.Parallel()
		qualification, err := QualifyPersonalSite(
			context.Background(),
			qualifyGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, nil
			}),
			candidate,
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if qualification.Outcome != personalsite.QualificationUnavailable {
			t.Fatalf("outcome = %q", qualification.Outcome)
		}
	})

	t.Run("nil_body", func(t *testing.T) {
		t.Parallel()
		qualification, err := QualifyPersonalSite(
			context.Background(),
			qualifyGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK}, nil
			}),
			candidate,
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if qualification.Outcome != personalsite.QualificationUnavailable {
			t.Fatalf("outcome = %q", qualification.Outcome)
		}
	})

	t.Run("read_error_without_ctx", func(t *testing.T) {
		t.Parallel()
		qualification, err := QualifyPersonalSite(
			context.Background(),
			qualifyGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html"}},
					Body:       io.NopCloser(errReader{}),
				}, nil
			}),
			candidate,
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if qualification.Outcome != personalsite.QualificationUnavailable {
			t.Fatalf("outcome = %q", qualification.Outcome)
		}
	})

	t.Run("read_error_with_canceled_ctx", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		qualification, err := QualifyPersonalSite(
			ctx,
			qualifyGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html"}},
					Body:       io.NopCloser(errReader{}),
				}, nil
			}),
			candidate,
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if qualification.Outcome != personalsite.QualificationTimeout {
			t.Fatalf("outcome = %q", qualification.Outcome)
		}
	})

	t.Run("temporary_robots", func(t *testing.T) {
		t.Parallel()
		qualification, err := QualifyPersonalSite(
			context.Background(),
			qualifyGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, robots.ErrTemporary
			}),
			candidate,
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if qualification.Outcome != personalsite.QualificationUnavailable {
			t.Fatalf("outcome = %q", qualification.Outcome)
		}
	})

	t.Run("canceled_error", func(t *testing.T) {
		t.Parallel()
		qualification, err := QualifyPersonalSite(
			context.Background(),
			qualifyGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, context.Canceled
			}),
			candidate,
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if qualification.Outcome != personalsite.QualificationUnavailable {
			t.Fatalf("outcome = %q", qualification.Outcome)
		}
	})

	t.Run("generic_transport", func(t *testing.T) {
		t.Parallel()
		qualification, err := QualifyPersonalSite(
			context.Background(),
			qualifyGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, errors.New("transport exploded")
			}),
			candidate,
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if qualification.Outcome != personalsite.QualificationUnavailable {
			t.Fatalf("outcome = %q", qualification.Outcome)
		}
	})

	t.Run("extract_too_large_via_decode", func(t *testing.T) {
		t.Parallel()
		// Body within readBounded limit but ExtractSignals reports too large
		// when content-type sniffing/decode path rejects oversized decoded
		// content is covered in personalsite; here ensure unsupported path
		// and assessed path already exist. Force unsupported via octet body.
		qualification, err := QualifyPersonalSite(
			context.Background(),
			qualifyGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{"application/octet-stream"},
					},
					Body: io.NopCloser(strings.NewReader("not-html")),
				}, nil
			}),
			candidate,
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if qualification.Outcome != personalsite.QualificationUnsupportedContent {
			t.Fatalf("outcome = %q", qualification.Outcome)
		}
	})
}

func TestMapQualifyFetchErrorCategories(t *testing.T) {
	t.Parallel()

	unsafeErr := netguard.ValidateAddr(netip.MustParseAddr("10.0.0.1"))
	if unsafeErr == nil {
		t.Fatal("expected unsafe address error")
	}

	cases := []struct {
		name string
		ctx  context.Context
		err  error
		want personalsite.QualificationOutcome
	}{
		{
			name: "robots_denied",
			ctx:  context.Background(),
			err:  robots.ErrDisallowed,
			want: personalsite.QualificationRobotsDenied,
		},
		{
			name: "ctx_done",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			}(),
			err:  errors.New("whatever"),
			want: personalsite.QualificationTimeout,
		},
		{
			name: "unsafe",
			ctx:  context.Background(),
			err:  unsafeErr,
			want: personalsite.QualificationNetworkRejected,
		},
		{
			name: "temporary",
			ctx:  context.Background(),
			err:  robots.ErrTemporary,
			want: personalsite.QualificationUnavailable,
		},
		{
			name: "dns",
			ctx:  context.Background(),
			err:  mustDNSError(t),
			want: personalsite.QualificationDNSFailure,
		},
		{
			name: "deadline",
			ctx:  context.Background(),
			err:  context.DeadlineExceeded,
			want: personalsite.QualificationTimeout,
		},
		{
			name: "canceled_error",
			ctx:  context.Background(),
			err:  context.Canceled,
			want: personalsite.QualificationUnavailable,
		},
	}

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got := mapQualifyFetchError(testCase.ctx, testCase.err)
			if got.Outcome != testCase.want {
				t.Fatalf(
					"outcome = %q, want %q",
					got.Outcome,
					testCase.want,
				)
			}
		})
	}
}

func TestRootPageURL(t *testing.T) {
	t.Parallel()

	got := rootPageURL(mustOrigin(t, "https://example.com:8443"))
	if got.String() != "https://example.com:8443/" {
		t.Fatalf("root = %q", got.String())
	}
}

func TestQualifyPersonalSiteExtractStatuses(t *testing.T) {
	// Not parallel: mutates package-level extractPersonalSiteSignals.
	candidate := mustOrigin(t, "https://example.com")
	htmlGetter := qualifyGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<html></html>")),
		}, nil
	})

	cases := []struct {
		name   string
		status personalsite.ExtractStatus
		want   personalsite.QualificationOutcome
	}{
		{
			name:   "too_large",
			status: personalsite.ExtractStatusTooLarge,
			want:   personalsite.QualificationTooLarge,
		},
		{
			name:   "unavailable",
			status: personalsite.ExtractStatusUnavailable,
			want:   personalsite.QualificationParserFailure,
		},
		{
			name:   "unknown",
			status: 0,
			want:   personalsite.QualificationUnavailable,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			original := extractPersonalSiteSignals
			t.Cleanup(func() { extractPersonalSiteSignals = original })
			extractPersonalSiteSignals = func(
				*url.URL,
				string,
				[]byte,
			) (personalsite.SiteSignals, personalsite.ExtractStatus, error) {
				return personalsite.SiteSignals{}, testCase.status, nil
			}

			qualification, err := QualifyPersonalSite(
				context.Background(),
				htmlGetter,
				candidate,
			)
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if qualification.Outcome != testCase.want {
				t.Fatalf(
					"outcome = %q, want %q",
					qualification.Outcome,
					testCase.want,
				)
			}
		})
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("boom")
}

type failResolver struct{}

func (failResolver) LookupNetIP(
	context.Context,
	string,
	string,
) ([]netip.Addr, error) {
	return nil, errors.New("nxdomain")
}

func mustDNSError(t *testing.T) error {
	t.Helper()
	_, err := netguard.Resolve(
		context.Background(),
		mustOrigin(t, "https://missing.example"),
		failResolver{},
	)
	if err == nil {
		t.Fatal("Resolve() error = nil, want DNS failure")
	}
	return err
}

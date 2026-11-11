package discovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/netguard"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/personalsite"
	"github.com/joshternet/joshbot/internal/robots"
)

type qualifyGetterFunc func(context.Context, *url.URL) (*http.Response, error)

func (getter qualifyGetterFunc) Get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	return getter(ctx, target)
}

func TestQualifyPersonalSiteAssessed(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/" {
			t.Fatalf("path = %q, want /", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(
			writer,
			`<!doctype html><html><head>
<script type="application/ld+json">{"@type":"Person"}</script>
</head><body><a href="/about">About</a></body></html>`,
		)
	}))
	t.Cleanup(server.Close)

	candidate := mustOrigin(t, server.URL)
	qualification, err := QualifyPersonalSite(
		context.Background(),
		qualifyHTTPGetter{client: server.Client()},
		candidate,
	)
	if err != nil {
		t.Fatalf("QualifyPersonalSite() error = %v", err)
	}
	if qualification.Outcome != personalsite.QualificationAssessed {
		t.Fatalf(
			"outcome = %q, want assessed",
			qualification.Outcome,
		)
	}
	if qualification.Assessment.Score <= 0 {
		t.Fatalf(
			"score = %d, want positive",
			qualification.Assessment.Score,
		)
	}
}

func TestQualifyPersonalSiteRobotsDenied(t *testing.T) {
	t.Parallel()

	candidate := mustOrigin(t, "https://example.com")
	qualification, err := QualifyPersonalSite(
		context.Background(),
		qualifyGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
			return nil, robots.ErrDisallowed
		}),
		candidate,
	)
	if err != nil {
		t.Fatalf("QualifyPersonalSite() error = %v", err)
	}
	if qualification.Outcome != personalsite.QualificationRobotsDenied {
		t.Fatalf(
			"outcome = %q, want robots_denied",
			qualification.Outcome,
		)
	}
	if qualification.Assessment.Score != 0 ||
		len(qualification.Assessment.Reasons) != 0 {
		t.Fatalf(
			"unexpected assessment %#v",
			qualification.Assessment,
		)
	}
}

func TestQualifyPersonalSiteUnsupportedContent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)

	qualification, err := QualifyPersonalSite(
		context.Background(),
		qualifyHTTPGetter{client: server.Client()},
		mustOrigin(t, server.URL),
	)
	if err != nil {
		t.Fatalf("QualifyPersonalSite() error = %v", err)
	}
	if qualification.Outcome != personalsite.QualificationUnsupportedContent {
		t.Fatalf(
			"outcome = %q, want unsupported_content",
			qualification.Outcome,
		)
	}
}

func TestQualifyPersonalSiteTooLarge(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write(
			[]byte(strings.Repeat("x", MaxRawBody+1)),
		)
	}))
	t.Cleanup(server.Close)

	qualification, err := QualifyPersonalSite(
		context.Background(),
		qualifyHTTPGetter{client: server.Client()},
		mustOrigin(t, server.URL),
	)
	if err != nil {
		t.Fatalf("QualifyPersonalSite() error = %v", err)
	}
	if qualification.Outcome != personalsite.QualificationTooLarge {
		t.Fatalf(
			"outcome = %q, want too_large",
			qualification.Outcome,
		)
	}
}

func TestQualifyPersonalSiteHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	qualification, err := QualifyPersonalSite(
		context.Background(),
		qualifyHTTPGetter{client: server.Client()},
		mustOrigin(t, server.URL),
	)
	if err != nil {
		t.Fatalf("QualifyPersonalSite() error = %v", err)
	}
	if qualification.Outcome != personalsite.QualificationHTTPError {
		t.Fatalf(
			"outcome = %q, want http_error",
			qualification.Outcome,
		)
	}
}

func TestQualifyPersonalSiteNetworkRejected(t *testing.T) {
	t.Parallel()

	unsafeErr := netguard.ValidateAddr(netip.MustParseAddr("127.0.0.1"))
	if unsafeErr == nil {
		t.Fatal("ValidateAddr(127.0.0.1) error = nil, want unsafe")
	}

	candidate := mustOrigin(t, "https://example.com")
	qualification, err := QualifyPersonalSite(
		context.Background(),
		qualifyGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
			return nil, unsafeErr
		}),
		candidate,
	)
	if err != nil {
		t.Fatalf("QualifyPersonalSite() error = %v", err)
	}
	if qualification.Outcome != personalsite.QualificationNetworkRejected {
		t.Fatalf(
			"outcome = %q, want network_rejected",
			qualification.Outcome,
		)
	}
}

func TestQualifyPersonalSiteTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		time.Millisecond,
	)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	qualification, err := QualifyPersonalSite(
		ctx,
		qualifyGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
			return nil, context.DeadlineExceeded
		}),
		mustOrigin(t, "https://example.com"),
	)
	if err != nil {
		t.Fatalf("QualifyPersonalSite() error = %v", err)
	}
	if qualification.Outcome != personalsite.QualificationTimeout {
		t.Fatalf(
			"outcome = %q, want timeout",
			qualification.Outcome,
		)
	}
}

func TestQualifyPersonalSiteInvalidArgs(t *testing.T) {
	t.Parallel()

	var nilContext context.Context
	_, err := QualifyPersonalSite(
		nilContext,
		qualifyGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
			return nil, errors.New("unused")
		}),
		mustOrigin(t, "https://example.com"),
	)
	if !errors.Is(err, errInvalidContext) {
		t.Fatalf("error = %v, want invalid context", err)
	}

	_, err = QualifyPersonalSite(
		context.Background(),
		nil,
		mustOrigin(t, "https://example.com"),
	)
	if !errors.Is(err, errGetterUnavailable) {
		t.Fatalf("error = %v, want getter unavailable", err)
	}
}

type qualifyHTTPGetter struct {
	client *http.Client
}

func (getter qualifyHTTPGetter) Get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		target.String(),
		nil,
	)
	if err != nil {
		return nil, err
	}
	return getter.client.Do(request)
}

func mustOrigin(t *testing.T, raw string) origin.Origin {
	t.Helper()

	parsed, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf("origin.Parse(%q) error = %v", raw, err)
	}
	return parsed
}

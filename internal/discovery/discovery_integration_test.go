package discovery_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/robots"
)

type discoveryIntegrationResolver struct{}

func (discoveryIntegrationResolver) LookupNetIP(
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

type discoveryIntegrationDialer struct {
	target string
}

func (dialer discoveryIntegrationDialer) DialContext(
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

type discoveryIntegrationSink struct {
	mu         sync.Mutex
	sources    []string
	candidates []discovery.Candidate
}

type discoveryIntegrationTelemetry struct{}

func (discoveryIntegrationTelemetry) BeginCrawl(
	context.Context,
	origin.Origin,
	discovery.CrawlConfig,
) (discovery.CrawlRunID, error) {
	return 0, nil
}

func (discoveryIntegrationTelemetry) RecordPageAttempt(
	context.Context,
	discovery.CrawlRunID,
	discovery.PageAttempt,
) error {
	return nil
}

func (discoveryIntegrationTelemetry) FinishCrawl(
	context.Context,
	discovery.CrawlRunID,
	discovery.CrawlResult,
	discovery.CrawlRunOutcome,
	string,
) error {
	return nil
}

func (sink *discoveryIntegrationSink) RecordCandidates(
	_ context.Context,
	source origin.Origin,
	candidates []discovery.Candidate,
) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()

	sink.sources = append(
		sink.sources,
		source.String(),
	)

	sink.candidates = append(
		sink.candidates,
		candidates...,
	)

	return nil
}

func TestDiscoveryIntegrationMultiPageCrawlDiscoversAndDeduplicatesCandidates(
	t *testing.T,
) {
	server := newDiscoveryIntegrationServer(t)
	defer server.Close()

	checker := robots.NewCheckerWithRequestDelayAndSigner(
		discoveryIntegrationResolver{},
		discoveryIntegrationDialer{
			target: server.Listener.Addr().String(),
		},
		0,
		nil,
	)

	source := mustDiscoveryIntegrationOrigin(
		t,
		"http://example.com",
	)

	sink := &discoveryIntegrationSink{}

	crawler, err := discovery.NewMultiPageCrawlerWithTelemetry(
		checker,
		sink,
		discoveryIntegrationTelemetry{},
		discovery.CrawlConfig{
			MaxDepth:                     1,
			MaxPages:                     4,
			MaxPageBytes:                 64 * 1024,
			RequestDelay:                 time.Millisecond,
			RedirectLimit:                5,
			PageTimeout:                  2 * time.Second,
			MaxAutomaticPromotionsPerRun: 0,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewMultiPageCrawler() error = %v",
			err,
		)
	}

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"MultiPageCrawler.Crawl() error = %v",
			err,
		)
	}

	if result.Source != source {
		t.Errorf(
			"Crawl() source = %q, want %q",
			result.Source.String(),
			source.String(),
		)
	}

	if result.PagesAttempted != 2 {
		t.Errorf(
			"Crawl() PagesAttempted = %d, want 2",
			result.PagesAttempted,
		)
	}

	if result.PagesParsed != 2 {
		t.Errorf(
			"Crawl() PagesParsed = %d, want 2",
			result.PagesParsed,
		)
	}

	if result.CandidatesDiscovered != 2 {
		t.Errorf(
			"Crawl() CandidatesDiscovered = %d, want 2",
			result.CandidatesDiscovered,
		)
	}

	if result.BudgetExhausted {
		t.Error(
			"Crawl() BudgetExhausted = true, want false",
		)
	}

	if result.FailureCategory != retry.CategoryNone {
		t.Errorf(
			"Crawl() FailureCategory = %q, want empty",
			result.FailureCategory,
		)
	}

	sink.mu.Lock()
	sources := append(
		[]string(nil),
		sink.sources...,
	)
	candidates := append(
		[]discovery.Candidate(nil),
		sink.candidates...,
	)
	sink.mu.Unlock()

	for _, candidateSource := range sources {
		if candidateSource != source.String() {
			t.Errorf(
				"sink source = %q, want %q",
				candidateSource,
				source.String(),
			)
		}
	}

	gotOrigins := candidateOrigins(
		candidates,
	)

	if len(gotOrigins) != 2 {
		t.Fatalf(
			"recorded candidate origins = %#v, want 2 origins",
			gotOrigins,
		)
	}

	for _, expected := range []string{
		"https://alpha.example",
		"https://beta.example",
	} {
		if !slices.Contains(
			gotOrigins,
			expected,
		) {
			t.Errorf(
				"recorded candidate origins = %#v, missing %q",
				gotOrigins,
				expected,
			)
		}
	}

	for _, candidate := range candidates {
		if candidate.Kind != discovery.KindLink {
			t.Errorf(
				"candidate %q kind = %v, want %v",
				candidate.Origin.String(),
				candidate.Kind,
				discovery.KindLink,
			)
		}
	}
}

func newDiscoveryIntegrationServer(
	t *testing.T,
) *httptest.Server {
	t.Helper()

	return httptest.NewServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			if request.UserAgent() != robots.UserAgent {
				t.Errorf(
					"User-Agent = %q, want %q",
					request.UserAgent(),
					robots.UserAgent,
				)
			}

			switch request.URL.Path {
			case "/robots.txt":
				writer.Header().Set(
					"Content-Type",
					"text/plain",
				)
				_, _ = writer.Write(
					[]byte(
						"User-agent: Joshternet-Joshbot\n" +
							"Allow: /\n",
					),
				)

			case "/":
				writer.Header().Set(
					"Content-Type",
					"text/html; charset=utf-8",
				)
				_, _ = writer.Write(
					[]byte(
						`<!doctype html>
<html>
<body>
<a href="/about">About</a>
<a href="https://alpha.example/first">Alpha</a>
</body>
</html>`,
					),
				)

			case "/about":
				writer.Header().Set(
					"Content-Type",
					"text/html; charset=utf-8",
				)
				_, _ = writer.Write(
					[]byte(
						`<!doctype html>
<html>
<body>
<a href="https://alpha.example/duplicate">Alpha again</a>
<a href="https://beta.example/path">Beta</a>
</body>
</html>`,
					),
				)

			default:
				http.NotFound(
					writer,
					request,
				)
			}
		}),
	)
}

func mustDiscoveryIntegrationOrigin(
	t *testing.T,
	raw string,
) origin.Origin {
	t.Helper()

	parsed, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			raw,
			err,
		)
	}

	return parsed
}

func candidateOrigins(
	candidates []discovery.Candidate,
) []string {
	origins := make(
		[]string,
		0,
		len(candidates),
	)

	for _, candidate := range candidates {
		origins = append(
			origins,
			candidate.Origin.String(),
		)
	}

	return origins
}

type discoveryIntegrationDelayTelemetry struct {
	recorded chan struct{}
	once     sync.Once
}

func (
	telemetry *discoveryIntegrationDelayTelemetry,
) BeginCrawl(
	context.Context,
	origin.Origin,
	discovery.CrawlConfig,
) (discovery.CrawlRunID, error) {
	return 0, nil
}

func (
	telemetry *discoveryIntegrationDelayTelemetry,
) RecordPageAttempt(
	context.Context,
	discovery.CrawlRunID,
	discovery.PageAttempt,
) error {
	telemetry.once.Do(
		func() {
			close(telemetry.recorded)
		},
	)

	return nil
}

func (
	telemetry *discoveryIntegrationDelayTelemetry,
) FinishCrawl(
	context.Context,
	discovery.CrawlRunID,
	discovery.CrawlResult,
	discovery.CrawlRunOutcome,
	string,
) error {
	return nil
}

func TestDiscoveryIntegrationRequestDelayHonorsCancellation(
	t *testing.T,
) {
	server := newDiscoveryIntegrationServer(t)
	defer server.Close()

	checker := robots.NewCheckerWithRequestDelayAndSigner(
		discoveryIntegrationResolver{},
		discoveryIntegrationDialer{
			target: server.Listener.Addr().String(),
		},
		0,
		nil,
	)

	source := mustDiscoveryIntegrationOrigin(
		t,
		"http://example.com",
	)

	telemetry := &discoveryIntegrationDelayTelemetry{
		recorded: make(chan struct{}),
	}

	crawler, err := discovery.NewMultiPageCrawlerWithTelemetry(
		checker,
		&discoveryIntegrationSink{},
		telemetry,
		discovery.CrawlConfig{
			MaxDepth:                     1,
			MaxPages:                     4,
			MaxPageBytes:                 64 * 1024,
			RequestDelay:                 time.Hour,
			RedirectLimit:                5,
			PageTimeout:                  2 * time.Second,
			MaxAutomaticPromotionsPerRun: 0,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewMultiPageCrawlerWithTelemetry() error = %v",
			err,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	defer cancel()

	type crawlResult struct {
		result discovery.CrawlResult
		err    error
	}

	done := make(
		chan crawlResult,
		1,
	)

	go func() {
		result, err := crawler.Crawl(
			ctx,
			source,
		)

		done <- crawlResult{
			result: result,
			err:    err,
		}
	}()

	select {
	case <-telemetry.recorded:
	case <-time.After(2 * time.Second):
		t.Fatal(
			"first page attempt was not recorded",
		)
	}

	cancel()

	select {
	case got := <-done:
		if !errors.Is(
			got.err,
			context.Canceled,
		) {
			t.Fatalf(
				"Crawl() error = %v, want context.Canceled",
				got.err,
			)
		}

		if got.result.PagesAttempted != 1 {
			t.Errorf(
				"Crawl() PagesAttempted = %d, want 1",
				got.result.PagesAttempted,
			)
		}

	case <-time.After(2 * time.Second):
		t.Fatal(
			"Crawl() did not stop after request-delay cancellation",
		)
	}
}

func TestDiscoveryIntegrationExtractPageLinksValidationAndDecoding(
	t *testing.T,
) {
	source := mustDiscoveryIntegrationOrigin(
		t,
		"https://example.com",
	)

	pageURL, err := url.Parse(
		"https://example.com/path",
	)
	if err != nil {
		t.Fatalf(
			"url.Parse() error = %v",
			err,
		)
	}

	otherPageURL, err := url.Parse(
		"https://other.example/path",
	)
	if err != nil {
		t.Fatalf(
			"url.Parse() error = %v",
			err,
		)
	}

	t.Run(
		"invalid source",
		func(t *testing.T) {
			_, _, err := discovery.ExtractPageLinks(
				origin.Origin{},
				pageURL,
				"text/html",
				[]byte("<html></html>"),
			)

			if err == nil {
				t.Fatal(
					"ExtractPageLinks() error = nil, want invalid source",
				)
			}
		},
	)

	t.Run(
		"nil page URL",
		func(t *testing.T) {
			_, _, err := discovery.ExtractPageLinks(
				source,
				nil,
				"text/html",
				[]byte("<html></html>"),
			)

			if err == nil {
				t.Fatal(
					"ExtractPageLinks() error = nil, want invalid page URL",
				)
			}
		},
	)

	t.Run(
		"different page origin",
		func(t *testing.T) {
			_, _, err := discovery.ExtractPageLinks(
				source,
				otherPageURL,
				"text/html",
				[]byte("<html></html>"),
			)

			if err == nil {
				t.Fatal(
					"ExtractPageLinks() error = nil, want mismatched page origin",
				)
			}
		},
	)

	t.Run(
		"raw body too large",
		func(t *testing.T) {
			links, status, err :=
				discovery.ExtractPageLinks(
					source,
					pageURL,
					"text/html",
					make(
						[]byte,
						discovery.MaxRawBody+1,
					),
				)

			if err != nil {
				t.Fatalf(
					"ExtractPageLinks() error = %v",
					err,
				)
			}

			if status != discovery.StatusTooLarge {
				t.Errorf(
					"status = %v, want %v",
					status,
					discovery.StatusTooLarge,
				)
			}

			if len(links.Internal) != 0 ||
				len(links.Candidates) != 0 {
				t.Errorf(
					"links = %#v, want empty",
					links,
				)
			}
		},
	)

	t.Run(
		"detects HTML content type",
		func(t *testing.T) {
			links, status, err :=
				discovery.ExtractPageLinks(
					source,
					pageURL,
					"",
					[]byte(
						`<!doctype html>
<html>
<body>
<a href="https://outside.example/path">Outside</a>
</body>
</html>`,
					),
				)

			if err != nil {
				t.Fatalf(
					"ExtractPageLinks() error = %v",
					err,
				)
			}

			if status != discovery.StatusComplete {
				t.Errorf(
					"status = %v, want %v",
					status,
					discovery.StatusComplete,
				)
			}

			if len(links.Candidates) != 1 ||
				links.Candidates[0].Origin.String() !=
					"https://outside.example" {
				t.Errorf(
					"candidates = %#v",
					links.Candidates,
				)
			}
		},
	)

	t.Run(
		"empty explicit HTML is unavailable",
		func(t *testing.T) {
			links, status, err :=
				discovery.ExtractPageLinks(
					source,
					pageURL,
					"text/html",
					nil,
				)

			if err != nil {
				t.Fatalf(
					"ExtractPageLinks() error = %v",
					err,
				)
			}

			if status != discovery.StatusUnavailable {
				t.Errorf(
					"status = %v, want %v",
					status,
					discovery.StatusUnavailable,
				)
			}

			if len(links.Internal) != 0 ||
				len(links.Candidates) != 0 {
				t.Errorf(
					"links = %#v, want empty",
					links,
				)
			}
		},
	)

	t.Run(
		"unknown charset falls back",
		func(t *testing.T) {
			links, status, err :=
				discovery.ExtractPageLinks(
					source,
					pageURL,
					"text/html; charset=x-joshternet-unknown",
					[]byte(
						`<!doctype html>
<html>
<body>
<a href="https://fallback.example/path">Fallback</a>
</body>
</html>`,
					),
				)

			if err != nil {
				t.Fatalf(
					"ExtractPageLinks() error = %v",
					err,
				)
			}

			if status != discovery.StatusComplete {
				t.Errorf(
					"status = %v, want %v",
					status,
					discovery.StatusComplete,
				)
			}

			if len(links.Candidates) != 1 ||
				links.Candidates[0].Origin.String() !=
					"https://fallback.example" {
				t.Errorf(
					"candidates = %#v",
					links.Candidates,
				)
			}
		},
	)

	t.Run(
		"decoded body too large",
		func(t *testing.T) {
			_, status, err :=
				discovery.ExtractPageLinks(
					source,
					pageURL,
					"text/html; charset=windows-1252",
					bytes.Repeat(
						[]byte{0x80},
						discovery.MaxRawBody,
					),
				)

			if err != nil {
				t.Fatalf(
					"ExtractPageLinks() error = %v",
					err,
				)
			}

			if status != discovery.StatusTooLarge {
				t.Errorf(
					"status = %v, want %v",
					status,
					discovery.StatusTooLarge,
				)
			}
		},
	)
}

func TestDiscoveryIntegrationExtractPageLinksNormalizesDocumentLinks(
	t *testing.T,
) {
	source := mustDiscoveryIntegrationOrigin(
		t,
		"https://example.com",
	)

	pageURL, err := url.Parse(
		"https://example.com/root/page",
	)
	if err != nil {
		t.Fatalf(
			"url.Parse() error = %v",
			err,
		)
	}

	links, status, err := discovery.ExtractPageLinks(
		source,
		pageURL,
		"text/html; charset=utf-8",
		[]byte(
			`<!doctype html>
<html>
<body>
<a>Missing href</a>
<a href="https://[broken">Malformed href</a>
<a href="mailto:josh@example.com">Unsupported scheme</a>
<a href="/inside#first">Inside</a>
<a href="/inside#second">Inside duplicate</a>
<a href="https://z.example/one">Z</a>
<a href="https://z.example/two">Z duplicate</a>
<a href="https://a.example/path">A</a>
</body>
</html>`,
		),
	)
	if err != nil {
		t.Fatalf(
			"ExtractPageLinks() error = %v",
			err,
		)
	}

	if status != discovery.StatusComplete {
		t.Fatalf(
			"status = %v, want %v",
			status,
			discovery.StatusComplete,
		)
	}

	if len(links.Internal) != 1 ||
		links.Internal[0].String() !=
			"https://example.com/inside" {
		t.Errorf(
			"internal links = %#v",
			links.Internal,
		)
	}

	if len(links.Candidates) != 2 {
		t.Fatalf(
			"candidates = %#v, want 2",
			links.Candidates,
		)
	}

	gotCandidates := []string{
		links.Candidates[0].Origin.String(),
		links.Candidates[1].Origin.String(),
	}

	wantCandidates := []string{
		"https://a.example",
		"https://z.example",
	}

	if !slices.Equal(
		gotCandidates,
		wantCandidates,
	) {
		t.Errorf(
			"candidate origins = %#v, want %#v",
			gotCandidates,
			wantCandidates,
		)
	}
}

func TestDiscoveryIntegrationExtractPageLinksHandlesDocumentBase(
	t *testing.T,
) {
	source := mustDiscoveryIntegrationOrigin(
		t,
		"https://example.com",
	)

	pageURL, err := url.Parse(
		"https://example.com/root/page",
	)
	if err != nil {
		t.Fatalf(
			"url.Parse() error = %v",
			err,
		)
	}

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "base without href",
			body: `<!doctype html>
<html>
<head><base></head>
<body><a href="child">Child</a></body>
</html>`,
			want: "https://example.com/root/child",
		},
		{
			name: "malformed base",
			body: `<!doctype html>
<html>
<head><base href="https://[broken"></head>
<body><a href="child">Child</a></body>
</html>`,
			want: "https://example.com/root/child",
		},
		{
			name: "unsupported base origin",
			body: `<!doctype html>
<html>
<head><base href="mailto:josh@example.com"></head>
<body><a href="child">Child</a></body>
</html>`,
			want: "https://example.com/root/child",
		},
		{
			name: "valid base",
			body: `<!doctype html>
<html>
<head><base href="/nested/"></head>
<body><a href="child">Child</a></body>
</html>`,
			want: "https://example.com/nested/child",
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				links, status, err :=
					discovery.ExtractPageLinks(
						source,
						pageURL,
						"text/html",
						[]byte(test.body),
					)

				if err != nil {
					t.Fatalf(
						"ExtractPageLinks() error = %v",
						err,
					)
				}

				if status != discovery.StatusComplete {
					t.Fatalf(
						"status = %v, want %v",
						status,
						discovery.StatusComplete,
					)
				}

				if len(links.Internal) != 1 {
					t.Fatalf(
						"internal links = %#v, want 1",
						links.Internal,
					)
				}

				if got :=
					links.Internal[0].String(); got != test.want {
					t.Errorf(
						"internal link = %q, want %q",
						got,
						test.want,
					)
				}
			},
		)
	}
}

package discovery_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
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

func TestDiscoveryIntegrationHomepageCrawlerUsesGuardedRobotsHTTP(
	t *testing.T,
) {
	server := newDiscoveryIntegrationServer(t)
	defer server.Close()

	checker := robots.NewChecker(
		discoveryIntegrationResolver{},
		discoveryIntegrationDialer{
			target: server.Listener.Addr().String(),
		},
	)

	source := mustDiscoveryIntegrationOrigin(
		t,
		"http://example.com",
	)

	crawler := discovery.NewCrawler(
		checker,
	)

	result, err := crawler.Discover(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawler.Discover() error = %v",
			err,
		)
	}

	if result.Status != discovery.StatusComplete {
		t.Fatalf(
			"Crawler.Discover() status = %v, want %v",
			result.Status,
			discovery.StatusComplete,
		)
	}

	if result.Source != source {
		t.Errorf(
			"Crawler.Discover() source = %q, want %q",
			result.Source.String(),
			source.String(),
		)
	}

	got := candidateOrigins(
		result.Candidates,
	)

	if !slices.Contains(
		got,
		"https://alpha.example",
	) {
		t.Errorf(
			"homepage candidates = %#v, want alpha.example",
			got,
		)
	}
}

func TestDiscoveryIntegrationMultiPageCrawlDiscoversAndDeduplicatesCandidates(
	t *testing.T,
) {
	server := newDiscoveryIntegrationServer(t)
	defer server.Close()

	checker := robots.NewChecker(
		discoveryIntegrationResolver{},
		discoveryIntegrationDialer{
			target: server.Listener.Addr().String(),
		},
	)

	source := mustDiscoveryIntegrationOrigin(
		t,
		"http://example.com",
	)

	sink := &discoveryIntegrationSink{}

	crawler, err := discovery.NewMultiPageCrawler(
		checker,
		sink,
		discovery.CrawlConfig{
			MaxDepth:                     1,
			MaxPages:                     4,
			MaxPageBytes:                 64 * 1024,
			RequestDelay:                 0,
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

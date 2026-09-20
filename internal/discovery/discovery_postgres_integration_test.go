package discovery_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/robots"
	"github.com/joshternet/joshbot/internal/store"
)

var discoveryPostgresSchemaSequence uint64

type discoveryPostgresResolver struct{}

func (discoveryPostgresResolver) LookupNetIP(
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

type discoveryPostgresDialer struct {
	target string
}

func (dialer discoveryPostgresDialer) DialContext(
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

type discoveryPostgresCandidateSink struct {
	store *store.DiscoveryStore
}

func (
	sink discoveryPostgresCandidateSink,
) RecordCandidates(
	ctx context.Context,
	source origin.Origin,
	candidates []discovery.Candidate,
) error {
	_, err := sink.store.RecordDiscovery(
		ctx,
		source,
		candidates,
	)

	return err
}

func TestDiscoveryPostgresIntegrationCrawlsAndPersistsCandidates(
	t *testing.T,
) {
	ctx := context.Background()

	pool := newDiscoveryPostgresIntegrationPool(t)

	discoveryStore, err := store.NewDiscoveryStore(
		pool,
	)
	if err != nil {
		t.Fatalf(
			"store.NewDiscoveryStore() error = %v",
			err,
		)
	}

	source, err := origin.Parse(
		"http://example.com",
	)
	if err != nil {
		t.Fatalf(
			"origin.Parse() error = %v",
			err,
		)
	}

	if err := discoveryStore.AddCrawlSeed(
		ctx,
		source,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v",
			err,
		)
	}

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
<a href="https://alpha.example/path">Alpha</a>
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
<a href="https://alpha.example/again">Alpha again</a>
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
	defer server.Close()

	checker := robots.NewChecker(
		discoveryPostgresResolver{},
		discoveryPostgresDialer{
			target: server.Listener.Addr().String(),
		},
	)

	crawler, err := discovery.NewMultiPageCrawler(
		checker,
		discoveryPostgresCandidateSink{
			store: discoveryStore,
		},
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

	runner, err := discovery.NewCrawlRunner(
		discoveryStore,
		crawler,
		discovery.CrawlRunnerConfig{
			DiscoveryInterval: time.Hour,
			PollInterval:      10 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewCrawlRunner() error = %v",
			err,
		)
	}

	report, err := runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf(
			"CrawlRunner.RunOnce() error = %v",
			err,
		)
	}

	if !report.Worked {
		t.Fatal(
			"CrawlRunner.RunOnce() Worked = false, want true",
		)
	}

	if report.Source != source {
		t.Errorf(
			"crawl source = %q, want %q",
			report.Source.String(),
			source.String(),
		)
	}

	if report.PagesAttempted != 2 {
		t.Errorf(
			"PagesAttempted = %d, want 2",
			report.PagesAttempted,
		)
	}

	if report.PagesParsed != 2 {
		t.Errorf(
			"PagesParsed = %d, want 2",
			report.PagesParsed,
		)
	}

	if report.CandidatesDiscovered != 2 {
		t.Errorf(
			"CandidatesDiscovered = %d, want 2",
			report.CandidatesDiscovered,
		)
	}

	if report.BudgetExhausted {
		t.Error(
			"BudgetExhausted = true, want false",
		)
	}

	var candidateCount int
	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM discovery_candidates
			WHERE origin IN (
				'https://alpha.example',
				'https://beta.example'
			)
		`,
	).Scan(&candidateCount); err != nil {
		t.Fatalf(
			"query discovery candidates: %v",
			err,
		)
	}

	if candidateCount != 2 {
		t.Errorf(
			"candidate count = %d, want 2",
			candidateCount,
		)
	}

	var edgeCount int
	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM discovery_edges
			WHERE source_origin = $1
				AND candidate_origin IN (
					'https://alpha.example',
					'https://beta.example'
				)
				AND kind = 'link'
		`,
		source.String(),
	).Scan(&edgeCount); err != nil {
		t.Fatalf(
			"query discovery edges: %v",
			err,
		)
	}

	if edgeCount != 2 {
		t.Errorf(
			"discovery edge count = %d, want 2",
			edgeCount,
		)
	}

	var probeCount int
	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_queue
			WHERE origin IN (
				'https://alpha.example',
				'https://beta.example'
			)
				AND mode = 'probe'
		`,
	).Scan(&probeCount); err != nil {
		t.Fatalf(
			"query discovery probes: %v",
			err,
		)
	}

	if probeCount != 2 {
		t.Errorf(
			"probe queue count = %d, want 2",
			probeCount,
		)
	}

	sources, err := discoveryStore.CrawlSources(
		ctx,
	)
	if err != nil {
		t.Fatalf(
			"CrawlSources() error = %v",
			err,
		)
	}

	if len(sources) != 1 {
		t.Fatalf(
			"CrawlSources() count = %d, want 1",
			len(sources),
		)
	}

	if sources[0].Origin != source {
		t.Errorf(
			"stored crawl source = %q, want %q",
			sources[0].Origin.String(),
			source.String(),
		)
	}

	if !sources[0].Seeded {
		t.Error(
			"stored crawl source Seeded = false, want true",
		)
	}

	secondReport, err := runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf(
			"second CrawlRunner.RunOnce() error = %v",
			err,
		)
	}

	if secondReport.Worked {
		t.Errorf(
			"second CrawlRunner.RunOnce() = %#v, want no work",
			secondReport,
		)
	}
}

func newDiscoveryPostgresIntegrationPool(
	t *testing.T,
) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv(
		"JOSHBOT_TEST_DATABASE_URL",
	)
	if databaseURL == "" {
		if os.Getenv(
			"JOSHBOT_REQUIRE_DATABASE_TESTS",
		) == "1" {
			t.Fatal(
				"JOSHBOT_TEST_DATABASE_URL is required",
			)
		}

		t.Skip(
			"JOSHBOT_TEST_DATABASE_URL is not configured",
		)
	}

	ctx := context.Background()

	adminPool, err := pgxpool.New(
		ctx,
		databaseURL,
	)
	if err != nil {
		t.Fatalf(
			"create discovery integration admin pool: %v",
			err,
		)
	}
	t.Cleanup(adminPool.Close)

	if err := adminPool.Ping(ctx); err != nil {
		t.Fatalf(
			"ping discovery integration database: %v",
			err,
		)
	}

	schema := fmt.Sprintf(
		"discovery_test_%d_%d",
		os.Getpid(),
		atomic.AddUint64(
			&discoveryPostgresSchemaSequence,
			1,
		),
	)

	if _, err := adminPool.Exec(
		ctx,
		"CREATE SCHEMA "+
			pgx.Identifier{schema}.Sanitize(),
	); err != nil {
		t.Fatalf(
			"create discovery integration schema: %v",
			err,
		)
	}

	schemaURL, err := url.Parse(
		databaseURL,
	)
	if err != nil {
		t.Fatalf(
			"parse discovery integration database URL: %v",
			err,
		)
	}

	query := schemaURL.Query()
	query.Set(
		"search_path",
		schema,
	)
	schemaURL.RawQuery = query.Encode()

	pool, err := pgxpool.New(
		ctx,
		schemaURL.String(),
	)
	if err != nil {
		t.Fatalf(
			"create discovery integration pool: %v",
			err,
		)
	}

	t.Cleanup(func() {
		pool.Close()

		if _, cleanupErr := adminPool.Exec(
			context.Background(),
			"DROP SCHEMA "+
				pgx.Identifier{schema}.Sanitize()+
				" CASCADE",
		); cleanupErr != nil {
			t.Errorf(
				"drop discovery integration schema: %v",
				cleanupErr,
			)
		}
	})

	if err := store.Migrate(
		ctx,
		pool,
	); err != nil {
		t.Fatalf(
			"migrate discovery integration schema: %v",
			err,
		)
	}

	return pool
}

package reporting

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/store"
)

var reportingSchemaCounter atomic.Uint64

type reportingFixture struct {
	crawlID int64
}

func TestNewPostgresReaderValidatesConfiguration(
	t *testing.T,
) {
	config := PostgresConfig{
		AutomaticCrawlEnabled: true,
		MaxPendingProbes:      1000,
	}

	if _, err := NewPostgresReader(
		nil,
		config,
	); !errors.Is(
		err,
		errPostgresPoolUnavailable,
	) {
		t.Errorf(
			"NewPostgresReader(nil) error = %v, want %v",
			err,
			errPostgresPoolUnavailable,
		)
	}

	pool := newReportingTestPool(t)

	if _, err := NewPostgresReader(
		pool,
		PostgresConfig{},
	); !errors.Is(
		err,
		errInvalidPostgresConfig,
	) {
		t.Errorf(
			"NewPostgresReader(invalid config) error = %v, want %v",
			err,
			errInvalidPostgresConfig,
		)
	}

	reader, err := NewPostgresReader(
		pool,
		config,
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v, want nil",
			err,
		)
	}

	if reader == nil {
		t.Fatal(
			"NewPostgresReader() = nil, want reader",
		)
	}
}

func TestPostgresReaderReturnsOperationalState(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)

	fixture := seedReportingFixture(
		t,
		pool,
	)
	if _, err := pool.Exec(ctx, `
		UPDATE crawl_runs SET
			promotions_admitted = 1,
			promotions_deferred = 2,
			failure_category = 'transport',
			pages_blocked = 3,
			pages_failed = 4,
			urls_found = 5,
			urls_enqueued = 6,
			frontier_remaining = 7
		WHERE id = $1
	`, fixture.crawlID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE crawl_page_attempts SET
			failure_category = 'transport',
			urls_found = 8,
			urls_enqueued = 9
		WHERE run_id = $1
	`, fixture.crawlID); err != nil {
		t.Fatal(err)
	}

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{
			AutomaticCrawlEnabled: true,
			MaxPendingProbes:      1,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	status, err := reader.Status(ctx)
	if err != nil {
		t.Fatalf(
			"Status() error = %v, want nil",
			err,
		)
	}

	if status.Control.DiscoveryPaused {
		t.Error(
			"DiscoveryPaused = true, want false",
		)
	}

	if !status.Control.VerificationPaused {
		t.Error(
			"VerificationPaused = false, want true",
		)
	}

	if !status.Backpressure.AutomaticCrawlEnabled {
		t.Error(
			"AutomaticCrawlEnabled = false, want true",
		)
	}

	if !status.Backpressure.Active {
		t.Error(
			"Backpressure.Active = false, want true",
		)
	}

	if status.Backpressure.PendingProbes != 1 {
		t.Errorf(
			"PendingProbes = %d, want 1",
			status.Backpressure.PendingProbes,
		)
	}

	if status.Backpressure.MaxPendingProbes != 1 {
		t.Errorf(
			"MaxPendingProbes = %d, want 1",
			status.Backpressure.MaxPendingProbes,
		)
	}

	if status.Queue.Total != 2 ||
		status.Queue.Probe != 1 ||
		status.Queue.Recurring != 1 ||
		status.Queue.Leased != 1 {
		t.Errorf(
			"queue summary = %#v, want total=2 probe=1 recurring=1 leased=1",
			status.Queue,
		)
	}

	if status.Queue.OldestAvailableAt == nil {
		t.Error(
			"OldestAvailableAt = nil, want value",
		)
	}

	if status.Sources.Total != 3 ||
		status.Sources.Seeded != 1 ||
		status.Sources.Automatic != 2 ||
		status.Sources.Verified != 1 ||
		status.Sources.Blocked != 1 ||
		status.Sources.CrawlEligible != 2 {
		t.Errorf(
			"source summary = %#v",
			status.Sources,
		)
	}

	if len(status.Services) != 1 {
		t.Fatalf(
			"services = %d, want 1",
			len(status.Services),
		)
	}

	if status.Services[0].Service != "worker" ||
		status.Services[0].InstanceID != "worker-1" ||
		status.Services[0].State != "running" {
		t.Errorf(
			"service = %#v",
			status.Services[0],
		)
	}

	sources, err := reader.Sources(
		ctx,
		10,
	)
	if err != nil {
		t.Fatalf(
			"Sources() error = %v",
			err,
		)
	}

	if len(sources) != 3 {
		t.Fatalf(
			"Sources() length = %d, want 3",
			len(sources),
		)
	}

	sourceByOrigin := make(
		map[string]CrawlSource,
		len(sources),
	)

	for _, source := range sources {
		sourceByOrigin[source.Origin] = source
	}

	seeded := sourceByOrigin["https://seed.example"]

	if !seeded.Seeded ||
		!seeded.Verified ||
		!seeded.CrawlEligible ||
		seeded.Blocked {
		t.Errorf(
			"seeded source = %#v",
			seeded,
		)
	}

	blocked := sourceByOrigin["https://blocked.example"]

	if !blocked.AutomaticallyDiscovered ||
		!blocked.Blocked ||
		blocked.CrawlEligible {
		t.Errorf(
			"blocked source = %#v",
			blocked,
		)
	}

	automatic := sourceByOrigin["https://auto.example"]

	if !automatic.AutomaticallyDiscovered ||
		automatic.Blocked ||
		!automatic.CrawlEligible {
		t.Errorf(
			"automatic source = %#v",
			automatic,
		)
	}

	runs, err := reader.Crawls(
		ctx,
		10,
	)
	if err != nil {
		t.Fatalf(
			"Crawls() error = %v",
			err,
		)
	}

	if len(runs) != 1 {
		t.Fatalf(
			"Crawls() length = %d, want 1",
			len(runs),
		)
	}

	if runs[0].ID != fixture.crawlID ||
		runs[0].SourceOrigin !=
			"https://seed.example" ||
		runs[0].Outcome != "complete" ||
		runs[0].PromotionsAdmitted != 1 ||
		runs[0].PromotionsDeferred != 2 ||
		runs[0].FailureCategory != "transport" ||
		runs[0].PagesBlocked != 3 ||
		runs[0].PagesFailed != 4 ||
		runs[0].URLsFound != 5 ||
		runs[0].URLsEnqueued != 6 ||
		runs[0].FrontierRemaining != 7 {
		t.Errorf(
			"crawl run = %#v",
			runs[0],
		)
	}

	detail, found, err := reader.Crawl(
		ctx,
		fixture.crawlID,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v",
			err,
		)
	}

	if !found {
		t.Fatal(
			"Crawl() found = false, want true",
		)
	}

	if detail.Run.ID != fixture.crawlID {
		t.Errorf(
			"Crawl() run ID = %d, want %d",
			detail.Run.ID,
			fixture.crawlID,
		)
	}

	if len(detail.Pages) != 1 {
		t.Fatalf(
			"Crawl() pages = %d, want 1",
			len(detail.Pages),
		)
	}

	if detail.Pages[0].Sequence != 1 ||
		detail.Pages[0].StatusCode == nil ||
		*detail.Pages[0].StatusCode != 200 ||
		detail.Pages[0].Outcome != "complete" ||
		detail.Pages[0].FailureCategory != "transport" ||
		detail.Pages[0].URLsFound != 8 ||
		detail.Pages[0].URLsEnqueued != 9 {
		t.Errorf(
			"page attempt = %#v",
			detail.Pages[0],
		)
	}

	_, found, err = reader.Crawl(
		ctx,
		fixture.crawlID+1000,
	)
	if err != nil {
		t.Fatalf(
			"missing Crawl() error = %v",
			err,
		)
	}

	if found {
		t.Error(
			"missing Crawl() found = true, want false",
		)
	}

	queue, err := reader.Queue(
		ctx,
		10,
	)
	if err != nil {
		t.Fatalf(
			"Queue() error = %v",
			err,
		)
	}

	if len(queue) != 2 {
		t.Fatalf(
			"Queue() length = %d, want 2",
			len(queue),
		)
	}

	var leasedProbe *QueueItem

	for index := range queue {
		if queue[index].Mode == "probe" {
			leasedProbe = &queue[index]

			break
		}
	}

	if leasedProbe == nil {
		t.Fatal(
			"probe queue item not found",
		)
	}

	if leasedProbe.LeaseOwner != "worker-1" ||
		leasedProbe.LeaseExpiresAt == nil ||
		leasedProbe.LastClaimedAt == nil {
		t.Errorf(
			"leased probe = %#v",
			leasedProbe,
		)
	}

	events, err := reader.QueueEvents(
		ctx,
		10,
	)
	if err != nil {
		t.Fatalf(
			"QueueEvents() error = %v",
			err,
		)
	}

	if len(events) != 3 {
		t.Fatalf(
			"QueueEvents() length = %d, want 3",
			len(events),
		)
	}

	if events[0].Event != "claimed" ||
		events[0].Mode != "probe" ||
		events[0].LeaseOwner != "worker-1" {
		t.Errorf(
			"latest queue event = %#v",
			events[0],
		)
	}

	services, err := reader.Services(ctx)
	if err != nil {
		t.Fatalf(
			"Services() error = %v",
			err,
		)
	}

	if len(services) != 1 ||
		services[0].Service != "worker" {
		t.Errorf(
			"Services() = %#v",
			services,
		)
	}
}

func TestPostgresReaderReportsInactiveBackpressure(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)

	seedReportingFixture(
		t,
		pool,
	)

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{
			AutomaticCrawlEnabled: false,
			MaxPendingProbes:      1,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	status, err := reader.Status(ctx)
	if err != nil {
		t.Fatalf(
			"Status() error = %v",
			err,
		)
	}

	if status.Backpressure.Active {
		t.Error(
			"Backpressure.Active = true, want false",
		)
	}
}

func TestPostgresReaderRejectsInvalidRequests(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{
			MaxPendingProbes: 1000,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	if _, err := reader.Sources(
		ctx,
		0,
	); !errors.Is(err, errInvalidLimit) {
		t.Errorf(
			"Sources(0) error = %v, want %v",
			err,
			errInvalidLimit,
		)
	}

	if _, err := reader.Crawls(
		ctx,
		maxLimit+1,
	); !errors.Is(err, errInvalidLimit) {
		t.Errorf(
			"Crawls(max+1) error = %v, want %v",
			err,
			errInvalidLimit,
		)
	}

	if _, _, err := reader.Crawl(
		ctx,
		0,
	); !errors.Is(err, errInvalidCrawlID) {
		t.Errorf(
			"Crawl(0) error = %v, want %v",
			err,
			errInvalidCrawlID,
		)
	}

	if _, err := reader.Queue(
		ctx,
		0,
	); !errors.Is(err, errInvalidLimit) {
		t.Errorf(
			"Queue(0) error = %v, want %v",
			err,
			errInvalidLimit,
		)
	}

	if _, err := reader.QueueEvents(
		ctx,
		-1,
	); !errors.Is(err, errInvalidLimit) {
		t.Errorf(
			"QueueEvents(-1) error = %v, want %v",
			err,
			errInvalidLimit,
		)
	}
	if _, err := reader.Audits(
		ctx,
		0,
	); !errors.Is(err, errInvalidLimit) {
		t.Errorf(
			"Audits(0) error = %v, want %v",
			err,
			errInvalidLimit,
		)
	}
}

func TestPostgresReaderPreservesDatabaseFailures(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{
			MaxPendingProbes: 1000,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	pool.Close()

	checks := []struct {
		name string
		call func() error
	}{
		{
			name: "status",
			call: func() error {
				_, err := reader.Status(ctx)

				return err
			},
		},
		{
			name: "sources",
			call: func() error {
				_, err := reader.Sources(
					ctx,
					1,
				)

				return err
			},
		},
		{
			name: "crawls",
			call: func() error {
				_, err := reader.Crawls(
					ctx,
					1,
				)

				return err
			},
		},
		{
			name: "crawl",
			call: func() error {
				_, _, err := reader.Crawl(
					ctx,
					1,
				)

				return err
			},
		},
		{
			name: "crawl pages",
			call: func() error {
				_, err := reader.crawlPages(
					ctx,
					1,
				)

				return err
			},
		},
		{
			name: "queue",
			call: func() error {
				_, err := reader.Queue(
					ctx,
					1,
				)

				return err
			},
		},
		{
			name: "queue events",
			call: func() error {
				_, err := reader.QueueEvents(
					ctx,
					1,
				)

				return err
			},
		},
		{
			name: "services",
			call: func() error {
				_, err := reader.Services(ctx)

				return err
			},
		},
		{
			name: "audits",
			call: func() error {
				_, err := reader.Audits(ctx, 1)
				return err
			},
		},
		{
			name: "metrics",
			call: func() error {
				_, err := reader.Metrics(ctx)
				return err
			},
		},
	}

	for _, check := range checks {
		t.Run(
			check.name,
			func(t *testing.T) {
				if err := check.call(); err == nil {
					t.Error(
						"error = nil, want database failure",
					)
				}
			},
		)
	}
}

func seedReportingFixture(
	t *testing.T,
	pool *pgxpool.Pool,
) reportingFixture {
	t.Helper()

	ctx := context.Background()

	now := time.Now().
		UTC().
		Truncate(time.Microsecond)

	seeded := "https://seed.example"
	blocked := "https://blocked.example"
	automatic := "https://auto.example"

	_, err := pool.Exec(
		ctx,
		`
			UPDATE crawl_control
			SET
				discovery_paused = false,
				verification_paused = true,
				updated_at = $1
			WHERE singleton
		`,
		now,
	)
	if err != nil {
		t.Fatalf(
			"update crawl control: %v",
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		`
			INSERT INTO origins (
				origin,
				first_observed_at
			)
			VALUES
				($1, $4),
				($2, $4),
				($3, $4)
		`,
		seeded,
		blocked,
		automatic,
		now.Add(-48*time.Hour),
	)
	if err != nil {
		t.Fatalf(
			"insert origins: %v",
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		`
			INSERT INTO verification_observations (
				origin,
				observed_at,
				outcome,
				version,
				identity
			)
			VALUES (
				$1,
				$2,
				'valid',
				1,
				'undeclared'
			)
		`,
		seeded,
		now.Add(-2*time.Hour),
	)
	if err != nil {
		t.Fatalf(
			"insert verification observation: %v",
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		`
			INSERT INTO discovery_candidates (
				origin,
				first_discovered_at,
				last_discovered_at
			)
			VALUES
				(
					$1,
					$3,
					$4
				),
				(
					$2,
					$3,
					$4
				)
		`,
		blocked,
		automatic,
		now.Add(-24*time.Hour),
		now.Add(-time.Hour),
	)
	if err != nil {
		t.Fatalf(
			"insert discovery candidates: %v",
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_state (
				source_origin,
				last_attempted_at,
				seeded,
				automatically_discovered,
				crawl_blocked
			)
			VALUES
				(
					$1,
					$4,
					true,
					false,
					false
				),
				(
					$2,
					$4,
					false,
					true,
					true
				),
				(
					$3,
					$4,
					false,
					true,
					false
				)
		`,
		seeded,
		blocked,
		automatic,
		now.Add(-time.Hour),
	)
	if err != nil {
		t.Fatalf(
			"insert discovery source state: %v",
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		`
			INSERT INTO verification_queue (
				origin,
				available_at,
				mode
			)
			VALUES
				(
					$1,
					$3,
					'recurring'
				),
				(
					$2,
					$4,
					'probe'
				)
		`,
		seeded,
		automatic,
		now.Add(-20*time.Minute),
		now.Add(-10*time.Minute),
	)
	if err != nil {
		t.Fatalf(
			"insert verification queue: %v",
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		`
			UPDATE verification_queue
			SET
				lease_generation =
					lease_generation + 1,
				lease_owner = 'worker-1',
				lease_expires_at = $2,
				last_claimed_at = $1
			WHERE origin = $3
		`,
		now,
		now.Add(10*time.Minute),
		automatic,
	)
	if err != nil {
		t.Fatalf(
			"claim verification queue fixture: %v",
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		`
			INSERT INTO crawl_service_heartbeats (
				service,
				instance_id,
				state,
				current_origin,
				message,
				started_at,
				updated_at
			)
			VALUES (
				'worker',
				'worker-1',
				'running',
				$1,
				'',
				$2,
				$3
			)
		`,
		automatic,
		now.Add(-time.Hour),
		now,
	)
	if err != nil {
		t.Fatalf(
			"insert service heartbeat: %v",
			err,
		)
	}

	var crawlID int64

	err = pool.QueryRow(
		ctx,
		`
			INSERT INTO crawl_runs (
				source_origin,
				started_at,
				finished_at,
				outcome,
				stop_reason,
				pages_attempted,
				pages_parsed,
				candidates_discovered,
				budget_exhausted,
				max_depth,
				max_pages,
				max_page_bytes,
				request_delay_milliseconds,
				redirect_limit,
				page_timeout_milliseconds
			)
			VALUES (
				$1,
				$2,
				$3,
				'complete',
				'frontier_exhausted',
				1,
				1,
				2,
				false,
				2,
				32,
				1048576,
				1000,
				5,
				10000
			)
			RETURNING id
		`,
		seeded,
		now.Add(-time.Hour),
		now.Add(-30*time.Minute),
	).Scan(&crawlID)
	if err != nil {
		t.Fatalf(
			"insert crawl run: %v",
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		`
			INSERT INTO crawl_page_attempts (
				run_id,
				sequence,
				requested_url,
				final_url,
				depth,
				started_at,
				duration_milliseconds,
				status_code,
				response_bytes,
				content_type,
				redirect_count,
				robots_decision,
				internal_link_count,
				external_link_count,
				outcome
			)
			VALUES (
				$1,
				1,
				$2,
				$2,
				0,
				$3,
				125,
				200,
				4096,
				'text/html',
				0,
				'allowed',
				3,
				2,
				'complete'
			)
		`,
		crawlID,
		seeded+"/",
		now.Add(-time.Hour),
	)
	if err != nil {
		t.Fatalf(
			"insert crawl page: %v",
			err,
		)
	}

	return reportingFixture{
		crawlID: crawlID,
	}
}

func newReportingTestPool(
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

	adminConfig, err := pgxpool.ParseConfig(
		databaseURL,
	)
	if err != nil {
		t.Fatalf(
			"parse test database URL: %v",
			err,
		)
	}

	adminPool, err := pgxpool.NewWithConfig(
		ctx,
		adminConfig,
	)
	if err != nil {
		t.Fatalf(
			"connect test database: %v",
			err,
		)
	}

	schema := fmt.Sprintf(
		"reporting_test_%d_%d",
		os.Getpid(),
		reportingSchemaCounter.Add(1),
	)

	if _, err := adminPool.Exec(
		ctx,
		"CREATE SCHEMA "+schema,
	); err != nil {
		adminPool.Close()

		t.Fatalf(
			"create test schema: %v",
			err,
		)
	}

	testConfig, err := pgxpool.ParseConfig(
		databaseURL,
	)
	if err != nil {
		_, _ = adminPool.Exec(
			ctx,
			"DROP SCHEMA "+schema+" CASCADE",
		)

		adminPool.Close()

		t.Fatalf(
			"parse isolated test database URL: %v",
			err,
		)
	}

	if testConfig.ConnConfig.RuntimeParams == nil {
		testConfig.ConnConfig.RuntimeParams =
			make(map[string]string)
	}

	testConfig.ConnConfig.RuntimeParams["search_path"] = schema

	pool, err := pgxpool.NewWithConfig(
		ctx,
		testConfig,
	)
	if err != nil {
		_, _ = adminPool.Exec(
			ctx,
			"DROP SCHEMA "+schema+" CASCADE",
		)

		adminPool.Close()

		t.Fatalf(
			"connect isolated test schema: %v",
			err,
		)
	}

	if err := store.Migrate(
		ctx,
		pool,
	); err != nil {
		pool.Close()

		_, _ = adminPool.Exec(
			ctx,
			"DROP SCHEMA "+schema+" CASCADE",
		)

		adminPool.Close()

		t.Fatalf(
			"migrate isolated test schema: %v",
			err,
		)
	}

	t.Cleanup(
		func() {
			pool.Close()

			_, _ = adminPool.Exec(
				context.Background(),
				"DROP SCHEMA "+schema+" CASCADE",
			)

			adminPool.Close()
		},
	)

	return pool
}

func TestPostgresReaderStatusPreservesServiceFailure(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)

	seedReportingFixture(
		t,
		pool,
	)

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{
			AutomaticCrawlEnabled: true,
			MaxPendingProbes:      1000,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		"DROP TABLE crawl_service_heartbeats",
	); err != nil {
		t.Fatalf(
			"drop crawl_service_heartbeats: %v",
			err,
		)
	}

	_, err = reader.Status(ctx)
	if err == nil {
		t.Fatal(
			"Status() error = nil, want service failure",
		)
	}
}

func TestPostgresReaderCrawlPreservesPageFailure(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)

	fixture := seedReportingFixture(
		t,
		pool,
	)

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{
			MaxPendingProbes: 1000,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		"DROP TABLE crawl_page_attempts",
	); err != nil {
		t.Fatalf(
			"drop crawl_page_attempts: %v",
			err,
		)
	}

	detail, found, err := reader.Crawl(
		ctx,
		fixture.crawlID,
	)

	if err == nil {
		t.Fatal(
			"Crawl() error = nil, want page failure",
		)
	}

	if found {
		t.Error(
			"Crawl() found = true, want false on page failure",
		)
	}

	if detail.Run.ID != 0 ||
		detail.Run.SourceOrigin != "" ||
		detail.Pages != nil {
		t.Errorf(
			"Crawl() detail = %#v, want zero value",
			detail,
		)
	}
}

func TestServicesReturnsAgedCurrentHeartbeat(t *testing.T) {
	ctx := context.Background()
	pool := newReportingTestPool(t)
	started := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	updated := started.Add(5 * time.Second)
	if _, err := pool.Exec(ctx, `
		INSERT INTO crawl_service_heartbeats (
			service, instance_id, state, current_origin, message,
			started_at, updated_at
		) VALUES (
			'worker', 'worker', 'running', '', 'idle-slot', $1, $2
		)
	`, started, updated); err != nil {
		t.Fatalf("insert aged heartbeat: %v", err)
	}

	reader, err := NewPostgresReader(pool, PostgresConfig{
		MaxPendingProbes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	services, err := reader.Services(ctx)
	if err != nil {
		t.Fatalf("Services() error = %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("Services() length = %d, want 1", len(services))
	}
	service := services[0]
	if service.Service != "worker" ||
		service.InstanceID != "worker" ||
		service.State != "running" ||
		service.Message != "idle-slot" ||
		!service.StartedAt.Equal(started) ||
		!service.UpdatedAt.Equal(updated) {
		t.Fatalf("Services() = %#v", service)
	}
}

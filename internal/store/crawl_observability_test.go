package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

func TestCrawlObservabilityPersistsSafeOrderedHistory(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	observability, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	source := mustStoreOrigin(t, "https://source.example")
	config := discovery.CrawlConfig{
		MaxDepth: 4, MaxPages: 50, MaxPageBytes: 1024,
		RequestDelay: time.Second, RedirectLimit: 5, PageTimeout: 10 * time.Second,
	}

	runID, err := observability.BeginCrawl(ctx, source, config)
	if err != nil || runID <= 0 {
		t.Fatalf("BeginCrawl() = %d, %v", runID, err)
	}
	status := 200
	attempt := discovery.PageAttempt{
		Sequence: 1, RequestedURL: "https://source.example/private/path",
		FinalURL: "https://source.example/final", Depth: 0,
		StartedAt: time.Now().UTC(), Duration: 125 * time.Millisecond,
		StatusCode: &status, ResponseBytes: 412, ContentType: "text/html",
		RedirectCount: 1, RobotsDecision: discovery.RobotsAllowed,
		InternalLinkCount: 3, ExternalLinkCount: 7,
		Outcome: discovery.PageComplete, FailureCategory: retry.CategoryTransport,
		URLsFound: 10, URLsEnqueued: 2,
	}
	if err := observability.RecordPageAttempt(ctx, runID, attempt); err != nil {
		t.Fatalf("RecordPageAttempt() error = %v", err)
	}
	result := discovery.CrawlResult{
		Source: source, PagesAttempted: 1, PagesParsed: 1,
		CandidatesDiscovered: 7,
	}
	if err := observability.FinishCrawlSummary(
		ctx, runID, result, discovery.CrawlRunComplete, "frontier_exhausted", 4,
	); err != nil {
		t.Fatalf("FinishCrawl() error = %v", err)
	}

	var (
		outcome, reason, requested, final, robots, pageOutcome string
		failureCategory, pageFailureCategory                   string
		attempted, parsed, candidates, sequence, depth, code   int
		pagesFailed, urlsFound, urlsEnqueued, remaining        int
		bytes, duration                                        int64
	)
	err = pool.QueryRow(ctx, `
		SELECT r.outcome, r.stop_reason, r.pages_attempted, r.pages_parsed,
			r.candidates_discovered, p.sequence, p.requested_url, p.final_url,
			p.depth, p.status_code, p.response_bytes, p.duration_milliseconds,
			p.robots_decision, p.outcome, r.failure_category, p.failure_category,
			r.pages_failed, r.urls_found, r.urls_enqueued, r.frontier_remaining
		FROM crawl_runs AS r
		JOIN crawl_page_attempts AS p ON p.run_id = r.id
		WHERE r.id = $1
	`, int64(runID)).Scan(
		&outcome, &reason, &attempted, &parsed, &candidates, &sequence,
		&requested, &final, &depth, &code, &bytes, &duration, &robots,
		&pageOutcome, &failureCategory, &pageFailureCategory, &pagesFailed,
		&urlsFound, &urlsEnqueued, &remaining,
	)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != "complete" || reason != "frontier_exhausted" ||
		attempted != 1 || parsed != 1 || candidates != 7 || sequence != 1 ||
		requested != attempt.RequestedURL || final != attempt.FinalURL ||
		depth != 0 || code != 200 || bytes != 412 || duration != 125 ||
		robots != "allowed" || pageOutcome != "complete" ||
		failureCategory != "none" || pageFailureCategory != "transport" ||
		pagesFailed != 0 || urlsFound != 10 || urlsEnqueued != 2 ||
		remaining != 4 {
		t.Fatalf("stored crawl telemetry did not match: %q %q %d %d %d %d %q %q %d %d %d %d %q %q",
			outcome, reason, attempted, parsed, candidates, sequence, requested,
			final, depth, code, bytes, duration, robots, pageOutcome)
	}
}

func TestCrawlObservabilityRejectsInvalidState(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	observability, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	source := mustStoreOrigin(t, "https://source.example")
	config := discovery.CrawlConfig{MaxPages: 1, MaxPageBytes: 1, RedirectLimit: 1, PageTimeout: time.Second}
	validAttempt := discovery.PageAttempt{Sequence: 1, RequestedURL: "https://source.example/", FinalURL: "https://source.example/", StartedAt: time.Now(), RobotsDecision: discovery.RobotsAllowed, Outcome: discovery.PageComplete}

	if _, err := (*DiscoveryStore)(nil).BeginCrawl(ctx, source, config); err == nil {
		t.Error("nil BeginCrawl() error = nil")
	}
	if _, err := observability.BeginCrawl(ctx, origin.Origin{}, config); !errors.Is(err, errInvalidCrawlRun) {
		t.Errorf("invalid BeginCrawl() error = %v", err)
	}
	badConfig := config
	badConfig.MaxPages = 0
	if _, err := observability.BeginCrawl(ctx, source, badConfig); !errors.Is(err, errInvalidCrawlRun) {
		t.Errorf("bad config error = %v", err)
	}
	badConfig = config
	badConfig.MaxAutomaticPromotionsPerRun = -1
	if _, err := observability.BeginCrawl(ctx, source, badConfig); !errors.Is(err, errInvalidCrawlRun) {
		t.Errorf("negative promotion budget error = %v", err)
	}
	if err := (*DiscoveryStore)(nil).RecordPageAttempt(ctx, 1, validAttempt); err == nil {
		t.Error("nil RecordPageAttempt() error = nil")
	}
	if err := observability.RecordPageAttempt(ctx, 0, validAttempt); !errors.Is(err, errInvalidPageAttempt) {
		t.Errorf("invalid attempt error = %v", err)
	}
	if err := observability.RecordPageAttempt(ctx, 999999, validAttempt); !errors.Is(err, errUnknownCrawlRun) {
		t.Errorf("unknown run record error = %v", err)
	}
	if err := (*DiscoveryStore)(nil).FinishCrawl(ctx, 1, discovery.CrawlResult{}, discovery.CrawlRunComplete, "done"); err == nil {
		t.Error("nil FinishCrawl() error = nil")
	}
	if err := observability.FinishCrawl(ctx, 0, discovery.CrawlResult{}, discovery.CrawlRunComplete, "done"); !errors.Is(err, errInvalidCrawlRun) {
		t.Errorf("invalid finish error = %v", err)
	}
	if err := observability.FinishCrawl(ctx, 999999, discovery.CrawlResult{}, discovery.CrawlRunComplete, "done"); !errors.Is(err, errUnknownCrawlRun) {
		t.Errorf("unknown finish error = %v", err)
	}
	if _, err := (*DiscoveryStore)(nil).PurgeCrawlTelemetry(ctx, time.Hour); err == nil {
		t.Error("nil purge error = nil")
	}
	if _, err := observability.PurgeCrawlTelemetry(ctx, 0); !errors.Is(err, errInvalidRetention) {
		t.Errorf("invalid retention error = %v", err)
	}
	if _, err := (*DiscoveryStore)(nil).CrawlControl(ctx); err == nil {
		t.Error("nil control error = nil")
	}
	if err := (*DiscoveryStore)(nil).SetProcessorPaused(ctx, "discovery", true); err == nil {
		t.Error("nil pause error = nil")
	}
	if err := observability.SetProcessorPaused(ctx, "unknown", true); !errors.Is(err, errInvalidServiceState) {
		t.Errorf("invalid processor error = %v", err)
	}
	if err := (*DiscoveryStore)(nil).UpsertServiceHeartbeat(ctx, ServiceHeartbeat{}); err == nil {
		t.Error("nil heartbeat error = nil")
	}
	if err := observability.UpsertServiceHeartbeat(ctx, ServiceHeartbeat{}); !errors.Is(err, errInvalidServiceState) {
		t.Errorf("invalid heartbeat error = %v", err)
	}
}

func TestCrawlObservabilityReturnsDatabaseFailures(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	observability, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	source := mustStoreOrigin(t, "https://source.example")
	config := discovery.CrawlConfig{MaxPages: 1, MaxPageBytes: 1, RedirectLimit: 1, PageTimeout: time.Second}
	attempt := discovery.PageAttempt{Sequence: 1, RequestedURL: "https://source.example/", FinalURL: "https://source.example/", StartedAt: time.Now(), RobotsDecision: discovery.RobotsAllowed, Outcome: discovery.PageComplete}
	heartbeat := ServiceHeartbeat{Service: "worker", InstanceID: "one", State: "running", StartedAt: time.Now(), UpdatedAt: time.Now()}

	checks := []func() error{
		func() error { _, err := observability.BeginCrawl(ctx, source, config); return err },
		func() error { return observability.RecordPageAttempt(ctx, 1, attempt) },
		func() error {
			return observability.FinishCrawl(ctx, 1, discovery.CrawlResult{}, discovery.CrawlRunComplete, "done")
		},
		func() error { _, err := observability.PurgeCrawlTelemetry(ctx, time.Hour); return err },
		func() error { _, err := observability.CrawlControl(ctx); return err },
		func() error { return observability.SetProcessorPaused(ctx, "discovery", true) },
		func() error { return observability.UpsertServiceHeartbeat(ctx, heartbeat) },
	}
	for index, check := range checks {
		if err := check(); err == nil {
			t.Errorf("database failure %d = nil", index)
		}
	}
}

func TestCrawlObservabilityValidationHelpers(t *testing.T) {
	statusLow, statusHigh := 99, 600
	attempts := []discovery.PageAttempt{
		{},
		{Sequence: 1, Depth: -1},
		{Sequence: 1, StartedAt: time.Now(), Duration: -1},
		{Sequence: 1, StartedAt: time.Now(), ResponseBytes: -1},
		{Sequence: 1, StartedAt: time.Now(), RedirectCount: -1},
		{Sequence: 1, StartedAt: time.Now(), InternalLinkCount: -1},
		{Sequence: 1, StartedAt: time.Now(), ExternalLinkCount: -1},
		{Sequence: 1, StartedAt: time.Now(), ContentType: strings.Repeat("x", 257)},
		{Sequence: 1, StartedAt: time.Now(), RequestedURL: "https://user:pass@example.test/", FinalURL: "https://example.test/", RobotsDecision: discovery.RobotsAllowed, Outcome: discovery.PageComplete},
		{Sequence: 1, StartedAt: time.Now(), RequestedURL: "https://example.test/?secret=yes", FinalURL: "https://example.test/", RobotsDecision: discovery.RobotsAllowed, Outcome: discovery.PageComplete},
		{Sequence: 1, StartedAt: time.Now(), RequestedURL: "https://example.test/", FinalURL: "https://example.test/", RobotsDecision: "invalid", Outcome: discovery.PageComplete},
		{Sequence: 1, StartedAt: time.Now(), RequestedURL: "https://example.test/", FinalURL: "https://example.test/", RobotsDecision: discovery.RobotsAllowed, Outcome: "invalid"},
		{Sequence: 1, StartedAt: time.Now(), RequestedURL: "https://example.test/", FinalURL: "https://example.test/", RobotsDecision: discovery.RobotsAllowed, Outcome: discovery.PageComplete, StatusCode: &statusLow},
		{Sequence: 1, StartedAt: time.Now(), RequestedURL: "https://example.test/", FinalURL: "https://example.test/", RobotsDecision: discovery.RobotsAllowed, Outcome: discovery.PageComplete, StatusCode: &statusHigh},
	}
	for index, attempt := range attempts {
		if validPageAttempt(attempt) {
			t.Errorf("invalid attempt %d accepted", index)
		}
	}
	for _, outcome := range []discovery.CrawlRunOutcome{"", "running"} {
		if validRunOutcome(outcome) {
			t.Errorf("invalid run outcome %q accepted", outcome)
		}
	}
}

func TestProcessorsFailClosedWhenControlStateIsUnavailable(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	discoveryStore, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := NewQueue(pool, QueueConfig{
		LeaseDuration: time.Minute, MinOriginInterval: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DROP TABLE crawl_control"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := discoveryStore.ClaimDiscoverySource(ctx, time.Hour); err == nil {
		t.Error("ClaimDiscoverySource() error = nil")
	}
	if _, _, err := queue.Claim(ctx, "worker"); err == nil {
		t.Error("Claim() error = nil")
	}
}

func TestCrawlControlPausesBothProcessorsIndependently(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	observability, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := observability.SetProcessorPaused(ctx, "discovery", true); err != nil {
		t.Fatal(err)
	}
	if err := observability.SetProcessorPaused(ctx, "verification", true); err != nil {
		t.Fatal(err)
	}
	control, err := observability.CrawlControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !control.DiscoveryPaused || !control.VerificationPaused || control.UpdatedAt.IsZero() {
		t.Fatalf("control = %#v", control)
	}

	seed := mustStoreOrigin(t, "https://seed.example")
	if err := observability.AddCrawlSeed(ctx, seed); err != nil {
		t.Fatal(err)
	}
	if claimed, found, err := observability.ClaimDiscoverySource(ctx, time.Hour); err != nil || found || claimed.String() != "" {
		t.Fatalf("paused discovery claim = %q, %v, %v", claimed.String(), found, err)
	}
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     time.Minute,
			MinOriginInterval: time.Second,
		},
		queueTestTime(),
	)
	if err := queue.Schedule(ctx, seed, queueTestTime()); err != nil {
		t.Fatal(err)
	}
	if lease, found, err := queue.Claim(ctx, "worker"); err != nil || found || lease != (Lease{}) {
		t.Fatalf("paused queue claim = %#v, %v, %v", lease, found, err)
	}
}

func TestCrawlHeartbeatAndRetention(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	observability, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	heartbeat := ServiceHeartbeat{
		Service: "discovery", InstanceID: "discovery-1", State: "running",
		CurrentOrigin: mustStoreOrigin(t, "https://source.example"),
		Message:       "crawling", StartedAt: now.Add(-time.Minute), UpdatedAt: now,
	}
	if err := observability.UpsertServiceHeartbeat(ctx, heartbeat); err != nil {
		t.Fatal(err)
	}
	heartbeat.State = "idle"
	heartbeat.CurrentOrigin = mustStoreOrigin(t, "https://source.example")
	heartbeat.Message = "waiting"
	heartbeat.UpdatedAt = now.Add(time.Second)
	if err := observability.UpsertServiceHeartbeat(ctx, heartbeat); err != nil {
		t.Fatal(err)
	}

	var state, message string
	if err := pool.QueryRow(ctx, `
		SELECT state, message FROM crawl_service_heartbeats
		WHERE service = 'discovery' AND instance_id = 'discovery-1'
	`).Scan(&state, &message); err != nil {
		t.Fatal(err)
	}
	if state != "idle" || message != "waiting" {
		t.Fatalf("heartbeat = %q %q", state, message)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO crawl_runs (
			source_origin, started_at, finished_at, outcome, max_depth,
			max_pages, max_page_bytes, request_delay_milliseconds,
			redirect_limit, page_timeout_milliseconds
		) VALUES (
			'https://old.example', statement_timestamp() - interval '40 days',
			statement_timestamp() - interval '39 days', 'complete',
			1, 1, 1, 0, 1, 1
		), (
			'https://running.example', statement_timestamp() - interval '40 days',
			NULL, 'running', 1, 1, 1, 0, 1, 1
		)
	`); err != nil {
		t.Fatal(err)
	}
	removed, err := observability.PurgeCrawlTelemetry(ctx, 30*24*time.Hour)
	if err != nil || removed != 1 {
		t.Fatalf("PurgeCrawlTelemetry() = %d, %v", removed, err)
	}
}

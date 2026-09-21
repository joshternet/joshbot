package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestCrawlObservabilityMigrationCreatesDurableContract(t *testing.T) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var tables int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
			AND table_name IN (
				'crawl_control', 'crawl_runs', 'crawl_page_attempts',
				'crawl_service_heartbeats', 'verification_queue_events'
			)
	`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 5 {
		t.Fatalf("observability table count = %d, want 5", tables)
	}

	var triggers int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_trigger AS trigger
		JOIN pg_class AS relation ON relation.oid = trigger.tgrelid
		JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		WHERE trigger.tgname = 'verification_queue_event_trigger'
			AND NOT trigger.tgisinternal
			AND namespace.nspname = current_schema()
	`).Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	if triggers != 1 {
		t.Fatalf("queue event trigger count = %d, want 1", triggers)
	}
}

func TestCrawlObservabilityPermissionMigrationAppliesWithoutRuntimeRole(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)
	for _, migration := range []string{
		"migrations/0001_initial.sql",
		"migrations/0002_verification_queue.sql",
		"migrations/0003_discovery.sql",
		"migrations/0004_crawl_sources.sql",
		"migrations/0005_automatic_crawl_sources.sql",
		"migrations/0006_crawl_observability.sql",
		"migrations/0007_crawl_observability_permissions.sql",
	} {
		applyRawStoreMigration(t, ctx, pool, migration)
	}
}

func TestObservabilityReportingMigrationIsAdditive(t *testing.T) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var columns int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema()
			AND (
				(table_name = 'crawl_runs' AND column_name IN (
					'promotions_admitted', 'promotions_deferred',
					'failure_category', 'pages_blocked', 'pages_failed',
					'urls_found', 'urls_enqueued', 'frontier_remaining'
				))
				OR
				(table_name = 'crawl_page_attempts' AND column_name IN (
					'failure_category', 'urls_found', 'urls_enqueued'
				))
			)
	`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 11 {
		t.Fatalf("migration 0012 observable columns = %d, want 11", columns)
	}

	var runID int64
	var promotionsAdmitted int
	if err := pool.QueryRow(ctx, `
		INSERT INTO crawl_runs (
			source_origin, started_at, max_depth, max_pages,
			max_page_bytes, request_delay_milliseconds,
			redirect_limit, page_timeout_milliseconds
		) VALUES (
			'https://source.example', statement_timestamp(), 0, 1,
			1, 0, 0, 1
		)
		RETURNING id, promotions_admitted
	`).Scan(&runID, &promotionsAdmitted); err != nil {
		t.Fatal(err)
	}
	if promotionsAdmitted != 0 {
		t.Errorf(
			"default promotions_admitted = %d, want 0",
			promotionsAdmitted,
		)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE crawl_runs
		SET promotions_admitted = -1
		WHERE id = $1
	`, runID); err == nil {
		t.Fatal("negative promotions_admitted update error = nil")
	}
}

func TestEphemeralServiceHeartbeatMigrationClearsGhostRows(t *testing.T) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)
	for _, migration := range []string{
		"migrations/0001_initial.sql",
		"migrations/0002_verification_queue.sql",
		"migrations/0003_discovery.sql",
		"migrations/0004_crawl_sources.sql",
		"migrations/0005_automatic_crawl_sources.sql",
		"migrations/0006_crawl_observability.sql",
		"migrations/0007_crawl_observability_permissions.sql",
		"migrations/0008_crawl_domain_avoid_rules.sql",
		"migrations/0009_automatic_admission.sql",
		"migrations/0010_retry_state.sql",
		"migrations/0011_operator_audit_events.sql",
		"migrations/0012_observability_reporting.sql",
		"migrations/0013_discovery_source_lease.sql",
	} {
		applyRawStoreMigration(t, ctx, pool, migration)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO origins (origin, first_observed_at)
		VALUES ('https://source.example', statement_timestamp());

		INSERT INTO verification_observations (
			origin, observed_at, outcome, version, identity
		) VALUES (
			'https://source.example', statement_timestamp(),
			'valid', 1, 'affirmed'
		);

		INSERT INTO discovery_source_state (source_origin, seeded)
		VALUES ('https://source.example', TRUE);

		INSERT INTO discovery_candidates (
			origin, first_discovered_at, last_discovered_at
		) VALUES (
			'https://candidate.example',
			statement_timestamp(),
			statement_timestamp()
		);

		INSERT INTO verification_queue (origin, available_at)
		VALUES ('https://queued.example', statement_timestamp());

		INSERT INTO operator_audit_events (
			action, target, caller, actor, result
		) VALUES (
			'pause', 'discovery', 'operator', 'josh', 'success'
		);

		INSERT INTO crawl_runs (
			source_origin, started_at, max_depth, max_pages,
			max_page_bytes, request_delay_milliseconds,
			redirect_limit, page_timeout_milliseconds
		) VALUES (
			'https://source.example', statement_timestamp(), 0, 1,
			1, 0, 0, 1
		);

		INSERT INTO crawl_page_attempts (
			run_id, sequence, requested_url, final_url, depth,
			started_at, duration_milliseconds, response_bytes,
			content_type, redirect_count, robots_decision,
			internal_link_count, external_link_count, outcome
		)
		SELECT id, 1, 'https://source.example/', 'https://source.example/',
			0, statement_timestamp(), 1, 1, 'text/html', 0, 'allowed',
			0, 0, 'complete'
		FROM crawl_runs;

		INSERT INTO crawl_service_heartbeats (
			service, instance_id, state, started_at, updated_at
		) VALUES
			('worker', 'worker-aabbcc', 'running', statement_timestamp(), statement_timestamp()),
			('worker', 'worker-ddeeff', 'stopping', statement_timestamp(), statement_timestamp()),
			('discovery', 'old-container-id', 'failed', statement_timestamp(), statement_timestamp());
	`, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("seed pre-migration history: %v", err)
	}

	applyRawStoreMigration(
		t,
		ctx,
		pool,
		"migrations/0014_clear_ephemeral_service_heartbeats.sql",
	)

	var heartbeats int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM crawl_service_heartbeats
	`).Scan(&heartbeats); err != nil {
		t.Fatal(err)
	}
	if heartbeats != 0 {
		t.Fatalf("heartbeat rows after cleanup = %d, want 0", heartbeats)
	}

	var preserved int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM crawl_runs)
			+ (SELECT count(*) FROM crawl_page_attempts)
			+ (SELECT count(*) FROM verification_queue)
			+ (SELECT count(*) FROM verification_queue_events)
			+ (SELECT count(*) FROM verification_observations)
			+ (SELECT count(*) FROM discovery_source_state)
			+ (SELECT count(*) FROM discovery_candidates)
			+ (SELECT count(*) FROM operator_audit_events)
	`).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if preserved != 8 {
		t.Fatalf("preserved operational rows = %d, want 8", preserved)
	}

	observability, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, heartbeat := range []ServiceHeartbeat{
		{
			Service: "worker", InstanceID: "worker", State: "starting",
			StartedAt: now, UpdatedAt: now,
		},
		{
			Service: "discovery", InstanceID: "discovery", State: "starting",
			StartedAt: now, UpdatedAt: now,
		},
	} {
		if err := observability.UpsertServiceHeartbeat(ctx, heartbeat); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM crawl_service_heartbeats
	`).Scan(&heartbeats); err != nil {
		t.Fatal(err)
	}
	if heartbeats != 2 {
		t.Fatalf("recreated heartbeat rows = %d, want 2", heartbeats)
	}
}

package store

import (
	"context"
	"testing"
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

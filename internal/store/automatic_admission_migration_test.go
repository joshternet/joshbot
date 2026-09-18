package store

import (
	"context"
	"testing"
)

func TestAutomaticAdmissionMigrationAddsRunBudgetState(t *testing.T) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var columnCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema()
			AND table_name = 'crawl_runs'
			AND column_name IN (
				'max_automatic_promotions',
				'automatic_promotions'
			)
	`).Scan(&columnCount); err != nil {
		t.Fatal(err)
	}
	if columnCount != 2 {
		t.Fatalf("automatic admission column count = %d, want 2", columnCount)
	}
	var tableCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.tables
		WHERE table_schema = current_schema()
			AND table_name IN (
				'crawl_run_discovery_candidates',
				'crawl_run_automatic_admission_batches'
			)
	`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 2 {
		t.Fatalf("automatic admission table count = %d, want 2", tableCount)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO crawl_runs (
			source_origin, started_at, max_depth, max_pages, max_page_bytes,
			request_delay_milliseconds, redirect_limit,
			page_timeout_milliseconds, max_automatic_promotions,
			automatic_promotions
		) VALUES (
			'https://invalid.example', statement_timestamp(), 0, 1, 1,
			0, 1, 1, 1, 2
		)
	`); err == nil {
		t.Fatal("crawl run accepted promotions above its immutable budget")
	}
}

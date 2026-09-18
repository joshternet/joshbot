package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRetryMigrationRejectsUnknownCategoriesAndDivergentQueueDueTime(t *testing.T) {
	ctx := context.Background()
	pool := newSerialStoreTestPool(t)

	for _, constraint := range []string{
		"verification_queue_retry_state_check",
		"discovery_candidates_retry_state_check",
		"discovery_source_state_retry_state_check",
		"crawl_run_automatic_admission_batches_retry_state_check",
	} {
		var definition string
		if err := pool.QueryRow(ctx, `
			SELECT pg_get_constraintdef(c.oid)
			FROM pg_constraint AS c
			JOIN pg_namespace AS n ON n.oid = c.connamespace
			WHERE c.conname = $1
				AND n.nspname = current_schema()
		`, constraint).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(definition, "last_failure_category") ||
			!strings.Contains(definition, "timeout") {
			t.Fatalf("constraint %s does not restrict categories: %s", constraint, definition)
		}
	}

	source := mustStoreOrigin(t, "https://example.com")
	queue := newFixedQueue(t, pool, QueueConfig{
		LeaseDuration:     10 * time.Minute,
		MinOriginInterval: time.Minute,
	}, queueTestTime())
	if err := queue.Schedule(ctx, source, queueTestTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE verification_queue
		SET consecutive_failures = 1,
			last_failure_category = 'invented',
			next_attempt_at = available_at
		WHERE origin = $1
	`, source.String()); err == nil {
		t.Fatal("unknown verification retry category accepted")
	}
	if _, err := pool.Exec(ctx, `
		UPDATE verification_queue
		SET consecutive_failures = 1,
			last_failure_category = 'timeout',
			next_attempt_at = available_at + interval '1 minute'
		WHERE origin = $1
	`, source.String()); err == nil {
		t.Fatal("divergent verification due times accepted")
	}
}

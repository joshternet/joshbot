package store

import (
	"context"
	"strings"
	"testing"
)

func TestVerificationQueueAbandonedEventMigration(t *testing.T) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var definition string
	if err := pool.QueryRow(ctx, `
		SELECT pg_get_functiondef(p.oid)
		FROM pg_proc AS p
		JOIN pg_namespace AS n ON n.oid = p.pronamespace
		WHERE n.nspname = current_schema()
			AND p.proname = 'record_verification_queue_event'
	`).Scan(&definition); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(definition, "queue_event := 'abandoned'") {
		t.Fatalf(
			"trigger function missing abandoned event:\n%s",
			definition,
		)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO verification_queue_events (
			origin,
			event,
			mode
		) VALUES (
			'https://example.com',
			'abandoned',
			'probe'
		)
	`); err != nil {
		t.Fatalf(
			"abandoned event insert error = %v",
			err,
		)
	}
}

package store

import (
	"context"
	"testing"
)

func TestDiscoveryCandidateAdmissionStateMigration(t *testing.T) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var tableCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.tables
		WHERE table_schema = current_schema()
			AND table_name = 'discovery_candidate_admission_state'
	`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 1 {
		t.Fatalf(
			"discovery_candidate_admission_state table count = %d, want 1",
			tableCount,
		)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_candidates (
			origin,
			first_discovered_at,
			last_discovered_at
		) VALUES (
			'https://candidate.example',
			statement_timestamp(),
			statement_timestamp()
		)
	`); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_candidate_admission_state (
			candidate_origin,
			outcome
		) VALUES (
			'https://candidate.example',
			'retry_deferred'
		)
	`); err == nil {
		t.Fatal("non-durable outcome accepted by CHECK")
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_candidate_admission_state (
			candidate_origin,
			outcome
		) VALUES (
			'https://candidate.example',
			'network_rejected'
		)
	`); err != nil {
		t.Fatalf(
			"network_rejected insert error = %v",
			err,
		)
	}
}

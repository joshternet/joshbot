package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCrawlSourceMigrationPreservesExistingState(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	for _, migration := range []string{
		"migrations/0001_initial.sql",
		"migrations/0002_verification_queue.sql",
		"migrations/0003_discovery.sql",
	} {
		applyRawStoreMigration(
			t,
			ctx,
			pool,
			migration,
		)
	}

	source := "https://verified.example"
	attemptedAt := time.Date(
		2026,
		time.September,
		5,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO origins (
				origin,
				first_observed_at
			)
			VALUES ($1, $2)
		`,
		source,
		attemptedAt.Add(-2*time.Hour),
	)
	if err != nil {
		t.Fatalf(
			"insert pre-migration origin: %v",
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
		source,
		attemptedAt.Add(-time.Hour),
	)
	if err != nil {
		t.Fatalf(
			"insert pre-migration observation: %v",
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_state (
				source_origin,
				last_attempted_at
			)
			VALUES ($1, $2)
		`,
		source,
		attemptedAt,
	)
	if err != nil {
		t.Fatalf(
			"insert pre-migration source state: %v",
			err,
		)
	}

	applyRawStoreMigration(
		t,
		ctx,
		pool,
		"migrations/0004_crawl_sources.sql",
	)

	var (
		gotAttemptedAt time.Time
		seeded         bool
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				last_attempted_at,
				seeded
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		source,
	).Scan(
		&gotAttemptedAt,
		&seeded,
	)
	if err != nil {
		t.Fatalf(
			"query migrated source state: %v",
			err,
		)
	}

	if !gotAttemptedAt.Equal(attemptedAt) {
		t.Errorf(
			"last_attempted_at = %v, want %v",
			gotAttemptedAt,
			attemptedAt,
		)
	}

	if seeded {
		t.Error(
			"seeded = true, want false",
		)
	}

	var nullable string
	err = pool.QueryRow(
		ctx,
		`
			SELECT is_nullable
			FROM information_schema.columns
			WHERE table_schema = current_schema()
				AND table_name = 'discovery_source_state'
				AND column_name = 'last_attempted_at'
		`,
	).Scan(&nullable)
	if err != nil {
		t.Fatalf(
			"query last_attempted_at nullability: %v",
			err,
		)
	}

	if nullable != "YES" {
		t.Errorf(
			"last_attempted_at is_nullable = %q, want YES",
			nullable,
		)
	}
}

func TestCrawlSourceMigrationAllowsIndependentSeedProvenance(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)

	const (
		source    = "https://seed.example"
		candidate = "https://candidate.example"
	)

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_state (
				source_origin,
				seeded
			)
			VALUES ($1, true)
		`,
		source,
	)
	if err != nil {
		t.Fatalf(
			"insert independent crawl seed: %v",
			err,
		)
	}

	var (
		seeded          bool
		lastAttemptedAt *time.Time
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				seeded,
				last_attempted_at
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		source,
	).Scan(
		&seeded,
		&lastAttemptedAt,
	)
	if err != nil {
		t.Fatalf(
			"query independent crawl seed: %v",
			err,
		)
	}

	if !seeded {
		t.Error(
			"seeded = false, want true",
		)
	}

	if lastAttemptedAt != nil {
		t.Errorf(
			"last_attempted_at = %v, want nil",
			lastAttemptedAt,
		)
	}

	var originCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM origins
			WHERE origin = $1
		`,
		source,
	).Scan(&originCount)
	if err != nil {
		t.Fatalf(
			"count verification origins: %v",
			err,
		)
	}

	if originCount != 0 {
		t.Errorf(
			"verification origin count = %d, want 0",
			originCount,
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
			VALUES (
				$1,
				clock_timestamp(),
				clock_timestamp()
			)
		`,
		candidate,
	)
	if err != nil {
		t.Fatalf(
			"insert seed candidate: %v",
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		`
			INSERT INTO discovery_edges (
				source_origin,
				candidate_origin,
				kind,
				first_discovered_at,
				last_discovered_at
			)
			VALUES (
				$1,
				$2,
				'link',
				clock_timestamp(),
				clock_timestamp()
			)
		`,
		source,
		candidate,
	)
	if err != nil {
		t.Fatalf(
			"insert independent seed provenance: %v",
			err,
		)
	}

	var edgeCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM discovery_edges
			WHERE source_origin = $1
				AND candidate_origin = $2
		`,
		source,
		candidate,
	).Scan(&edgeCount)
	if err != nil {
		t.Fatalf(
			"count independent seed provenance: %v",
			err,
		)
	}

	if edgeCount != 1 {
		t.Errorf(
			"independent seed edge count = %d, want 1",
			edgeCount,
		)
	}
}

func TestCrawlSourceMigrationIsForwardOnly(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)

	migration, err := os.ReadFile(
		"migrations/0004_crawl_sources.sql",
	)
	if err != nil {
		t.Fatalf(
			"read crawl-source migration: %v",
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		string(migration),
		pgx.QueryExecModeSimpleProtocol,
	)
	if err == nil {
		t.Fatal(
			"reapply crawl-source migration error = nil, want non-nil",
		)
	}
}

func newCrawlSourceMigrationTestPool(
	t *testing.T,
) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	for _, migration := range []string{
		"migrations/0001_initial.sql",
		"migrations/0002_verification_queue.sql",
		"migrations/0003_discovery.sql",
		"migrations/0004_crawl_sources.sql",
	} {
		applyRawStoreMigration(
			t,
			ctx,
			pool,
			migration,
		)
	}

	return pool
}

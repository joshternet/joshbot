package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiscoveryMigrationPreservesExistingQueueAsRecurring(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	applyRawStoreMigration(
		t,
		ctx,
		pool,
		"migrations/0001_initial.sql",
	)
	applyRawStoreMigration(
		t,
		ctx,
		pool,
		"migrations/0002_verification_queue.sql",
	)

	availableAt := time.Date(
		2026,
		time.September,
		2,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO verification_queue (
				origin,
				available_at
			)
			VALUES (
				'https://example.com',
				$1
			)
		`,
		availableAt,
	)
	if err != nil {
		t.Fatalf(
			"insert pre-migration queue row: %v",
			err,
		)
	}

	applyRawStoreMigration(
		t,
		ctx,
		pool,
		"migrations/0003_discovery.sql",
	)

	var (
		mode              string
		storedAvailableAt time.Time
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				mode,
				available_at
			FROM verification_queue
			WHERE origin = 'https://example.com'
		`,
	).Scan(
		&mode,
		&storedAvailableAt,
	)
	if err != nil {
		t.Fatalf(
			"query migrated queue row: %v",
			err,
		)
	}

	if mode != "recurring" {
		t.Errorf(
			"queue mode = %q, want recurring",
			mode,
		)
	}

	if !storedAvailableAt.Equal(availableAt) {
		t.Errorf(
			"available_at = %v, want %v",
			storedAvailableAt,
			availableAt,
		)
	}
}

func TestDiscoveryMigrationCreatesPrivateTables(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	var count int
	err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM information_schema.tables
			WHERE table_schema = current_schema()
				AND table_name IN (
					'discovery_source_state',
					'discovery_candidates',
					'discovery_edges'
				)
		`,
	).Scan(&count)
	if err != nil {
		t.Fatalf(
			"count discovery tables: %v",
			err,
		)
	}

	if count != 3 {
		t.Errorf(
			"discovery table count = %d, want 3",
			count,
		)
	}
}

func TestDiscoveryMigrationRejectsInvalidQueueMode(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO verification_queue (
				origin,
				available_at,
				mode
			)
			VALUES (
				'https://example.com',
				clock_timestamp(),
				'permanent'
			)
		`,
	)
	if err == nil {
		t.Fatal(
			"invalid queue mode error = nil, want non-nil",
		)
	}
}

func TestDiscoveryMigrationIsForwardOnly(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	migration, err := os.ReadFile(
		"migrations/0003_discovery.sql",
	)
	if err != nil {
		t.Fatalf(
			"read discovery migration: %v",
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
			"reapply discovery migration error = nil, want non-nil",
		)
	}
}

func applyRawStoreMigration(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	path string,
) {
	t.Helper()

	migration, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf(
			"read migration %q: %v",
			path,
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		string(migration),
		pgx.QueryExecModeSimpleProtocol,
	)
	if err != nil {
		t.Fatalf(
			"apply migration %q: %v",
			path,
			err,
		)
	}
}

package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestVerificationReprobeBackfillMigrationRestoresEligibleHistory(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	applyVerificationReprobeSchema(t, ctx, pool)

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO origins (
				origin,
				first_observed_at
			)
			VALUES
				(
					'https://absent.example',
					statement_timestamp() - interval '40 days'
				),
				(
					'https://invalid.example',
					statement_timestamp() - interval '40 days'
				),
				(
					'https://unsupported.example',
					statement_timestamp() - interval '40 days'
				),
				(
					'https://redirect.example',
					statement_timestamp() - interval '40 days'
				),
				(
					'https://newer-unavailable.example',
					statement_timestamp() - interval '40 days'
				),
				(
					'https://newer-robots.example',
					statement_timestamp() - interval '40 days'
				),
				(
					'https://valid.example',
					statement_timestamp() - interval '40 days'
				),
				(
					'https://same-time.example',
					statement_timestamp() - interval '40 days'
				),
				(
					'https://already-queued.example',
					statement_timestamp() - interval '40 days'
				);

			INSERT INTO verification_observations (
				origin,
				observed_at,
				outcome
			)
			VALUES
				(
					'https://absent.example',
					statement_timestamp() - interval '30 days',
					'absent'
				),
				(
					'https://invalid.example',
					statement_timestamp() - interval '29 days',
					'invalid'
				),
				(
					'https://unsupported.example',
					statement_timestamp() - interval '28 days',
					'unsupported_version'
				),
				(
					'https://redirect.example',
					statement_timestamp() - interval '27 days',
					'cross_origin_redirect'
				),
				(
					'https://newer-unavailable.example',
					statement_timestamp() - interval '5 days',
					'absent'
				),
				(
					'https://newer-unavailable.example',
					statement_timestamp() - interval '1 day',
					'unavailable'
				),
				(
					'https://newer-robots.example',
					statement_timestamp() - interval '5 days',
					'invalid'
				),
				(
					'https://newer-robots.example',
					statement_timestamp() - interval '1 day',
					'robots_denied'
				),
				(
					'https://already-queued.example',
					statement_timestamp() - interval '25 days',
					'absent'
				);

			INSERT INTO verification_observations (
				origin,
				observed_at,
				outcome
			)
			VALUES (
				'https://same-time.example',
				statement_timestamp() - interval '2 days',
				'absent'
			);

			INSERT INTO verification_observations (
				origin,
				observed_at,
				outcome
			)
			SELECT
				origin,
				observed_at,
				'unavailable'
			FROM verification_observations
			WHERE origin = 'https://same-time.example'
				AND outcome = 'absent'
			ORDER BY id DESC
			LIMIT 1;

			INSERT INTO verification_observations (
				origin,
				observed_at,
				outcome,
				version,
				identity
			)
			VALUES (
				'https://valid.example',
				statement_timestamp() - interval '1 day',
				'valid',
				1,
				'affirmed'
			);

			INSERT INTO verification_queue (
				origin,
				available_at,
				mode
			)
			VALUES (
				'https://already-queued.example',
				statement_timestamp() + interval '30 days',
				'recurring'
			);
		`,
		pgx.QueryExecModeSimpleProtocol,
	)
	if err != nil {
		t.Fatalf(
			"seed pre-0017 verification history: %v",
			err,
		)
	}

	var existingAvailableAt time.Time
	err = pool.QueryRow(
		ctx,
		`
			SELECT available_at
			FROM verification_queue
			WHERE origin = 'https://already-queued.example'
		`,
	).Scan(&existingAvailableAt)
	if err != nil {
		t.Fatalf(
			"query existing queue row before migration: %v",
			err,
		)
	}

	var migrationStarted time.Time
	if err := pool.QueryRow(
		ctx,
		"SELECT clock_timestamp()",
	).Scan(&migrationStarted); err != nil {
		t.Fatalf(
			"query migration start time: %v",
			err,
		)
	}

	applyRawStoreMigration(
		t,
		ctx,
		pool,
		"migrations/0017_backfill_verification_reprobes.sql",
	)

	var migrationFinished time.Time
	if err := pool.QueryRow(
		ctx,
		"SELECT clock_timestamp()",
	).Scan(&migrationFinished); err != nil {
		t.Fatalf(
			"query migration finish time: %v",
			err,
		)
	}

	rows, err := pool.Query(
		ctx,
		`
			SELECT
				origin,
				mode,
				available_at
			FROM verification_queue
			WHERE origin IN (
				'https://absent.example',
				'https://invalid.example',
				'https://unsupported.example',
				'https://redirect.example'
			)
			ORDER BY available_at, origin
		`,
	)
	if err != nil {
		t.Fatalf(
			"query backfilled queue rows: %v",
			err,
		)
	}
	defer rows.Close()

	type scheduledRow struct {
		origin      string
		mode        string
		availableAt time.Time
	}

	var scheduled []scheduledRow
	for rows.Next() {
		var row scheduledRow
		if err := rows.Scan(
			&row.origin,
			&row.mode,
			&row.availableAt,
		); err != nil {
			t.Fatalf(
				"scan backfilled queue row: %v",
				err,
			)
		}

		scheduled = append(scheduled, row)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf(
			"iterate backfilled queue rows: %v",
			err,
		)
	}

	wantOrder := []string{
		"https://absent.example",
		"https://invalid.example",
		"https://unsupported.example",
		"https://redirect.example",
	}

	if len(scheduled) != len(wantOrder) {
		t.Fatalf(
			"backfilled queue rows = %d, want %d",
			len(scheduled),
			len(wantOrder),
		)
	}

	for index, wantOrigin := range wantOrder {
		row := scheduled[index]

		if row.origin != wantOrigin {
			t.Errorf(
				"backfilled origin %d = %q, want %q",
				index,
				row.origin,
				wantOrigin,
			)
		}

		if row.mode != "reprobe" {
			t.Errorf(
				"backfilled mode for %q = %q, want reprobe",
				row.origin,
				row.mode,
			)
		}

		if row.availableAt.Before(migrationStarted) {
			t.Errorf(
				"available_at for %q = %v, before migration start %v",
				row.origin,
				row.availableAt,
				migrationStarted,
			)
		}

		if row.availableAt.After(
			migrationFinished.Add(14 * 24 * time.Hour),
		) {
			t.Errorf(
				"available_at for %q = %v, beyond catch-up window",
				row.origin,
				row.availableAt,
			)
		}

		if index > 0 &&
			!scheduled[index-1].availableAt.Before(
				row.availableAt,
			) {
			t.Errorf(
				"available_at for %q = %v, want after %q at %v",
				row.origin,
				row.availableAt,
				scheduled[index-1].origin,
				scheduled[index-1].availableAt,
			)
		}
	}

	spread := scheduled[len(scheduled)-1].availableAt.Sub(
		scheduled[0].availableAt,
	)
	if spread < 7*24*time.Hour {
		t.Errorf(
			"historical catch-up spread = %v, want at least 7 days",
			spread,
		)
	}

	var excludedCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_queue
			WHERE origin IN (
				'https://newer-unavailable.example',
				'https://newer-robots.example',
				'https://valid.example',
				'https://same-time.example'
			)
		`,
	).Scan(&excludedCount)
	if err != nil {
		t.Fatalf(
			"count excluded queue rows: %v",
			err,
		)
	}

	if excludedCount != 0 {
		t.Errorf(
			"excluded queue rows = %d, want 0",
			excludedCount,
		)
	}

	var (
		existingMode      string
		storedAvailableAt time.Time
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				mode,
				available_at
			FROM verification_queue
			WHERE origin = 'https://already-queued.example'
		`,
	).Scan(
		&existingMode,
		&storedAvailableAt,
	)
	if err != nil {
		t.Fatalf(
			"query existing queue row after migration: %v",
			err,
		)
	}

	if existingMode != "recurring" {
		t.Errorf(
			"existing queue mode = %q, want recurring",
			existingMode,
		)
	}

	if !storedAvailableAt.Equal(existingAvailableAt) {
		t.Errorf(
			"existing available_at = %v, want %v",
			storedAvailableAt,
			existingAvailableAt,
		)
	}

	var reprobeEvents int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_queue_events
			WHERE event = 'scheduled'
				AND mode = 'reprobe'
		`,
	).Scan(&reprobeEvents)
	if err != nil {
		t.Fatalf(
			"count reprobe queue events: %v",
			err,
		)
	}

	if reprobeEvents != len(wantOrder) {
		t.Errorf(
			"scheduled reprobe events = %d, want %d",
			reprobeEvents,
			len(wantOrder),
		)
	}
}

func TestVerificationReprobeBackfillMigrationPreservesRecheckFloor(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	applyVerificationReprobeSchema(t, ctx, pool)

	var observedAt time.Time
	err := pool.QueryRow(
		ctx,
		`
			WITH inserted_origin AS (
				INSERT INTO origins (
					origin,
					first_observed_at
				)
				VALUES (
					'https://recent.example',
					statement_timestamp()
				)
			)
			INSERT INTO verification_observations (
				origin,
				observed_at,
				outcome
			)
			VALUES (
				'https://recent.example',
				statement_timestamp(),
				'absent'
			)
			RETURNING observed_at
		`,
	).Scan(&observedAt)
	if err != nil {
		t.Fatalf(
			"seed recent verification observation: %v",
			err,
		)
	}

	applyRawStoreMigration(
		t,
		ctx,
		pool,
		"migrations/0017_backfill_verification_reprobes.sql",
	)

	var (
		mode        string
		availableAt time.Time
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				mode,
				available_at
			FROM verification_queue
			WHERE origin = 'https://recent.example'
		`,
	).Scan(
		&mode,
		&availableAt,
	)
	if err != nil {
		t.Fatalf(
			"query recent reprobe row: %v",
			err,
		)
	}

	if mode != "reprobe" {
		t.Errorf(
			"recent queue mode = %q, want reprobe",
			mode,
		)
	}

	minimumAvailableAt := observedAt.Add(24 * time.Hour)
	if availableAt.Before(minimumAvailableAt) {
		t.Errorf(
			"recent available_at = %v, want at or after %v",
			availableAt,
			minimumAvailableAt,
		)
	}
}

func applyVerificationReprobeSchema(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()

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
		"migrations/0014_clear_ephemeral_service_heartbeats.sql",
		"migrations/0015_http_4xx_failure_category.sql",
		"migrations/0016_verification_reprobe.sql",
	} {
		applyRawStoreMigration(
			t,
			ctx,
			pool,
			migration,
		)
	}
}

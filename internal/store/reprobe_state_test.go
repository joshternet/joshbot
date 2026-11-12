package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/declaration"
)

/**
 * TestRecordVerificationDoesNotWriteReprobeState verifies RecordVerification
 * persists observations without updating verification_reprobe_state.
 */
func TestRecordVerificationDoesNotWriteReprobeState(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	storage := New(pool)

	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	base := time.Date(
		2026,
		time.September,
		28,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	outcomes := []declaration.Outcome{
		declaration.OutcomeValid,
		declaration.OutcomeAbsent,
		declaration.OutcomeUnavailable,
		declaration.OutcomeInvalid,
		declaration.OutcomeRobotsDenied,
		declaration.OutcomeCrossOriginRedirect,
		declaration.OutcomeUnsupportedVersion,
	}

	for index, outcome := range outcomes {
		result := declaration.Result{
			Outcome: outcome,
			Origin:  source,
		}

		if outcome == declaration.OutcomeValid {
			result.Declaration = declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityAffirmed,
			}
		}

		if err := storage.RecordVerification(
			ctx,
			base.Add(time.Duration(index)*time.Minute),
			result,
		); err != nil {
			t.Fatalf(
				"RecordVerification(%v) error = %v",
				outcome,
				err,
			)
		}
	}

	var missCount int64
	err := pool.QueryRow(
		ctx,
		`
			SELECT miss_count
			FROM verification_reprobe_state
			WHERE origin = $1
		`,
		source.String(),
	).Scan(&missCount)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf(
			"reprobe state lookup error = %v, miss_count = %d; want ErrNoRows",
			err,
			missCount,
		)
	}

	var observationCount int
	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_observations
			WHERE origin = $1
		`,
		source.String(),
	).Scan(&observationCount); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if observationCount != len(outcomes) {
		t.Fatalf(
			"observation count = %d, want %d",
			observationCount,
			len(outcomes),
		)
	}
}

/**
 * TestLegacyReprobeCompletionRemovesQueueRow verifies terminal completion of a
 * legacy reprobe row removes active queue work while retaining the observation.
 */
func TestLegacyReprobeCompletionRemovesQueueRow(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	now := time.Date(
		2026,
		time.September,
		28,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	queue, clock := newControlledQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     30 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		now,
	)

	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO origins (
				origin,
				first_observed_at
			)
			VALUES ($1, $2)
		`,
		source.String(),
		now.Add(-24*time.Hour),
	); err != nil {
		t.Fatalf("insert origin: %v", err)
	}

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO verification_reprobe_state (
				origin,
				miss_count
			)
			VALUES ($1, 4)
		`,
		source.String(),
	); err != nil {
		t.Fatalf("insert reprobe state: %v", err)
	}

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO verification_queue (
				origin,
				available_at,
				mode
			)
			VALUES ($1, $2, 'reprobe')
		`,
		source.String(),
		now,
	); err != nil {
		t.Fatalf("insert reprobe queue row: %v", err)
	}

	lease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if !found {
		t.Fatal("Claim() found = false, want true")
	}

	completedAt := now.Add(time.Minute)
	clock.Set(completedAt)

	if err := queue.CompleteVerification(
		ctx,
		lease,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  source,
		},
		24*time.Hour,
	); err != nil {
		t.Fatalf("CompleteVerification() error = %v", err)
	}

	var queueCount int
	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_queue
			WHERE origin = $1
		`,
		source.String(),
	).Scan(&queueCount); err != nil {
		t.Fatalf("count queue rows: %v", err)
	}
	if queueCount != 0 {
		t.Fatalf("queue row count = %d, want 0", queueCount)
	}

	var missCount int64
	if err := pool.QueryRow(
		ctx,
		`
			SELECT miss_count
			FROM verification_reprobe_state
			WHERE origin = $1
		`,
		source.String(),
	).Scan(&missCount); err != nil {
		t.Fatalf("read reprobe miss count: %v", err)
	}
	if missCount != 4 {
		t.Errorf(
			"reprobe miss count = %d, want unchanged 4",
			missCount,
		)
	}

	var observationCount int
	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_observations
			WHERE origin = $1
		`,
		source.String(),
	).Scan(&observationCount); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if observationCount != 1 {
		t.Fatalf(
			"observation count = %d, want 1",
			observationCount,
		)
	}
}

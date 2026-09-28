package store

import (
	"context"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
)

func TestRecordVerificationMaintainsCompactReprobeState(
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

	results := []struct {
		outcome declaration.Outcome
		want    int64
	}{
		{
			outcome: declaration.OutcomeValid,
			want:    0,
		},
		{
			outcome: declaration.OutcomeAbsent,
			want:    1,
		},
		{
			outcome: declaration.OutcomeUnavailable,
			want:    1,
		},
		{
			outcome: declaration.OutcomeInvalid,
			want:    2,
		},
		{
			outcome: declaration.OutcomeRobotsDenied,
			want:    3,
		},
		{
			outcome: declaration.OutcomeCrossOriginRedirect,
			want:    4,
		},
		{
			outcome: declaration.OutcomeUnsupportedVersion,
			want:    5,
		},
	}

	for index, test := range results {
		result := declaration.Result{
			Outcome: test.outcome,
			Origin:  source,
		}

		if test.outcome ==
			declaration.OutcomeValid {
			result.Declaration =
				declaration.Declaration{
					Version:  1,
					Identity: declaration.IdentityAffirmed,
				}
		}

		if err := storage.RecordVerification(
			ctx,
			base.Add(
				time.Duration(index)*
					time.Minute,
			),
			result,
		); err != nil {
			t.Fatalf(
				"RecordVerification(%v) error = %v",
				test.outcome,
				err,
			)
		}

		var got int64

		if err := pool.QueryRow(
			ctx,
			`
				SELECT miss_count
				FROM verification_reprobe_state
				WHERE origin = $1
			`,
			source.String(),
		).Scan(&got); err != nil {
			t.Fatalf(
				"read reprobe state: %v",
				err,
			)
		}

		if got != test.want {
			t.Errorf(
				"miss count after %v = %d, want %d",
				test.outcome,
				got,
				test.want,
			)
		}
	}
}

func TestQueueReprobeSchedulingUsesCompactState(
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
			LeaseDuration:        30 * time.Minute,
			MinOriginInterval:    time.Minute,
			FirstReprobeInterval: 12 * time.Hour,
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
		t.Fatalf(
			"insert origin: %v",
			err,
		)
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
		t.Fatalf(
			"insert reprobe state: %v",
			err,
		)
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
		t.Fatalf(
			"insert reprobe queue row: %v",
			err,
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
		t.Fatalf(
			"count observations before completion: %v",
			err,
		)
	}

	if observationCount != 0 {
		t.Fatalf(
			"observations before completion = %d, want 0",
			observationCount,
		)
	}

	lease, found, err := queue.Claim(
		ctx,
		"worker-a",
	)
	if err != nil {
		t.Fatalf(
			"Claim() error = %v",
			err,
		)
	}

	if !found {
		t.Fatal(
			"Claim() found = false, want true",
		)
	}

	completedAt := now.Add(
		time.Minute,
	)
	clock.Set(
		completedAt,
	)

	if err := queue.CompleteVerification(
		ctx,
		lease,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  source,
		},
		24*time.Hour,
	); err != nil {
		t.Fatalf(
			"CompleteVerification() error = %v",
			err,
		)
	}

	state := readQueueState(
		t,
		pool,
		source.String(),
	)

	wantAvailableAt :=
		completedAt.Add(
			maxReprobeInterval,
		)

	if !state.availableAt.Equal(
		wantAvailableAt,
	) {
		t.Errorf(
			"available_at = %v, want %v",
			state.availableAt,
			wantAvailableAt,
		)
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
		t.Fatalf(
			"read reprobe miss count: %v",
			err,
		)
	}

	if missCount != 5 {
		t.Errorf(
			"reprobe miss count = %d, want 5",
			missCount,
		)
	}

	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_observations
			WHERE origin = $1
		`,
		source.String(),
	).Scan(&observationCount); err != nil {
		t.Fatalf(
			"count observations after completion: %v",
			err,
		)
	}

	if observationCount != 1 {
		t.Errorf(
			"observations after completion = %d, want 1",
			observationCount,
		)
	}
}

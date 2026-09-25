package store

import (
	"context"
	"testing"
	"time"
)

func TestQueueCoalescesDuplicateSchedulesAtEarliestTime(
	t *testing.T,
) {
	t1 := time.Date(
		2026,
		time.August,
		31,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	t2 := t1.Add(time.Hour)

	tests := []struct {
		name             string
		firstAvailableAt time.Time
		nextAvailableAt  time.Time
		wantAvailableAt  time.Time
	}{
		{
			name:             "earlier duplicate advances work",
			firstAvailableAt: t2,
			nextAvailableAt:  t1,
			wantAvailableAt:  t1,
		},
		{
			name:             "later duplicate does not delay work",
			firstAvailableAt: t1,
			nextAvailableAt:  t2,
			wantAvailableAt:  t1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := newStoreTestPool(t)

			queue, err := NewQueue(
				pool,
				QueueConfig{
					LeaseDuration:     10 * time.Minute,
					MinOriginInterval: time.Hour,
				},
			)
			if err != nil {
				t.Fatalf(
					"NewQueue() error = %v, want nil",
					err,
				)
			}

			source := mustStoreOrigin(
				t,
				"https://example.com",
			)
			equivalentSource := mustStoreOrigin(
				t,
				"https://EXAMPLE.com:443/path?query=value#fragment",
			)

			if source != equivalentSource {
				t.Fatalf(
					"equivalent origins differ: %q and %q",
					source,
					equivalentSource,
				)
			}

			if err := queue.Schedule(
				ctx,
				source,
				test.firstAvailableAt,
			); err != nil {
				t.Fatalf(
					"first Schedule() error = %v, want nil",
					err,
				)
			}

			if err := queue.Schedule(
				ctx,
				equivalentSource,
				test.nextAvailableAt,
			); err != nil {
				t.Fatalf(
					"duplicate Schedule() error = %v, want nil",
					err,
				)
			}

			var (
				rowCount          int
				storedAvailableAt time.Time
			)
			err = pool.QueryRow(
				ctx,
				`
					SELECT
						count(*),
						min(available_at)
					FROM verification_queue
					WHERE origin = $1
				`,
				source.String(),
			).Scan(
				&rowCount,
				&storedAvailableAt,
			)
			if err != nil {
				t.Fatalf(
					"query scheduled work: %v",
					err,
				)
			}

			if rowCount != 1 {
				t.Errorf(
					"queue row count = %d, want 1",
					rowCount,
				)
			}

			if !storedAvailableAt.Equal(
				test.wantAvailableAt,
			) {
				t.Errorf(
					"available_at = %v, want %v",
					storedAvailableAt,
					test.wantAvailableAt,
				)
			}
		})
	}
}

func TestQueueSchedulePromotesReprobeToRecurring(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	queue, err := NewQueue(
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Hour,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewQueue() error = %v, want nil",
			err,
		)
	}

	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	later := time.Date(
		2026,
		time.September,
		25,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	earlier := later.Add(-time.Hour)

	_, err = pool.Exec(
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
		later,
	)
	if err != nil {
		t.Fatalf(
			"seed reprobe work: %v",
			err,
		)
	}

	if err := queue.Schedule(
		ctx,
		source,
		earlier,
	); err != nil {
		t.Fatalf(
			"Schedule() error = %v, want nil",
			err,
		)
	}

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
			WHERE origin = $1
		`,
		source.String(),
	).Scan(
		&mode,
		&availableAt,
	)
	if err != nil {
		t.Fatalf(
			"read scheduled reprobe: %v",
			err,
		)
	}

	if mode != "recurring" {
		t.Errorf(
			"queue mode = %q, want recurring",
			mode,
		)
	}

	if !availableAt.Equal(earlier) {
		t.Errorf(
			"available_at = %v, want %v",
			availableAt,
			earlier,
		)
	}
}

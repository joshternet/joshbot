package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestRescheduleWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.September,
		18,
		22,
		0,
		0,
		0,
		time.UTC,
	)
	lease := queueUnitLease(
		t,
		now.Add(-5*time.Minute),
		now.Add(5*time.Minute),
	)
	availableAt := now.Add(2 * time.Hour)

	t.Run(
		"validation",
		func(t *testing.T) {
			var queue *Queue

			err := queue.Reschedule(
				ctx,
				lease,
				availableAt,
			)

			if !errors.Is(
				err,
				errQueueUnavailable,
			) {
				t.Fatalf(
					"Reschedule() error = %v, want %v",
					err,
					errQueueUnavailable,
				)
			}
		},
	)

	t.Run(
		"invalid lease",
		func(t *testing.T) {
			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				queueUnitClock{
					now: now,
				},
			)

			err := queue.Reschedule(
				ctx,
				Lease{},
				availableAt,
			)

			if !errors.Is(
				err,
				errInvalidLease,
			) {
				t.Fatalf(
					"Reschedule() error = %v, want %v",
					err,
					errInvalidLease,
				)
			}
		},
	)

	t.Run(
		"invalid available time",
		func(t *testing.T) {
			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				queueUnitClock{
					now: now,
				},
			)

			err := queue.Reschedule(
				ctx,
				lease,
				time.Time{},
			)

			if !errors.Is(
				err,
				errInvalidAvailableAt,
			) {
				t.Fatalf(
					"Reschedule() error = %v, want %v",
					err,
					errInvalidAvailableAt,
				)
			}
		},
	)

	t.Run(
		"clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test reschedule clock failure",
			)

			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				queueUnitClock{
					err: testErr,
				},
			)

			err := queue.Reschedule(
				ctx,
				lease,
				availableAt,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read queue clock",
				) {
				t.Fatalf(
					"Reschedule() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test reschedule database failure",
			)

			database := &discoveryUnitDatabase{
				execResults: []discoveryUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				database,
				queueUnitClock{
					now: now,
				},
			)

			err := queue.Reschedule(
				ctx,
				lease,
				availableAt,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: reschedule queue work",
				) {
				t.Fatalf(
					"Reschedule() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"lease lost",
		func(t *testing.T) {
			database := &discoveryUnitDatabase{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 0",
						),
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				database,
				queueUnitClock{
					now: now,
				},
			)

			err := queue.Reschedule(
				ctx,
				lease,
				availableAt,
			)

			if !errors.Is(
				err,
				ErrLeaseLost,
			) {
				t.Fatalf(
					"Reschedule() error = %v, want %v",
					err,
					ErrLeaseLost,
				)
			}
		},
	)

	t.Run(
		"success",
		func(t *testing.T) {
			database := &discoveryUnitDatabase{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
						),
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				database,
				queueUnitClock{
					now: now,
				},
			)

			err := queue.Reschedule(
				ctx,
				lease,
				availableAt,
			)
			if err != nil {
				t.Fatalf(
					"Reschedule() error = %v",
					err,
				)
			}
		},
	)
}

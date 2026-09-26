package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestAbandonVerificationWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.September,
		26,
		19,
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

	t.Run(
		"validation",
		func(t *testing.T) {
			var queue *Queue

			err := queue.AbandonVerification(
				ctx,
				lease,
			)

			if !errors.Is(
				err,
				errQueueUnavailable,
			) {
				t.Fatalf(
					"AbandonVerification() error = %v, want %v",
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

			err := queue.AbandonVerification(
				ctx,
				Lease{},
			)

			if !errors.Is(
				err,
				errInvalidLease,
			) {
				t.Fatalf(
					"AbandonVerification() error = %v, want %v",
					err,
					errInvalidLease,
				)
			}
		},
	)

	t.Run(
		"clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test abandon clock failure",
			)

			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				queueUnitClock{
					err: testErr,
				},
			)

			err := queue.AbandonVerification(
				ctx,
				lease,
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
					"AbandonVerification() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test abandon database failure",
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

			err := queue.AbandonVerification(
				ctx,
				lease,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: abandon verification lease",
				) {
				t.Fatalf(
					"AbandonVerification() error = %v",
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

			err := queue.AbandonVerification(
				ctx,
				lease,
			)

			if !errors.Is(
				err,
				ErrLeaseLost,
			) {
				t.Fatalf(
					"AbandonVerification() error = %v, want %v",
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

			err := queue.AbandonVerification(
				ctx,
				lease,
			)
			if err != nil {
				t.Fatalf(
					"AbandonVerification() error = %v, want nil",
					err,
				)
			}
		},
	)
}

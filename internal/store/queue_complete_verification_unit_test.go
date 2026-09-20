package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/retry"
)

type completeVerificationClock struct {
	now time.Time
	err error
}

func (clock completeVerificationClock) Now(
	context.Context,
	*pgxpool.Pool,
) (time.Time, error) {
	return clock.now, clock.err
}

func (clock completeVerificationClock) NowTransaction(
	context.Context,
	pgx.Tx,
) (time.Time, error) {
	return clock.now, clock.err
}

func TestCompleteVerificationWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.September,
		18,
		22,
		30,
		0,
		0,
		time.UTC,
	)

	lease := queueUnitLease(
		t,
		now.Add(-5*time.Minute),
		now.Add(5*time.Minute),
	)

	validResult := declaration.Result{
		Outcome: declaration.OutcomeValid,
		Origin:  lease.Origin,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityAffirmed,
		},
	}

	t.Run(
		"validation",
		func(t *testing.T) {
			var queue *Queue

			err := queue.CompleteVerification(
				ctx,
				lease,
				validResult,
				time.Hour,
			)

			if !errors.Is(
				err,
				errQueueUnavailable,
			) {
				t.Fatalf(
					"CompleteVerification() error = %v, want %v",
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
				completeVerificationClock{
					now: now,
				},
			)

			err := queue.CompleteVerification(
				ctx,
				Lease{},
				validResult,
				time.Hour,
			)

			if !errors.Is(
				err,
				errInvalidLease,
			) {
				t.Fatalf(
					"CompleteVerification() error = %v, want %v",
					err,
					errInvalidLease,
				)
			}
		},
	)

	t.Run(
		"invalid result origin",
		func(t *testing.T) {
			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				completeVerificationClock{
					now: now,
				},
			)

			result := validResult
			result.Origin = declaration.Result{}.Origin

			err := queue.CompleteVerification(
				ctx,
				lease,
				result,
				time.Hour,
			)

			if !errors.Is(
				err,
				errInvalidOrigin,
			) {
				t.Fatalf(
					"CompleteVerification() error = %v, want %v",
					err,
					errInvalidOrigin,
				)
			}
		},
	)

	t.Run(
		"origin mismatch",
		func(t *testing.T) {
			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				completeVerificationClock{
					now: now,
				},
			)

			result := validResult
			result.Origin = mustStoreOrigin(
				t,
				"https://other.example",
			)

			err := queue.CompleteVerification(
				ctx,
				lease,
				result,
				time.Hour,
			)

			if !errors.Is(
				err,
				errCompletionOriginMismatch,
			) {
				t.Fatalf(
					"CompleteVerification() error = %v, want %v",
					err,
					errCompletionOriginMismatch,
				)
			}
		},
	)

	t.Run(
		"invalid result",
		func(t *testing.T) {
			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				completeVerificationClock{
					now: now,
				},
			)

			result := validResult
			result.Declaration.Version = 2

			err := queue.CompleteVerification(
				ctx,
				lease,
				result,
				time.Hour,
			)

			if !errors.Is(
				err,
				errInvalidResult,
			) {
				t.Fatalf(
					"CompleteVerification() error = %v, want %v",
					err,
					errInvalidResult,
				)
			}
		},
	)

	t.Run(
		"invalid recheck duration",
		func(t *testing.T) {
			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				completeVerificationClock{
					now: now,
				},
			)

			err := queue.CompleteVerification(
				ctx,
				lease,
				validResult,
				0,
			)

			if !errors.Is(
				err,
				errInvalidRecheckAfter,
			) {
				t.Fatalf(
					"CompleteVerification() error = %v, want %v",
					err,
					errInvalidRecheckAfter,
				)
			}
		},
	)

	t.Run(
		"begin failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test complete verification begin failure",
			)

			database := &discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginErr:              testErr,
			}

			queue := queueSimpleDatabaseStore(
				database,
				completeVerificationClock{
					now: now,
				},
			)

			err := queue.CompleteVerification(
				ctx,
				lease,
				validResult,
				time.Hour,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: complete verification",
				) {
				t.Fatalf(
					"CompleteVerification() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"completion clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test completion clock failure",
			)

			tx := &discoveryPolicyUnitTx{}

			queue := queueSimpleDatabaseStore(
				discoveryPolicyDatabase(tx),
				completeVerificationClock{
					err: testErr,
				},
			)

			err := queue.CompleteVerification(
				ctx,
				lease,
				validResult,
				time.Hour,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read queue completion clock",
				) ||
				!strings.Contains(
					err.Error(),
					"store: complete verification",
				) {
				t.Fatalf(
					"CompleteVerification() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"lease lost while locking",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						err: pgx.ErrNoRows,
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				discoveryPolicyDatabase(tx),
				completeVerificationClock{
					now: now,
				},
			)

			err := queue.CompleteVerification(
				ctx,
				lease,
				validResult,
				time.Hour,
			)

			if !errors.Is(
				err,
				ErrLeaseLost,
			) {
				t.Fatalf(
					"CompleteVerification() error = %v, want %v",
					err,
					ErrLeaseLost,
				)
			}
		},
	)

	t.Run(
		"lease lock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test completion lease lock failure",
			)

			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						err: testErr,
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				discoveryPolicyDatabase(tx),
				completeVerificationClock{
					now: now,
				},
			)

			err := queue.CompleteVerification(
				ctx,
				lease,
				validResult,
				time.Hour,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: complete verification",
				) {
				t.Fatalf(
					"CompleteVerification() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"completion write failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test completion write failure",
			)

			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							0,
						},
					},
				},
				execResults: []discoveryUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				discoveryPolicyDatabase(tx),
				completeVerificationClock{
					now: now,
				},
			)

			err := queue.CompleteVerification(
				ctx,
				lease,
				validResult,
				time.Hour,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: complete verification",
				) {
				t.Fatalf(
					"CompleteVerification() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"lease lost while completing",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							0,
						},
					},
				},
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 0",
						),
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				discoveryPolicyDatabase(tx),
				completeVerificationClock{
					now: now,
				},
			)

			err := queue.CompleteVerification(
				ctx,
				lease,
				validResult,
				time.Hour,
			)

			if !errors.Is(
				err,
				ErrLeaseLost,
			) {
				t.Fatalf(
					"CompleteVerification() error = %v, want %v",
					err,
					ErrLeaseLost,
				)
			}
		},
	)

	t.Run(
		"transient unavailable success",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							0,
						},
					},
				},
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
						),
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				discoveryPolicyDatabase(tx),
				completeVerificationClock{
					now: now,
				},
			)

			result := declaration.Result{
				Outcome:         declaration.OutcomeUnavailable,
				Origin:          lease.Origin,
				FailureCategory: retry.CategoryNone,
			}

			err := queue.CompleteVerification(
				ctx,
				lease,
				result,
				time.Hour,
			)
			if err != nil {
				t.Fatalf(
					"CompleteVerification() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"valid success",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							0,
						},
					},
				},
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
						),
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				discoveryPolicyDatabase(tx),
				completeVerificationClock{
					now: now,
				},
			)

			err := queue.CompleteVerification(
				ctx,
				lease,
				validResult,
				time.Hour,
			)
			if err != nil {
				t.Fatalf(
					"CompleteVerification() error = %v",
					err,
				)
			}
		},
	)
}

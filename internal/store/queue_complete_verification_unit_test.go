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

type completeVerificationCaptureTx struct {
	*discoveryPolicyUnitTx

	execQueries   []string
	execArguments [][]any
}

func (tx *completeVerificationCaptureTx) Exec(
	ctx context.Context,
	query string,
	arguments ...any,
) (pgconn.CommandTag, error) {
	tx.execQueries = append(
		tx.execQueries,
		query,
	)
	tx.execArguments = append(
		tx.execArguments,
		append([]any(nil), arguments...),
	)

	return tx.discoveryPolicyUnitTx.Exec(
		ctx,
		query,
		arguments...,
	)
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

func TestCompleteVerificationPassesReprobeDecisionWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.September,
		23,
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

	baseTx := &discoveryPolicyUnitTx{
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
	tx := &completeVerificationCaptureTx{
		discoveryPolicyUnitTx: baseTx,
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
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  lease.Origin,
		},
		24*time.Hour,
	)
	if err != nil {
		t.Fatalf(
			"CompleteVerification() error = %v",
			err,
		)
	}

	if len(tx.execArguments) != 1 {
		t.Fatalf(
			"completion Exec count = %d, want 1",
			len(tx.execArguments),
		)
	}

	arguments := tx.execArguments[0]
	if len(arguments) != 13 {
		t.Fatalf(
			"completion argument count = %d, want 13",
			len(arguments),
		)
	}

	reprobe, ok := arguments[12].(bool)
	if !ok || !reprobe {
		t.Fatalf(
			"reprobe argument = %#v, want true",
			arguments[12],
		)
	}

	if len(tx.execQueries) != 1 {
		t.Fatalf(
			"completion query count = %d, want 1",
			len(tx.execQueries),
		)
	}

	normalizedQuery := strings.Join(
		strings.Fields(tx.execQueries[0]),
		" ",
	)
	if !strings.Contains(
		normalizedQuery,
		"leased_queue.mode IN (",
	) ||
		!strings.Contains(
			normalizedQuery,
			"'recurring', 'reprobe'",
		) {
		t.Fatalf(
			"completion query does not retain existing reprobes",
		)
	}
}

func TestShouldReprobeOutcome(t *testing.T) {
	tests := []struct {
		name    string
		outcome declaration.Outcome
		want    bool
	}{
		{
			name:    "valid",
			outcome: declaration.OutcomeValid,
		},
		{
			name:    "absent",
			outcome: declaration.OutcomeAbsent,
			want:    true,
		},
		{
			name:    "invalid",
			outcome: declaration.OutcomeInvalid,
			want:    true,
		},
		{
			name:    "unsupported version",
			outcome: declaration.OutcomeUnsupportedVersion,
			want:    true,
		},
		{
			name:    "unavailable",
			outcome: declaration.OutcomeUnavailable,
		},
		{
			name:    "robots denied",
			outcome: declaration.OutcomeRobotsDenied,
		},
		{
			name:    "cross-origin redirect",
			outcome: declaration.OutcomeCrossOriginRedirect,
			want:    true,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				if got := shouldReprobeOutcome(
					test.outcome,
				); got != test.want {
					t.Fatalf(
						"shouldReprobeOutcome(%v) = %v, want %v",
						test.outcome,
						got,
						test.want,
					)
				}
			},
		)
	}
}

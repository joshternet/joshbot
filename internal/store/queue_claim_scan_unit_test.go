package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestClaimWithStatsSkipDiagnosticsWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	now := queueTestTime()
	workerID := "worker-scan"

	t.Run("locked skip", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{values: []any{false}},
				{values: []any{false}},
				{err: pgx.ErrNoRows},
				{values: []any{true}},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://locked.example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
				{rows: &discoveryUnitRows{}},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)

		_, stats, err := queue.ClaimWithStats(ctx, workerID)
		if err != nil {
			t.Fatalf("ClaimWithStats() error = %v", err)
		}
		if stats.Outcome != ClaimOutcomeDeferred {
			t.Fatalf("outcome = %s, want deferred", stats.Outcome)
		}
		if stats.LockedSkips != 1 {
			t.Fatalf("LockedSkips = %d, want 1", stats.LockedSkips)
		}
		if stats.IneligibleSkips != 0 {
			t.Fatalf("IneligibleSkips = %d, want 0", stats.IneligibleSkips)
		}
	})

	t.Run("ineligible skip", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{values: []any{false}},
				{values: []any{false}},
				{err: pgx.ErrNoRows},
				{values: []any{false}},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://gone.example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
				{rows: &discoveryUnitRows{}},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)

		_, stats, err := queue.ClaimWithStats(ctx, workerID)
		if err != nil {
			t.Fatalf("ClaimWithStats() error = %v", err)
		}
		if stats.Outcome != ClaimOutcomeDeferred {
			t.Fatalf("outcome = %s, want deferred", stats.Outcome)
		}
		if stats.IneligibleSkips != 1 {
			t.Fatalf("IneligibleSkips = %d, want 1", stats.IneligibleSkips)
		}
		if stats.LockedSkips != 0 {
			t.Fatalf("LockedSkips = %d, want 0", stats.LockedSkips)
		}
	})

	t.Run("advisory busy skip", func(t *testing.T) {
		tryFalse := false
		tx := &discoveryPolicyUnitTx{
			tryAdvisoryResult: &tryFalse,
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{values: []any{false}},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://busy.example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
				{rows: &discoveryUnitRows{}},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)

		_, stats, err := queue.ClaimWithStats(ctx, workerID)
		if err != nil {
			t.Fatalf("ClaimWithStats() error = %v", err)
		}
		if stats.Outcome != ClaimOutcomeDeferred {
			t.Fatalf("outcome = %s, want deferred", stats.Outcome)
		}
		if stats.BusySkips != 1 {
			t.Fatalf("BusySkips = %d, want 1", stats.BusySkips)
		}
	})

	t.Run("lock timeout recovers as deferred", func(t *testing.T) {
		timeoutErr := &pgconn.PgError{Code: "55P03"}
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{err: timeoutErr},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://timeout.example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
				{rows: &discoveryUnitRows{}},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)

		_, stats, err := queue.ClaimWithStats(ctx, workerID)
		if err != nil {
			t.Fatalf("ClaimWithStats() error = %v", err)
		}
		if stats.Outcome != ClaimOutcomeDeferred {
			t.Fatalf("outcome = %s, want deferred", stats.Outcome)
		}
		if stats.LockTimeouts != 1 {
			t.Fatalf("LockTimeouts = %d, want 1", stats.LockTimeouts)
		}
	})

	t.Run("keyset continuation page", func(t *testing.T) {
		claimedAt := now
		expiresAt := now.Add(10 * time.Minute)
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				// first candidate busy
				{values: []any{true}},
				// second page reserve + lock + upsert + bind
				{values: []any{false}},
				{values: []any{false}},
				{
					values: []any{
						"https://solo.page-two.test",
						int64(0),
					},
				},
				{values: []any{"page-two.test"}},
				{
					values: []any{
						workerID,
						int64(1),
						claimedAt,
						expiresAt,
					},
				},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://page-one.example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://solo.page-two.test",
									int64(0),
									now.Add(time.Second),
									2,
								},
							},
						},
					},
				},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)

		lease, stats, err := queue.ClaimWithStats(ctx, workerID)
		if err != nil {
			t.Fatalf("ClaimWithStats() error = %v", err)
		}
		if stats.Outcome != ClaimOutcomeSuccess {
			t.Fatalf("outcome = %s, want success (%#v)", stats.Outcome, stats)
		}
		if lease.Origin.String() != "https://solo.page-two.test" {
			t.Fatalf("origin = %s", lease.Origin)
		}
	})
}

func TestIsLockTimeoutErrorWithoutDatabase(t *testing.T) {
	if isLockTimeoutError(errors.New("nope")) {
		t.Fatal("expected false for plain error")
	}
	if !isLockTimeoutError(&pgconn.PgError{Code: "55P03"}) {
		t.Fatal("expected true for 55P03")
	}
}

func TestClaimBudgetExhaustionWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	now := queueTestTime()
	originalMax := claimMaxCandidatesExamined
	claimMaxCandidatesExamined = 1
	t.Cleanup(func() { claimMaxCandidatesExamined = originalMax })

	tx := &discoveryPolicyUnitTx{
		rowResults: []discoveryUnitRow{
			{values: []any{false}},
			{values: []any{true}},
		},
		queryResults: []discoveryUnitQueryResult{
			{
				rows: &discoveryUnitRows{
					rows: []discoveryUnitRow{
						{
							values: []any{
								"https://a.example.com",
								int64(0),
								now,
								2,
							},
						},
						{
							values: []any{
								"https://b.example.com",
								int64(0),
								now,
								2,
							},
						},
					},
				},
			},
		},
	}
	queue := queueSimpleDatabaseStore(
		discoveryPolicyDatabase(tx),
		queueUnitClock{now: now},
	)

	_, stats, err := queue.ClaimWithStats(ctx, "worker-budget")
	if err != nil {
		t.Fatalf("ClaimWithStats() error = %v", err)
	}
	if stats.Outcome != ClaimOutcomeDeferred {
		t.Fatalf("outcome = %s, want deferred", stats.Outcome)
	}
}

func TestClaimWithStatsSavepointFailuresWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	now := queueTestTime()
	testErr := errors.New("savepoint boom")

	t.Run("create savepoint failure", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			scriptSavepoints: true,
			execResults: []discoveryUnitExecResult{
				{err: testErr},
			},
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://sp.example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)
		_, _, err := queue.ClaimWithStats(ctx, "worker")
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("release savepoint failure", func(t *testing.T) {
		claimedAt := now
		expiresAt := now.Add(time.Minute)
		tx := &discoveryPolicyUnitTx{
			scriptSavepoints: true,
			execResults: []discoveryUnitExecResult{
				{},             // SAVEPOINT
				{err: testErr}, // RELEASE
			},
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{values: []any{false}},
				{values: []any{false}},
				{
					values: []any{
						"https://rel.example.com",
						int64(0),
					},
				},
				{values: []any{"example.com"}},
				{
					values: []any{
						"worker",
						int64(1),
						claimedAt,
						expiresAt,
					},
				},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://rel.example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)
		_, _, err := queue.ClaimWithStats(ctx, "worker")
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestClaimWithStatsAdditionalBranchesWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	now := queueTestTime()

	t.Run("budget before list", func(t *testing.T) {
		original := claimMaxCandidatesExamined
		claimMaxCandidatesExamined = 0
		t.Cleanup(func() { claimMaxCandidatesExamined = original })

		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)
		_, stats, err := queue.ClaimWithStats(ctx, "worker")
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if stats.Outcome != ClaimOutcomeDeferred {
			t.Fatalf("outcome = %s", stats.Outcome)
		}
	})

	t.Run("scan failure", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{values: []any{"https://x.example.com"}},
						},
					},
				},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)
		_, _, err := queue.ClaimWithStats(ctx, "worker")
		if err == nil {
			t.Fatal("expected scan error")
		}
	})

	t.Run("rows iteration error", func(t *testing.T) {
		testErr := errors.New("rows boom")
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						err: testErr,
					},
				},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)
		_, _, err := queue.ClaimWithStats(ctx, "worker")
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("for update hard error", func(t *testing.T) {
		testErr := errors.New("for update boom")
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{values: []any{false}},
				{values: []any{false}},
				{err: testErr},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://fu.example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)
		_, _, err := queue.ClaimWithStats(ctx, "worker")
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("exists after miss failure", func(t *testing.T) {
		testErr := errors.New("exists boom")
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{values: []any{false}},
				{values: []any{false}},
				{err: pgx.ErrNoRows},
				{err: testErr},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://ex.example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)
		_, _, err := queue.ClaimWithStats(ctx, "worker")
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("write failure", func(t *testing.T) {
		testErr := errors.New("write boom")
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{values: []any{false}},
				{values: []any{false}},
				{
					values: []any{
						"https://wr.example.com",
						int64(0),
					},
				},
				{err: testErr},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://wr.example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)
		_, _, err := queue.ClaimWithStats(ctx, "worker")
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("write miss busy", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{values: []any{false}},
				{values: []any{false}},
				{
					values: []any{
						"https://miss.example.com",
						int64(0),
					},
				},
				{err: pgx.ErrNoRows},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://miss.example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
				{rows: &discoveryUnitRows{}},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)
		_, stats, err := queue.ClaimWithStats(ctx, "worker")
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if stats.BusySkips != 1 {
			t.Fatalf("BusySkips = %d", stats.BusySkips)
		}
	})
}

type claimAttemptClock struct {
	times []time.Time
	errs  []error
	index int
}

func (clock *claimAttemptClock) Now(
	context.Context,
	*pgxpool.Pool,
) (time.Time, error) {
	return clock.next()
}

func (clock *claimAttemptClock) NowTransaction(
	context.Context,
	pgx.Tx,
) (time.Time, error) {
	return clock.next()
}

func (clock *claimAttemptClock) next() (time.Time, error) {
	if clock.index >= len(clock.times) && clock.index >= len(clock.errs) {
		return time.Time{}, errors.New("claimAttemptClock exhausted")
	}
	var (
		now time.Time
		err error
	)
	if clock.index < len(clock.times) {
		now = clock.times[clock.index]
	}
	if clock.index < len(clock.errs) {
		err = clock.errs[clock.index]
	}
	clock.index++
	return now, err
}

func TestAttemptClaimCandidateClockBranchesWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	now := queueTestTime()

	t.Run("attempt clock failure", func(t *testing.T) {
		testErr := errors.New("attempt clock boom")
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://clk.example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			&claimAttemptClock{
				times: []time.Time{now},
				errs:  []error{nil, testErr},
			},
		)
		_, _, err := queue.ClaimWithStats(ctx, "worker")
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("zero attempt clock falls back", func(t *testing.T) {
		claimedAt := now
		expiresAt := now.Add(time.Minute)
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{values: []any{false}},
				{values: []any{false}},
				{
					values: []any{
						"https://zero.example.com",
						int64(0),
					},
				},
				{values: []any{"example.com"}},
				{
					values: []any{
						"worker",
						int64(1),
						claimedAt,
						expiresAt,
					},
				},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://zero.example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
			},
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			&claimAttemptClock{
				times: []time.Time{now, {}},
				errs:  []error{nil, nil},
			},
		)
		lease, stats, err := queue.ClaimWithStats(ctx, "worker")
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if stats.Outcome != ClaimOutcomeSuccess {
			t.Fatalf("outcome = %s", stats.Outcome)
		}
		if lease.Generation != 1 {
			t.Fatalf("generation = %d", lease.Generation)
		}
	})
}

package store

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/database"
	"github.com/joshternet/joshbot/internal/discovery"
)

type fleetBudgetCaptureDatabase struct {
	base  *storeFakePostgres
	query string
	args  []any
}

func (database *fleetBudgetCaptureDatabase) Begin(
	ctx context.Context,
) (pgx.Tx, error) {
	return database.base.Begin(ctx)
}

func (database *fleetBudgetCaptureDatabase) Exec(
	ctx context.Context,
	query string,
	args ...any,
) (pgconn.CommandTag, error) {
	return database.base.Exec(ctx, query, args...)
}

func (database *fleetBudgetCaptureDatabase) Query(
	ctx context.Context,
	query string,
	args ...any,
) (pgx.Rows, error) {
	return database.base.Query(ctx, query, args...)
}

func (database *fleetBudgetCaptureDatabase) QueryRow(
	ctx context.Context,
	query string,
	args ...any,
) pgx.Row {
	database.query = query
	database.args = append([]any(nil), args...)

	return database.base.QueryRow(ctx, query, args...)
}

func TestFleetBudgetWithoutDatabase(t *testing.T) {
	ctx := context.Background()

	t.Run("constructor rejects an unusable budget", func(t *testing.T) {
		budget, err := NewFleetBudget(nil, 1, 1)
		if !errors.Is(err, errPoolUnavailable) || budget != nil {
			t.Fatalf(
				"NewFleetBudget() = %v, %v, want nil, %v",
				budget,
				err,
				errPoolUnavailable,
			)
		}

		for _, test := range []struct {
			name    string
			rate    int
			maxByte int64
		}{
			{name: "zero rate", rate: 0, maxByte: 1},
			{name: "negative rate", rate: -1, maxByte: 1},
			{name: "rate without spacing", rate: math.MaxInt, maxByte: 1},
			{name: "zero bytes", rate: 1, maxByte: 0},
			{name: "negative bytes", rate: 1, maxByte: -1},
		} {
			t.Run(test.name, func(t *testing.T) {
				constructed, constructErr := newFleetBudget(
					&storeFakePostgres{},
					test.rate,
					test.maxByte,
				)
				if !errors.Is(constructErr, errInvalidFleetBudget) ||
					constructed != nil {
					t.Fatalf(
						"newFleetBudget() = %v, %v, want nil, %v",
						constructed,
						constructErr,
						errInvalidFleetBudget,
					)
				}
			})
		}

		unpooled, poolErr := newFleetBudget(nil, 1, 1)
		if !errors.Is(poolErr, errPoolUnavailable) || unpooled != nil {
			t.Fatalf(
				"newFleetBudget() = %v, %v, want nil, %v",
				unpooled,
				poolErr,
				errPoolUnavailable,
			)
		}

		config, configErr := pgxpool.ParseConfig(
			"postgres://unit:unit@127.0.0.1:1/unit",
		)
		if configErr != nil {
			t.Fatal(configErr)
		}
		pool, poolCreateErr := pgxpool.NewWithConfig(ctx, config)
		if poolCreateErr != nil {
			t.Fatal(poolCreateErr)
		}
		t.Cleanup(pool.Close)

		budgeted, budgetErr := NewFleetBudget(pool, 1, 10)
		if budgetErr != nil || budgeted == nil {
			t.Fatalf(
				"NewFleetBudget() = %v, %v, want a budget",
				budgeted,
				budgetErr,
			)
		}
	})

	t.Run("acquire rejects invalid callers", func(t *testing.T) {
		var missing *FleetBudget
		if _, err := missing.Acquire(ctx, 0); !errors.Is(err, errPoolUnavailable) {
			t.Fatalf("Acquire() error = %v, want %v", err, errPoolUnavailable)
		}

		empty := &FleetBudget{}
		if _, err := empty.Acquire(ctx, 0); !errors.Is(err, errPoolUnavailable) {
			t.Fatalf("Acquire() error = %v, want %v", err, errPoolUnavailable)
		}

		budget, err := newFleetBudget(&storeFakePostgres{}, 1, 10)
		if err != nil {
			t.Fatal(err)
		}
		var nilContext context.Context
		if _, err = budget.Acquire(nilContext, 0); !errors.Is(err, errInvalidContext) {
			t.Fatalf("Acquire() error = %v, want %v", err, errInvalidContext)
		}

		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err = budget.Acquire(canceled, 0); !errors.Is(err, context.Canceled) {
			t.Fatalf("Acquire() error = %v, want %v", err, context.Canceled)
		}
		if _, err = budget.Acquire(ctx, -1); !errors.Is(err, errInvalidFleetBudget) {
			t.Fatalf("Acquire() error = %v, want %v", err, errInvalidFleetBudget)
		}

		budget.requestInterval = 0
		if _, err = budget.Acquire(ctx, 0); !errors.Is(err, errInvalidFleetBudget) {
			t.Fatalf("Acquire() error = %v, want %v", err, errInvalidFleetBudget)
		}
	})

	t.Run("acquire reserves a slot", func(t *testing.T) {
		captured := &fleetBudgetCaptureDatabase{
			base: &storeFakePostgres{
				rowResults: []storeFakeRow{{
					values: []any{false, 1.5, 3.0},
				}},
			},
		}
		budget, err := newFleetBudget(captured, 2, 64)
		if err != nil {
			t.Fatal(err)
		}

		wait, err := budget.Acquire(ctx, 9)
		if err != nil {
			t.Fatal(err)
		}
		if wait != 1500*time.Millisecond {
			t.Fatalf("wait = %s, want 1.5s", wait)
		}
		if !strings.Contains(captured.query, "crawl_fleet_budget") {
			t.Fatalf("query = %q, want the fleet budget update", captured.query)
		}
		if len(captured.args) != 3 ||
			captured.args[0] != int64(9) ||
			captured.args[1] != int64(64) ||
			captured.args[2] != 500*time.Millisecond {
			t.Fatalf("args = %#v, want pending, cap, and interval", captured.args)
		}
	})

	t.Run("acquire reports a spent byte cap", func(t *testing.T) {
		budget, err := newFleetBudget(
			&storeFakePostgres{
				rowResults: []storeFakeRow{{
					values: []any{true, 0.0, 12.5},
				}},
			},
			1,
			10,
		)
		if err != nil {
			t.Fatal(err)
		}

		wait, err := budget.Acquire(ctx, 4)
		var budgetErr *FleetByteBudgetError
		if wait != 0 || !errors.As(err, &budgetErr) {
			t.Fatalf("Acquire() = %s, %v, want a fleet byte budget error", wait, err)
		}
		if budgetErr.FleetByteBudgetDelay() != 12500*time.Millisecond {
			t.Fatalf(
				"retry after = %s, want 12.5s",
				budgetErr.FleetByteBudgetDelay(),
			)
		}
		if budgetErr.Error() == "" {
			t.Fatal("fleet byte budget error text is empty")
		}
	})

	t.Run("acquire clamps a non-positive wait", func(t *testing.T) {
		budget, err := newFleetBudget(
			&storeFakePostgres{
				rowResults: []storeFakeRow{{
					values: []any{false, -2.0, math.NaN()},
				}},
			},
			1,
			10,
		)
		if err != nil {
			t.Fatal(err)
		}

		wait, err := budget.Acquire(ctx, 0)
		if err != nil || wait != 0 {
			t.Fatalf("Acquire() = %s, %v, want 0, nil", wait, err)
		}
		if durationFromSeconds(math.Inf(1)) != 0 {
			t.Fatal("infinite seconds produced a wait")
		}
	})

	t.Run("acquire returns a database failure", func(t *testing.T) {
		budget, err := newFleetBudget(
			&storeFakePostgres{
				rowResults: []storeFakeRow{{
					err: errUnexpectedStoreDatabaseCall,
				}},
			},
			1,
			10,
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = budget.Acquire(ctx, 0)
		if !errors.Is(err, errUnexpectedStoreDatabaseCall) {
			t.Fatalf("Acquire() error = %v, want the database error", err)
		}
	})

	t.Run("nil byte budget delay is zero", func(t *testing.T) {
		var budgetErr *FleetByteBudgetError
		if budgetErr.FleetByteBudgetDelay() != 0 {
			t.Fatal("nil fleet byte budget delay is not zero")
		}
	})
}

func TestReleaseDiscoverySourceLeaseWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.November, 5, 12, 0, 0, 0, time.UTC)
	lease := discovery.CrawlSourceLease{
		Origin:     mustStoreOrigin(t, "https://example.com"),
		Generation: 4,
		ClaimedAt:  now.Add(-time.Minute),
		ExpiresAt:  now.Add(time.Minute),
	}

	t.Run("validation", func(t *testing.T) {
		var store *DiscoveryStore
		err := store.ReleaseDiscoverySourceLease(ctx, lease)
		if !errors.Is(err, errDiscoveryStoreUnavailable) {
			t.Fatalf(
				"ReleaseDiscoverySourceLease() error = %v, want %v",
				err,
				errDiscoveryStoreUnavailable,
			)
		}
	})

	t.Run("invalid lease", func(t *testing.T) {
		store := discoveryReleaseStore(
			t,
			&discoveryPolicyUnitDatabase{},
			now,
		)
		err := store.ReleaseDiscoverySourceLease(
			ctx,
			discovery.CrawlSourceLease{},
		)
		if !errors.Is(err, errInvalidDiscoverySourceLease) {
			t.Fatalf(
				"ReleaseDiscoverySourceLease() error = %v, want %v",
				err,
				errInvalidDiscoverySourceLease,
			)
		}
	})

	t.Run("canceled", func(t *testing.T) {
		store := discoveryReleaseStore(
			t,
			&discoveryPolicyUnitDatabase{},
			now,
		)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		err := store.ReleaseDiscoverySourceLease(canceled, lease)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf(
				"ReleaseDiscoverySourceLease() error = %v, want %v",
				err,
				context.Canceled,
			)
		}
	})

	t.Run("begin failure", func(t *testing.T) {
		store := discoveryReleaseStore(
			t,
			&discoveryPolicyUnitDatabase{beginErr: errDiscoveryPolicyBegin},
			now,
		)
		err := store.ReleaseDiscoverySourceLease(ctx, lease)
		if !errors.Is(err, errDiscoveryPolicyBegin) {
			t.Fatalf(
				"ReleaseDiscoverySourceLease() error = %v, want %v",
				err,
				errDiscoveryPolicyBegin,
			)
		}
	})

	t.Run("clock failure", func(t *testing.T) {
		store, err := newDiscoveryStore(
			&discoveryPolicyUnitDatabase{
				beginTx: &discoveryPolicyUnitTx{},
			},
			discoveryCompletionClock{err: errDiscoveryClockUnavailable},
		)
		if err != nil {
			t.Fatal(err)
		}
		err = store.ReleaseDiscoverySourceLease(ctx, lease)
		if !errors.Is(err, errDiscoveryClockUnavailable) {
			t.Fatalf(
				"ReleaseDiscoverySourceLease() error = %v, want %v",
				err,
				errDiscoveryClockUnavailable,
			)
		}
	})

	t.Run("update failure", func(t *testing.T) {
		store := discoveryReleaseStore(
			t,
			releaseDatabase(pgconn.CommandTag{}, errUnexpectedDiscoveryUnitDatabaseCall),
			now,
		)
		err := store.ReleaseDiscoverySourceLease(ctx, lease)
		if !errors.Is(err, errUnexpectedDiscoveryUnitDatabaseCall) {
			t.Fatalf(
				"ReleaseDiscoverySourceLease() error = %v, want %v",
				err,
				errUnexpectedDiscoveryUnitDatabaseCall,
			)
		}
	})

	t.Run("lost lease", func(t *testing.T) {
		store := discoveryReleaseStore(
			t,
			releaseDatabase(pgconn.NewCommandTag("UPDATE 0"), nil),
			now,
		)
		err := store.ReleaseDiscoverySourceLease(ctx, lease)
		if !errors.Is(err, ErrDiscoverySourceLeaseLost) {
			t.Fatalf(
				"ReleaseDiscoverySourceLease() error = %v, want %v",
				err,
				ErrDiscoverySourceLeaseLost,
			)
		}
	})

	t.Run("success clears only the lease", func(t *testing.T) {
		tx := &discoveryLeaseReleaseCaptureTx{
			discoveryPolicyUnitTx: discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{{
					tag: pgconn.NewCommandTag("UPDATE 1"),
				}},
			},
		}
		store := discoveryReleaseStore(
			t,
			&discoveryPolicyUnitDatabase{beginTx: tx},
			now,
		)
		if err := store.ReleaseDiscoverySourceLease(ctx, lease); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(tx.query, "lease_owner = NULL") ||
			!strings.Contains(tx.query, "lease_expires_at = NULL") ||
			strings.Contains(tx.query, "consecutive_failures") ||
			strings.Contains(tx.query, "last_attempted_at") {
			t.Fatalf("release query = %q, want lease columns only", tx.query)
		}
		if len(tx.args) != 3 ||
			tx.args[0] != lease.Origin.String() ||
			tx.args[1] != lease.Generation ||
			tx.args[2] != now {
			t.Fatalf("release args = %#v, want origin, generation, and now", tx.args)
		}
	})
}

type discoveryLeaseReleaseCaptureTx struct {
	discoveryPolicyUnitTx

	query string
	args  []any
}

func (tx *discoveryLeaseReleaseCaptureTx) Exec(
	ctx context.Context,
	query string,
	args ...any,
) (pgconn.CommandTag, error) {
	tx.query = query
	tx.args = append([]any(nil), args...)

	return tx.discoveryPolicyUnitTx.Exec(ctx, query, args...)
}

func discoveryReleaseStore(
	t *testing.T,
	pool database.Postgres,
	now time.Time,
) *DiscoveryStore {
	t.Helper()

	store, err := newDiscoveryStore(
		pool,
		discoveryCompletionClock{now: now},
	)
	if err != nil {
		t.Fatal(err)
	}

	return store
}

func releaseDatabase(
	tag pgconn.CommandTag,
	err error,
) *discoveryPolicyUnitDatabase {
	return &discoveryPolicyUnitDatabase{
		beginTx: &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{{
				tag: tag,
				err: err,
			}},
		},
	}
}

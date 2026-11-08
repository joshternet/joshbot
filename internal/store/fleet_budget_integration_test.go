package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/origin"
)

func TestFleetBudgetAndLeaseReleaseIntegration(t *testing.T) {
	t.Run("budget without database", TestFleetBudgetWithoutDatabase)
	t.Run(
		"release without database",
		TestReleaseDiscoverySourceLeaseWithoutDatabase,
	)

	ctx := context.Background()
	pool := newSerialStoreTestPool(t)
	budget, err := NewFleetBudget(pool, 1, 100)
	if err != nil {
		t.Fatal(err)
	}

	firstWait, err := budget.Acquire(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if firstWait > time.Second {
		t.Fatalf("first wait = %s, want at most 1s", firstWait)
	}

	var nextRequestAt time.Time
	var bytesUsed int64
	if err := pool.QueryRow(ctx, `
		SELECT next_request_at, bytes_used
		FROM crawl_fleet_budget
		WHERE id = 1
	`).Scan(&nextRequestAt, &bytesUsed); err != nil {
		t.Fatal(err)
	}
	if bytesUsed != 0 {
		t.Fatalf("bytes used = %d, want 0", bytesUsed)
	}

	secondWait, err := budget.Acquire(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if secondWait < 200*time.Millisecond || secondWait > 2*time.Second {
		t.Fatalf("second wait = %s, want about 1s", secondWait)
	}

	var moved time.Time
	if err := pool.QueryRow(ctx, `
		SELECT next_request_at
		FROM crawl_fleet_budget
		WHERE id = 1
	`).Scan(&moved); err != nil {
		t.Fatal(err)
	}
	if !moved.After(nextRequestAt) {
		t.Fatalf(
			"next request = %s, want after %s",
			moved,
			nextRequestAt,
		)
	}

	held := moved
	_, err = budget.Acquire(ctx, 100)
	var spent *FleetByteBudgetError
	if !errors.As(err, &spent) {
		t.Fatalf("Acquire() error = %v, want a spent byte budget", err)
	}
	if spent.RetryAfter <= 0 || spent.RetryAfter > 24*time.Hour {
		t.Fatalf("retry after = %s, want within the next UTC day", spent.RetryAfter)
	}

	var afterCap time.Time
	if err := pool.QueryRow(ctx, `
		SELECT next_request_at, bytes_used
		FROM crawl_fleet_budget
		WHERE id = 1
	`).Scan(&afterCap, &bytesUsed); err != nil {
		t.Fatal(err)
	}
	if !afterCap.Equal(held) {
		t.Fatalf("next request = %s, want %s unchanged", afterCap, held)
	}
	if bytesUsed != 100 {
		t.Fatalf("bytes used = %d, want 100", bytesUsed)
	}

	_, err = budget.Acquire(ctx, 5)
	if !errors.As(err, &spent) {
		t.Fatalf("Acquire() error = %v, want the cap to stay spent", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT bytes_used FROM crawl_fleet_budget WHERE id = 1
	`).Scan(&bytesUsed); err != nil {
		t.Fatal(err)
	}
	if bytesUsed != 105 {
		t.Fatalf("bytes used = %d, want 105", bytesUsed)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE crawl_fleet_budget
		SET
			byte_window_started_at = now() - interval '2 days',
			bytes_used = 500,
			next_request_at = TIMESTAMP WITH TIME ZONE 'epoch'
	`); err != nil {
		t.Fatal(err)
	}

	rolled, err := NewFleetBudget(pool, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rolled.Acquire(ctx, 7); err != nil {
		t.Fatal(err)
	}
	var window time.Time
	if err := pool.QueryRow(ctx, `
		SELECT byte_window_started_at, bytes_used
		FROM crawl_fleet_budget
		WHERE id = 1
	`).Scan(&window, &bytesUsed); err != nil {
		t.Fatal(err)
	}
	if bytesUsed != 7 {
		t.Fatalf("rolled bytes used = %d, want 7", bytesUsed)
	}
	var today time.Time
	if err := pool.QueryRow(ctx, `
		SELECT date_trunc('day', timezone('UTC', now())) AT TIME ZONE 'UTC'
	`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	if !window.Equal(today) {
		t.Fatalf("window = %s, want %s", window, today)
	}

	concurrent, err := NewFleetBudget(pool, 1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE crawl_fleet_budget
		SET
			bytes_used = 0,
			next_request_at = now()
	`); err != nil {
		t.Fatal(err)
	}

	var waits [2]time.Duration
	var acquireErr [2]error
	var group sync.WaitGroup
	start := make(chan struct{})
	for index := range waits {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			waits[index], acquireErr[index] = concurrent.Acquire(ctx, 0)
		}(index)
	}
	close(start)
	group.Wait()
	for index := range acquireErr {
		if acquireErr[index] != nil {
			t.Fatal(acquireErr[index])
		}
	}
	shorter := waits[0]
	longer := waits[1]
	if longer < shorter {
		shorter, longer = longer, shorter
	}
	if longer-shorter < 200*time.Millisecond || longer-shorter > 2*time.Second {
		t.Fatalf(
			"concurrent waits = %s and %s, want about 1s apart",
			waits[0],
			waits[1],
		)
	}

	source := seedDiscoveryTestSource(
		t,
		pool,
		"https://fleet-budget.example",
		time.Now().UTC(),
	)
	discoveryStore, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := discoveryStore.ClaimDiscoverySourceLease(
		ctx,
		testDiscoveryLeaseOwner,
		168*time.Hour,
		5*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("claim found = false, want true")
	}

	before := readDiscoveryLeaseState(t, pool, source)
	if err := discoveryStore.ReleaseDiscoverySourceLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	after := readDiscoveryLeaseState(t, pool, source)
	if after.leaseOwner != nil || after.leaseExpiresAt != nil {
		t.Fatalf("released lease = %v / %v, want cleared", after.leaseOwner, after.leaseExpiresAt)
	}
	if after.consecutiveFailures != before.consecutiveFailures ||
		!sameTime(after.lastAttemptedAt, before.lastAttemptedAt) ||
		!sameTime(after.nextAttemptAt, before.nextAttemptAt) {
		t.Fatalf("release changed retry state from %#v to %#v", before, after)
	}

	if err := discoveryStore.ReleaseDiscoverySourceLease(ctx, lease); !errors.Is(
		err,
		ErrDiscoverySourceLeaseLost,
	) {
		t.Fatalf(
			"second release error = %v, want %v",
			err,
			ErrDiscoverySourceLeaseLost,
		)
	}
}

type discoveryLeaseReleaseState struct {
	leaseOwner          *string
	leaseExpiresAt      *time.Time
	consecutiveFailures int
	lastAttemptedAt     *time.Time
	nextAttemptAt       *time.Time
}

func readDiscoveryLeaseState(
	t *testing.T,
	pool *pgxpool.Pool,
	source origin.Origin,
) discoveryLeaseReleaseState {
	t.Helper()

	var state discoveryLeaseReleaseState
	if err := pool.QueryRow(
		context.Background(),
		`
			SELECT
				lease_owner,
				lease_expires_at,
				consecutive_failures,
				last_attempted_at,
				next_attempt_at
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		source.String(),
	).Scan(
		&state.leaseOwner,
		&state.leaseExpiresAt,
		&state.consecutiveFailures,
		&state.lastAttemptedAt,
		&state.nextAttemptAt,
	); err != nil {
		t.Fatal(err)
	}

	return state
}

func sameTime(left *time.Time, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return left.Equal(*right)
}

package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type discoveryLeaseClaimResult struct {
	origin    string
	claimedAt time.Time
	expiresAt time.Time
	found     bool
	err       error
}

func TestDiscoverySourceLeaseClaimClockFollowsAdvisoryWait(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newSerialStoreTestPool(t)

	source := mustStoreOrigin(
		t,
		"https://claim-clock-after-lock.example",
	)

	setupStore, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v",
			err,
		)
	}

	if err := setupStore.AddCrawlSeed(
		ctx,
		source,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v",
			err,
		)
	}

	lockTransaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf(
			"begin lock transaction: %v",
			err,
		)
	}

	lockReleased := false
	defer func() {
		if !lockReleased {
			rollbackTestTransaction(
				t,
				lockTransaction,
			)
		}
	}()

	var holderPID int
	if err := lockTransaction.QueryRow(
		ctx,
		"SELECT pg_backend_pid()",
	).Scan(
		&holderPID,
	); err != nil {
		t.Fatalf(
			"read lock holder backend PID: %v",
			err,
		)
	}

	if _, err := lockTransaction.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock($1)",
		discoveryClaimAdvisoryLockKey,
	); err != nil {
		t.Fatalf(
			"lock discovery claim advisory lock: %v",
			err,
		)
	}

	operationPool := newStoreSiblingPool(
		t,
		pool,
		"",
	)

	var waiterPID int
	if err := operationPool.QueryRow(
		ctx,
		"SELECT pg_backend_pid()",
	).Scan(
		&waiterPID,
	); err != nil {
		t.Fatalf(
			"read claim waiter backend PID: %v",
			err,
		)
	}

	observerPool := newStoreSiblingPool(
		t,
		pool,
		"",
	)

	operationStore, err := NewDiscoveryStore(
		operationPool,
	)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore(operation) error = %v",
			err,
		)
	}

	const leaseDuration = 5 * time.Minute

	resultChannel := make(
		chan discoveryLeaseClaimResult,
		1,
	)

	go func() {
		lease, found, claimErr :=
			operationStore.ClaimDiscoverySourceLease(
				ctx,
				testDiscoveryLeaseOwner,
				time.Hour,
				leaseDuration,
			)

		resultChannel <- discoveryLeaseClaimResult{
			origin:    lease.Origin.String(),
			claimedAt: lease.ClaimedAt,
			expiresAt: lease.ExpiresAt,
			found:     found,
			err:       claimErr,
		}
	}()

	waitForDiscoveryClaimAdvisoryBlock(
		t,
		ctx,
		observerPool,
		holderPID,
		waiterPID,
	)

	var blockedAt time.Time
	if err := observerPool.QueryRow(
		ctx,
		"SELECT clock_timestamp()",
	).Scan(
		&blockedAt,
	); err != nil {
		t.Fatalf(
			"read timestamp while claim is blocked: %v",
			err,
		)
	}

	if err := lockTransaction.Rollback(ctx); err != nil {
		t.Fatalf(
			"release discovery claim advisory lock: %v",
			err,
		)
	}
	lockReleased = true

	var result discoveryLeaseClaimResult
	select {
	case result = <-resultChannel:
	case <-time.After(5 * time.Second):
		t.Fatal(
			"discovery claim did not complete after advisory lock release",
		)
	}

	if result.err != nil {
		t.Fatalf(
			"ClaimDiscoverySourceLease() error = %v",
			result.err,
		)
	}

	if !result.found {
		t.Fatal(
			"ClaimDiscoverySourceLease() found = false, want true",
		)
	}

	if result.origin != source.String() {
		t.Fatalf(
			"ClaimDiscoverySourceLease() origin = %q, want %q",
			result.origin,
			source.String(),
		)
	}

	if result.claimedAt.Before(blockedAt) {
		t.Fatalf(
			"claim timestamp = %v, before blocked timestamp %v",
			result.claimedAt,
			blockedAt,
		)
	}

	wantExpiresAt := result.claimedAt.Add(
		leaseDuration,
	)
	if !result.expiresAt.Equal(
		wantExpiresAt,
	) {
		t.Fatalf(
			"lease expiration = %v, want %v",
			result.expiresAt,
			wantExpiresAt,
		)
	}

	state := readDiscoveryLeaseIntegrationState(
		t,
		ctx,
		observerPool,
		source.String(),
	)

	assertDiscoveryLeaseIntegrationTime(
		t,
		"stored last_claimed_at",
		state.lastClaimedAt,
		result.claimedAt,
	)

	assertDiscoveryLeaseIntegrationTime(
		t,
		"stored lease_expires_at",
		state.leaseExpiresAt,
		result.expiresAt,
	)
}

func waitForDiscoveryClaimAdvisoryBlock(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	holderPID int,
	waiterPID int,
) {
	t.Helper()

	waitContext, cancel := context.WithTimeout(
		ctx,
		5*time.Second,
	)
	defer cancel()

	ticker := time.NewTicker(
		10 * time.Millisecond,
	)
	defer ticker.Stop()

	for {
		var blocked bool
		if err := pool.QueryRow(
			waitContext,
			`
				SELECT
					$1::integer =
					ANY(
						pg_blocking_pids(
							$2::integer
						)
					)
			`,
			holderPID,
			waiterPID,
		).Scan(
			&blocked,
		); err != nil {
			t.Fatalf(
				"inspect discovery claim advisory wait: %v",
				err,
			)
		}

		if blocked {
			return
		}

		select {
		case <-waitContext.Done():
			t.Fatalf(
				"backend %d did not block behind advisory-lock holder %d: %v",
				waiterPID,
				holderPID,
				waitContext.Err(),
			)

		case <-ticker.C:
		}
	}
}

package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestQueueActiveLeaseBlocksDuplicateClaim(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     15 * time.Minute,
			MinOriginInterval: time.Hour,
		},
		now,
	)
	source := mustStoreOrigin(t, "https://example.com")

	if err := queue.Schedule(ctx, source, now); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	firstLease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil {
		t.Fatalf("first Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("first Claim() found = false, want true")
	}

	secondLease, found, err := queue.Claim(ctx, "worker-b")
	if err != nil {
		t.Fatalf("second Claim() error = %v, want nil", err)
	}

	if found {
		t.Error("second Claim() found = true, want false")
	}

	if secondLease != (Lease{}) {
		t.Errorf(
			"second Claim() lease = %#v, want zero value",
			secondLease,
		)
	}

	var (
		storedOwner      string
		storedGeneration int64
		storedClaimedAt  time.Time
		storedExpiresAt  time.Time
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				lease_owner,
				lease_generation,
				last_claimed_at,
				lease_expires_at
			FROM verification_queue
			WHERE origin = $1
		`,
		source.String(),
	).Scan(
		&storedOwner,
		&storedGeneration,
		&storedClaimedAt,
		&storedExpiresAt,
	)
	if err != nil {
		t.Fatalf("query active lease: %v", err)
	}

	if storedOwner != firstLease.WorkerID {
		t.Errorf(
			"stored owner = %q, want %q",
			storedOwner,
			firstLease.WorkerID,
		)
	}

	if storedGeneration != firstLease.Generation {
		t.Errorf(
			"stored generation = %d, want %d",
			storedGeneration,
			firstLease.Generation,
		)
	}

	if !storedClaimedAt.Equal(firstLease.ClaimedAt) {
		t.Errorf(
			"stored claim time = %v, want %v",
			storedClaimedAt,
			firstLease.ClaimedAt,
		)
	}

	if !storedExpiresAt.Equal(firstLease.ExpiresAt) {
		t.Errorf(
			"stored expiration = %v, want %v",
			storedExpiresAt,
			firstLease.ExpiresAt,
		)
	}
}

func TestQueueConcurrentWorkersClaimDistinctOrigins(
	t *testing.T,
) {
	const claimCount = 12

	ctx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()

	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     15 * time.Minute,
			MinOriginInterval: time.Hour,
		},
		now,
	)

	for index := 0; index < claimCount; index++ {
		source := mustStoreOrigin(
			t,
			fmt.Sprintf(
				"https://origin-%02d.example",
				index,
			),
		)

		if err := queue.Schedule(ctx, source, now); err != nil {
			t.Fatalf(
				"Schedule(%q) error = %v, want nil",
				source,
				err,
			)
		}
	}

	start := make(chan struct{})
	results := make(
		chan concurrentClaimResult,
		claimCount,
	)

	var waitGroup sync.WaitGroup
	for index := 0; index < claimCount; index++ {
		waitGroup.Add(1)

		go func(workerIndex int) {
			defer waitGroup.Done()

			<-start

			lease, found, err := queue.Claim(
				ctx,
				fmt.Sprintf(
					"worker-%02d",
					workerIndex,
				),
			)
			results <- concurrentClaimResult{
				lease: lease,
				found: found,
				err:   err,
			}
		}(index)
	}

	close(start)
	waitGroup.Wait()
	close(results)

	claimedOrigins := make(map[string]string)
	successfulClaims := 0

	for result := range results {
		if result.err != nil {
			t.Errorf(
				"concurrent Claim() error = %v, want nil",
				result.err,
			)
			continue
		}

		if !result.found {
			t.Error(
				"concurrent Claim() found = false, want true",
			)
			continue
		}

		successfulClaims++

		if result.lease.Generation != 1 {
			t.Errorf(
				"lease generation = %d, want 1",
				result.lease.Generation,
			)
		}

		originKey := result.lease.Origin.String()
		if previousWorker, exists :=
			claimedOrigins[originKey]; exists {
			t.Errorf(
				"origin %q claimed by both %q and %q",
				originKey,
				previousWorker,
				result.lease.WorkerID,
			)
			continue
		}

		claimedOrigins[originKey] =
			result.lease.WorkerID
	}

	if successfulClaims != claimCount {
		t.Errorf(
			"successful claims = %d, want %d",
			successfulClaims,
			claimCount,
		)
	}

	if len(claimedOrigins) != claimCount {
		t.Errorf(
			"distinct claimed origins = %d, want %d",
			len(claimedOrigins),
			claimCount,
		)
	}
}

func TestQueueSingleOriginHasOneConcurrentWinner(
	t *testing.T,
) {
	const claimantCount = 32

	ctx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()

	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     15 * time.Minute,
			MinOriginInterval: time.Hour,
		},
		now,
	)
	source := mustStoreOrigin(t, "https://example.com")

	if err := queue.Schedule(ctx, source, now); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	start := make(chan struct{})
	results := make(
		chan concurrentClaimResult,
		claimantCount,
	)

	var waitGroup sync.WaitGroup
	for index := 0; index < claimantCount; index++ {
		waitGroup.Add(1)

		go func(workerIndex int) {
			defer waitGroup.Done()

			<-start

			lease, found, err := queue.Claim(
				ctx,
				fmt.Sprintf(
					"worker-%02d",
					workerIndex,
				),
			)
			results <- concurrentClaimResult{
				lease: lease,
				found: found,
				err:   err,
			}
		}(index)
	}

	close(start)
	waitGroup.Wait()
	close(results)

	successfulClaims := 0
	var winningLease Lease

	for result := range results {
		if result.err != nil {
			t.Errorf(
				"concurrent Claim() error = %v, want nil",
				result.err,
			)
			continue
		}

		if !result.found {
			if result.lease != (Lease{}) {
				t.Errorf(
					"unsuccessful lease = %#v, want zero value",
					result.lease,
				)
			}

			continue
		}

		successfulClaims++
		winningLease = result.lease
	}

	if successfulClaims != 1 {
		t.Errorf(
			"successful claims = %d, want 1",
			successfulClaims,
		)
	}

	if successfulClaims == 1 {
		if winningLease.Origin != source {
			t.Errorf(
				"winning origin = %q, want %q",
				winningLease.Origin,
				source,
			)
		}

		if winningLease.Generation != 1 {
			t.Errorf(
				"winning generation = %d, want 1",
				winningLease.Generation,
			)
		}
	}

	var generation int64
	err := pool.QueryRow(
		ctx,
		`
			SELECT lease_generation
			FROM verification_queue
			WHERE origin = $1
		`,
		source.String(),
	).Scan(&generation)
	if err != nil {
		t.Fatalf("query generation: %v", err)
	}

	if generation != 1 {
		t.Errorf(
			"stored generation = %d, want 1",
			generation,
		)
	}
}

func TestQueueSkipsLockedCandidate(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     15 * time.Minute,
			MinOriginInterval: time.Hour,
		},
		now,
	)

	firstSource := mustStoreOrigin(
		t,
		"https://example.com",
	)
	secondSource := mustStoreOrigin(
		t,
		"https://example.net",
	)

	for _, source := range []string{
		firstSource.String(),
		secondSource.String(),
	} {
		if err := queue.Schedule(
			ctx,
			mustStoreOrigin(t, source),
			now,
		); err != nil {
			t.Fatalf(
				"Schedule(%q) error = %v, want nil",
				source,
				err,
			)
		}
	}

	lockTransaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lock transaction: %v", err)
	}
	defer func() {
		_ = lockTransaction.Rollback(
			context.Background(),
		)
	}()

	var lockedOrigin string
	err = lockTransaction.QueryRow(
		ctx,
		`
			SELECT origin
			FROM verification_queue
			WHERE available_at <= $1
			ORDER BY
				available_at ASC,
				origin ASC
			FOR UPDATE
			LIMIT 1
		`,
		now,
	).Scan(&lockedOrigin)
	if err != nil {
		t.Fatalf("lock first candidate: %v", err)
	}

	if lockedOrigin != firstSource.String() {
		t.Fatalf(
			"locked origin = %q, want %q",
			lockedOrigin,
			firstSource,
		)
	}

	claimContext, cancel := context.WithTimeout(
		ctx,
		5*time.Second,
	)
	defer cancel()

	lease, found, err := queue.Claim(
		claimContext,
		"worker-a",
	)
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("Claim() found = false, want true")
	}

	if lease.Origin != secondSource {
		t.Errorf(
			"Claim() origin = %q, want %q",
			lease.Origin,
			secondSource,
		)
	}

	if err := lockTransaction.Rollback(ctx); err != nil {
		t.Fatalf("rollback lock transaction: %v", err)
	}

	recoveredLease, found, err := queue.Claim(
		ctx,
		"worker-b",
	)
	if err != nil {
		t.Fatalf(
			"Claim() after rollback error = %v, want nil",
			err,
		)
	}

	if !found {
		t.Fatal(
			"Claim() after rollback found = false, want true",
		)
	}

	if recoveredLease.Origin != firstSource {
		t.Errorf(
			"Claim() after rollback origin = %q, want %q",
			recoveredLease.Origin,
			firstSource,
		)
	}
}

func TestQueueClaimClosesTransactionBeforeReturning(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     15 * time.Minute,
			MinOriginInterval: time.Hour,
		},
		now,
	)
	source := mustStoreOrigin(t, "https://example.com")

	if err := queue.Schedule(ctx, source, now); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	_, found, err := queue.Claim(ctx, "worker-a")
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("Claim() found = false, want true")
	}

	probeTransaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin probe transaction: %v", err)
	}
	defer func() {
		_ = probeTransaction.Rollback(
			context.Background(),
		)
	}()

	var lockedOrigin string
	err = probeTransaction.QueryRow(
		ctx,
		`
			SELECT origin
			FROM verification_queue
			WHERE origin = $1
			FOR UPDATE NOWAIT
		`,
		source.String(),
	).Scan(&lockedOrigin)
	if err != nil {
		t.Fatalf(
			"lock claimed row after Claim() returned: %v",
			err,
		)
	}

	if lockedOrigin != source.String() {
		t.Errorf(
			"locked origin = %q, want %q",
			lockedOrigin,
			source,
		)
	}
}

type concurrentClaimResult struct {
	lease Lease
	found bool
	err   error
}

package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestSameNeighborhoodClaimsDoNotBlockOnUpsert verifies fail-fast neighborhood
// acquisition: many workers contending on one neighborhood produce one live
// lease and prompt skips, not claim errors from unique-index waits.
func TestSameNeighborhoodClaimsDoNotBlockOnUpsert(t *testing.T) {
	ctx := context.Background()
	// Serial pool keeps contention deterministic under parallel package tests.
	pool := newSerialStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     5 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		now,
	)

	const workers = 16
	for index := 0; index < workers; index++ {
		source := mustStoreOrigin(
			t,
			fmt.Sprintf("https://host-%02d.contention.test", index),
		)
		if err := queue.Schedule(ctx, source, now); err != nil {
			t.Fatalf("Schedule(%s) error = %v", source, err)
		}
	}

	type result struct {
		found bool
		err   error
		stats ClaimStats
	}
	results := make([]result, workers)
	var started sync.WaitGroup
	var release sync.WaitGroup
	started.Add(workers)
	release.Add(1)

	var join sync.WaitGroup
	join.Add(workers)
	for index := 0; index < workers; index++ {
		go func(workerIndex int) {
			defer join.Done()
			started.Done()
			release.Wait()
			workerID := fmt.Sprintf("worker-%02d", workerIndex)
			lease, stats, err := queue.ClaimWithStats(
				ctx,
				workerID,
			)
			results[workerIndex] = result{
				found: stats.Outcome == ClaimOutcomeSuccess,
				err:   err,
				stats: stats,
			}
			_ = lease
		}(index)
	}
	started.Wait()
	release.Done()
	join.Wait()

	successes := 0
	for index, item := range results {
		if item.err != nil {
			t.Fatalf(
				"worker %d ClaimWithStats error = %v",
				index,
				item.err,
			)
		}
		if item.found {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf(
			"successful claims = %d, want 1 (one neighborhood)",
			successes,
		)
	}

	var live int
	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM domain_inflight_leases
			WHERE lease_expires_at > $1
		`,
		now,
	).Scan(&live); err != nil {
		t.Fatalf("count live domain leases: %v", err)
	}
	if live != 1 {
		t.Fatalf("live domain leases = %d, want 1", live)
	}
}

// TestClaimFindsWorkBehindBusyNeighborhoodBatch ensures scanning continues
// past a full busy batch to an available neighborhood without reporting
// false no_work.
func TestClaimFindsWorkBehindBusyNeighborhoodBatch(t *testing.T) {
	ctx := context.Background()
	// Serial pool keeps contention deterministic under parallel package tests.
	pool := newSerialStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     5 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		now,
	)

	holder := mustStoreOrigin(t, "https://busy-00.claimscan.test")
	if err := queue.Schedule(ctx, holder, now); err != nil {
		t.Fatalf("Schedule(holder) error = %v", err)
	}
	holderLease, found, err := queue.Claim(ctx, "holder")
	if err != nil || !found {
		t.Fatalf(
			"holder Claim() = %#v, %t, %v",
			holderLease,
			found,
			err,
		)
	}

	for index := 1; index <= claimCandidateBatchSize+4; index++ {
		source := mustStoreOrigin(
			t,
			fmt.Sprintf("https://busy-%02d.claimscan.test", index),
		)
		if err := queue.Schedule(ctx, source, now); err != nil {
			t.Fatalf("Schedule(%s) error = %v", source, err)
		}
	}

	free := mustStoreOrigin(t, "https://solo.claimscan-free.test")
	if err := queue.Schedule(ctx, free, now); err != nil {
		t.Fatalf("Schedule(free) error = %v", err)
	}

	lease, stats, err := queue.ClaimWithStats(ctx, "seeker")
	if err != nil {
		t.Fatalf("ClaimWithStats() error = %v", err)
	}
	if stats.Outcome != ClaimOutcomeSuccess {
		t.Fatalf(
			"outcome = %s, want success (stats=%#v)",
			stats.Outcome,
			stats,
		)
	}
	if lease.Origin != free {
		t.Fatalf(
			"claimed %s, want %s",
			lease.Origin,
			free,
		)
	}
	if stats.BusySkips == 0 {
		t.Fatal("expected busy skips while scanning past claimscan.test")
	}
}

// TestClaimReportsDeferredForBusyOnlyQueue ensures temporary neighborhood
// contention is not reported as an empty queue.
func TestClaimReportsDeferredForBusyOnlyQueue(t *testing.T) {
	ctx := context.Background()
	pool := newSerialStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     5 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		now,
	)

	holder := mustStoreOrigin(t, "https://busy-only-00.deferred.test")
	if err := queue.Schedule(ctx, holder, now); err != nil {
		t.Fatalf("Schedule(holder) error = %v", err)
	}
	if _, found, err := queue.Claim(ctx, "holder"); err != nil || !found {
		t.Fatalf("holder Claim() found=%t err=%v", found, err)
	}

	for index := 1; index <= 4; index++ {
		source := mustStoreOrigin(
			t,
			fmt.Sprintf("https://busy-only-%02d.deferred.test", index),
		)
		if err := queue.Schedule(ctx, source, now); err != nil {
			t.Fatalf("Schedule(%s) error = %v", source, err)
		}
	}

	_, stats, err := queue.ClaimWithStats(ctx, "seeker")
	if err != nil {
		t.Fatalf("ClaimWithStats() error = %v", err)
	}
	if stats.Outcome != ClaimOutcomeDeferred {
		t.Fatalf("outcome = %s, want deferred (stats=%#v)", stats.Outcome, stats)
	}
	if stats.BusySkips == 0 {
		t.Fatal("expected busy skips for deferred-only busy queue")
	}
}

// TestClaimDistinguishesLockedRowFromIneligibleCandidate verifies diagnostic
// counters separate row-lock contention from eligibility loss after listing.
func TestClaimDistinguishesLockedRowFromIneligibleCandidate(t *testing.T) {
	ctx := context.Background()
	pool := newSerialStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     5 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		now,
	)

	lockedOrigin := mustStoreOrigin(t, "https://locked.rowlock.test")
	if err := queue.Schedule(ctx, lockedOrigin, now); err != nil {
		t.Fatalf("Schedule(locked) error = %v", err)
	}

	holdTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin hold tx: %v", err)
	}
	defer func() { _ = holdTx.Rollback(ctx) }()

	var held string
	if err := holdTx.QueryRow(
		ctx,
		`
			SELECT origin
			FROM verification_queue
			WHERE origin = $1
			FOR UPDATE
		`,
		lockedOrigin.String(),
	).Scan(&held); err != nil {
		t.Fatalf("hold FOR UPDATE: %v", err)
	}

	_, lockedStats, err := queue.ClaimWithStats(ctx, "locker-seeker")
	if err != nil {
		t.Fatalf("ClaimWithStats(locked) error = %v", err)
	}
	if lockedStats.Outcome != ClaimOutcomeDeferred {
		t.Fatalf(
			"locked outcome = %s, want deferred (stats=%#v)",
			lockedStats.Outcome,
			lockedStats,
		)
	}
	if lockedStats.LockedSkips == 0 {
		t.Fatalf(
			"locked skips = %d, want > 0 (stats=%#v)",
			lockedStats.LockedSkips,
			lockedStats,
		)
	}
	if lockedStats.IneligibleSkips != 0 {
		t.Fatalf(
			"ineligible skips = %d, want 0 while row remains eligible",
			lockedStats.IneligibleSkips,
		)
	}

	if err := holdTx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback hold tx: %v", err)
	}

	if _, err := pool.Exec(
		ctx,
		`
			UPDATE verification_queue
			SET available_at = $2
			WHERE origin = $1
		`,
		lockedOrigin.String(),
		now.Add(time.Hour),
	); err != nil {
		t.Fatalf("make ineligible: %v", err)
	}

	_, ineligibleStats, err := queue.ClaimWithStats(ctx, "ineligible-seeker")
	if err != nil {
		t.Fatalf("ClaimWithStats(ineligible) error = %v", err)
	}
	if ineligibleStats.Outcome != ClaimOutcomeNoWork {
		t.Fatalf(
			"ineligible outcome = %s, want no_work (stats=%#v)",
			ineligibleStats.Outcome,
			ineligibleStats,
		)
	}
	if ineligibleStats.LockedSkips != 0 {
		t.Fatalf(
			"locked skips = %d after eligibility loss, want 0",
			ineligibleStats.LockedSkips,
		)
	}
}

// TestConcurrentVerificationAndDiscoverySameNeighborhood proves the
// advisory-before-row protocol does not deadlock when verification and
// discovery contend for one neighborhood.
func TestConcurrentVerificationAndDiscoverySameNeighborhood(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newSerialStoreTestPool(t)
	now := queueTestTime()
	leaseDuration := 5 * time.Minute

	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     leaseDuration,
			MinOriginInterval: time.Minute,
		},
		now,
	)
	verifyOrigin := mustStoreOrigin(t, "https://host-a.mixed-contention.test")
	if err := queue.Schedule(ctx, verifyOrigin, now); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	_ = seedDiscoveryTestSource(
		t,
		pool,
		"https://host-b.mixed-contention.test",
		now,
	)
	discoveryStore := newDiscoveryTestStore(t, pool, now)

	type result struct {
		kind  string
		found bool
		err   error
	}
	results := make([]result, 2)
	var started sync.WaitGroup
	var release sync.WaitGroup
	started.Add(2)
	release.Add(1)
	var join sync.WaitGroup
	join.Add(2)

	go func() {
		defer join.Done()
		started.Done()
		release.Wait()
		_, found, err := queue.Claim(ctx, "verify-worker")
		results[0] = result{kind: "verify", found: found, err: err}
	}()
	go func() {
		defer join.Done()
		started.Done()
		release.Wait()
		_, found, err := discoveryStore.ClaimDiscoverySourceLease(
			ctx,
			"discovery-worker",
			168*time.Hour,
			leaseDuration,
		)
		results[1] = result{kind: "discovery", found: found, err: err}
	}()
	started.Wait()
	release.Done()
	join.Wait()

	foundCount := 0
	for _, item := range results {
		if item.err != nil {
			t.Fatalf("%s claim error = %v", item.kind, item.err)
		}
		if item.found {
			foundCount++
		}
	}
	if foundCount != 1 {
		t.Fatalf("successful claims = %d, want 1", foundCount)
	}

	var live int
	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM domain_inflight_leases
			WHERE lease_expires_at > $1
		`,
		now,
	).Scan(&live); err != nil {
		t.Fatalf("count live domain leases: %v", err)
	}
	if live != 1 {
		t.Fatalf("live domain leases = %d, want 1", live)
	}
}

// TestRenewBlocksExpiredTakeoverUntilCommit ensures a concurrent claim cannot
// take over a neighborhood while the holder is renewing.
func TestRenewBlocksExpiredTakeoverUntilCommit(t *testing.T) {
	ctx := context.Background()
	pool := newSerialStoreTestPool(t)
	start := queueTestTime()
	leaseDuration := 5 * time.Minute

	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     leaseDuration,
			MinOriginInterval: time.Minute,
		},
		start,
	)
	source := mustStoreOrigin(t, "https://renew-race.example.com")
	if err := queue.Schedule(ctx, source, start); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	lease, found, err := queue.Claim(ctx, "owner")
	if err != nil || !found {
		t.Fatalf("Claim() = %#v, %t, %v", lease, found, err)
	}

	sibling := mustStoreOrigin(t, "https://renew-race-sibling.example.com")
	if err := queue.Schedule(ctx, sibling, start); err != nil {
		t.Fatalf("Schedule(sibling) error = %v", err)
	}

	renewQueue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     leaseDuration,
			MinOriginInterval: time.Minute,
		},
		start.Add(2*time.Minute),
	)

	var renewErr error
	var claimFound bool
	var claimErr error
	var started sync.WaitGroup
	var join sync.WaitGroup
	started.Add(2)
	join.Add(2)

	go func() {
		defer join.Done()
		started.Done()
		started.Wait()
		_, renewErr = renewQueue.Renew(ctx, lease)
	}()
	go func() {
		defer join.Done()
		started.Done()
		started.Wait()
		_, claimFound, claimErr = queue.Claim(ctx, "taker")
	}()
	join.Wait()

	if renewErr != nil {
		t.Fatalf("Renew() error = %v", renewErr)
	}
	if claimErr != nil {
		t.Fatalf("concurrent Claim() error = %v", claimErr)
	}
	if claimFound {
		t.Fatal("concurrent Claim() took neighborhood during renew")
	}
}

// TestStaleGenerationCannotReleaseNewerLease fences abandon/release against
// a superseded generation.
func TestStaleGenerationCannotReleaseNewerLease(t *testing.T) {
	ctx := context.Background()
	pool := newSerialStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     time.Minute,
			MinOriginInterval: time.Second,
		},
		now,
	)
	source := mustStoreOrigin(t, "https://fence.example.com")
	if err := queue.Schedule(ctx, source, now); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	stale, found, err := queue.Claim(ctx, "stale-owner")
	if err != nil || !found {
		t.Fatalf("Claim(stale) = %#v, %t, %v", stale, found, err)
	}

	// Expire durable + queue lease, then reclaim with a new generation.
	if _, err := pool.Exec(
		ctx,
		`
			UPDATE verification_queue
			SET last_claimed_at = $2,
				lease_expires_at = $3
			WHERE origin = $1
		`,
		source.String(),
		now.Add(-2*time.Minute),
		now.Add(-time.Minute),
	); err != nil {
		t.Fatalf("expire queue lease: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		`
			UPDATE domain_inflight_leases
			SET claimed_at = $2,
				lease_expires_at = $3
			WHERE holder_origin = $1
		`,
		source.String(),
		now.Add(-2*time.Minute),
		now.Add(-time.Minute),
	); err != nil {
		t.Fatalf("expire domain lease: %v", err)
	}

	newerQueue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     5 * time.Minute,
			MinOriginInterval: time.Second,
		},
		now,
	)
	newer, found, err := newerQueue.Claim(ctx, "new-owner")
	if err != nil || !found {
		t.Fatalf("Claim(new) = %#v, %t, %v", newer, found, err)
	}
	if newer.Generation <= stale.Generation {
		t.Fatalf(
			"new generation %d, want > %d",
			newer.Generation,
			stale.Generation,
		)
	}

	if err := queue.AbandonVerification(ctx, stale); err == nil {
		t.Fatal("AbandonVerification(stale) error = nil, want lost")
	}

	var liveOwner string
	var liveGen int64
	if err := pool.QueryRow(
		ctx,
		`
			SELECT lease_owner, lease_generation
			FROM domain_inflight_leases
			WHERE domain_key = $1
		`,
		domainInflightKey(source),
	).Scan(&liveOwner, &liveGen); err != nil {
		t.Fatalf("read domain lease: %v", err)
	}
	if liveOwner != "new-owner" || liveGen != newer.Generation {
		t.Fatalf(
			"domain lease = %s/%d, want new-owner/%d",
			liveOwner,
			liveGen,
			newer.Generation,
		)
	}
}

// TestAcquireLockTimeoutDoesNotLeakToLaterStatements verifies acquisition
// restores lock_timeout before subsequent work in the same transaction.
func TestAcquireLockTimeoutDoesNotLeakToLaterStatements(t *testing.T) {
	ctx := context.Background()
	pool := newSerialStoreTestPool(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	source := mustStoreOrigin(t, "https://timeout-scope.example.com")
	now := queueTestTime()
	written, err := writeDomainInflightLease(
		ctx,
		tx,
		domainInflightKey(source),
		source,
		"timeout-worker",
		1,
		now,
		now.Add(5*time.Minute),
	)
	if err != nil {
		t.Fatalf("writeDomainInflightLease() error = %v", err)
	}
	if !written {
		t.Fatal("writeDomainInflightLease() written = false")
	}

	var lockTimeout string
	if err := tx.QueryRow(ctx, "SHOW lock_timeout").Scan(&lockTimeout); err != nil {
		t.Fatalf("SHOW lock_timeout: %v", err)
	}
	if lockTimeout != "0" && lockTimeout != "0ms" {
		t.Fatalf("lock_timeout after acquire = %q, want 0", lockTimeout)
	}
}

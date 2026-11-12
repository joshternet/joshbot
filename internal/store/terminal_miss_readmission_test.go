package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/discovery"
)

/**
 * TestRobotsDeniedTerminalMissDoesNotReadmitFromStaleEvidence verifies that a
 * robots-denied first probe removes queue work and that pending-drain admission
 * will not recreate a probe from discovery evidence that is not newer than the
 * observation.
 */
func TestRobotsDeniedTerminalMissDoesNotReadmitFromStaleEvidence(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	fixture := newProbeCompletionFixtureInPool(t, pool)
	completedAt := fixture.lease.ClaimedAt.Add(time.Minute)
	fixture.queue.clock = fixedQueueClock{now: completedAt}

	if err := fixture.queue.CompleteVerification(
		ctx,
		fixture.lease,
		declaration.Result{
			Outcome: declaration.OutcomeRobotsDenied,
			Origin:  fixture.source,
		},
		24*time.Hour,
	); err != nil {
		t.Fatalf("CompleteVerification() error = %v", err)
	}

	assertQueueRowAbsent(t, fixture)

	store, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{
			Enabled:          false,
			MaxPendingProbes: 10,
		},
	)
	if err != nil {
		t.Fatalf("NewDiscoveryStoreWithAutomaticCrawling() error = %v", err)
	}

	seed := mustStoreOrigin(t, "https://seed-robots.example")
	discoveredAt := completedAt.Add(-time.Minute)
	if _, err := pool.Exec(ctx, `
		INSERT INTO origins (origin, first_observed_at)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, seed.String(), discoveredAt); err != nil {
		t.Fatalf("insert seed origin: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_source_state (source_origin, seeded)
		VALUES ($1, true)
		ON CONFLICT DO NOTHING
	`, seed.String()); err != nil {
		t.Fatalf("insert source state: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_candidates (
			origin,
			first_discovered_at,
			last_discovered_at
		)
		VALUES ($1, $2, $2)
		ON CONFLICT (origin) DO UPDATE
		SET last_discovered_at = EXCLUDED.last_discovered_at
	`, fixture.source.String(), discoveredAt); err != nil {
		t.Fatalf("insert candidate: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_edges (
			source_origin,
			candidate_origin,
			kind,
			first_discovered_at,
			last_discovered_at
		)
		VALUES ($1, $2, 'link', $3, $3)
		ON CONFLICT DO NOTHING
	`, seed.String(), fixture.source.String(), discoveredAt); err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	scheduled, err := store.SchedulePendingDiscoveryProbes(ctx)
	if err != nil {
		t.Fatalf(
			"SchedulePendingDiscoveryProbes() error = %v",
			err,
		)
	}
	if scheduled != 0 {
		t.Fatalf(
			"SchedulePendingDiscoveryProbes() scheduled = %d, want 0",
			scheduled,
		)
	}
}

/**
 * TestInFlightRediscoveryIsCoveredByTerminalCompletion verifies that discovery
 * recorded while a probe lease is held does not qualify for readmission after
 * that lease completes with a terminal miss, because last_discovered_at is not
 * strictly newer than the completion observation. Discovery after completion
 * remains eligible.
 */
func TestInFlightRediscoveryIsCoveredByTerminalCompletion(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	claimedAt := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		claimedAt,
	)
	source := mustStoreOrigin(t, "https://inflight.example")
	seed := mustStoreOrigin(t, "https://seed-inflight.example")

	if _, err := pool.Exec(ctx, `
		INSERT INTO origins (origin, first_observed_at)
		VALUES ($1, $2), ($3, $2)
	`, seed.String(), claimedAt, source.String()); err != nil {
		t.Fatalf("insert origins: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_source_state (
			source_origin,
			seeded,
			automatically_discovered
		)
		VALUES ($1, true, false)
	`, seed.String()); err != nil {
		t.Fatalf("insert seed state: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO verification_queue (origin, available_at, mode)
		VALUES ($1, $2, 'probe')
	`, source.String(), claimedAt); err != nil {
		t.Fatalf("insert probe: %v", err)
	}

	lease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil || !found {
		t.Fatalf("Claim() = %#v, %t, %v", lease, found, err)
	}

	discoveryAt := claimedAt.Add(2 * time.Second)
	discoveryStore := newDiscoveryTestStore(t, pool, discoveryAt)
	if _, err := discoveryStore.RecordDiscovery(
		ctx,
		seed,
		[]discovery.Candidate{{
			Origin: source,
			Kind:   discovery.KindLink,
		}},
	); err != nil {
		t.Fatalf("RecordDiscovery() error = %v", err)
	}

	completedAt := claimedAt.Add(5 * time.Second)
	queue.clock = fixedQueueClock{now: completedAt}
	if err := queue.CompleteVerification(
		ctx,
		lease,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  source,
		},
		24*time.Hour,
	); err != nil {
		t.Fatalf("CompleteVerification() error = %v", err)
	}

	var queueCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM verification_queue
		WHERE origin = $1
	`, source.String()).Scan(&queueCount); err != nil {
		t.Fatalf("count queue: %v", err)
	}
	if queueCount != 0 {
		t.Fatalf("queue count after terminal miss = %d, want 0", queueCount)
	}

	scheduled, err := discoveryStore.SchedulePendingDiscoveryProbes(ctx)
	if err != nil {
		t.Fatalf(
			"SchedulePendingDiscoveryProbes() error = %v",
			err,
		)
	}
	if scheduled != 0 {
		t.Fatalf(
			"in-flight rediscovery scheduled = %d, want 0",
			scheduled,
		)
	}

	postCompletionAt := completedAt.Add(time.Minute)
	postStore := newDiscoveryTestStore(t, pool, postCompletionAt)
	postStore.automatic = AutomaticCrawlConfig{
		Enabled:          false,
		MaxPendingProbes: 10,
	}
	if _, err := postStore.RecordDiscovery(
		ctx,
		seed,
		[]discovery.Candidate{{
			Origin: source,
			Kind:   discovery.KindLink,
		}},
	); err != nil {
		t.Fatalf("post-completion RecordDiscovery() error = %v", err)
	}

	var postQueued bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM verification_queue WHERE origin = $1
		)
	`, source.String()).Scan(&postQueued); err != nil {
		t.Fatalf("check post-completion queue: %v", err)
	}
	if !postQueued {
		t.Fatal("post-completion rediscovery did not schedule a probe")
	}
}

/**
 * TestOperatorScheduleSurvivesTerminalCompletionRace verifies that an operator
 * Schedule to recurring after a terminal probe completion remains queued, and
 * that a stale lease cannot complete after losing authority.
 */
func TestOperatorScheduleSurvivesTerminalCompletionRace(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	fixture := newProbeCompletionFixtureInPool(t, pool)
	completedAt := fixture.lease.ClaimedAt.Add(time.Minute)
	fixture.queue.clock = fixedQueueClock{now: completedAt}

	if err := fixture.queue.CompleteVerification(
		ctx,
		fixture.lease,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  fixture.source,
		},
		24*time.Hour,
	); err != nil {
		t.Fatalf("CompleteVerification() error = %v", err)
	}

	scheduleAt := completedAt.Add(time.Second)
	if err := fixture.queue.Schedule(
		ctx,
		fixture.source,
		scheduleAt,
	); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	assertQueueMode(
		t,
		ctx,
		pool,
		fixture.source.String(),
		"recurring",
	)

	err := fixture.queue.CompleteVerification(
		ctx,
		fixture.lease,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  fixture.source,
		},
		24*time.Hour,
	)
	if err != ErrLeaseLost {
		t.Fatalf(
			"stale CompleteVerification() error = %v, want ErrLeaseLost",
			err,
		)
	}

	assertQueueMode(
		t,
		ctx,
		pool,
		fixture.source.String(),
		"recurring",
	)
}

/**
 * TestExpiredLegacyReprobeCleanupReleasesDomainInflight verifies manual cleanup
 * may retire expired-lease reprobe rows and that domain-inflight state does not
 * remain held for the deleted origin, while active leases stay protected.
 */
func TestExpiredLegacyReprobeCleanupReleasesDomainInflight(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	source := mustStoreOrigin(t, "https://expired-reprobe.example")

	if _, err := pool.Exec(ctx, `
		INSERT INTO verification_queue (
			origin,
			available_at,
			mode,
			lease_owner,
			lease_generation,
			lease_expires_at,
			last_claimed_at
		)
		VALUES ($1, $2, 'reprobe', 'worker-a', 1, $3, $2)
	`, source.String(), now.Add(-2*time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatalf("insert expired reprobe: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO domain_inflight_leases (
			domain_key,
			holder_origin,
			lease_owner,
			lease_generation,
			claimed_at,
			lease_expires_at
		)
		VALUES ($1, $2, 'worker-a', 1, $3, $4)
	`, domainInflightKey(source), source.String(), now.Add(-2*time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatalf("insert domain inflight: %v", err)
	}

	if _, err := pool.Exec(ctx, legacyReprobeCleanupSQL, now); err != nil {
		t.Fatalf("cleanup exec error = %v", err)
	}

	var queueCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM verification_queue WHERE origin = $1
	`, source.String()).Scan(&queueCount); err != nil {
		t.Fatalf("count queue: %v", err)
	}
	if queueCount != 0 {
		t.Fatalf("queue count = %d, want 0", queueCount)
	}

	var inflightCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM domain_inflight_leases
		WHERE holder_origin = $1
	`, source.String()).Scan(&inflightCount); err != nil {
		t.Fatalf("count domain inflight: %v", err)
	}
	if inflightCount != 0 {
		t.Fatalf(
			"domain inflight count = %d, want 0 after expired cleanup",
			inflightCount,
		)
	}

	active := mustStoreOrigin(t, "https://active-reprobe.example")
	if _, err := pool.Exec(ctx, `
		INSERT INTO verification_queue (
			origin,
			available_at,
			mode,
			lease_owner,
			lease_generation,
			lease_expires_at,
			last_claimed_at
		)
		VALUES ($1, $2, 'reprobe', 'worker-b', 2, $3, $2)
	`, active.String(), now, now.Add(time.Minute)); err != nil {
		t.Fatalf("insert active reprobe: %v", err)
	}
	if _, err := pool.Exec(ctx, legacyReprobeCleanupSQL, now); err != nil {
		t.Fatalf("second cleanup exec error = %v", err)
	}

	var activeCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM verification_queue WHERE origin = $1
	`, active.String()).Scan(&activeCount); err != nil {
		t.Fatalf("count active queue: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("active reprobe count = %d, want 1", activeCount)
	}
}

// legacyReprobeCleanupSQL deletes unleased or expired legacy reprobe rows and
// clears matching domain-inflight holders. Kept next to tests and the runbook.
const legacyReprobeCleanupSQL = `
	WITH deleted AS (
		DELETE FROM verification_queue AS queued
		WHERE queued.mode = 'reprobe'
			AND (
				queued.lease_owner IS NULL
				OR (
					queued.lease_expires_at IS NOT NULL
					AND queued.lease_expires_at <= $1::timestamptz
				)
			)
		RETURNING
			queued.origin,
			queued.lease_generation
	)
	DELETE FROM domain_inflight_leases AS inflight
	USING deleted
	WHERE inflight.holder_origin = deleted.origin
		AND inflight.lease_generation = deleted.lease_generation
`

/**
 * newProbeCompletionFixtureInPool builds a probe-mode completion fixture in an
 * existing pool so related discovery assertions can share the database.
 *
 * @param t - test harness
 * @param pool - shared PostgreSQL pool
 * @returns probe completion fixture
 */
func newProbeCompletionFixtureInPool(
	t *testing.T,
	pool *pgxpool.Pool,
) completionFixture {
	t.Helper()

	fixture := newCompletionFixtureInPool(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)

	_, err := fixture.pool.Exec(
		fixture.ctx,
		`
			UPDATE verification_queue
			SET mode = 'probe'
			WHERE origin = $1
		`,
		fixture.source.String(),
	)
	if err != nil {
		t.Fatalf("set queue mode to probe: %v", err)
	}

	return fixture
}

package store

import (
	"context"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/retry"
)

func TestUnsupportedOriginProbeRemovesAndSuppresses(
	t *testing.T,
) {
	fixture := newProbeCompletionFixture(t)
	completedAt := fixture.lease.ClaimedAt.Add(time.Minute)
	fixture.queue.clock = fixedQueueClock{now: completedAt}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		declaration.Result{
			Outcome: declaration.OutcomeUnavailable,
			Origin:  fixture.source,
			FailureCategory: retry.
				CategoryUnsupportedOrigin,
		},
		24*time.Hour,
	); err != nil {
		t.Fatalf(
			"CompleteVerification() error = %v",
			err,
		)
	}

	var queueCount int
	if err := fixture.pool.QueryRow(
		fixture.ctx,
		`
			SELECT count(*)
			FROM verification_queue
			WHERE origin = $1
		`,
		fixture.source.String(),
	).Scan(&queueCount); err != nil {
		t.Fatalf("count queue rows: %v", err)
	}
	if queueCount != 0 {
		t.Fatalf(
			"queue row count = %d, want 0",
			queueCount,
		)
	}

	var (
		failureCategory *string
		nextAttemptAt   *time.Time
		failures        int
	)
	if err := fixture.pool.QueryRow(
		fixture.ctx,
		`
			SELECT
				last_failure_category,
				next_attempt_at,
				consecutive_failures
			FROM discovery_candidates
			WHERE origin = $1
		`,
		fixture.source.String(),
	).Scan(
		&failureCategory,
		&nextAttemptAt,
		&failures,
	); err != nil {
		t.Fatalf("query candidate suppression: %v", err)
	}
	if failureCategory == nil ||
		*failureCategory != string(
			retry.CategoryUnsupportedOrigin,
		) {
		t.Fatalf(
			"candidate failure_category = %v, want %q",
			failureCategory,
			retry.CategoryUnsupportedOrigin,
		)
	}
	if failures < 1 {
		t.Fatalf(
			"candidate consecutive_failures = %d, want >= 1",
			failures,
		)
	}
	wantSuppressUntil := completedAt.Add(
		probeFailureSuppressionInterval,
	)
	if nextAttemptAt == nil ||
		!nextAttemptAt.Equal(wantSuppressUntil) {
		t.Fatalf(
			"candidate next_attempt_at = %v, want %v",
			nextAttemptAt,
			wantSuppressUntil,
		)
	}

	var observationCategory *string
	if err := fixture.pool.QueryRow(
		fixture.ctx,
		`
			SELECT failure_category
			FROM verification_observations
			WHERE origin = $1
		`,
		fixture.source.String(),
	).Scan(&observationCategory); err != nil {
		t.Fatalf("query observation category: %v", err)
	}
	if observationCategory == nil ||
		*observationCategory != string(
			retry.CategoryUnsupportedOrigin,
		) {
		t.Fatalf(
			"observation failure_category = %v, want %q",
			observationCategory,
			retry.CategoryUnsupportedOrigin,
		)
	}
}

func TestSuppressedProbeIsNotReadmittedUntilCooldown(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	source := mustStoreOrigin(t, "https://source.example")
	friend := mustStoreOrigin(t, "https://friend.example")

	store, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{
			Enabled:          false,
			MaxPendingProbes: 10,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordDiscovery(
		ctx,
		source,
		[]discovery.Candidate{{
			Origin: friend,
			Kind:   discovery.KindLink,
		}},
	); err != nil {
		t.Fatalf("RecordDiscovery() error = %v", err)
	}

	queue, err := NewQueue(
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	lease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if !found {
		t.Fatal("Claim() found = false, want true")
	}
	if lease.Origin != friend {
		t.Fatalf(
			"claimed origin = %s, want %s",
			lease.Origin,
			friend,
		)
	}

	if err := queue.CompleteVerification(
		ctx,
		lease,
		declaration.Result{
			Outcome: declaration.OutcomeUnavailable,
			Origin:  friend,
			FailureCategory: retry.
				CategoryUnsupportedOrigin,
		},
		24*time.Hour,
	); err != nil {
		t.Fatalf(
			"CompleteVerification() error = %v",
			err,
		)
	}

	scheduled, err := store.SchedulePendingDiscoveryProbes(ctx)
	if err != nil {
		t.Fatalf(
			"SchedulePendingDiscoveryProbes() during suppression error = %v",
			err,
		)
	}
	if scheduled != 0 {
		t.Fatalf(
			"SchedulePendingDiscoveryProbes() during suppression scheduled = %d, want 0",
			scheduled,
		)
	}

	if _, err := store.RecordDiscovery(
		ctx,
		source,
		[]discovery.Candidate{{
			Origin: friend,
			Kind:   discovery.KindLink,
		}},
	); err != nil {
		t.Fatalf(
			"RecordDiscovery() during suppression error = %v",
			err,
		)
	}

	var queuedDuring bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM verification_queue
			WHERE origin = $1 AND mode = 'probe'
		)
	`, friend.String()).Scan(&queuedDuring); err != nil {
		t.Fatal(err)
	}
	if queuedDuring {
		t.Fatal(
			"friend was re-queued while probe suppression was active",
		)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE discovery_candidates
		SET next_attempt_at = statement_timestamp() - interval '1 second'
		WHERE origin = $1
	`, friend.String()); err != nil {
		t.Fatalf("expire candidate suppression: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE verification_observations
		SET observed_at = statement_timestamp() - interval '25 hours'
		WHERE origin = $1
	`, friend.String()); err != nil {
		t.Fatalf("age observation: %v", err)
	}
	// Keep discovery evidence newer than the aged observation so the drain
	// path treats the origin as eligible again after the cooldown.
	if _, err := pool.Exec(ctx, `
		UPDATE discovery_candidates
		SET last_discovered_at = statement_timestamp()
		WHERE origin = $1
	`, friend.String()); err != nil {
		t.Fatalf("refresh discovery evidence: %v", err)
	}

	scheduled, err = store.SchedulePendingDiscoveryProbes(ctx)
	if err != nil {
		t.Fatalf(
			"SchedulePendingDiscoveryProbes() after cooldown error = %v",
			err,
		)
	}
	if scheduled != 1 {
		t.Fatalf(
			"SchedulePendingDiscoveryProbes() after cooldown scheduled = %d, want 1",
			scheduled,
		)
	}
}

func TestTransientUnavailableProbeStillRetained(
	t *testing.T,
) {
	tests := []struct {
		name     string
		category retry.Category
	}{
		{
			name:     "declaration_unavailable",
			category: retry.CategoryDeclarationUnavailable,
		},
		{
			name:     "http_429",
			category: retry.CategoryHTTP429,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProbeCompletionFixture(t)
			completedAt := fixture.lease.ClaimedAt.Add(
				time.Minute,
			)
			fixture.queue.clock = fixedQueueClock{
				now: completedAt,
			}

			if err := fixture.queue.CompleteVerification(
				fixture.ctx,
				fixture.lease,
				declaration.Result{
					Outcome: declaration.
						OutcomeUnavailable,
					Origin:          fixture.source,
					FailureCategory: test.category,
				},
				24*time.Hour,
			); err != nil {
				t.Fatalf(
					"CompleteVerification() error = %v",
					err,
				)
			}

			assertQueueMode(
				t,
				fixture.ctx,
				fixture.pool,
				fixture.source.String(),
				"probe",
			)

			state := readCompletionQueueState(t, fixture)
			if state.consecutiveFailures != 1 ||
				state.lastFailureCategory == nil ||
				*state.lastFailureCategory !=
					string(test.category) ||
				state.nextAttemptAt == nil {
				t.Fatalf(
					"transient retry state = %#v",
					state,
				)
			}

			var observationCategory *string
			if err := fixture.pool.QueryRow(
				fixture.ctx,
				`
					SELECT failure_category
					FROM verification_observations
					WHERE origin = $1
				`,
				fixture.source.String(),
			).Scan(&observationCategory); err != nil {
				t.Fatalf(
					"query observation category: %v",
					err,
				)
			}
			if observationCategory == nil ||
				*observationCategory != string(
					test.category,
				) {
				t.Fatalf(
					"observation failure_category = %v, want %q",
					observationCategory,
					test.category,
				)
			}
		})
	}
}

func TestSchedulePendingDiscoveryProbesSkipsNonTransientUnavailable(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	source := mustStoreOrigin(t, "https://source.example")
	blocked := mustStoreOrigin(
		t,
		"https://blocked-probe.example",
	)
	open := mustStoreOrigin(t, "https://open-probe.example")

	store, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{
			Enabled:          false,
			MaxPendingProbes: 10,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordDiscovery(
		ctx,
		source,
		[]discovery.Candidate{
			{Origin: blocked, Kind: discovery.KindLink},
			{Origin: open, Kind: discovery.KindLink},
		},
	); err != nil {
		t.Fatalf("RecordDiscovery() error = %v", err)
	}

	if _, err := pool.Exec(ctx, `
		DELETE FROM verification_queue
		WHERE origin IN ($1, $2)
	`, blocked.String(), open.String()); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO origins (origin, first_observed_at)
		VALUES ($1, statement_timestamp())
		ON CONFLICT (origin) DO NOTHING
	`, blocked.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO verification_observations (
			origin,
			observed_at,
			outcome,
			failure_category
		)
		SELECT
			$1,
			last_discovered_at,
			'unavailable',
			'unsupported_origin'
		FROM discovery_candidates
		WHERE origin = $1
	`, blocked.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE discovery_candidates
		SET
			consecutive_failures = 1,
			last_failure_category = 'unsupported_origin',
			next_attempt_at = statement_timestamp() + interval '12 hours'
		WHERE origin = $1
	`, blocked.String()); err != nil {
		t.Fatal(err)
	}

	scheduled, err := store.SchedulePendingDiscoveryProbes(ctx)
	if err != nil {
		t.Fatalf(
			"SchedulePendingDiscoveryProbes() error = %v",
			err,
		)
	}
	if scheduled != 1 {
		t.Fatalf(
			"SchedulePendingDiscoveryProbes() scheduled = %d, want 1",
			scheduled,
		)
	}

	var blockedQueued, openQueued bool
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM verification_queue WHERE origin = $1
			),
			EXISTS (
				SELECT 1 FROM verification_queue WHERE origin = $2
			)
	`, blocked.String(), open.String()).Scan(
		&blockedQueued,
		&openQueued,
	); err != nil {
		t.Fatal(err)
	}
	if blockedQueued {
		t.Fatal(
			"non-transient unavailable origin was re-queued",
		)
	}
	if !openQueued {
		t.Fatal("open origin was not drained into the probe queue")
	}
}

func TestAutomaticAdmissionResetPreservesProbeSuppression(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := newAutomaticAdmissionStore(t, pool, 1, 10)
	source := mustStoreOrigin(t, "https://source.example")
	friend := mustStoreOrigin(t, "https://friend.example")

	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	runID := beginAutomaticAdmissionRun(t, store, source, 1)
	if _, err := store.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{{
			Origin: friend,
			Kind:   discovery.KindLink,
		}},
	); err != nil {
		t.Fatalf("RecordDiscoveryForRun() error = %v", err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM verification_queue WHERE origin = $1
	`, friend.String()); err != nil {
		t.Fatal(err)
	}

	pending, err := store.PendingAutomaticCandidates(ctx, runID)
	if err != nil {
		t.Fatalf("PendingAutomaticCandidates() error = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf(
			"pending candidates = %d, want 1",
			len(pending),
		)
	}

	suppressUntil := time.Now().UTC().Add(12 * time.Hour).
		Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `
		UPDATE discovery_candidates
		SET
			consecutive_failures = 1,
			last_failure_category = 'unsupported_origin',
			next_attempt_at = $2
		WHERE origin = $1
	`, friend.String(), suppressUntil); err != nil {
		t.Fatalf("restore probe suppression: %v", err)
	}

	if err := store.AdmitAutomaticCandidates(
		ctx,
		runID,
		pending,
	); err != nil {
		t.Fatalf("AdmitAutomaticCandidates() error = %v", err)
	}

	var (
		category *string
		nextAt   *time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT last_failure_category, next_attempt_at
		FROM discovery_candidates
		WHERE origin = $1
	`, friend.String()).Scan(&category, &nextAt); err != nil {
		t.Fatal(err)
	}
	if category == nil ||
		*category != string(retry.CategoryUnsupportedOrigin) {
		t.Fatalf(
			"last_failure_category = %v, want unsupported_origin",
			category,
		)
	}
	if nextAt == nil || !nextAt.Equal(suppressUntil) {
		t.Fatalf(
			"next_attempt_at = %v, want %v",
			nextAt,
			suppressUntil,
		)
	}

	var outcome string
	if err := pool.QueryRow(ctx, `
		SELECT outcome
		FROM crawl_run_automatic_admission_batches
		WHERE admission_run_id = $1 AND candidate_origin = $2
	`, int64(runID), friend.String()).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if outcome != "policy_deferred" {
		t.Fatalf(
			"admission outcome = %q, want policy_deferred",
			outcome,
		)
	}
}

func TestObservationFailureCategoryMigration(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	var nullable bool
	if err := pool.QueryRow(ctx, `
		SELECT is_nullable = 'YES'
		FROM information_schema.columns
		WHERE table_name = 'verification_observations'
			AND column_name = 'failure_category'
	`).Scan(&nullable); err != nil {
		t.Fatalf("query failure_category column: %v", err)
	}
	if !nullable {
		t.Fatal("failure_category should be nullable")
	}

	source := mustStoreOrigin(t, "https://category.example")
	if _, err := pool.Exec(ctx, `
		INSERT INTO origins (origin, first_observed_at)
		VALUES ($1, statement_timestamp())
		ON CONFLICT (origin) DO NOTHING
	`, source.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO verification_observations (
			origin,
			observed_at,
			outcome,
			failure_category
		)
		VALUES (
			$1,
			statement_timestamp(),
			'unavailable',
			'unsupported_origin'
		)
	`, source.String()); err != nil {
		t.Fatalf(
			"insert observation with failure_category: %v",
			err,
		)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO verification_observations (
			origin,
			observed_at,
			outcome,
			failure_category
		)
		VALUES (
			$1,
			statement_timestamp(),
			'unavailable',
			'not_a_real_category'
		)
	`, source.String()); err == nil {
		t.Fatal(
			"invalid failure_category was accepted",
		)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_candidates (
			origin,
			first_discovered_at,
			last_discovered_at,
			consecutive_failures,
			last_failure_category,
			next_attempt_at
		)
		VALUES (
			$1,
			statement_timestamp(),
			statement_timestamp(),
			1,
			'unsupported_origin',
			statement_timestamp() + interval '1 hour'
		)
		ON CONFLICT (origin) DO UPDATE
		SET
			consecutive_failures = 1,
			last_failure_category = 'unsupported_origin',
			next_attempt_at = statement_timestamp() + interval '1 hour'
	`, source.String()); err != nil {
		t.Fatalf(
			"candidate non-transient suppression rejected: %v",
			err,
		)
	}
}

package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/discovery"
)

func TestDiscoveryScheduleTerminalMissLeavesAutomaticSourceKnown(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)

	completedAt := fixture.claimed.Add(time.Minute)
	fixture.queue.clock = fixedQueueClock{
		now: completedAt,
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		validCompletionResult(fixture.source),
		time.Hour,
	); err != nil {
		t.Fatalf(
			"CompleteVerification(valid) error = %v",
			err,
		)
	}

	if _, err := fixture.pool.Exec(
		fixture.ctx,
		`
			INSERT INTO discovery_source_state (
				source_origin,
				automatically_discovered
			)
			VALUES ($1, true)
			ON CONFLICT (source_origin) DO UPDATE
			SET automatically_discovered = true
		`,
		fixture.source.String(),
	); err != nil {
		t.Fatalf(
			"set automatic classification: %v",
			err,
		)
	}

	if !discoverySourceIsScheduled(
		t,
		fixture.ctx,
		fixture.pool,
		fixture.source.String(),
	) {
		t.Fatal(
			"valid source is not scheduled",
		)
	}

	fixture.queue.clock = fixedQueueClock{
		now: completedAt.Add(time.Hour),
	}

	lease, found, err := fixture.queue.Claim(
		fixture.ctx,
		"worker-b",
	)
	if err != nil {
		t.Fatalf(
			"Claim() error = %v",
			err,
		)
	}
	if !found {
		t.Fatal(
			"Claim() found = false, want true",
		)
	}

	fixture.queue.clock = fixedQueueClock{
		now: lease.ClaimedAt.Add(time.Minute),
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		lease,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  fixture.source,
		},
		time.Hour,
	); err != nil {
		t.Fatalf(
			"CompleteVerification(absent) error = %v",
			err,
		)
	}

	if discoverySourceIsScheduled(
		t,
		fixture.ctx,
		fixture.pool,
		fixture.source.String(),
	) {
		t.Fatal(
			"terminal automatic source remains scheduled",
		)
	}

	var automaticallyDiscovered bool
	if err := fixture.pool.QueryRow(
		fixture.ctx,
		`
			SELECT automatically_discovered
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		fixture.source.String(),
	).Scan(
		&automaticallyDiscovered,
	); err != nil {
		t.Fatalf(
			"query automatic classification: %v",
			err,
		)
	}

	if !automaticallyDiscovered {
		t.Fatal(
			"terminal miss removed durable automatic classification",
		)
	}

	rediscoverySource := mustStoreOrigin(
		t,
		"https://rediscovery.example",
	)

	discoveryStore := newDiscoveryTestStore(
		t,
		fixture.pool,
		lease.ClaimedAt.Add(2*time.Minute),
	)

	if err := discoveryStore.AddCrawlSeed(
		fixture.ctx,
		rediscoverySource,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v",
			err,
		)
	}

	if _, err := discoveryStore.RecordDiscovery(
		fixture.ctx,
		rediscoverySource,
		[]discovery.Candidate{
			{
				Origin: fixture.source,
				Kind:   discovery.KindLink,
			},
		},
	); err != nil {
		t.Fatalf(
			"RecordDiscovery() error = %v",
			err,
		)
	}

	if !discoverySourceIsScheduled(
		t,
		fixture.ctx,
		fixture.pool,
		fixture.source.String(),
	) {
		t.Fatal(
			"fresh rediscovery did not reactivate automatic source",
		)
	}
}

func TestDiscoveryScheduleTerminalMissPreservesSeed(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)

	completedAt := fixture.claimed.Add(time.Minute)
	fixture.queue.clock = fixedQueueClock{
		now: completedAt,
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		validCompletionResult(fixture.source),
		time.Hour,
	); err != nil {
		t.Fatalf(
			"CompleteVerification(valid) error = %v",
			err,
		)
	}

	discoveryStore, err := NewDiscoveryStore(
		fixture.pool,
	)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v",
			err,
		)
	}

	if err := discoveryStore.AddCrawlSeed(
		fixture.ctx,
		fixture.source,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v",
			err,
		)
	}

	fixture.queue.clock = fixedQueueClock{
		now: completedAt.Add(time.Hour),
	}

	lease, found, err := fixture.queue.Claim(
		fixture.ctx,
		"worker-b",
	)
	if err != nil {
		t.Fatalf(
			"Claim() error = %v",
			err,
		)
	}
	if !found {
		t.Fatal(
			"Claim() found = false, want true",
		)
	}

	fixture.queue.clock = fixedQueueClock{
		now: lease.ClaimedAt.Add(time.Minute),
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		lease,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  fixture.source,
		},
		time.Hour,
	); err != nil {
		t.Fatalf(
			"CompleteVerification(absent) error = %v",
			err,
		)
	}

	if !discoverySourceIsScheduled(
		t,
		fixture.ctx,
		fixture.pool,
		fixture.source.String(),
	) {
		t.Fatal(
			"terminal miss unscheduled explicit seed",
		)
	}
}

func TestDiscoveryScheduleFreshRediscoveryReactivatesFormerParticipant(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)

	validAt := fixture.claimed.Add(time.Minute)
	fixture.queue.clock = fixedQueueClock{
		now: validAt,
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		validCompletionResult(fixture.source),
		time.Hour,
	); err != nil {
		t.Fatalf(
			"CompleteVerification(valid) error = %v",
			err,
		)
	}

	fixture.queue.clock = fixedQueueClock{
		now: validAt.Add(time.Hour),
	}

	verificationLease, found, err := fixture.queue.Claim(
		fixture.ctx,
		"worker-b",
	)
	if err != nil {
		t.Fatalf(
			"Claim() error = %v",
			err,
		)
	}
	if !found {
		t.Fatal(
			"Claim() found = false, want true",
		)
	}

	terminalAt := verificationLease.ClaimedAt.Add(time.Minute)
	fixture.queue.clock = fixedQueueClock{
		now: terminalAt,
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		verificationLease,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  fixture.source,
		},
		time.Hour,
	); err != nil {
		t.Fatalf(
			"CompleteVerification(absent) error = %v",
			err,
		)
	}

	if discoverySourceIsScheduled(
		t,
		fixture.ctx,
		fixture.pool,
		fixture.source.String(),
	) {
		t.Fatal(
			"former participant remains scheduled after terminal miss",
		)
	}

	rediscoverySource := mustStoreOrigin(
		t,
		"https://rediscovery-source.example",
	)
	rediscoveredAt := terminalAt.Add(time.Minute)

	reactivationStore := newDiscoveryTestStore(
		t,
		fixture.pool,
		rediscoveredAt,
	)

	if err := reactivationStore.AddCrawlSeed(
		fixture.ctx,
		rediscoverySource,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v",
			err,
		)
	}

	if _, err := reactivationStore.RecordDiscovery(
		fixture.ctx,
		rediscoverySource,
		[]discovery.Candidate{
			{
				Origin: fixture.source,
				Kind:   discovery.KindLink,
			},
		},
	); err != nil {
		t.Fatalf(
			"RecordDiscovery() error = %v",
			err,
		)
	}

	if err := reactivationStore.RemoveCrawlSeed(
		fixture.ctx,
		rediscoverySource,
	); err != nil {
		t.Fatalf(
			"RemoveCrawlSeed() error = %v",
			err,
		)
	}

	if !discoverySourceIsScheduled(
		t,
		fixture.ctx,
		fixture.pool,
		fixture.source.String(),
	) {
		t.Fatal(
			"fresh rediscovery did not reactivate former participant",
		)
	}

	disabledStore := newDiscoveryTestStore(
		t,
		fixture.pool,
		rediscoveredAt.Add(time.Minute),
	)

	disabledLease, found, err :=
		disabledStore.ClaimDiscoverySourceLease(
			fixture.ctx,
			testDiscoveryLeaseOwner,
			time.Hour,
			time.Minute,
		)
	if err != nil {
		t.Fatalf(
			"disabled ClaimDiscoverySourceLease() error = %v",
			err,
		)
	}
	if found ||
		disabledLease != (discovery.CrawlSourceLease{}) {
		t.Fatalf(
			"disabled automatic claim = %#v, %v, want zero, false",
			disabledLease,
			found,
		)
	}

	enabledStore, err := newDiscoveryStoreWithConfig(
		fixture.pool,
		&discoveryTestClock{
			times: []time.Time{
				rediscoveredAt.Add(2 * time.Minute),
			},
		},
		AutomaticCrawlConfig{
			Enabled:          true,
			MaxPendingProbes: 10,
		},
	)
	if err != nil {
		t.Fatalf(
			"newDiscoveryStoreWithConfig() error = %v",
			err,
		)
	}

	discoveryLease, found, err :=
		enabledStore.ClaimDiscoverySourceLease(
			fixture.ctx,
			testDiscoveryLeaseOwner,
			time.Hour,
			time.Minute,
		)
	if err != nil {
		t.Fatalf(
			"enabled ClaimDiscoverySourceLease() error = %v",
			err,
		)
	}
	if !found {
		t.Fatal(
			"enabled ClaimDiscoverySourceLease() found = false, want true",
		)
	}
	if discoveryLease.Origin != fixture.source {
		t.Fatalf(
			"enabled ClaimDiscoverySourceLease() origin = %q, want %q",
			discoveryLease.Origin,
			fixture.source,
		)
	}

	var automaticallyDiscovered bool
	if err := fixture.pool.QueryRow(
		fixture.ctx,
		`
			SELECT automatically_discovered
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		fixture.source.String(),
	).Scan(
		&automaticallyDiscovered,
	); err != nil {
		t.Fatalf(
			"query source classification: %v",
			err,
		)
	}

	if automaticallyDiscovered {
		t.Fatal(
			"fresh rediscovery reclassified former participant as automatic",
		)
	}
}

func discoverySourceIsScheduled(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	rawOrigin string,
) bool {
	t.Helper()

	var scheduled bool
	if err := pool.QueryRow(
		ctx,
		`
			SELECT EXISTS (
				SELECT 1
				FROM discovery_source_schedule
				WHERE source_origin = $1
			)
		`,
		rawOrigin,
	).Scan(
		&scheduled,
	); err != nil {
		t.Fatalf(
			"query discovery schedule: %v",
			err,
		)
	}

	return scheduled
}

func TestDiscoveryScheduleNonNewerRediscoveryDoesNotReactivate(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)

	validAt := fixture.claimed.Add(time.Minute)
	fixture.queue.clock = fixedQueueClock{
		now: validAt,
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		validCompletionResult(fixture.source),
		time.Hour,
	); err != nil {
		t.Fatalf(
			"CompleteVerification(valid) error = %v",
			err,
		)
	}

	if _, err := fixture.pool.Exec(
		fixture.ctx,
		`
			INSERT INTO discovery_source_state (
				source_origin,
				automatically_discovered
			)
			VALUES ($1, true)
			ON CONFLICT (source_origin) DO UPDATE
			SET automatically_discovered = true
		`,
		fixture.source.String(),
	); err != nil {
		t.Fatalf(
			"set automatic classification: %v",
			err,
		)
	}

	fixture.queue.clock = fixedQueueClock{
		now: validAt.Add(time.Hour),
	}

	lease, found, err := fixture.queue.Claim(
		fixture.ctx,
		"worker-b",
	)
	if err != nil {
		t.Fatalf(
			"Claim() error = %v",
			err,
		)
	}
	if !found {
		t.Fatal(
			"Claim() found = false, want true",
		)
	}

	terminalAt := lease.ClaimedAt.Add(time.Minute)
	fixture.queue.clock = fixedQueueClock{
		now: terminalAt,
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		lease,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  fixture.source,
		},
		time.Hour,
	); err != nil {
		t.Fatalf(
			"CompleteVerification(absent) error = %v",
			err,
		)
	}

	if discoverySourceIsScheduled(
		t,
		fixture.ctx,
		fixture.pool,
		fixture.source.String(),
	) {
		t.Fatal(
			"terminal automatic source remains scheduled",
		)
	}

	rediscoverySource := mustStoreOrigin(
		t,
		"https://stale-rediscovery-source.example",
	)

	staleStore := newDiscoveryTestStore(
		t,
		fixture.pool,
		terminalAt,
	)

	if err := staleStore.AddCrawlSeed(
		fixture.ctx,
		rediscoverySource,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v",
			err,
		)
	}

	if _, err := staleStore.RecordDiscovery(
		fixture.ctx,
		rediscoverySource,
		[]discovery.Candidate{
			{
				Origin: fixture.source,
				Kind:   discovery.KindLink,
			},
		},
	); err != nil {
		t.Fatalf(
			"RecordDiscovery() error = %v",
			err,
		)
	}

	if discoverySourceIsScheduled(
		t,
		fixture.ctx,
		fixture.pool,
		fixture.source.String(),
	) {
		t.Fatal(
			"equal-time rediscovery reactivated terminal source",
		)
	}
}

func TestDiscoveryScheduleTerminalMissPreservesNewerRediscovery(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)

	validAt := fixture.claimed.Add(time.Minute)
	fixture.queue.clock = fixedQueueClock{
		now: validAt,
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		validCompletionResult(fixture.source),
		time.Hour,
	); err != nil {
		t.Fatalf(
			"CompleteVerification(valid) error = %v",
			err,
		)
	}

	fixture.queue.clock = fixedQueueClock{
		now: validAt.Add(time.Hour),
	}

	lease, found, err := fixture.queue.Claim(
		fixture.ctx,
		"worker-b",
	)
	if err != nil {
		t.Fatalf(
			"Claim() error = %v",
			err,
		)
	}
	if !found {
		t.Fatal(
			"Claim() found = false, want true",
		)
	}

	terminalAt := lease.ClaimedAt.Add(time.Minute)
	freshAt := terminalAt.Add(time.Minute)

	rediscoverySource := mustStoreOrigin(
		t,
		"https://newer-rediscovery-source.example",
	)

	rediscoveryStore := newDiscoveryTestStore(
		t,
		fixture.pool,
		freshAt,
	)

	if err := rediscoveryStore.AddCrawlSeed(
		fixture.ctx,
		rediscoverySource,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v",
			err,
		)
	}

	if _, err := rediscoveryStore.RecordDiscovery(
		fixture.ctx,
		rediscoverySource,
		[]discovery.Candidate{
			{
				Origin: fixture.source,
				Kind:   discovery.KindLink,
			},
		},
	); err != nil {
		t.Fatalf(
			"RecordDiscovery() error = %v",
			err,
		)
	}

	fixture.queue.clock = fixedQueueClock{
		now: terminalAt,
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		lease,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  fixture.source,
		},
		time.Hour,
	); err != nil {
		t.Fatalf(
			"CompleteVerification(absent) error = %v",
			err,
		)
	}

	if !discoverySourceIsScheduled(
		t,
		fixture.ctx,
		fixture.pool,
		fixture.source.String(),
	) {
		t.Fatal(
			"terminal verification removed newer rediscovery scheduling",
		)
	}
}

func TestDiscoveryScheduleFormerParticipantWithoutAuthoritativeObservationDoesNotReactivate(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	participant := mustStoreOrigin(
		t,
		"https://former-participant-no-authoritative.example",
	)
	source := mustStoreOrigin(
		t,
		"https://rediscovery-source.example",
	)

	participatedAt := queueTestTime()

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO origins (
				origin,
				first_observed_at,
				first_participated_at,
				initial_declaration_version,
				initial_declaration_identity,
				latest_declaration_check_at,
				latest_declaration_check_outcome
			)
			VALUES (
				$1,
				$2,
				$2,
				1,
				'affirmed',
				$2,
				'unavailable'
			)
		`,
		participant.String(),
		participatedAt,
	); err != nil {
		t.Fatalf(
			"insert former participant metadata: %v",
			err,
		)
	}

	store := newDiscoveryTestStore(
		t,
		pool,
		participatedAt.Add(time.Hour),
	)

	if err := store.AddCrawlSeed(
		ctx,
		participant,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed(participant) error = %v",
			err,
		)
	}

	if !discoverySourceIsScheduled(
		t,
		ctx,
		pool,
		participant.String(),
	) {
		t.Fatal(
			"explicit seed was not scheduled",
		)
	}

	if err := store.RemoveCrawlSeed(
		ctx,
		participant,
	); err != nil {
		t.Fatalf(
			"RemoveCrawlSeed(participant) error = %v",
			err,
		)
	}

	if discoverySourceIsScheduled(
		t,
		ctx,
		pool,
		participant.String(),
	) {
		t.Fatal(
			"former participant without authoritative observation remained scheduled after seed removal",
		)
	}

	if err := store.AddCrawlSeed(
		ctx,
		source,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed(source) error = %v",
			err,
		)
	}

	if _, err := store.RecordDiscovery(
		ctx,
		source,
		[]discovery.Candidate{
			{
				Origin: participant,
				Kind:   discovery.KindLink,
			},
		},
	); err != nil {
		t.Fatalf(
			"RecordDiscovery() error = %v",
			err,
		)
	}

	if discoverySourceIsScheduled(
		t,
		ctx,
		pool,
		participant.String(),
	) {
		t.Fatal(
			"former participant without authoritative observation was reactivated",
		)
	}
}

func TestDiscoveryScheduleRediscoveryRestoresCurrentValidSource(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)

	validAt := fixture.claimed.Add(time.Minute)
	fixture.queue.clock = fixedQueueClock{
		now: validAt,
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		validCompletionResult(fixture.source),
		time.Hour,
	); err != nil {
		t.Fatalf(
			"CompleteVerification(valid) error = %v",
			err,
		)
	}

	if _, err := fixture.pool.Exec(
		fixture.ctx,
		`
			DELETE FROM discovery_source_schedule
			WHERE source_origin = $1
		`,
		fixture.source.String(),
	); err != nil {
		t.Fatalf(
			"remove valid source schedule fixture: %v",
			err,
		)
	}

	if discoverySourceIsScheduled(
		t,
		fixture.ctx,
		fixture.pool,
		fixture.source.String(),
	) {
		t.Fatal(
			"valid source fixture remains scheduled",
		)
	}

	rediscoverySource := mustStoreOrigin(
		t,
		"https://valid-rediscovery-source.example",
	)

	rediscoveryStore := newDiscoveryTestStore(
		t,
		fixture.pool,
		validAt.Add(-time.Minute),
	)

	if err := rediscoveryStore.AddCrawlSeed(
		fixture.ctx,
		rediscoverySource,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v",
			err,
		)
	}

	if _, err := rediscoveryStore.RecordDiscovery(
		fixture.ctx,
		rediscoverySource,
		[]discovery.Candidate{
			{
				Origin: fixture.source,
				Kind:   discovery.KindLink,
			},
		},
	); err != nil {
		t.Fatalf(
			"RecordDiscovery() error = %v",
			err,
		)
	}

	if !discoverySourceIsScheduled(
		t,
		fixture.ctx,
		fixture.pool,
		fixture.source.String(),
	) {
		t.Fatal(
			"current valid source was not restored to discovery scheduling",
		)
	}
}

func TestDiscoveryScheduleCrawlSourcesIncludesScheduleOnlySource(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	scheduledOrigin := mustStoreOrigin(
		t,
		"https://schedule-only.example",
	)

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_schedule (
				source_origin
			)
			VALUES ($1)
		`,
		scheduledOrigin.String(),
	); err != nil {
		t.Fatalf(
			"insert schedule-only source: %v",
			err,
		)
	}

	store, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v",
			err,
		)
	}

	sources, err := store.CrawlSources(ctx)
	if err != nil {
		t.Fatalf(
			"CrawlSources() error = %v",
			err,
		)
	}

	for _, source := range sources {
		if source.Origin != scheduledOrigin {
			continue
		}

		if source.Seeded ||
			source.AutomaticallyDiscovered ||
			source.Blocked ||
			source.Verified ||
			source.FirstDiscoveredAt != nil ||
			source.LastDiscoveredAt != nil {
			t.Fatalf(
				"schedule-only CrawlSource = %#v, want classification defaults",
				source,
			)
		}

		return
	}

	t.Fatalf(
		"CrawlSources() omitted schedule-only origin %q",
		scheduledOrigin,
	)
}

func TestPendingAutomaticCandidatesFiltersSchedulingFacts(
	t *testing.T,
) {
	tests := []struct {
		name    string
		prepare func(
			*testing.T,
			*pgxpool.Pool,
			discovery.Candidate,
			time.Time,
		)
		wantPending bool
	}{
		{
			name: "already scheduled",
			prepare: func(
				t *testing.T,
				pool *pgxpool.Pool,
				candidate discovery.Candidate,
				_ time.Time,
			) {
				t.Helper()

				if _, err := pool.Exec(
					context.Background(),
					`
						INSERT INTO discovery_source_schedule (
							source_origin
						)
						VALUES ($1)
					`,
					candidate.Origin.String(),
				); err != nil {
					t.Fatalf(
						"insert scheduled candidate: %v",
						err,
					)
				}
			},
		},
		{
			name: "current valid",
			prepare: func(
				t *testing.T,
				pool *pgxpool.Pool,
				candidate discovery.Candidate,
				discoveredAt time.Time,
			) {
				t.Helper()

				seedDiscoveryTestObservation(
					t,
					pool,
					candidate.Origin,
					discoveredAt.Add(time.Minute),
					declaration.OutcomeValid,
					declaration.IdentityUndeclared,
				)
			},
		},
		{
			name: "terminal newer than discovery",
			prepare: func(
				t *testing.T,
				pool *pgxpool.Pool,
				candidate discovery.Candidate,
				discoveredAt time.Time,
			) {
				t.Helper()

				seedDiscoveryTestObservation(
					t,
					pool,
					candidate.Origin,
					discoveredAt.Add(time.Minute),
					declaration.OutcomeAbsent,
					0,
				)
			},
		},
		{
			name: "terminal equal to discovery",
			prepare: func(
				t *testing.T,
				pool *pgxpool.Pool,
				candidate discovery.Candidate,
				discoveredAt time.Time,
			) {
				t.Helper()

				seedDiscoveryTestObservation(
					t,
					pool,
					candidate.Origin,
					discoveredAt,
					declaration.OutcomeAbsent,
					0,
				)
			},
		},
		{
			name: "discovery newer than terminal",
			prepare: func(
				t *testing.T,
				pool *pgxpool.Pool,
				candidate discovery.Candidate,
				discoveredAt time.Time,
			) {
				t.Helper()

				seedDiscoveryTestObservation(
					t,
					pool,
					candidate.Origin,
					discoveredAt.Add(-time.Minute),
					declaration.OutcomeAbsent,
					0,
				)
			},
			wantPending: true,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				ctx := context.Background()
				pool := newStoreTestPool(t)
				store, runID :=
					automaticPendingFixtureInPool(
						t,
						pool,
					)
				candidate := discovery.Candidate{
					Origin: mustStoreOrigin(
						t,
						"https://candidate.example",
					),
					Kind: discovery.KindLink,
				}

				var discoveredAt time.Time
				if err := pool.QueryRow(
					ctx,
					`
						SELECT last_discovered_at
						FROM discovery_candidates
						WHERE origin = $1
					`,
					candidate.Origin.String(),
				).Scan(
					&discoveredAt,
				); err != nil {
					t.Fatalf(
						"query candidate discovery time: %v",
						err,
					)
				}

				test.prepare(
					t,
					pool,
					candidate,
					discoveredAt,
				)

				pending, err :=
					store.PendingAutomaticCandidates(
						ctx,
						runID,
					)
				if err != nil {
					t.Fatalf(
						"PendingAutomaticCandidates() error = %v",
						err,
					)
				}

				if test.wantPending {
					if len(pending) != 1 ||
						pending[0].Origin !=
							candidate.Origin {
						t.Fatalf(
							"pending candidates = %#v, want %q",
							pending,
							candidate.Origin,
						)
					}
					return
				}

				if len(pending) != 0 {
					t.Fatalf(
						"pending candidates = %#v, want none",
						pending,
					)
				}
			},
		)
	}
}

func TestCompleteAutomaticCandidatesTreatsNewScheduleAsExisting(
	t *testing.T,
) {
	ctx := context.Background()
	pool, store, runID, candidate :=
		automaticCompletionFixture(t)

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_schedule (
				source_origin
			)
			VALUES ($1)
		`,
		candidate.Origin.String(),
	); err != nil {
		t.Fatalf(
			"insert post-allocation schedule: %v",
			err,
		)
	}

	if err := store.CompleteAutomaticCandidates(
		ctx,
		runID,
		[]AutomaticCandidateResult{
			{
				Candidate: candidate,
			},
		},
	); err != nil {
		t.Fatalf(
			"CompleteAutomaticCandidates() error = %v",
			err,
		)
	}

	var outcome string
	if err := pool.QueryRow(
		ctx,
		`
			SELECT outcome
			FROM crawl_run_automatic_admission_batches
			WHERE admission_run_id = $1
				AND candidate_origin = $2
		`,
		int64(runID),
		candidate.Origin.String(),
	).Scan(
		&outcome,
	); err != nil {
		t.Fatalf(
			"query automatic admission outcome: %v",
			err,
		)
	}

	if outcome != "existing" {
		t.Fatalf(
			"automatic admission outcome = %q, want existing",
			outcome,
		)
	}

	var sourceStateCount int
	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		candidate.Origin.String(),
	).Scan(
		&sourceStateCount,
	); err != nil {
		t.Fatalf(
			"count candidate source state: %v",
			err,
		)
	}

	if sourceStateCount != 0 {
		t.Fatalf(
			"candidate source state count = %d, want 0",
			sourceStateCount,
		)
	}

	if !discoverySourceIsScheduled(
		t,
		ctx,
		pool,
		candidate.Origin.String(),
	) {
		t.Fatal(
			"existing schedule was removed during automatic completion",
		)
	}
}

func TestCompleteAutomaticCandidatesDefersNewerTerminalObservation(
	t *testing.T,
) {
	ctx := context.Background()
	pool, store, runID, candidate :=
		automaticCompletionFixture(t)

	var discoveredAt time.Time
	if err := pool.QueryRow(
		ctx,
		`
			SELECT last_discovered_at
			FROM discovery_candidates
			WHERE origin = $1
		`,
		candidate.Origin.String(),
	).Scan(
		&discoveredAt,
	); err != nil {
		t.Fatalf(
			"query candidate discovery time: %v",
			err,
		)
	}

	seedDiscoveryTestObservation(
		t,
		pool,
		candidate.Origin,
		discoveredAt.Add(time.Minute),
		declaration.OutcomeAbsent,
		0,
	)

	if err := store.CompleteAutomaticCandidates(
		ctx,
		runID,
		[]AutomaticCandidateResult{
			{
				Candidate: candidate,
			},
		},
	); err != nil {
		t.Fatalf(
			"CompleteAutomaticCandidates() error = %v",
			err,
		)
	}

	var outcome string
	if err := pool.QueryRow(
		ctx,
		`
			SELECT outcome
			FROM crawl_run_automatic_admission_batches
			WHERE admission_run_id = $1
				AND candidate_origin = $2
		`,
		int64(runID),
		candidate.Origin.String(),
	).Scan(
		&outcome,
	); err != nil {
		t.Fatalf(
			"query automatic admission outcome: %v",
			err,
		)
	}

	if outcome != "policy_deferred" {
		t.Fatalf(
			"automatic admission outcome = %q, want policy_deferred",
			outcome,
		)
	}

	if discoverySourceIsScheduled(
		t,
		ctx,
		pool,
		candidate.Origin.String(),
	) {
		t.Fatal(
			"newer terminal observation was undone by automatic completion",
		)
	}

	var sourceStateCount int
	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		candidate.Origin.String(),
	).Scan(
		&sourceStateCount,
	); err != nil {
		t.Fatalf(
			"count candidate source state: %v",
			err,
		)
	}

	if sourceStateCount != 0 {
		t.Fatalf(
			"terminal candidate source state count = %d, want 0",
			sourceStateCount,
		)
	}

	var queued bool
	if err := pool.QueryRow(
		ctx,
		`
			SELECT EXISTS (
				SELECT 1
				FROM verification_queue
				WHERE origin = $1
			)
		`,
		candidate.Origin.String(),
	).Scan(
		&queued,
	); err != nil {
		t.Fatalf(
			"query candidate verification queue: %v",
			err,
		)
	}

	if queued {
		t.Fatal(
			"newer terminal observation scheduled a new verification probe",
		)
	}
}

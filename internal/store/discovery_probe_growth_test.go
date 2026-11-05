package store

import (
	"context"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
)

func TestRecordDiscoverySchedulesProbesWhenAutomaticCrawlEnabled(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	source := mustStoreOrigin(t, "https://source.example")
	friend := mustStoreOrigin(t, "https://friend.example")

	store, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{Enabled: true},
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

	var queued bool
	var automaticSources int
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				FROM verification_queue
				WHERE origin = $1 AND mode = 'probe'
			),
			(
				SELECT count(*)
				FROM discovery_source_state
				WHERE automatically_discovered
			)
	`, friend.String()).Scan(&queued, &automaticSources); err != nil {
		t.Fatal(err)
	}

	if !queued {
		t.Fatal("friend origin was not probe-scheduled when automatic crawl is enabled")
	}
	if automaticSources != 0 {
		t.Fatalf(
			"automatic source count = %d, want 0 before admission",
			automaticSources,
		)
	}
}

func TestSchedulePendingDiscoveryProbesDrainsCapacityBacklog(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	source := mustStoreOrigin(t, "https://source.example")
	first := mustStoreOrigin(t, "https://alpha.example")
	second := mustStoreOrigin(t, "https://bravo.example")

	store, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{
			Enabled:          false,
			MaxPendingProbes: 1,
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
			{Origin: first, Kind: discovery.KindLink},
			{Origin: second, Kind: discovery.KindLink},
		},
	); err != nil {
		t.Fatalf("RecordDiscovery() error = %v", err)
	}

	var firstQueued, secondQueued bool
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM verification_queue WHERE origin = $1
			),
			EXISTS (
				SELECT 1 FROM verification_queue WHERE origin = $2
			)
	`, first.String(), second.String()).Scan(
		&firstQueued,
		&secondQueued,
	); err != nil {
		t.Fatal(err)
	}
	if !firstQueued {
		t.Fatal("alpha was not probe-scheduled under capacity")
	}
	if secondQueued {
		t.Fatal("bravo was probe-scheduled despite full capacity")
	}

	if _, err := pool.Exec(ctx, `
		DELETE FROM verification_queue WHERE origin = $1
	`, first.String()); err != nil {
		t.Fatal(err)
	}
	// A terminal observation at-or-after last discovery keeps alpha out of the
	// drain set so freed capacity serves the stranded overflow origin.
	if _, err := pool.Exec(ctx, `
		INSERT INTO origins (origin, first_observed_at)
		VALUES ($1, statement_timestamp())
		ON CONFLICT (origin) DO NOTHING
	`, first.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO verification_observations (
			origin,
			observed_at,
			outcome
		)
		SELECT
			$1,
			last_discovered_at,
			'absent'
		FROM discovery_candidates
		WHERE origin = $1
	`, first.String()); err != nil {
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

	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM verification_queue WHERE origin = $1
			),
			EXISTS (
				SELECT 1 FROM verification_queue WHERE origin = $2
			)
	`, first.String(), second.String()).Scan(
		&firstQueued,
		&secondQueued,
	); err != nil {
		t.Fatal(err)
	}
	if firstQueued {
		t.Fatal("alpha was re-queued despite terminal observation")
	}
	if !secondQueued {
		t.Fatal("bravo was not drained into the probe queue")
	}
}

func TestSchedulePendingDiscoveryProbesSkipsAvoidedAndBlocked(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	source := mustStoreOrigin(t, "https://source.example")
	open := mustStoreOrigin(t, "https://open.example")
	blocked := mustStoreOrigin(t, "https://blocked.example")
	avoided := mustStoreOrigin(t, "https://writer.blogspot.com")

	store, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCrawlBlocked(ctx, blocked, true); err != nil {
		t.Fatal(err)
	}

	recordedAt := time.Now().UTC()
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_candidates (
			origin,
			first_discovered_at,
			last_discovered_at
		)
		VALUES
			($1, $4, $4),
			($2, $4, $4),
			($3, $4, $4)
		ON CONFLICT (origin) DO NOTHING
	`, open.String(), blocked.String(), avoided.String(), recordedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_edges (
			source_origin,
			candidate_origin,
			kind,
			first_discovered_at,
			last_discovered_at
		)
		VALUES
			($1, $2, 'link', $5, $5),
			($1, $3, 'link', $5, $5),
			($1, $4, 'link', $5, $5)
		ON CONFLICT DO NOTHING
	`, source.String(), open.String(), blocked.String(), avoided.String(), recordedAt); err != nil {
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

	var openQueued, blockedQueued, avoidedQueued bool
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM verification_queue WHERE origin = $1
			),
			EXISTS (
				SELECT 1 FROM verification_queue WHERE origin = $2
			),
			EXISTS (
				SELECT 1 FROM verification_queue WHERE origin = $3
			)
	`, open.String(), blocked.String(), avoided.String()).Scan(
		&openQueued,
		&blockedQueued,
		&avoidedQueued,
	); err != nil {
		t.Fatal(err)
	}
	if !openQueued {
		t.Fatal("open origin was not probe-scheduled")
	}
	if blockedQueued {
		t.Fatal("crawl-blocked origin was probe-scheduled")
	}
	if avoidedQueued {
		t.Fatal("avoid-listed origin was probe-scheduled")
	}
}

func TestSchedulePendingDiscoveryProbesReconcilesStrandedCandidates(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	source := mustStoreOrigin(t, "https://joshuamorris.info")
	friend := mustStoreOrigin(t, "https://friend.example")

	store, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{Enabled: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}

	recordedAt := time.Now().UTC()
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_candidates (
			origin,
			first_discovered_at,
			last_discovered_at
		)
		VALUES ($1, $2, $2)
	`, friend.String(), recordedAt); err != nil {
		t.Fatal(err)
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
	`, source.String(), friend.String(), recordedAt); err != nil {
		t.Fatal(err)
	}

	var queuedBefore bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM verification_queue WHERE origin = $1
		)
	`, friend.String()).Scan(&queuedBefore); err != nil {
		t.Fatal(err)
	}
	if queuedBefore {
		t.Fatal("stranded candidate was already queued")
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

	var queuedAfter bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM verification_queue
			WHERE origin = $1 AND mode = 'probe'
		)
	`, friend.String()).Scan(&queuedAfter); err != nil {
		t.Fatal(err)
	}
	if !queuedAfter {
		t.Fatal("stranded friend was not reconciled into the probe queue")
	}
}

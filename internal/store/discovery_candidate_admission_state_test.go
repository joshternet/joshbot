package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/retry"
)

func TestUpsertDurableAdmissionOutcomeReturnsErrorOnCancelledContext(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("pool.Begin() error = %v", err)
	}
	defer tx.Rollback(ctx)

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	err = upsertDurableAdmissionOutcome(
		canceled,
		tx,
		"https://cancelled.example",
		"network_rejected",
		time.Now().UTC(),
	)
	if err == nil {
		t.Fatal(
			"upsertDurableAdmissionOutcome() error = nil, want context error",
		)
	}
	if !strings.Contains(err.Error(), "record durable admission outcome") {
		t.Errorf(
			"error = %v, want 'record durable admission outcome' prefix",
			err,
		)
	}
}

func TestCompleteAutomaticCandidatesErrorFromDurableAdmissionOutcome(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := newAutomaticAdmissionStore(t, pool, 10, 10)
	source := mustStoreOrigin(t, "https://source.example")
	candidate := mustStoreOrigin(t, "https://durable-error.example")

	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}

	runID := beginAutomaticAdmissionRun(t, store, source, 10)
	if _, err := store.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{
			{Origin: candidate, Kind: discovery.KindLink},
		},
	); err != nil {
		t.Fatal(err)
	}

	// PendingAutomaticCandidates creates the batch rows with
	// outcome = 'pending' so CompleteAutomaticCandidates can
	// process them.
	pending, err := store.PendingAutomaticCandidates(ctx, runID)
	if err != nil {
		t.Fatalf("PendingAutomaticCandidates() error = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}

	// Add a CHECK constraint that rejects the candidate_origin,
	// causing the upsert to fail inside the transaction.
	if _, err := pool.Exec(ctx, `
		ALTER TABLE discovery_candidate_admission_state
		ADD CONSTRAINT integration_block_insert
		CHECK (candidate_origin != 'https://durable-error.example')
	`); err != nil {
		t.Fatalf("add blocking constraint: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `
			ALTER TABLE discovery_candidate_admission_state
			DROP CONSTRAINT IF EXISTS integration_block_insert
		`)
	})

	err = store.CompleteAutomaticCandidates(
		ctx,
		runID,
		[]AutomaticCandidateResult{
			{
				Candidate: discovery.Candidate{
					Origin: candidate,
					Kind:   discovery.KindLink,
				},
				FailureCategory: retry.CategoryUnsafeAddress,
			},
		},
	)
	if err == nil {
		t.Fatal("CompleteAutomaticCandidates() error = nil, want upsert failure")
	}
	if !strings.Contains(err.Error(), "record durable admission outcome") {
		t.Errorf("error = %v, want durable admission outcome error", err)
	}
}

func TestCompleteAutomaticCandidatesErrorFromDurableOutcomeExisting(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := newAutomaticAdmissionStore(t, pool, 10, 10)
	source := mustStoreOrigin(t, "https://source.example")
	candidate := mustStoreOrigin(t, "https://already-discovered.example")

	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}

	runID := beginAutomaticAdmissionRun(t, store, source, 10)
	if _, err := store.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{
			{Origin: candidate, Kind: discovery.KindLink},
		},
	); err != nil {
		t.Fatal(err)
	}

	// Create the pending batch before making the candidate "existing".
	pending, err := store.PendingAutomaticCandidates(ctx, runID)
	if err != nil {
		t.Fatalf("PendingAutomaticCandidates() error = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}

	// Make the candidate a scheduled discovery source after it was
	// added to the batch so the "existing" branch is reached during
	// CompleteAutomaticCandidates.
	if err := store.AddCrawlSeed(ctx, candidate); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `
		ALTER TABLE discovery_candidate_admission_state
		ADD CONSTRAINT integration_block_existing
		CHECK (candidate_origin != 'https://already-discovered.example')
	`); err != nil {
		t.Fatalf("add blocking constraint: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `
			ALTER TABLE discovery_candidate_admission_state
			DROP CONSTRAINT IF EXISTS integration_block_existing
		`)
	})

	err = store.CompleteAutomaticCandidates(
		ctx,
		runID,
		[]AutomaticCandidateResult{
			{
				Candidate: discovery.Candidate{
					Origin: candidate,
					Kind:   discovery.KindLink,
				},
			},
		},
	)
	if err == nil {
		t.Fatal("CompleteAutomaticCandidates() error = nil, want upsert failure")
	}
	if !strings.Contains(err.Error(), "record durable admission outcome") {
		t.Errorf("error = %v, want durable admission outcome error", err)
	}
}

func TestCompleteAutomaticCandidatesErrorFromDurableOutcomePromoted(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := newAutomaticAdmissionStore(t, pool, 10, 10)
	source := mustStoreOrigin(t, "https://source.example")
	candidate := mustStoreOrigin(t, "https://promotable.example")

	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}

	runID := beginAutomaticAdmissionRun(t, store, source, 10)
	if _, err := store.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{
			{Origin: candidate, Kind: discovery.KindLink},
		},
	); err != nil {
		t.Fatal(err)
	}

	pending, err := store.PendingAutomaticCandidates(ctx, runID)
	if err != nil {
		t.Fatalf("PendingAutomaticCandidates() error = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}

	if _, err := pool.Exec(ctx, `
		ALTER TABLE discovery_candidate_admission_state
		ADD CONSTRAINT integration_block_promoted
		CHECK (candidate_origin != 'https://promotable.example')
	`); err != nil {
		t.Fatalf("add blocking constraint: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `
			ALTER TABLE discovery_candidate_admission_state
			DROP CONSTRAINT IF EXISTS integration_block_promoted
		`)
	})

	// FailureCategory zero + candidate not already discovered
	// → promotion path (line 1693).
	err = store.CompleteAutomaticCandidates(
		ctx,
		runID,
		[]AutomaticCandidateResult{
			{
				Candidate: discovery.Candidate{
					Origin: candidate,
					Kind:   discovery.KindLink,
				},
			},
		},
	)
	if err == nil {
		t.Fatal("CompleteAutomaticCandidates() error = nil, want upsert failure")
	}
	if !strings.Contains(err.Error(), "record durable admission outcome") {
		t.Errorf("error = %v, want durable admission outcome error", err)
	}
}

func TestPendingAutomaticCandidatesKeepsDurableRejectsAfterPurge(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := newAutomaticAdmissionStore(t, pool, 1, 10)
	source := mustStoreOrigin(t, "https://source.example")
	unsafe := mustStoreOrigin(t, "https://unsafe.example")

	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}

	runID := beginAutomaticAdmissionRun(t, store, source, 1)
	if _, err := store.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{
			{Origin: unsafe, Kind: discovery.KindLink},
		},
	); err != nil {
		t.Fatal(err)
	}

	pending, err := store.PendingAutomaticCandidates(ctx, runID)
	if err != nil {
		t.Fatalf("PendingAutomaticCandidates() error = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %#v, want one unsafe candidate", pending)
	}

	if err := store.CompleteAutomaticCandidates(
		ctx,
		runID,
		[]AutomaticCandidateResult{
			{
				Candidate: discovery.Candidate{
					Origin: unsafe,
					Kind:   discovery.KindLink,
				},
				FailureCategory: retry.CategoryUnsafeAddress,
			},
		},
	); err != nil {
		t.Fatalf("CompleteAutomaticCandidates() error = %v", err)
	}

	var durableOutcome string
	if err := pool.QueryRow(ctx, `
		SELECT outcome
		FROM discovery_candidate_admission_state
		WHERE candidate_origin = $1
	`, unsafe.String()).Scan(&durableOutcome); err != nil {
		t.Fatalf("read durable admission state: %v", err)
	}
	if durableOutcome != "network_rejected" {
		t.Fatalf(
			"durable outcome = %q, want network_rejected",
			durableOutcome,
		)
	}

	if err := store.FinishCrawl(
		ctx,
		runID,
		discovery.CrawlResult{Source: source},
		discovery.CrawlRunComplete,
		"done",
	); err != nil {
		t.Fatal(err)
	}

	removed, err := store.PurgeCrawlTelemetry(ctx, time.Nanosecond)
	if err != nil {
		t.Fatalf("PurgeCrawlTelemetry() error = %v", err)
	}
	if removed < 1 {
		t.Fatalf("PurgeCrawlTelemetry() removed = %d, want >= 1", removed)
	}

	var batchCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM crawl_run_automatic_admission_batches
		WHERE candidate_origin = $1
	`, unsafe.String()).Scan(&batchCount); err != nil {
		t.Fatal(err)
	}
	if batchCount != 0 {
		t.Fatalf(
			"batch telemetry remained after purge: %d",
			batchCount,
		)
	}

	secondRun := beginAutomaticAdmissionRun(t, store, source, 1)
	pending, err = store.PendingAutomaticCandidates(ctx, secondRun)
	if err != nil {
		t.Fatalf(
			"PendingAutomaticCandidates() after purge error = %v",
			err,
		)
	}
	if len(pending) != 0 {
		t.Fatalf(
			"pending after purge = %#v, want none",
			pending,
		)
	}
}

package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

func TestAutomaticAdmissionPersistsEvidenceAndUsesOneRunBudget(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	discoveryStore := newAutomaticAdmissionStore(t, pool, 2, 10)
	source := mustStoreOrigin(t, "https://source.example")
	existing := mustStoreOrigin(t, "https://aardvark.example")
	alpha := mustStoreOrigin(t, "https://alpha.example")
	bravo := mustStoreOrigin(t, "https://bravo.example")
	charlie := mustStoreOrigin(t, "https://charlie.example")
	unsafe := mustStoreOrigin(t, "https://unsafe.example")

	if err := discoveryStore.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_source_state
			(source_origin, automatically_discovered)
		VALUES ($1, true)
	`, existing.String()); err != nil {
		t.Fatal(err)
	}

	runID := beginAutomaticAdmissionRun(t, discoveryStore, source, 2)
	for _, batch := range [][]discovery.Candidate{
		{
			{Origin: charlie, Kind: discovery.KindLink},
			{Origin: unsafe, Kind: discovery.KindLink},
		},
		{
			{Origin: bravo, Kind: discovery.KindLink},
			{Origin: existing, Kind: discovery.KindLink},
			{Origin: alpha, Kind: discovery.KindLink},
		},
	} {
		if _, err := discoveryStore.RecordDiscoveryForRun(ctx, runID, source, batch); err != nil {
			t.Fatalf("RecordDiscoveryForRun() error = %v", err)
		}
	}

	pending, err := discoveryStore.PendingAutomaticCandidates(ctx, runID)
	if err != nil {
		t.Fatalf("PendingAutomaticCandidates() error = %v", err)
	}
	safe := candidatesExcept(pending, unsafe.String())
	if err := discoveryStore.AdmitAutomaticCandidates(ctx, runID, safe); err != nil {
		t.Fatalf("AdmitAutomaticCandidates() error = %v", err)
	}

	got := automaticSourceNames(t, ctx, pool)
	want := []string{
		existing.String(),
		alpha.String(),
		bravo.String(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("automatic sources = %#v, want %#v", got, want)
	}

	var evidenceCount, edgeCount, runCandidateCount, maxPromotions, promotions int
	var admitted, deferred int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM discovery_candidates),
			(SELECT count(*) FROM discovery_edges),
			(SELECT count(*) FROM crawl_run_discovery_candidates WHERE run_id = $1),
			max_automatic_promotions,
			automatic_promotions,
			promotions_admitted,
			promotions_deferred
		FROM crawl_runs
		WHERE id = $1
	`, int64(runID)).Scan(
		&evidenceCount,
		&edgeCount,
		&runCandidateCount,
		&maxPromotions,
		&promotions,
		&admitted,
		&deferred,
	); err != nil {
		t.Fatal(err)
	}
	if evidenceCount != 5 || edgeCount != 5 || runCandidateCount != 5 ||
		maxPromotions != 2 || promotions != 2 {
		t.Fatalf(
			"evidence/edges/run/max/promotions = %d/%d/%d/%d/%d, want 5/5/5/2/2",
			evidenceCount,
			edgeCount,
			runCandidateCount,
			maxPromotions,
			promotions,
		)
	}
	if admitted != 2 || deferred != 3 {
		t.Fatalf(
			"admitted/deferred = %d/%d, want 2/3 including every original link candidate",
			admitted,
			deferred,
		)
	}
}

func TestAutomaticAdmissionTransientFailurePersistsAcrossStoreRestart(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	first := newAutomaticAdmissionStore(t, pool, 2, 10)
	now := queueTestTime()
	first.clock = staticDiscoveryClock{now: now}
	source := mustStoreOrigin(t, "https://source.example")
	candidate := mustStoreOrigin(t, "https://candidate.example")
	unsafe := mustStoreOrigin(t, "https://unsafe.example")
	if err := first.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	runID := beginAutomaticAdmissionRun(t, first, source, 2)
	if _, err := first.RecordDiscoveryForRun(ctx, runID, source, []discovery.Candidate{
		{Origin: candidate, Kind: discovery.KindLink},
		{Origin: unsafe, Kind: discovery.KindLink},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.PendingAutomaticCandidates(ctx, runID); err != nil {
		t.Fatal(err)
	}
	if err := first.CompleteAutomaticCandidates(ctx, runID, []AutomaticCandidateResult{
		{Candidate: discovery.Candidate{Origin: candidate, Kind: discovery.KindLink}, FailureCategory: retry.CategoryDNS},
		{Candidate: discovery.Candidate{Origin: unsafe, Kind: discovery.KindLink}, FailureCategory: retry.CategoryUnsafeAddress},
	}); err != nil {
		t.Fatal(err)
	}

	_ = newAutomaticAdmissionStore(t, pool, 1, 10)
	var failures int
	var category string
	var nextAttempt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT
			candidate.consecutive_failures,
			candidate.last_failure_category,
			candidate.next_attempt_at
		FROM discovery_candidates AS candidate
		JOIN crawl_run_automatic_admission_batches AS batch
			ON batch.candidate_origin = candidate.origin
		WHERE candidate.origin = $1
			AND batch.admission_run_id = $2
			AND batch.consecutive_failures = candidate.consecutive_failures
			AND batch.last_failure_category = candidate.last_failure_category
			AND batch.next_attempt_at = candidate.next_attempt_at
	`, candidate.String(), int64(runID)).Scan(&failures, &category, &nextAttempt); err != nil {
		t.Fatal(err)
	}
	if failures != 1 || category != string(retry.CategoryDNS) || nextAttempt.IsZero() {
		t.Fatalf("persisted retry = %d, %q, %v", failures, category, nextAttempt)
	}
	if err := first.FinishCrawl(
		ctx, runID, discovery.CrawlResult{Source: source}, discovery.CrawlRunComplete, "done",
	); err != nil {
		t.Fatal(err)
	}

	restarted := newAutomaticAdmissionStore(t, pool, 1, 10)
	restarted.clock = staticDiscoveryClock{now: now.Add(4 * time.Minute)}
	secondRun := beginAutomaticAdmissionRun(t, restarted, source, 1)
	if pending, err := restarted.PendingAutomaticCandidates(ctx, secondRun); err != nil || len(pending) != 0 {
		t.Fatalf("early pending = %#v, %v", pending, err)
	}
	restarted.clock = staticDiscoveryClock{now: now.Add(5 * time.Minute)}
	pending, err := restarted.PendingAutomaticCandidates(ctx, secondRun)
	if err != nil || !reflect.DeepEqual(pending, []discovery.Candidate{{
		Origin: candidate, Kind: discovery.KindLink,
	}}) {
		t.Fatalf("due pending = %#v, %v", pending, err)
	}
	if err := restarted.AdmitAutomaticCandidates(ctx, secondRun, pending); err != nil {
		t.Fatal(err)
	}
	got := automaticSourceNames(t, ctx, pool)
	if !reflect.DeepEqual(got, []string{candidate.String()}) {
		t.Fatalf("automatic sources = %#v", got)
	}
	var unsafeQueueCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM verification_queue WHERE origin = $1
	`, unsafe.String()).Scan(&unsafeQueueCount); err != nil {
		t.Fatal(err)
	}
	if unsafeQueueCount != 0 {
		t.Fatalf("unsafe verification queue count = %d, want 0", unsafeQueueCount)
	}
}

type staticDiscoveryClock struct{ now time.Time }

func (clock staticDiscoveryClock) NowTransaction(
	context.Context,
	pgx.Tx,
) (time.Time, error) {
	return clock.now, nil
}

func TestAutomaticAdmissionDefersThenAdmitsInCanonicalOrder(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	discoveryStore := newAutomaticAdmissionStore(t, pool, 1, 10)
	source := mustStoreOrigin(t, "https://source.example")
	alpha := mustStoreOrigin(t, "https://alpha.example")
	bravo := mustStoreOrigin(t, "https://bravo.example")

	if err := discoveryStore.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	firstRun := beginAutomaticAdmissionRun(t, discoveryStore, source, 1)
	if _, err := discoveryStore.RecordDiscoveryForRun(ctx, firstRun, source, []discovery.Candidate{
		{Origin: bravo, Kind: discovery.KindLink},
		{Origin: alpha, Kind: discovery.KindLink},
	}); err != nil {
		t.Fatal(err)
	}

	pending, err := discoveryStore.PendingAutomaticCandidates(ctx, firstRun)
	if err != nil {
		t.Fatal(err)
	}
	if err := discoveryStore.AdmitAutomaticCandidates(ctx, firstRun, pending); err != nil {
		t.Fatal(err)
	}
	if got := automaticSourceNames(t, ctx, pool); !reflect.DeepEqual(
		got,
		[]string{alpha.String()},
	) {
		t.Fatalf("first run automatic sources = %#v", got)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM verification_queue`); err != nil {
		t.Fatal(err)
	}
	secondRun := beginAutomaticAdmissionRun(t, discoveryStore, source, 1)
	pending, err = discoveryStore.PendingAutomaticCandidates(ctx, secondRun)
	if err != nil {
		t.Fatal(err)
	}
	if err := discoveryStore.AdmitAutomaticCandidates(ctx, secondRun, pending); err != nil {
		t.Fatal(err)
	}
	if got := automaticSourceNames(t, ctx, pool); !reflect.DeepEqual(
		got,
		[]string{alpha.String(), bravo.String()},
	) {
		t.Fatalf("second run automatic sources = %#v", got)
	}
}

func TestAutomaticAdmissionBuildsBoundedDurableRunBatches(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	discoveryStore := newAutomaticAdmissionStore(t, pool, 2, 200)
	source := mustStoreOrigin(t, "https://source.example")
	if err := discoveryStore.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	firstRun := beginAutomaticAdmissionRun(t, discoveryStore, source, 2)
	candidates := make([]discovery.Candidate, 100)
	for index := range candidates {
		candidates[index] = discovery.Candidate{
			Origin: mustStoreOrigin(
				t,
				fmt.Sprintf("https://candidate-%03d.example", index),
			),
			Kind: discovery.KindLink,
		}
	}
	if _, err := discoveryStore.RecordDiscoveryForRun(
		ctx,
		firstRun,
		source,
		candidates,
	); err != nil {
		t.Fatal(err)
	}

	firstBatch, err := discoveryStore.PendingAutomaticCandidates(ctx, firstRun)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstBatch) != 2 ||
		firstBatch[0].Origin != candidates[0].Origin ||
		firstBatch[1].Origin != candidates[1].Origin {
		t.Fatalf("first bounded batch = %#v", firstBatch)
	}
	if err := discoveryStore.AdmitAutomaticCandidates(ctx, firstRun, firstBatch); err != nil {
		t.Fatal(err)
	}

	secondRun := beginAutomaticAdmissionRun(t, discoveryStore, source, 2)
	secondBatch, err := discoveryStore.PendingAutomaticCandidates(ctx, secondRun)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondBatch) != 2 ||
		secondBatch[0].Origin != candidates[2].Origin ||
		secondBatch[1].Origin != candidates[3].Origin {
		t.Fatalf("later bounded batch = %#v", secondBatch)
	}

	var associated, attributed int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM crawl_run_discovery_candidates WHERE run_id = $1),
			(SELECT count(*) FROM crawl_run_automatic_admission_batches
			 WHERE admission_run_id = $2 AND discovered_run_id = $1)
	`, int64(firstRun), int64(secondRun)).Scan(&associated, &attributed); err != nil {
		t.Fatal(err)
	}
	if associated != 100 || attributed != 2 {
		t.Fatalf("associated/attributed = %d/%d, want 100/2", associated, attributed)
	}
}

func TestAutomaticAdmissionProbesRedirectsWithoutPromotingThem(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	discoveryStore := newAutomaticAdmissionStore(t, pool, 1, 10)
	source := mustStoreOrigin(t, "https://source.example")
	link := mustStoreOrigin(t, "https://link.example")
	redirect := mustStoreOrigin(t, "https://redirect.example")
	if err := discoveryStore.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	runID := beginAutomaticAdmissionRun(t, discoveryStore, source, 1)
	if _, err := discoveryStore.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{
			{Origin: redirect, Kind: discovery.KindRedirect},
			{Origin: link, Kind: discovery.KindLink},
		},
	); err != nil {
		t.Fatal(err)
	}
	pending, err := discoveryStore.PendingAutomaticCandidates(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Origin != link {
		t.Fatalf("automatic candidates = %#v, want link only", pending)
	}
	if err := discoveryStore.AdmitAutomaticCandidates(ctx, runID, pending); err != nil {
		t.Fatal(err)
	}
	var probes, redirectSources int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM verification_queue
			 WHERE origin IN ($1, $2) AND mode = 'probe'),
			(SELECT count(*) FROM discovery_source_state
			 WHERE source_origin = $2 AND automatically_discovered)
	`, link.String(), redirect.String()).Scan(&probes, &redirectSources); err != nil {
		t.Fatal(err)
	}
	if probes != 2 || redirectSources != 0 {
		t.Fatalf("probes/redirect sources = %d/%d, want 2/0", probes, redirectSources)
	}
}

func TestAutomaticAdmissionKeepsNetworkRejectedEvidenceUnpromoted(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	discoveryStore := newAutomaticAdmissionStore(t, pool, 1, 10)
	source := mustStoreOrigin(t, "https://source.example")
	unsafe := mustStoreOrigin(t, "https://unsafe.example")
	if err := discoveryStore.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	runID := beginAutomaticAdmissionRun(t, discoveryStore, source, 1)
	if _, err := discoveryStore.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{{Origin: unsafe, Kind: discovery.KindLink}},
	); err != nil {
		t.Fatal(err)
	}
	pending, err := discoveryStore.PendingAutomaticCandidates(ctx, runID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("unsafe pending = %#v, %v", pending, err)
	}
	if err := discoveryStore.AdmitAutomaticCandidates(ctx, runID, nil); err != nil {
		t.Fatal(err)
	}
	secondRun := beginAutomaticAdmissionRun(t, discoveryStore, source, 1)
	pending, err = discoveryStore.PendingAutomaticCandidates(ctx, secondRun)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("network-rejected candidate was rebatched: %#v", pending)
	}
	var evidence, automatic int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM discovery_candidates WHERE origin = $1),
			(SELECT count(*) FROM discovery_source_state
			 WHERE source_origin = $1 AND automatically_discovered)
	`, unsafe.String()).Scan(&evidence, &automatic); err != nil {
		t.Fatal(err)
	}
	if evidence != 1 || automatic != 0 {
		t.Fatalf("unsafe evidence/automatic = %d/%d, want 1/0", evidence, automatic)
	}
}

func TestAutomaticAdmissionPromotesRecurringCandidateWithinRunBudget(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	discoveryStore := newAutomaticAdmissionStore(t, pool, 1, 1)
	source := mustStoreOrigin(t, "https://source.example")
	candidate := mustStoreOrigin(t, "https://recurring.example")
	if err := discoveryStore.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	runID := beginAutomaticAdmissionRun(t, discoveryStore, source, 1)
	if _, err := discoveryStore.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{{Origin: candidate, Kind: discovery.KindLink}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO verification_queue (origin, available_at, mode)
		VALUES ($1, statement_timestamp(), 'recurring')
	`, candidate.String()); err != nil {
		t.Fatal(err)
	}
	pending, err := discoveryStore.PendingAutomaticCandidates(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := discoveryStore.AdmitAutomaticCandidates(ctx, runID, pending); err != nil {
		t.Fatal(err)
	}
	var automatic bool
	var mode string
	var promotions int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT automatically_discovered FROM discovery_source_state
			 WHERE source_origin = $1),
			(SELECT mode FROM verification_queue WHERE origin = $1),
			(SELECT automatic_promotions FROM crawl_runs WHERE id = $2)
	`, candidate.String(), int64(runID)).Scan(
		&automatic,
		&mode,
		&promotions,
	); err != nil {
		t.Fatal(err)
	}
	if !automatic || mode != "recurring" || promotions != 1 {
		t.Fatalf(
			"automatic/mode/promotions = %t/%q/%d, want true/recurring/1",
			automatic,
			mode,
			promotions,
		)
	}
}

func TestAutomaticAdmissionEnforcesStrictGlobalProbeCapacity(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	firstStore := newAutomaticAdmissionStore(t, pool, 2, 1)
	secondStore := newAutomaticAdmissionStore(t, pool, 2, 1)
	source := mustStoreOrigin(t, "https://source.example")
	alpha := mustStoreOrigin(t, "https://alpha.example")
	bravo := mustStoreOrigin(t, "https://bravo.example")

	if err := firstStore.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	firstRun := beginAutomaticAdmissionRun(t, firstStore, source, 2)
	if _, err := firstStore.RecordDiscoveryForRun(ctx, firstRun, source, []discovery.Candidate{
		{Origin: bravo, Kind: discovery.KindLink},
		{Origin: alpha, Kind: discovery.KindLink},
	}); err != nil {
		t.Fatal(err)
	}
	secondRun := beginAutomaticAdmissionRun(t, secondStore, source, 2)
	pending, err := firstStore.PendingAutomaticCandidates(ctx, firstRun)
	if err != nil {
		t.Fatal(err)
	}
	secondPending, err := secondStore.PendingAutomaticCandidates(ctx, secondRun)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, admission := range []struct {
		store      *DiscoveryStore
		runID      discovery.CrawlRunID
		candidates []discovery.Candidate
	}{
		{store: firstStore, runID: firstRun, candidates: pending},
		{store: secondStore, runID: secondRun, candidates: secondPending},
	} {
		group.Add(1)
		go func(
			admissionStore *DiscoveryStore,
			runID discovery.CrawlRunID,
			candidates []discovery.Candidate,
		) {
			defer group.Done()
			<-start
			results <- admissionStore.AdmitAutomaticCandidates(ctx, runID, candidates)
		}(admission.store, admission.runID, admission.candidates)
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("concurrent admission error = %v", err)
		}
	}

	var pendingProbes int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM verification_queue WHERE mode = 'probe'`,
	).Scan(&pendingProbes); err != nil {
		t.Fatal(err)
	}
	if pendingProbes != 1 {
		t.Fatalf("pending probes = %d, want 1", pendingProbes)
	}
	if got := automaticSourceNames(t, ctx, pool); !reflect.DeepEqual(
		got,
		[]string{alpha.String()},
	) {
		t.Fatalf("capacity-admitted sources = %#v", got)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM verification_queue`); err != nil {
		t.Fatal(err)
	}
	laterRun := beginAutomaticAdmissionRun(t, firstStore, source, 1)
	later, err := firstStore.PendingAutomaticCandidates(ctx, laterRun)
	if err != nil {
		t.Fatal(err)
	}
	if len(later) != 1 || later[0].Origin != bravo {
		t.Fatalf("capacity-deferred later batch = %#v", later)
	}
	if err := firstStore.AdmitAutomaticCandidates(ctx, laterRun, later); err != nil {
		t.Fatal(err)
	}
	if got := automaticSourceNames(t, ctx, pool); !reflect.DeepEqual(
		got,
		[]string{alpha.String(), bravo.String()},
	) {
		t.Fatalf("later automatic sources = %#v", got)
	}
}

func TestAutomaticAdmissionAllocatesOneBatchUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	firstStore := newAutomaticAdmissionStore(t, pool, 2, 10)
	secondStore := newAutomaticAdmissionStore(t, pool, 2, 10)
	source := mustStoreOrigin(t, "https://source.example")
	if err := firstStore.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	runID := beginAutomaticAdmissionRun(t, firstStore, source, 2)
	if _, err := firstStore.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{
			{Origin: mustStoreOrigin(t, "https://charlie.example"), Kind: discovery.KindLink},
			{Origin: mustStoreOrigin(t, "https://alpha.example"), Kind: discovery.KindLink},
			{Origin: mustStoreOrigin(t, "https://bravo.example"), Kind: discovery.KindLink},
		},
	); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan []discovery.Candidate, 2)
	errorsChannel := make(chan error, 2)
	var group sync.WaitGroup
	for _, admissionStore := range []*DiscoveryStore{firstStore, secondStore} {
		group.Add(1)
		go func(candidateStore *DiscoveryStore) {
			defer group.Done()
			<-start
			pending, err := candidateStore.PendingAutomaticCandidates(ctx, runID)
			results <- pending
			errorsChannel <- err
		}(admissionStore)
	}
	close(start)
	group.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Errorf("PendingAutomaticCandidates() error = %v", err)
		}
	}
	var first []discovery.Candidate
	for pending := range results {
		if first == nil {
			first = pending
			continue
		}
		if !reflect.DeepEqual(pending, first) {
			t.Fatalf("concurrent batches differ: %#v and %#v", first, pending)
		}
	}
	if len(first) != 2 ||
		first[0].Origin.String() != "https://alpha.example" ||
		first[1].Origin.String() != "https://bravo.example" {
		t.Fatalf("concurrent batch = %#v", first)
	}
	var batchCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM crawl_run_automatic_admission_batches
		WHERE admission_run_id = $1
	`, int64(runID)).Scan(&batchCount); err != nil {
		t.Fatal(err)
	}
	if batchCount != 2 {
		t.Fatalf("durable batch count = %d, want 2", batchCount)
	}
}

func TestAutomaticAdmissionValidatesStateAndCandidates(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	disabled, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	if pending, err := disabled.PendingAutomaticCandidates(ctx, 0); err != nil || pending != nil {
		t.Fatalf("disabled pending candidates = %#v, %v", pending, err)
	}
	if err := disabled.AdmitAutomaticCandidates(ctx, 0, nil); err != nil {
		t.Fatalf("disabled admission error = %v", err)
	}
	if _, err := (*DiscoveryStore)(nil).PendingAutomaticCandidates(ctx, 1); err == nil {
		t.Fatal("nil store PendingAutomaticCandidates() error = nil")
	}
	if err := (*DiscoveryStore)(nil).AdmitAutomaticCandidates(ctx, 1, nil); err == nil {
		t.Fatal("nil store AdmitAutomaticCandidates() error = nil")
	}

	enabled := newAutomaticAdmissionStore(t, pool, 1, 1)
	if _, err := enabled.PendingAutomaticCandidates(ctx, 999999); !errors.Is(
		err,
		errUnknownCrawlRun,
	) {
		t.Fatalf("unknown-run pending error = %v", err)
	}
	if err := enabled.AdmitAutomaticCandidates(ctx, 0, nil); !errors.Is(err, errInvalidCrawlRun) {
		t.Fatalf("zero-run admission error = %v", err)
	}
	if err := enabled.AdmitAutomaticCandidates(ctx, 999999, nil); !errors.Is(err, errUnknownCrawlRun) {
		t.Fatalf("unknown-run admission error = %v", err)
	}

	valid := discovery.Candidate{
		Origin: mustStoreOrigin(t, "https://candidate.example"),
		Kind:   discovery.KindLink,
	}
	tests := [][]discovery.Candidate{
		{{Kind: discovery.KindLink}},
		{{Origin: valid.Origin, Kind: 99}},
		{valid, valid},
	}
	for index, candidates := range tests {
		if err := enabled.AdmitAutomaticCandidates(ctx, 1, candidates); !errors.Is(
			err,
			errInvalidDiscoveryCandidate,
		) {
			t.Errorf("invalid candidate set %d error = %v", index, err)
		}
	}

	source := mustStoreOrigin(t, "https://source.example")
	if err := enabled.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	candidate := discovery.Candidate{
		Origin: mustStoreOrigin(t, "https://attributed.example"),
		Kind:   discovery.KindLink,
	}
	if _, err := enabled.RecordDiscoveryForRun(
		ctx,
		0,
		source,
		[]discovery.Candidate{candidate},
	); !errors.Is(err, errInvalidCrawlRun) {
		t.Fatalf("zero-run evidence error = %v", err)
	}
	if _, err := enabled.RecordDiscoveryForRun(
		ctx,
		999999,
		source,
		[]discovery.Candidate{candidate},
	); !errors.Is(err, errUnknownCrawlRun) {
		t.Fatalf("unknown-run evidence error = %v", err)
	}
	runID := beginAutomaticAdmissionRun(t, enabled, source, 1)
	pending, err := enabled.PendingAutomaticCandidates(ctx, runID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("empty run pending = %#v, %v", pending, err)
	}
	if err := enabled.AdmitAutomaticCandidates(ctx, runID, nil); err != nil {
		t.Fatalf("empty admission error = %v", err)
	}
	if err := enabled.FinishCrawl(
		ctx,
		runID,
		discovery.CrawlResult{Source: source},
		discovery.CrawlRunComplete,
		"done",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := enabled.PendingAutomaticCandidates(ctx, runID); !errors.Is(
		err,
		errUnknownCrawlRun,
	) {
		t.Fatalf("finished-run pending error = %v", err)
	}
	if _, err := enabled.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{candidate},
	); !errors.Is(err, errUnknownCrawlRun) {
		t.Fatalf("finished-run evidence error = %v", err)
	}
	if err := enabled.AdmitAutomaticCandidates(ctx, runID, nil); !errors.Is(
		err,
		errUnknownCrawlRun,
	) {
		t.Fatalf("finished-run admission error = %v", err)
	}
}

func TestPendingAutomaticCandidatesRejectsCorruptOrigin(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	discoveryStore := newAutomaticAdmissionStore(t, pool, 1, 1)
	runID := beginAutomaticAdmissionRun(
		t,
		discoveryStore,
		mustStoreOrigin(t, "https://source.example"),
		1,
	)
	if _, err := pool.Exec(ctx, `
		ALTER TABLE discovery_candidates
			DROP CONSTRAINT discovery_candidates_origin_check
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		ALTER TABLE discovery_edges
			DROP CONSTRAINT discovery_edges_distinct_origins_check
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_candidates (
			origin, first_discovered_at, last_discovered_at
		) VALUES ('bad', statement_timestamp(), statement_timestamp())
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_edges (
			source_origin, candidate_origin, kind,
			first_discovered_at, last_discovered_at
		) VALUES (
			'https://source.example', 'bad', 'link',
			statement_timestamp(), statement_timestamp()
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO crawl_run_discovery_candidates (
			run_id, candidate_origin, kind
		) VALUES ($1, 'bad', 'link')
	`, int64(runID)); err != nil {
		t.Fatal(err)
	}
	if _, err := discoveryStore.PendingAutomaticCandidates(ctx, runID); err == nil {
		t.Fatal("PendingAutomaticCandidates() corrupt-origin error = nil")
	}
}

func TestAutomaticAdmissionRechecksExactBlockPolicy(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	discoveryStore := newAutomaticAdmissionStore(t, pool, 1, 1)
	source := mustStoreOrigin(t, "https://source.example")
	candidate := discovery.Candidate{
		Origin: mustStoreOrigin(t, "https://blocked.example"),
		Kind:   discovery.KindLink,
	}
	if err := discoveryStore.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	runID := beginAutomaticAdmissionRun(t, discoveryStore, source, 1)
	if _, err := discoveryStore.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{candidate},
	); err != nil {
		t.Fatal(err)
	}
	pending, err := discoveryStore.PendingAutomaticCandidates(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pre-block pending candidates = %#v, want one", pending)
	}
	if err := discoveryStore.SetCrawlBlocked(ctx, candidate.Origin, true); err != nil {
		t.Fatal(err)
	}
	if err := discoveryStore.AdmitAutomaticCandidates(
		ctx,
		runID,
		pending,
	); err != nil {
		t.Fatal(err)
	}
	var automatic, queued bool
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT automatically_discovered FROM discovery_source_state WHERE source_origin = $1),
			EXISTS (SELECT 1 FROM verification_queue WHERE origin = $1)
	`, candidate.Origin.String()).Scan(&automatic, &queued); err != nil {
		t.Fatal(err)
	}
	if automatic || queued {
		t.Fatalf("blocked candidate automatic/queued = %t/%t", automatic, queued)
	}
}

func newAutomaticAdmissionStore(
	t *testing.T,
	pool *pgxpool.Pool,
	maxPromotions int,
	maxPending int,
) *DiscoveryStore {
	t.Helper()
	discoveryStore, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{
			Enabled:                      true,
			MaxAutomaticPromotionsPerRun: maxPromotions,
			MaxPendingProbes:             maxPending,
		},
	)
	if err != nil {
		t.Fatalf("construct automatic discovery store: %v", err)
	}
	return discoveryStore
}

func beginAutomaticAdmissionRun(
	t *testing.T,
	discoveryStore *DiscoveryStore,
	source origin.Origin,
	maxPromotions int,
) discovery.CrawlRunID {
	t.Helper()
	runID, err := discoveryStore.BeginCrawl(
		context.Background(),
		source,
		discovery.CrawlConfig{
			MaxDepth:                     1,
			MaxPages:                     1,
			MaxPageBytes:                 1,
			RedirectLimit:                1,
			PageTimeout:                  time.Second,
			MaxAutomaticPromotionsPerRun: maxPromotions,
		},
	)
	if err != nil {
		t.Fatalf("BeginCrawl() error = %v", err)
	}
	return runID
}

func candidatesExcept(
	candidates []discovery.Candidate,
	excluded string,
) []discovery.Candidate {
	filtered := make([]discovery.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Origin.String() != excluded {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func automaticSourceNames(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT source_origin
		FROM discovery_source_state
		WHERE automatically_discovered
		ORDER BY source_origin
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

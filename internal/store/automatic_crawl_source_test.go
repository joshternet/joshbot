package store

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
)

func TestRecordDiscoveryPromotesEveryAutomaticSource(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	source := mustStoreOrigin(t, "https://source.example")

	store, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{Enabled: true},
	)
	if err != nil {
		t.Fatalf("construct discovery store: %v", err)
	}

	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatalf("AddCrawlSeed() error = %v, want nil", err)
	}

	candidates := []discovery.Candidate{
		{
			Origin: mustStoreOrigin(t, "https://charlie.example"),
			Kind:   discovery.KindLink,
		},
		{
			Origin: mustStoreOrigin(t, "https://alpha.example"),
			Kind:   discovery.KindLink,
		},
		{
			Origin: mustStoreOrigin(t, "https://bravo.example"),
			Kind:   discovery.KindRedirect,
		},
	}

	if _, err := store.RecordDiscovery(
		ctx,
		source,
		candidates,
	); err != nil {
		t.Fatalf("RecordDiscovery() error = %v, want nil", err)
	}
	admitResolvedAutomaticCandidates(t, ctx, store, source)

	rows, err := pool.Query(
		ctx,
		`
			SELECT source_origin
			FROM discovery_source_state
			WHERE automatically_discovered
			ORDER BY source_origin
		`,
	)
	if err != nil {
		t.Fatalf("query automatic sources: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var stored string
		if err := rows.Scan(&stored); err != nil {
			t.Fatalf("scan automatic source: %v", err)
		}
		got = append(got, stored)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate automatic sources: %v", err)
	}

	want := []string{
		"https://alpha.example",
		"https://charlie.example",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("automatic sources = %#v, want %#v", got, want)
	}

	claimed, found, err := store.ClaimDiscoverySource(
		ctx,
		time.Hour,
	)
	if err != nil {
		t.Fatalf("ClaimDiscoverySource() error = %v, want nil", err)
	}
	if !found {
		t.Fatal("ClaimDiscoverySource() found = false, want true")
	}
	if claimed.String() != "https://alpha.example" {
		t.Errorf(
			"claimed source = %q, want %q",
			claimed.String(),
			"https://alpha.example",
		)
	}
}

func TestRecordDiscoveryLeavesAutomaticSourcesDisabledByDefault(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	source := mustStoreOrigin(t, "https://source.example")
	candidate := mustStoreOrigin(t, "https://candidate.example")

	store, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf("construct discovery store: %v", err)
	}

	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatalf("AddCrawlSeed() error = %v, want nil", err)
	}

	if _, err := store.RecordDiscovery(
		ctx,
		source,
		[]discovery.Candidate{{
			Origin: candidate,
			Kind:   discovery.KindLink,
		}},
	); err != nil {
		t.Fatalf("RecordDiscovery() error = %v, want nil", err)
	}

	var count int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM discovery_source_state
			WHERE automatically_discovered
		`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count automatic sources: %v", err)
	}

	if count != 0 {
		t.Errorf("automatic source count = %d, want 0", count)
	}
}

func TestRecordDiscoveryRetainsExcludedPlatformsWithoutSchedulingThem(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	seed := mustStoreOrigin(t, "https://seed.example")
	discoveryStore, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{
			Enabled:              true,
			ExcludedHostSuffixes: "blogspot.com,wordpress.com",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := discoveryStore.AddCrawlSeed(ctx, seed); err != nil {
		t.Fatal(err)
	}
	candidates := []discovery.Candidate{
		{Origin: mustStoreOrigin(t, "https://writer.blogspot.com"), Kind: discovery.KindLink},
		{Origin: mustStoreOrigin(t, "https://wordpress.com"), Kind: discovery.KindLink},
		{Origin: mustStoreOrigin(t, "https://independent.example"), Kind: discovery.KindLink},
	}
	if _, err := discoveryStore.RecordDiscovery(ctx, seed, candidates); err != nil {
		t.Fatal(err)
	}
	admitResolvedAutomaticCandidates(t, ctx, discoveryStore, seed)

	var candidatesStored, edgesStored, automaticSources, queued int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM discovery_candidates),
		(SELECT count(*) FROM discovery_edges),
		(SELECT count(*) FROM discovery_source_state WHERE automatically_discovered),
		(SELECT count(*) FROM verification_queue)
	`).Scan(&candidatesStored, &edgesStored, &automaticSources, &queued); err != nil {
		t.Fatal(err)
	}
	if candidatesStored != 3 || edgesStored != 3 || automaticSources != 1 || queued != 1 {
		t.Fatalf("stored candidates/edges/automatic/queued = %d/%d/%d/%d, want 3/3/1/1", candidatesStored, edgesStored, automaticSources, queued)
	}

	excluded := candidates[0].Origin
	if err := discoveryStore.AddCrawlSeed(ctx, excluded); err != nil {
		t.Fatal(err)
	}
	var seeded bool
	if err := pool.QueryRow(ctx, `SELECT seeded FROM discovery_source_state WHERE source_origin=$1`, excluded.String()).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	if !seeded {
		t.Fatal("curated excluded platform seed was not retained")
	}
}

func TestConcurrentDiscoveryPromotesEveryAutomaticSource(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	discoveryStore, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{Enabled: true},
	)
	if err != nil {
		t.Fatalf("construct discovery store: %v", err)
	}

	sources := []origin.Origin{
		mustStoreOrigin(t, "https://source-a.example"),
		mustStoreOrigin(t, "https://source-b.example"),
	}
	candidates := []origin.Origin{
		mustStoreOrigin(t, "https://candidate-a.example"),
		mustStoreOrigin(t, "https://candidate-b.example"),
	}

	for _, source := range sources {
		if err := discoveryStore.AddCrawlSeed(
			ctx,
			source,
		); err != nil {
			t.Fatalf("AddCrawlSeed() error = %v, want nil", err)
		}
	}

	results := make(chan error, len(sources))
	var group sync.WaitGroup
	for index := range sources {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, recordErr := discoveryStore.RecordDiscovery(
				ctx,
				sources[index],
				[]discovery.Candidate{{
					Origin: candidates[index],
					Kind:   discovery.KindLink,
				}},
			)
			results <- recordErr
		}(index)
	}
	group.Wait()
	close(results)

	for err := range results {
		if err != nil {
			t.Errorf("RecordDiscovery() error = %v, want nil", err)
		}
	}
	admitResolvedAutomaticCandidates(t, ctx, discoveryStore, sources[0])

	var count int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM discovery_source_state
			WHERE automatically_discovered
		`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count automatic sources: %v", err)
	}

	if count != 2 {
		t.Errorf("automatic source count = %d, want 2", count)
	}
}

func TestAutomaticSourceContinuesDiscoveryWithoutCreatingParticipants(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	seed := mustStoreOrigin(t, "https://z-seed.example")
	first := mustStoreOrigin(t, "https://alpha.example")
	second := mustStoreOrigin(t, "https://bravo.example")

	discoveryStore, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{Enabled: true},
	)
	if err != nil {
		t.Fatalf("construct discovery store: %v", err)
	}
	if err := discoveryStore.AddCrawlSeed(ctx, seed); err != nil {
		t.Fatalf("AddCrawlSeed() error = %v, want nil", err)
	}

	if _, err := discoveryStore.RecordDiscovery(
		ctx,
		seed,
		[]discovery.Candidate{{Origin: first, Kind: discovery.KindLink}},
	); err != nil {
		t.Fatalf("record seed discovery: %v", err)
	}
	admitResolvedAutomaticCandidates(t, ctx, discoveryStore, seed)

	claimed, found, err := discoveryStore.ClaimDiscoverySource(ctx, time.Hour)
	if err != nil {
		t.Fatalf("claim automatic source: %v", err)
	}
	if !found || claimed != first {
		t.Fatalf(
			"claimed source = %q, %v, want %q, true",
			claimed.String(),
			found,
			first.String(),
		)
	}

	if _, err := discoveryStore.RecordDiscovery(
		ctx,
		first,
		[]discovery.Candidate{{Origin: second, Kind: discovery.KindLink}},
	); err != nil {
		t.Fatalf("record automatic-source discovery: %v", err)
	}
	admitResolvedAutomaticCandidates(t, ctx, discoveryStore, first)

	var automaticCount int
	err = pool.QueryRow(
		ctx,
		"SELECT count(*) FROM discovery_source_state WHERE automatically_discovered",
	).Scan(&automaticCount)
	if err != nil {
		t.Fatalf("count automatic sources: %v", err)
	}
	if automaticCount != 2 {
		t.Errorf("automatic source count = %d, want 2", automaticCount)
	}

	var verificationOriginCount int
	err = pool.QueryRow(
		ctx,
		"SELECT count(*) FROM origins WHERE origin IN ($1, $2)",
		first.String(),
		second.String(),
	).Scan(&verificationOriginCount)
	if err != nil {
		t.Fatalf("count verification origins: %v", err)
	}
	if verificationOriginCount != 0 {
		t.Errorf(
			"verification origin count = %d, want 0",
			verificationOriginCount,
		)
	}
}

func TestRecordDiscoveryDoesNotPromoteRedirectTargets(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	source := mustStoreOrigin(t, "https://source.example")
	redirect := mustStoreOrigin(t, "https://redirect.example")

	discoveryStore, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{Enabled: true},
	)
	if err != nil {
		t.Fatalf("construct discovery store: %v", err)
	}
	if err := discoveryStore.AddCrawlSeed(ctx, source); err != nil {
		t.Fatalf("add crawl seed: %v", err)
	}

	result, err := discoveryStore.RecordDiscovery(
		ctx,
		source,
		[]discovery.Candidate{{
			Origin: redirect,
			Kind:   discovery.KindRedirect,
		}},
	)
	if err != nil {
		t.Fatalf("record redirect discovery: %v", err)
	}
	admitResolvedAutomaticCandidates(t, ctx, discoveryStore, source)
	if result.Accepted != 1 {
		t.Errorf("record result = %#v, want one accepted candidate", result)
	}

	var automatic, queued bool
	err = pool.QueryRow(
		ctx,
		`SELECT
			EXISTS (
				SELECT 1
				FROM discovery_source_state
				WHERE source_origin = $1
					AND automatically_discovered
			),
			EXISTS (
				SELECT 1
				FROM verification_queue
				WHERE origin = $1
					AND mode = 'probe'
			)`,
		redirect.String(),
	).Scan(&automatic, &queued)
	if err != nil {
		t.Fatalf("read redirect crawl classification: %v", err)
	}
	if automatic {
		t.Error("redirect target became an automatic crawl source")
	}
	if !queued {
		t.Error("redirect target was not retained as a verification candidate")
	}
}

func TestAutomaticCrawlingWaitsForProbeQueueBackpressure(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	seed := mustStoreOrigin(t, "https://seed.example")
	candidate := mustStoreOrigin(t, "https://candidate.example")

	discoveryStore, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{
			Enabled:          true,
			MaxPendingProbes: 1,
		},
	)
	if err != nil {
		t.Fatalf("construct discovery store: %v", err)
	}
	if err := discoveryStore.AddCrawlSeed(ctx, seed); err != nil {
		t.Fatalf("add crawl seed: %v", err)
	}

	claimed, found, err := discoveryStore.ClaimDiscoverySource(ctx, time.Hour)
	if err != nil || !found || claimed != seed {
		t.Fatalf(
			"claim seed = %q, %v, %v, want %q, true, nil",
			claimed.String(),
			found,
			err,
			seed.String(),
		)
	}

	if _, err := discoveryStore.RecordDiscovery(
		ctx,
		seed,
		[]discovery.Candidate{{
			Origin: candidate,
			Kind:   discovery.KindLink,
		}},
	); err != nil {
		t.Fatalf("record discovery: %v", err)
	}
	admitResolvedAutomaticCandidates(t, ctx, discoveryStore, seed)

	claimed, found, err = discoveryStore.ClaimDiscoverySource(ctx, time.Hour)
	if err != nil {
		t.Fatalf("claim under backpressure: %v", err)
	}
	if found || claimed.String() != "" {
		t.Errorf(
			"claim under backpressure = %q, %v, want zero, false",
			claimed.String(),
			found,
		)
	}

	if _, err := pool.Exec(ctx, "DELETE FROM verification_queue"); err != nil {
		t.Fatalf("drain probe queue: %v", err)
	}

	claimed, found, err = discoveryStore.ClaimDiscoverySource(ctx, time.Hour)
	if err != nil || !found || claimed != candidate {
		t.Errorf(
			"claim after drain = %q, %v, %v, want %q, true, nil",
			claimed.String(),
			found,
			err,
			candidate.String(),
		)
	}
}

func admitResolvedAutomaticCandidates(
	t *testing.T,
	ctx context.Context,
	discoveryStore *DiscoveryStore,
	source origin.Origin,
) {
	t.Helper()
	runID := beginAutomaticAdmissionRun(
		t,
		discoveryStore,
		source,
		discoveryStore.automatic.maxAutomaticPromotionsPerRun(),
	)
	pending, err := discoveryStore.PendingAutomaticCandidates(ctx, runID)
	if err != nil {
		t.Fatalf("PendingAutomaticCandidates() error = %v", err)
	}
	if err := discoveryStore.AdmitAutomaticCandidates(ctx, runID, pending); err != nil {
		t.Fatalf("AdmitAutomaticCandidates() error = %v", err)
	}
}

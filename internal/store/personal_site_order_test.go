package store

import (
	"context"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/personalsite"
)

func TestPendingAutomaticCandidatesOrdersByPersonalSiteScore(
	t *testing.T,
) {
	pool := newStoreTestPool(t)
	ctx := context.Background()

	store := newAutomaticAdmissionStore(t, pool, 5, 1000)
	store.automatic.PersonalSiteScoring = personalsite.ScoringModeOrder

	source := mustStoreOrigin(t, "https://source.example")
	low := mustStoreOrigin(t, "https://low.example")
	high := mustStoreOrigin(t, "https://high.example")
	mid := mustStoreOrigin(t, "https://mid.example")
	oldUnassessed := mustStoreOrigin(t, "https://old.example")
	newerUnassessed := mustStoreOrigin(t, "https://newer.example")

	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}

	runID := beginAutomaticAdmissionRun(t, store, source, 5)
	if _, err := store.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{
			{Origin: low, Kind: discovery.KindLink},
			{Origin: high, Kind: discovery.KindLink},
			{Origin: mid, Kind: discovery.KindLink},
			{Origin: oldUnassessed, Kind: discovery.KindLink},
			{Origin: newerUnassessed, Kind: discovery.KindLink},
		},
	); err != nil {
		t.Fatalf("RecordDiscoveryForRun() error = %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE discovery_candidates
		SET first_discovered_at = $2
		WHERE origin = $1
	`, oldUnassessed.String(), time.Now().UTC().Add(-48*time.Hour)); err != nil {
		t.Fatalf("age old candidate: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE discovery_candidates
		SET first_discovered_at = $2
		WHERE origin = $1
	`, newerUnassessed.String(), time.Now().UTC().Add(-1*time.Hour)); err != nil {
		t.Fatalf("age newer candidate: %v", err)
	}

	upsertScore := func(candidate origin.Origin, score int) {
		t.Helper()
		if err := store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{
				Outcome: personalsite.QualificationAssessed,
				Assessment: personalsite.Assessment{
					Score:   score,
					Reasons: []personalsite.ReasonCode{},
					Version: personalsite.AssessmentVersion,
				},
			},
		); err != nil {
			t.Fatalf(
				"UpsertPersonalSiteAssessment(%s) error = %v",
				candidate.String(),
				err,
			)
		}
	}
	upsertScore(low, 10)
	upsertScore(mid, 50)
	upsertScore(high, 90)

	pending, err := store.PendingAutomaticCandidates(ctx, runID)
	if err != nil {
		t.Fatalf("PendingAutomaticCandidates() error = %v", err)
	}
	if len(pending) != 5 {
		t.Fatalf("pending len = %d, want 5", len(pending))
	}

	origins := make([]string, 0, len(pending))
	for _, item := range pending {
		origins = append(origins, item.Origin.String())
	}

	if !containsString(origins, oldUnassessed.String()) {
		t.Fatalf(
			"pending origins = %#v, want exploration pick %s",
			origins,
			oldUnassessed.String(),
		)
	}
	if !containsString(origins, high.String()) ||
		!containsString(origins, mid.String()) {
		t.Fatalf(
			"pending origins = %#v, want high and mid scored picks",
			origins,
		)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

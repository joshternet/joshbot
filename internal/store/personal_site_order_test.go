package store

import (
	"context"
	"testing"

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
	store.testBackfillPersonalSiteAssessments = false

	source := mustStoreOrigin(t, "https://source.example")
	low := mustStoreOrigin(t, "https://low.example")
	high := mustStoreOrigin(t, "https://high.example")
	mid := mustStoreOrigin(t, "https://mid.example")
	unassessed := mustStoreOrigin(t, "https://unassessed.example")
	zero := mustStoreOrigin(t, "https://zero.example")

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
			{Origin: unassessed, Kind: discovery.KindLink},
			{Origin: zero, Kind: discovery.KindLink},
		},
	); err != nil {
		t.Fatalf("RecordDiscoveryForRun() error = %v", err)
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
	upsertScore(zero, 0)

	pending, err := store.PendingAutomaticCandidates(ctx, runID)
	if err != nil {
		t.Fatalf("PendingAutomaticCandidates() error = %v", err)
	}
	if len(pending) != 4 {
		t.Fatalf("pending len = %d, want 4 assessed only", len(pending))
	}

	wantOrder := []string{
		high.String(),
		mid.String(),
		low.String(),
		zero.String(),
	}
	for index, want := range wantOrder {
		if pending[index].Origin.String() != want {
			t.Fatalf(
				"pending[%d] = %s, want %s (full=%#v)",
				index,
				pending[index].Origin.String(),
				want,
				pendingOrigins(pending),
			)
		}
	}
}

func pendingOrigins(pending []discovery.Candidate) []string {
	origins := make([]string, 0, len(pending))
	for _, item := range pending {
		origins = append(origins, item.Origin.String())
	}
	return origins
}

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/personalsite"
)

func TestPersonalSiteQualifyClaimFence(
	t *testing.T,
) {
	pool := newStoreTestPool(t)
	ctx := context.Background()
	store := newAutomaticAdmissionStore(t, pool, 10, 1000)
	store.testBackfillPersonalSiteAssessments = false

	source := mustStoreOrigin(t, "https://source.example")
	candidate := mustStoreOrigin(t, "https://claim.example")
	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	runID := beginAutomaticAdmissionRun(t, store, source, 10)
	if _, err := store.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{{
			Origin: candidate,
			Kind:   discovery.KindLink,
		}},
	); err != nil {
		t.Fatal(err)
	}

	claims, err := store.ClaimPersonalSiteQualifications(
		ctx,
		"worker-a",
		5,
	)
	if err != nil {
		t.Fatalf("claim error = %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("claims = %#v", claims)
	}

	second, err := store.ClaimPersonalSiteQualifications(
		ctx,
		"worker-b",
		5,
	)
	if err != nil {
		t.Fatalf("second claim error = %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second claims = %#v, want none while leased", second)
	}

	stale := claims[0]
	if _, err := pool.Exec(ctx, `
		UPDATE discovery_candidates
		SET personal_site_claim_expires_at = $2
		WHERE origin = $1
	`, candidate.String(), time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	reclaimed, err := store.ClaimPersonalSiteQualifications(
		ctx,
		"worker-b",
		5,
	)
	if err != nil {
		t.Fatalf("reclaim error = %v", err)
	}
	if len(reclaimed) != 1 {
		t.Fatalf("reclaimed = %#v", reclaimed)
	}

	err = store.CompletePersonalSiteQualification(
		ctx,
		stale,
		personalsite.Qualification{
			Outcome: personalsite.QualificationAssessed,
			Assessment: personalsite.Assessment{
				Score:   99,
				Reasons: []personalsite.ReasonCode{},
				Version: personalsite.AssessmentVersion,
			},
		},
	)
	if !errors.Is(err, ErrPersonalSiteClaimLost) {
		t.Fatalf("stale complete error = %v, want claim lost", err)
	}

	if err := store.CompletePersonalSiteQualification(
		ctx,
		reclaimed[0],
		personalsite.Qualification{
			Outcome: personalsite.QualificationAssessed,
			Assessment: personalsite.Assessment{
				Score:   42,
				Reasons: []personalsite.ReasonCode{personalsite.ReasonFeed},
				Version: personalsite.AssessmentVersion,
			},
		},
	); err != nil {
		t.Fatalf("fresh complete error = %v", err)
	}

	var score int
	if err := pool.QueryRow(ctx, `
		SELECT personal_site_score
		FROM discovery_candidates
		WHERE origin = $1
	`, candidate.String()).Scan(&score); err != nil {
		t.Fatal(err)
	}
	if score != 42 {
		t.Fatalf("score = %d, want 42 from fenced writer", score)
	}
}

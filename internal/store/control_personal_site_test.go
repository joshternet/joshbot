package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/control"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/personalsite"
)

func TestControlPersonalSiteOverrideAndRescore(
	t *testing.T,
) {
	pool := newStoreTestPool(t)
	ctx := context.Background()
	discoveryStore := newAutomaticAdmissionStore(t, pool, 10, 1000)
	discoveryStore.testBackfillPersonalSiteAssessments = false
	controlStore, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}

	source := mustStoreOrigin(t, "https://source.example")
	candidate := mustStoreOrigin(t, "https://personal.example")
	if err := discoveryStore.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	if _, err := discoveryStore.RecordDiscovery(
		ctx,
		source,
		[]discovery.Candidate{{
			Origin: candidate,
			Kind:   discovery.KindLink,
		}},
	); err != nil {
		t.Fatal(err)
	}
	if err := discoveryStore.UpsertPersonalSiteAssessment(
		ctx,
		candidate,
		personalsite.Qualification{
			Outcome: personalsite.QualificationAssessed,
			Assessment: personalsite.Assessment{
				Score:   10,
				Reasons: []personalsite.ReasonCode{personalsite.ReasonFeed},
				Version: personalsite.AssessmentVersion,
			},
		},
	); err != nil {
		t.Fatal(err)
	}

	audit := control.Audit{
		OccurredAt: time.Now().UTC(),
		Action:     "personal-site.override.set",
		Target:     candidate.String(),
		Caller:     "test",
		Actor:      "operator-api",
		Result:     control.ResultSuccess,
		Reason:     "prioritize indie site",
	}
	if err := controlStore.SetPersonalSiteOverride(
		ctx,
		candidate.String(),
		90,
		0,
		audit,
	); err != nil {
		t.Fatalf("set override error = %v", err)
	}

	var (
		calculated int
		override   int
		revision   int64
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			personal_site_score,
			personal_site_override_score,
			personal_site_override_revision
		FROM discovery_candidates
		WHERE origin = $1
	`, candidate.String()).Scan(&calculated, &override, &revision); err != nil {
		t.Fatal(err)
	}
	if calculated != 10 || override != 90 || revision != 1 {
		t.Fatalf(
			"calculated=%d override=%d revision=%d",
			calculated,
			override,
			revision,
		)
	}

	conflict := audit
	if err := controlStore.SetPersonalSiteOverride(
		ctx,
		candidate.String(),
		80,
		0,
		conflict,
	); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("conflict error = %v", err)
	}

	rescoreAudit := control.Audit{
		OccurredAt: time.Now().UTC(),
		Action:     "personal-site.rescore.request",
		Target:     candidate.String(),
		Caller:     "test",
		Actor:      "operator-api",
		Result:     control.ResultSuccess,
		Reason:     "recheck",
	}
	status, err := controlStore.RequestPersonalSiteRescore(
		ctx,
		candidate.String(),
		rescoreAudit,
	)
	if err != nil || status != "ok" {
		t.Fatalf("rescore = %q, %v", status, err)
	}
	status, err = controlStore.RequestPersonalSiteRescore(
		ctx,
		candidate.String(),
		rescoreAudit,
	)
	if err != nil || status != "already_pending" {
		t.Fatalf("rescore dedupe = %q, %v", status, err)
	}

	claims, err := discoveryStore.ClaimPersonalSiteQualifications(
		ctx,
		"worker-a",
		5,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].Origin.String() != candidate.String() {
		t.Fatalf("claims = %#v", claims)
	}

	activeStatus, err := controlStore.RequestPersonalSiteRescore(
		ctx,
		candidate.String(),
		rescoreAudit,
	)
	if err != nil || activeStatus != "active" {
		t.Fatalf("rescore while claimed = %q, %v", activeStatus, err)
	}
}

func TestControlPersonalSiteOverrideRequiresAssessed(
	t *testing.T,
) {
	pool := newStoreTestPool(t)
	ctx := context.Background()
	discoveryStore := newAutomaticAdmissionStore(t, pool, 10, 1000)
	discoveryStore.testBackfillPersonalSiteAssessments = false
	controlStore, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}

	source := mustStoreOrigin(t, "https://source.example")
	candidate := mustStoreOrigin(t, "https://pending.example")
	if err := discoveryStore.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	if _, err := discoveryStore.RecordDiscovery(
		ctx,
		source,
		[]discovery.Candidate{{
			Origin: candidate,
			Kind:   discovery.KindLink,
		}},
	); err != nil {
		t.Fatal(err)
	}

	err = controlStore.SetPersonalSiteOverride(
		ctx,
		candidate.String(),
		90,
		0,
		control.Audit{
			OccurredAt: time.Now().UTC(),
			Action:     "personal-site.override.set",
			Target:     candidate.String(),
			Caller:     "test",
			Actor:      "operator-api",
			Result:     control.ResultSuccess,
			Reason:     "boost unassessed",
		},
	)
	if !errors.Is(err, control.ErrForbidden) {
		t.Fatalf("override unassessed error = %v, want forbidden", err)
	}
}

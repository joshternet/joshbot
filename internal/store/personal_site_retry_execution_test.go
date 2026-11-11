package store

import (
	"context"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/personalsite"
)

func TestPersonalSiteQualifySkipsFutureRetriesAndProgressesOthers(
	t *testing.T,
) {
	pool := newStoreTestPool(t)
	ctx := context.Background()
	store := newAutomaticAdmissionStore(t, pool, 10, 1000)
	store.testBackfillPersonalSiteAssessments = false

	source := mustStoreOrigin(t, "https://source.example")
	dueOrigin := mustStoreOrigin(t, "https://due.example")
	deferredOrigin := mustStoreOrigin(t, "https://deferred.example")
	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	runID := beginAutomaticAdmissionRun(t, store, source, 10)
	if _, err := store.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{
			{Origin: dueOrigin, Kind: discovery.KindLink},
			{Origin: deferredOrigin, Kind: discovery.KindLink},
		},
	); err != nil {
		t.Fatal(err)
	}

	future := time.Now().UTC().Add(48 * time.Hour)
	if _, err := pool.Exec(ctx, `
		UPDATE discovery_candidates
		SET
			personal_site_qualification_outcome = 'http_error',
			personal_site_http_status = 503,
			personal_site_retry_class = 'transient',
			personal_site_failure_count = 1,
			personal_site_next_attempt_at = $2,
			personal_site_assessed_at = statement_timestamp()
		WHERE origin = $1
	`, deferredOrigin.String(), future); err != nil {
		t.Fatal(err)
	}

	claims, err := store.ClaimPersonalSiteQualifications(
		ctx,
		"worker-retry",
		5,
	)
	if err != nil {
		t.Fatalf("claim error = %v", err)
	}
	if len(claims) != 1 || claims[0].Origin.String() != dueOrigin.String() {
		t.Fatalf("claims = %#v, want only %s", claims, dueOrigin)
	}

	if err := store.CompletePersonalSiteQualification(
		ctx,
		claims[0],
		personalsite.Qualification{
			Outcome:    personalsite.QualificationTimeout,
			HTTPStatus: 0,
		},
	); err != nil {
		t.Fatalf("complete error = %v", err)
	}

	var nextAttempt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT personal_site_next_attempt_at
		FROM discovery_candidates
		WHERE origin = $1
	`, dueOrigin.String()).Scan(&nextAttempt); err != nil {
		t.Fatal(err)
	}
	if !nextAttempt.After(time.Now().UTC().Add(12 * time.Hour)) {
		t.Fatalf("next_attempt_at = %v, want >= 24h ladder", nextAttempt)
	}

	second, err := store.ClaimPersonalSiteQualifications(
		ctx,
		"worker-retry",
		5,
	)
	if err != nil {
		t.Fatalf("second claim error = %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second claims = %#v, want none while both deferred", second)
	}
}

package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/personalsite"
)

func TestUpsertPersonalSiteAssessmentReplaceSemantics(t *testing.T) {
	pool := newStoreTestPool(t)
	ctx := context.Background()

	store, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{Enabled: true},
	)
	if err != nil {
		t.Fatalf("NewDiscoveryStoreWithAutomaticCrawling() error = %v", err)
	}

	source := mustParseOrigin(t, "https://source.example")
	candidate := mustParseOrigin(t, "https://personal.example")
	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatalf("AddCrawlSeed() error = %v", err)
	}
	if _, err := store.RecordDiscovery(
		ctx,
		source,
		[]discovery.Candidate{{
			Origin: candidate,
			Kind:   discovery.KindLink,
		}},
	); err != nil {
		t.Fatalf("RecordDiscovery() error = %v", err)
	}

	first := personalsite.Qualification{
		Outcome: personalsite.QualificationAssessed,
		Assessment: personalsite.Assessment{
			Score:   40,
			Reasons: []personalsite.ReasonCode{personalsite.ReasonAboutLink},
			Version: personalsite.AssessmentVersion,
		},
	}
	if err := store.UpsertPersonalSiteAssessment(
		ctx,
		candidate,
		first,
	); err != nil {
		t.Fatalf("UpsertPersonalSiteAssessment() error = %v", err)
	}

	second := personalsite.Qualification{
		Outcome: personalsite.QualificationAssessed,
		Assessment: personalsite.Assessment{
			Score: 85,
			Reasons: []personalsite.ReasonCode{
				personalsite.ReasonFeed,
				personalsite.ReasonPersonSchema,
			},
			Version: personalsite.AssessmentVersion,
		},
	}
	if err := store.UpsertPersonalSiteAssessment(
		ctx,
		candidate,
		second,
	); err != nil {
		t.Fatalf("UpsertPersonalSiteAssessment() replace error = %v", err)
	}

	var (
		score    int
		version  int
		outcome  string
		rawCodes []byte
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			personal_site_score,
			personal_site_assessment_version,
			personal_site_qualification_outcome,
			personal_site_reason_codes
		FROM discovery_candidates
		WHERE origin = $1
	`, candidate.String()).Scan(
		&score,
		&version,
		&outcome,
		&rawCodes,
	); err != nil {
		t.Fatalf("QueryRow() error = %v", err)
	}
	if score != 85 || version != personalsite.AssessmentVersion ||
		outcome != string(personalsite.QualificationAssessed) {
		t.Fatalf(
			"persisted assessment = %d/%d/%s, want 85/%d/assessed",
			score,
			version,
			outcome,
			personalsite.AssessmentVersion,
		)
	}

	var codes []string
	if err := json.Unmarshal(rawCodes, &codes); err != nil {
		t.Fatalf("Unmarshal reason codes error = %v", err)
	}
	if len(codes) != 2 {
		t.Fatalf("reason codes = %#v, want 2", codes)
	}

	unavailable := personalsite.Qualification{
		Outcome: personalsite.QualificationRobotsDenied,
	}
	if err := store.UpsertPersonalSiteAssessment(
		ctx,
		candidate,
		unavailable,
	); err != nil {
		t.Fatalf(
			"UpsertPersonalSiteAssessment(unavailable) error = %v",
			err,
		)
	}

	var (
		nullableScore   *int
		nullableVersion *int
		nullableCodes   []byte
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			personal_site_score,
			personal_site_assessment_version,
			personal_site_qualification_outcome,
			personal_site_reason_codes
		FROM discovery_candidates
		WHERE origin = $1
	`, candidate.String()).Scan(
		&nullableScore,
		&nullableVersion,
		&outcome,
		&nullableCodes,
	); err != nil {
		t.Fatalf("QueryRow(unavailable) error = %v", err)
	}
	if nullableScore != nil || nullableVersion != nil ||
		nullableCodes != nil ||
		outcome != string(personalsite.QualificationRobotsDenied) {
		t.Fatalf(
			"unavailable row = score=%v version=%v outcome=%s codes=%v",
			nullableScore,
			nullableVersion,
			outcome,
			nullableCodes,
		)
	}
}

func TestUpsertPersonalSiteAssessmentRejectsInvalidScore(t *testing.T) {
	pool := newStoreTestPool(t)
	ctx := context.Background()

	store, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf("NewDiscoveryStore() error = %v", err)
	}

	source := mustParseOrigin(t, "https://source.example")
	candidate := mustParseOrigin(t, "https://candidate.example")
	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatalf("AddCrawlSeed() error = %v", err)
	}
	if _, err := store.RecordDiscovery(
		ctx,
		source,
		[]discovery.Candidate{{
			Origin: candidate,
			Kind:   discovery.KindLink,
		}},
	); err != nil {
		t.Fatalf("RecordDiscovery() error = %v", err)
	}

	err = store.UpsertPersonalSiteAssessment(
		ctx,
		candidate,
		personalsite.Qualification{
			Outcome: personalsite.QualificationAssessed,
			Assessment: personalsite.Assessment{
				Score:   101,
				Version: 1,
			},
		},
	)
	if err == nil {
		t.Fatal("UpsertPersonalSiteAssessment(101) error = nil")
	}
}

func TestUpsertPersonalSiteAssessmentUnknownCandidate(t *testing.T) {
	pool := newStoreTestPool(t)
	store, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf("NewDiscoveryStore() error = %v", err)
	}

	err = store.UpsertPersonalSiteAssessment(
		context.Background(),
		mustParseOrigin(t, "https://missing.example"),
		personalsite.Qualification{
			Outcome: personalsite.QualificationUnavailable,
		},
	)
	if !errors.Is(err, errInvalidDiscoveryCandidate) {
		t.Fatalf(
			"error = %v, want %v",
			err,
			errInvalidDiscoveryCandidate,
		)
	}
}

func TestCandidatesNeedingPersonalSiteAssessment(t *testing.T) {
	pool := newStoreTestPool(t)
	ctx := context.Background()

	store, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{Enabled: true},
	)
	if err != nil {
		t.Fatalf("NewDiscoveryStoreWithAutomaticCrawling() error = %v", err)
	}

	source := mustParseOrigin(t, "https://source.example")
	first := mustParseOrigin(t, "https://a.example")
	second := mustParseOrigin(t, "https://b.example")
	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatalf("AddCrawlSeed() error = %v", err)
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

	if err := store.UpsertPersonalSiteAssessment(
		ctx,
		first,
		personalsite.Qualification{
			Outcome: personalsite.QualificationAssessed,
			Assessment: personalsite.Assessment{
				Score:   50,
				Reasons: []personalsite.ReasonCode{},
				Version: personalsite.AssessmentVersion,
			},
		},
	); err != nil {
		t.Fatalf("UpsertPersonalSiteAssessment() error = %v", err)
	}

	pending, err := store.CandidatesNeedingPersonalSiteAssessment(ctx, 10)
	if err != nil {
		t.Fatalf(
			"CandidatesNeedingPersonalSiteAssessment() error = %v",
			err,
		)
	}
	if len(pending) != 1 || pending[0] != second {
		t.Fatalf(
			"pending = %#v, want [%s]",
			pending,
			second.String(),
		)
	}
}

func mustParseOrigin(t *testing.T, raw string) origin.Origin {
	t.Helper()

	parsed, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf("origin.Parse(%q) error = %v", raw, err)
	}
	return parsed
}

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/database"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/personalsite"
)

func TestUpsertPersonalSiteAssessmentWithoutDatabase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	candidate := mustStoreOrigin(t, "https://personal.example")

	t.Run("nil_store", func(t *testing.T) {
		t.Parallel()
		var store *DiscoveryStore
		err := store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{
				Outcome: personalsite.QualificationUnavailable,
			},
		)
		if !errors.Is(err, errDiscoveryStoreUnavailable) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("invalid_origin", func(t *testing.T) {
		t.Parallel()
		store := personalSiteUnitStore(t, &discoveryUnitDatabase{}, true)
		err := store.UpsertPersonalSiteAssessment(
			ctx,
			origin.Origin{},
			personalsite.Qualification{
				Outcome: personalsite.QualificationUnavailable,
			},
		)
		if !errors.Is(err, errInvalidOrigin) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("invalid_outcome", func(t *testing.T) {
		t.Parallel()
		store := personalSiteUnitStore(t, &discoveryUnitDatabase{}, true)
		err := store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{Outcome: "nope"},
		)
		if err == nil {
			t.Fatal("error = nil")
		}
	})

	t.Run("score_out_of_range", func(t *testing.T) {
		t.Parallel()
		store := personalSiteUnitStore(t, &discoveryUnitDatabase{}, true)
		err := store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{
				Outcome: personalsite.QualificationAssessed,
				Assessment: personalsite.Assessment{
					Score: -1,
				},
			},
		)
		if err == nil {
			t.Fatal("error = nil")
		}
	})

	t.Run("assessed_defaults_and_exec_error", func(t *testing.T) {
		t.Parallel()
		database := &discoveryUnitDatabase{
			execResults: []discoveryUnitExecResult{
				{err: errors.New("exec failed")},
			},
		}
		store := personalSiteUnitStore(t, database, true)
		err := store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{
				Outcome: personalsite.QualificationAssessed,
				Assessment: personalsite.Assessment{
					Score:   40,
					Reasons: nil,
					Version: 0,
				},
			},
		)
		if err == nil || !strings.Contains(err.Error(), "exec failed") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("rows_affected_zero", func(t *testing.T) {
		t.Parallel()
		database := &discoveryUnitDatabase{
			execResults: []discoveryUnitExecResult{
				{tag: pgconn.NewCommandTag("UPDATE 0")},
			},
		}
		store := personalSiteUnitStore(t, database, true)
		err := store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{
				Outcome: personalsite.QualificationRobotsDenied,
			},
		)
		if !errors.Is(err, errInvalidDiscoveryCandidate) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		database := &discoveryUnitDatabase{
			execResults: []discoveryUnitExecResult{
				{tag: pgconn.NewCommandTag("UPDATE 1")},
			},
		}
		store := personalSiteUnitStore(t, database, true)
		err := store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{
				Outcome: personalsite.QualificationAssessed,
				Assessment: personalsite.Assessment{
					Score: 77,
					Reasons: []personalsite.ReasonCode{
						personalsite.ReasonFeed,
					},
					Version: personalsite.AssessmentVersion,
				},
			},
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestCandidatesNeedingPersonalSiteAssessmentWithoutDatabase(
	t *testing.T,
) {
	t.Parallel()

	ctx := context.Background()

	t.Run("nil_store", func(t *testing.T) {
		t.Parallel()
		var store *DiscoveryStore
		_, err := store.CandidatesNeedingPersonalSiteAssessment(ctx, 1)
		if !errors.Is(err, errDiscoveryStoreUnavailable) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("non_positive_limit", func(t *testing.T) {
		t.Parallel()
		store := personalSiteUnitStore(t, &discoveryUnitDatabase{}, true)
		origins, err := store.CandidatesNeedingPersonalSiteAssessment(ctx, 0)
		if err != nil || origins != nil {
			t.Fatalf("origins=%v err=%v", origins, err)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		t.Parallel()
		store := personalSiteUnitStore(t, &discoveryUnitDatabase{}, false)
		origins, err := store.CandidatesNeedingPersonalSiteAssessment(ctx, 5)
		if err != nil || origins != nil {
			t.Fatalf("origins=%v err=%v", origins, err)
		}
	})

	t.Run("exclusions_query_error", func(t *testing.T) {
		t.Parallel()
		database := &discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{err: errors.New("avoid rules failed")},
				},
			},
		}
		store := personalSiteUnitStore(t, database, true)
		_, err := store.CandidatesNeedingPersonalSiteAssessment(ctx, 5)
		if err == nil {
			t.Fatal("error = nil")
		}
	})

	t.Run("query_error", func(t *testing.T) {
		t.Parallel()
		database := &discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{rows: &discoveryUnitRows{}},
					{err: errors.New("candidate query failed")},
				},
			},
		}
		store := personalSiteUnitStore(t, database, true)
		_, err := store.CandidatesNeedingPersonalSiteAssessment(ctx, 5)
		if err == nil || !strings.Contains(err.Error(), "candidate query failed") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("scan_and_parse", func(t *testing.T) {
		t.Parallel()
		database := &discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{rows: &discoveryUnitRows{}},
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{values: []any{"https://a.example"}},
								{values: []any{"not a url"}},
							},
						},
					},
				},
			},
		}
		store := personalSiteUnitStore(t, database, true)
		_, err := store.CandidatesNeedingPersonalSiteAssessment(ctx, 5)
		if err == nil {
			t.Fatal("error = nil, want parse failure")
		}
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		database := &discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{rows: &discoveryUnitRows{}},
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{values: []any{"https://a.example"}},
								{values: []any{"https://b.example"}},
							},
						},
					},
				},
			},
		}
		store := personalSiteUnitStore(t, database, true)
		origins, err := store.CandidatesNeedingPersonalSiteAssessment(ctx, 5)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if len(origins) != 2 {
			t.Fatalf("origins = %#v", origins)
		}
	})

	t.Run("rows_err", func(t *testing.T) {
		t.Parallel()
		database := &discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{rows: &discoveryUnitRows{}},
					{
						rows: &discoveryUnitRows{
							err: errors.New("row iteration failed"),
						},
					},
				},
			},
		}
		store := personalSiteUnitStore(t, database, true)
		_, err := store.CandidatesNeedingPersonalSiteAssessment(ctx, 5)
		if err == nil {
			t.Fatal("error = nil")
		}
	})

	t.Run("scan_error", func(t *testing.T) {
		t.Parallel()
		database := &discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{rows: &discoveryUnitRows{}},
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{err: errors.New("scan failed")},
							},
						},
					},
				},
			},
		}
		store := personalSiteUnitStore(t, database, true)
		_, err := store.CandidatesNeedingPersonalSiteAssessment(ctx, 5)
		if err == nil {
			t.Fatal("error = nil")
		}
	})
}

func TestAutomaticAdmissionAllocationQuery(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	patterns := []string{"example.com"}

	offSQL, offArgs := automaticAdmissionAllocationQuery(
		personalsite.ScoringModeOff,
		41,
		patterns,
		10,
		now,
	)
	if !strings.Contains(offSQL, "ORDER BY origin") {
		t.Fatalf("off SQL missing origin order: %s", offSQL)
	}
	if len(offArgs) != 4 {
		t.Fatalf("off args = %#v", offArgs)
	}

	orderSQL, orderArgs := automaticAdmissionAllocationQuery(
		personalsite.ScoringModeOrder,
		41,
		patterns,
		10,
		now,
	)
	if !strings.Contains(orderSQL, "personal_site_score DESC") {
		t.Fatalf("order SQL missing score order: %s", orderSQL)
	}
	if !strings.Contains(orderSQL, "first_discovered_at ASC") {
		t.Fatalf("order SQL missing exploration order: %s", orderSQL)
	}
	if len(orderArgs) != 5 {
		t.Fatalf("order args = %#v", orderArgs)
	}
	if orderArgs[2] != 1 { // exploration for batch 10
		t.Fatalf("exploration arg = %#v, want 1", orderArgs[2])
	}
}

func TestValidPersonalSiteOutcome(t *testing.T) {
	t.Parallel()

	if validPersonalSiteOutcome("nope") {
		t.Fatal("invalid outcome accepted")
	}
	if !validPersonalSiteOutcome(personalsite.QualificationAssessed) {
		t.Fatal("assessed rejected")
	}
}

func personalSiteUnitStore(
	t *testing.T,
	database database.Postgres,
	enabled bool,
) *DiscoveryStore {
	t.Helper()

	store, err := newDiscoveryStoreWithConfig(
		database,
		databaseQueueClock{},
		AutomaticCrawlConfig{Enabled: enabled},
	)
	if err != nil {
		t.Fatalf("newDiscoveryStoreWithConfig() error = %v", err)
	}
	return store
}

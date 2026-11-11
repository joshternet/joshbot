package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/personalsite"
)

func personalSiteClaimStore(
	t *testing.T,
	database *discoveryPolicyUnitDatabase,
	enabled bool,
) *DiscoveryStore {
	t.Helper()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	store, err := newDiscoveryStoreWithConfig(
		database,
		claimDiscoveryClock{now: now},
		AutomaticCrawlConfig{Enabled: enabled},
	)
	if err != nil {
		t.Fatalf("newDiscoveryStoreWithConfig() error = %v", err)
	}
	return store
}

func TestClaimPersonalSiteQualificationsWithoutDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

	t.Run("nil_store", func(t *testing.T) {
		t.Parallel()
		var store *DiscoveryStore
		_, err := store.ClaimPersonalSiteQualifications(ctx, "owner", 1)
		if !errors.Is(err, errDiscoveryStoreUnavailable) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("invalid_owner", func(t *testing.T) {
		t.Parallel()
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
			},
			true,
		)
		_, err := store.ClaimPersonalSiteQualifications(ctx, "", 1)
		if !errors.Is(err, errInvalidPersonalSiteClaimOwner) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("disabled_or_non_positive", func(t *testing.T) {
		t.Parallel()
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
			},
			false,
		)
		claims, err := store.ClaimPersonalSiteQualifications(ctx, "owner-1", 1)
		if err != nil || claims != nil {
			t.Fatalf("claims=%v err=%v", claims, err)
		}
		enabled := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
			},
			true,
		)
		claims, err = enabled.ClaimPersonalSiteQualifications(ctx, "owner-1", 0)
		if err != nil || claims != nil {
			t.Fatalf("limit0 claims=%v err=%v", claims, err)
		}
	})

	t.Run("exclusions_error", func(t *testing.T) {
		t.Parallel()
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{
					queryResults: []discoveryUnitQueryResult{
						{err: errors.New("avoid failed")},
					},
				},
			},
			true,
		)
		_, err := store.ClaimPersonalSiteQualifications(ctx, "owner-1", 2)
		if err == nil {
			t.Fatal("error = nil")
		}
	})

	t.Run("success_scan_and_parse", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			queryResults: []discoveryUnitQueryResult{{
				rows: &discoveryUnitRows{
					rows: []discoveryUnitRow{
						{values: []any{
							"https://a.example",
							int64(2),
							now.Add(2 * time.Minute),
						}},
						{values: []any{
							"not a url",
							int64(3),
							now.Add(2 * time.Minute),
						}},
					},
				},
			}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{
					queryResults: []discoveryUnitQueryResult{
						{rows: &discoveryUnitRows{}},
					},
				},
				beginTx: tx,
			},
			true,
		)
		_, err := store.ClaimPersonalSiteQualifications(ctx, "owner-1", 2)
		if err == nil || !strings.Contains(err.Error(), "parse personal site claim") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			queryResults: []discoveryUnitQueryResult{{
				rows: &discoveryUnitRows{
					rows: []discoveryUnitRow{{
						values: []any{
							"https://a.example",
							int64(4),
							now.Add(2 * time.Minute),
						},
					}},
				},
			}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{
					queryResults: []discoveryUnitQueryResult{
						{rows: &discoveryUnitRows{}},
					},
				},
				beginTx: tx,
			},
			true,
		)
		claims, err := store.ClaimPersonalSiteQualifications(ctx, "owner-1", 1)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if len(claims) != 1 ||
			claims[0].Origin.String() != "https://a.example" ||
			claims[0].Generation != 4 ||
			claims[0].Owner != "owner-1" {
			t.Fatalf("claims = %#v", claims)
		}
	})

	t.Run("query_error", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			queryResults: []discoveryUnitQueryResult{
				{err: errors.New("claim query failed")},
			},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{
					queryResults: []discoveryUnitQueryResult{
						{rows: &discoveryUnitRows{}},
					},
				},
				beginTx: tx,
			},
			true,
		)
		_, err := store.ClaimPersonalSiteQualifications(ctx, "owner-1", 1)
		if err == nil || !strings.Contains(err.Error(), "claim personal site") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("scan_error", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			queryResults: []discoveryUnitQueryResult{{
				rows: &discoveryUnitRows{
					rows: []discoveryUnitRow{
						{err: errors.New("scan failed")},
					},
				},
			}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{
					queryResults: []discoveryUnitQueryResult{
						{rows: &discoveryUnitRows{}},
					},
				},
				beginTx: tx,
			},
			true,
		)
		_, err := store.ClaimPersonalSiteQualifications(ctx, "owner-1", 1)
		if err == nil {
			t.Fatal("error = nil")
		}
	})

	t.Run("rows_err", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			queryResults: []discoveryUnitQueryResult{{
				rows: &discoveryUnitRows{
					err: errors.New("rows failed"),
				},
			}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{
					queryResults: []discoveryUnitQueryResult{
						{rows: &discoveryUnitRows{}},
					},
				},
				beginTx: tx,
			},
			true,
		)
		_, err := store.ClaimPersonalSiteQualifications(ctx, "owner-1", 1)
		if err == nil {
			t.Fatal("error = nil")
		}
	})

	t.Run("clock_error", func(t *testing.T) {
		t.Parallel()
		store, err := newDiscoveryStoreWithConfig(
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{
					queryResults: []discoveryUnitQueryResult{
						{rows: &discoveryUnitRows{}},
					},
				},
				beginTx: &discoveryPolicyUnitTx{},
			},
			claimDiscoveryClock{err: errors.New("clock failed")},
			AutomaticCrawlConfig{Enabled: true},
		)
		if err != nil {
			t.Fatalf("store error = %v", err)
		}
		_, err = store.ClaimPersonalSiteQualifications(ctx, "owner-1", 1)
		if err == nil || !strings.Contains(err.Error(), "personal site clock") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestCompletePersonalSiteQualificationWithoutDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	candidate := mustStoreOrigin(t, "https://personal.example")
	claim := PersonalSiteQualifyClaim{
		Origin:     candidate,
		Generation: 2,
		Owner:      "owner-1",
	}

	t.Run("validation", func(t *testing.T) {
		t.Parallel()
		var store *DiscoveryStore
		err := store.CompletePersonalSiteQualification(
			ctx,
			claim,
			personalsite.Qualification{Outcome: personalsite.QualificationUnavailable},
		)
		if !errors.Is(err, errDiscoveryStoreUnavailable) {
			t.Fatalf("error = %v", err)
		}

		unit := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
			},
			true,
		)
		err = unit.CompletePersonalSiteQualification(
			ctx,
			PersonalSiteQualifyClaim{},
			personalsite.Qualification{Outcome: personalsite.QualificationUnavailable},
		)
		if !errors.Is(err, errInvalidPersonalSiteClaimOwner) {
			t.Fatalf("invalid claim error = %v", err)
		}
		err = unit.CompletePersonalSiteQualification(
			ctx,
			claim,
			personalsite.Qualification{Outcome: "nope"},
		)
		if err == nil {
			t.Fatal("invalid outcome accepted")
		}
	})

	t.Run("claim_lost", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{{err: pgx.ErrNoRows}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               tx,
			},
			true,
		)
		err := store.CompletePersonalSiteQualification(
			ctx,
			claim,
			personalsite.Qualification{Outcome: personalsite.QualificationUnavailable},
		)
		if !errors.Is(err, ErrPersonalSiteClaimLost) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("load_error", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{{err: errors.New("load failed")}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               tx,
			},
			true,
		)
		err := store.CompletePersonalSiteQualification(
			ctx,
			claim,
			personalsite.Qualification{Outcome: personalsite.QualificationUnavailable},
		)
		if err == nil || !strings.Contains(err.Error(), "load personal site claim") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("persist_and_complete", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{{
				values: []any{(*string)(nil), 0, (*int)(nil), (*int)(nil)},
			}},
			execResults: []discoveryUnitExecResult{{
				tag: pgconn.NewCommandTag("UPDATE 1"),
			}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               tx,
			},
			true,
		)
		err := store.CompletePersonalSiteQualification(
			ctx,
			claim,
			personalsite.Qualification{
				Outcome: personalsite.QualificationAssessed,
				Assessment: personalsite.Assessment{
					Score:   40,
					Version: personalsite.AssessmentVersion,
					Reasons: []personalsite.ReasonCode{
						personalsite.ReasonAboutLink,
					},
				},
			},
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("exec_zero_rows", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{{
				values: []any{(*string)(nil), 0, (*int)(nil), (*int)(nil)},
			}},
			execResults: []discoveryUnitExecResult{{
				tag: pgconn.NewCommandTag("UPDATE 0"),
			}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               tx,
			},
			true,
		)
		err := store.CompletePersonalSiteQualification(
			ctx,
			claim,
			personalsite.Qualification{Outcome: personalsite.QualificationUnavailable},
		)
		if !errors.Is(err, ErrPersonalSiteClaimLost) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("exec_error", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{{
				values: []any{(*string)(nil), 0, (*int)(nil), (*int)(nil)},
			}},
			execResults: []discoveryUnitExecResult{{
				err: errors.New("update failed"),
			}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               tx,
			},
			true,
		)
		err := store.CompletePersonalSiteQualification(
			ctx,
			claim,
			personalsite.Qualification{Outcome: personalsite.QualificationUnavailable},
		)
		if err == nil {
			t.Fatal("error = nil")
		}
	})

	t.Run("invalid_assessed_score", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{{
				values: []any{(*string)(nil), 0, (*int)(nil), (*int)(nil)},
			}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               tx,
			},
			true,
		)
		err := store.CompletePersonalSiteQualification(
			ctx,
			claim,
			personalsite.Qualification{
				Outcome: personalsite.QualificationAssessed,
				Assessment: personalsite.Assessment{
					Score: 101,
				},
			},
		)
		if err == nil {
			t.Fatal("error = nil")
		}
	})

	t.Run("clock_error", func(t *testing.T) {
		t.Parallel()
		store, err := newDiscoveryStoreWithConfig(
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               &discoveryPolicyUnitTx{},
			},
			claimDiscoveryClock{err: errors.New("clock failed")},
			AutomaticCrawlConfig{Enabled: true},
		)
		if err != nil {
			t.Fatalf("store error = %v", err)
		}
		err = store.CompletePersonalSiteQualification(
			ctx,
			claim,
			personalsite.Qualification{Outcome: personalsite.QualificationUnavailable},
		)
		if err == nil || !strings.Contains(err.Error(), "personal site clock") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestUpsertPersonalSiteAssessmentTransactionPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	candidate := mustStoreOrigin(t, "https://personal.example")

	t.Run("success_failure_path", func(t *testing.T) {
		t.Parallel()
		prior := "transient"
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{{
				values: []any{&prior, 2},
			}},
			execResults: []discoveryUnitExecResult{{
				tag: pgconn.NewCommandTag("UPDATE 1"),
			}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               tx,
			},
			true,
		)
		err := store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{
				Outcome:    personalsite.QualificationHTTPError,
				HTTPStatus: 503,
			},
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("missing_row", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{{err: pgx.ErrNoRows}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               tx,
			},
			true,
		)
		err := store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{Outcome: personalsite.QualificationUnavailable},
		)
		if !errors.Is(err, errInvalidDiscoveryCandidate) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("zero_rows_updated", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{{
				values: []any{(*string)(nil), 0},
			}},
			execResults: []discoveryUnitExecResult{{
				tag: pgconn.NewCommandTag("UPDATE 0"),
			}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               tx,
			},
			true,
		)
		err := store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{Outcome: personalsite.QualificationUnavailable},
		)
		if !errors.Is(err, errInvalidDiscoveryCandidate) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("assessed_with_defaults", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{{
				values: []any{(*string)(nil), 0},
			}},
			execResults: []discoveryUnitExecResult{{
				tag: pgconn.NewCommandTag("UPDATE 1"),
			}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               tx,
			},
			true,
		)
		err := store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{
				Outcome: personalsite.QualificationAssessed,
				Assessment: personalsite.Assessment{
					Score: 12,
				},
			},
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("clock_error", func(t *testing.T) {
		t.Parallel()
		store, err := newDiscoveryStoreWithConfig(
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               &discoveryPolicyUnitTx{},
			},
			claimDiscoveryClock{err: errors.New("clock failed")},
			AutomaticCrawlConfig{Enabled: true},
		)
		if err != nil {
			t.Fatalf("store error = %v", err)
		}
		err = store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{Outcome: personalsite.QualificationUnavailable},
		)
		if err == nil || !strings.Contains(err.Error(), "personal site clock") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("load_error", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{{err: errors.New("load failed")}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               tx,
			},
			true,
		)
		err := store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{Outcome: personalsite.QualificationUnavailable},
		)
		if err == nil || !strings.Contains(err.Error(), "load personal site row") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("exec_error", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{{
				values: []any{(*string)(nil), 0},
			}},
			execResults: []discoveryUnitExecResult{{
				err: errors.New("update failed"),
			}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               tx,
			},
			true,
		)
		err := store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{Outcome: personalsite.QualificationUnavailable},
		)
		if err == nil || !strings.Contains(err.Error(), "upsert personal site assessment") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("persist_state_error", func(t *testing.T) {
		t.Parallel()
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{{
				values: []any{(*string)(nil), 0},
			}},
		}
		store := personalSiteClaimStore(
			t,
			&discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginTx:               tx,
			},
			true,
		)
		err := store.UpsertPersonalSiteAssessment(
			ctx,
			candidate,
			personalsite.Qualification{
				Outcome: personalsite.QualificationAssessed,
				Assessment: personalsite.Assessment{
					Score: 101,
				},
			},
		)
		if err == nil || !strings.Contains(err.Error(), "out of range") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestPersonalSitePersistStateAndBackfill(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

	_, _, _, _, _, _, _, _, err := personalSitePersistState(
		personalsite.Qualification{
			Outcome: personalsite.QualificationAssessed,
			Assessment: personalsite.Assessment{
				Score: -5,
			},
		},
		nil,
		0,
		now,
	)
	if err == nil {
		t.Fatal("out of range accepted")
	}

	prior := string(personalsite.RetryClassTransient)
	_, _, _, _, status, next, count, class, err := personalSitePersistState(
		personalsite.Qualification{
			Outcome:    personalsite.QualificationHTTPError,
			HTTPStatus: 429,
			RetryAfter: 90 * time.Second,
		},
		&prior,
		1,
		now,
	)
	if err != nil || status == nil || *status != 429 || next == nil || count != 1 || class == nil {
		t.Fatalf(
			"failure persist status=%v next=%v count=%d class=%v err=%v",
			status,
			next,
			count,
			class,
			err,
		)
	}

	store := personalSiteClaimStore(
		t,
		&discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				execResults: []discoveryUnitExecResult{{
					tag: pgconn.NewCommandTag("UPDATE 3"),
				}},
			},
		},
		true,
	)
	if err := store.backfillPersonalSiteAssessmentsForTests(context.Background()); err != nil {
		t.Fatalf("backfill error = %v", err)
	}

	failing := personalSiteClaimStore(
		t,
		&discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				execResults: []discoveryUnitExecResult{{
					err: errors.New("backfill failed"),
				}},
			},
		},
		true,
	)
	if err := failing.backfillPersonalSiteAssessmentsForTests(context.Background()); err == nil {
		t.Fatal("backfill error = nil")
	}

	pendingStore := personalSiteClaimStore(
		t,
		&discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				execResults: []discoveryUnitExecResult{{
					tag: pgconn.NewCommandTag("UPDATE 1"),
				}},
			},
			beginTx: &discoveryPolicyUnitTx{
				discoveryScheduleLockErr: nil,
			},
		},
		true,
	)
	pendingStore.testBackfillPersonalSiteAssessments = true
	_, err = pendingStore.PendingAutomaticCandidates(context.Background(), 0)
	if !errors.Is(err, errInvalidCrawlRun) {
		t.Fatalf("pending invalid run = %v", err)
	}
}

func TestPendingAutomaticCandidatesBackfillWithoutDatabase(t *testing.T) {
	t.Parallel()

	store := personalSiteClaimStore(
		t,
		&discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				execResults: []discoveryUnitExecResult{{
					err: errors.New("backfill boom"),
				}},
			},
		},
		true,
	)
	store.testBackfillPersonalSiteAssessments = true
	_, err := store.PendingAutomaticCandidates(context.Background(), 7)
	if err == nil || !strings.Contains(err.Error(), "backfill") {
		t.Fatalf("error = %v", err)
	}
}

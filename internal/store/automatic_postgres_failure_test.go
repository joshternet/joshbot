package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/retry"
)

func TestRecordDiscoveryHandlesProbeSchedulingOutcomes(t *testing.T) {
	ctx := context.Background()
	source := mustStoreOrigin(t, "https://source.example")
	candidate := discovery.Candidate{
		Origin: mustStoreOrigin(t, "https://candidate.example"),
		Kind:   discovery.KindLink,
	}
	pool := newStoreTestPool(t)

	t.Run("all excluded", func(t *testing.T) {
		resetAutomaticFixtureData(t, pool)
		store, err := newDiscoveryStoreWithConfig(
			pool,
			databaseQueueClock{},
			AutomaticCrawlConfig{ExcludedHostSuffixes: "candidate.example"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AddCrawlSeed(ctx, source); err != nil {
			t.Fatal(err)
		}
		result, err := store.RecordDiscovery(ctx, source, []discovery.Candidate{candidate})
		if err != nil || result.Accepted != 1 {
			t.Fatalf("RecordDiscovery() = %#v, %v", result, err)
		}
	})

	t.Run("exclusion query", func(t *testing.T) {
		resetAutomaticFixtureData(t, pool)
		store, err := NewDiscoveryStore(pool)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AddCrawlSeed(ctx, source); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(
			ctx,
			"ALTER TABLE crawl_domain_avoid_rules RENAME TO crawl_domain_avoid_rules_unavailable",
		); err != nil {
			t.Fatal(err)
		}
		defer executeSchemaCleanup(
			t,
			pool,
			"ALTER TABLE crawl_domain_avoid_rules_unavailable RENAME TO crawl_domain_avoid_rules",
		)
		result, err := store.RecordDiscovery(ctx, source, []discovery.Candidate{candidate})
		if err == nil || result.Accepted != 0 {
			t.Fatalf("RecordDiscovery() = %#v, %v", result, err)
		}
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM discovery_candidates").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("durable evidence count = %d, want 1", count)
		}
	})

	t.Run("probe insert", func(t *testing.T) {
		resetAutomaticFixtureData(t, pool)
		store, err := NewDiscoveryStore(pool)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AddCrawlSeed(ctx, source); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			CREATE FUNCTION reject_probe_admission() RETURNS trigger
			LANGUAGE plpgsql AS $$
			BEGIN RAISE EXCEPTION 'reject probe admission'; END $$;
			CREATE TRIGGER reject_probe_admission
				BEFORE INSERT ON verification_queue
				FOR EACH STATEMENT EXECUTE FUNCTION reject_probe_admission()
		`); err != nil {
			t.Fatal(err)
		}
		defer executeSchemaCleanup(t, pool, `
			DROP TRIGGER reject_probe_admission ON verification_queue;
			DROP FUNCTION reject_probe_admission()
		`)
		if _, err := store.RecordDiscovery(ctx, source, []discovery.Candidate{candidate}); err == nil ||
			!strings.Contains(err.Error(), "admit discovery probes") {
			t.Fatalf("probe insert error = %v", err)
		}
	})
}

func TestScheduleProbeCandidatesReturnsAdvisoryLockTimeout(t *testing.T) {
	ctx := context.Background()
	pool := newSerialStoreTestPool(t)
	store, err := NewDiscoveryStore(newStoreSiblingPool(t, pool, "250ms"))
	if err != nil {
		t.Fatal(err)
	}
	lockTransaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTestTransaction(t, lockTransaction)
	if _, err := lockTransaction.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock($1)",
		automaticAdmissionAdvisoryLockKey,
	); err != nil {
		t.Fatal(err)
	}
	candidate := discovery.Candidate{
		Origin: mustStoreOrigin(t, "https://locked.example"),
		Kind:   discovery.KindLink,
	}
	if err := store.scheduleProbeCandidates(
		ctx,
		[]discovery.Candidate{candidate},
		time.Now().UTC(),
	); err == nil || !strings.Contains(err.Error(), "lock probe admission") {
		t.Fatalf("advisory lock error = %v", err)
	}
}

func TestPendingAutomaticCandidatesHandlesValidationAndStageFailures(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	t.Run("invalid run", func(t *testing.T) {
		resetAutomaticFixtureData(t, pool)
		store := newAutomaticAdmissionStore(t, pool, 1, 1)
		if _, err := store.PendingAutomaticCandidates(ctx, 0); !errors.Is(err, errInvalidCrawlRun) {
			t.Fatalf("invalid run error = %v", err)
		}
	})

	t.Run("clock", func(t *testing.T) {
		resetAutomaticFixtureData(t, pool)
		store := newAutomaticAdmissionStore(t, pool, 1, 1)
		source := mustStoreOrigin(t, "https://source.example")
		runID := beginAutomaticAdmissionRun(t, store, source, 1)
		clockError := errors.New("automatic admission clock failure")
		store.clock = &discoveryTestClock{err: clockError}
		if _, err := store.PendingAutomaticCandidates(ctx, runID); !errors.Is(err, clockError) {
			t.Fatalf("clock error = %v", err)
		}
	})

	tests := []struct {
		name    string
		setup   string
		cleanup string
		want    string
	}{
		{
			name:    "read run",
			setup:   "ALTER TABLE crawl_runs RENAME TO crawl_runs_unavailable",
			cleanup: "ALTER TABLE crawl_runs_unavailable RENAME TO crawl_runs",
			want:    "read automatic admission run",
		},
		{
			name: "allocate batch",
			setup: `
				CREATE FUNCTION reject_batch_allocation() RETURNS trigger
				LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'reject batch allocation'; END $$;
				CREATE TRIGGER reject_batch_allocation
					BEFORE INSERT ON crawl_run_automatic_admission_batches
					FOR EACH STATEMENT EXECUTE FUNCTION reject_batch_allocation()
			`,
			cleanup: `
				DROP TRIGGER reject_batch_allocation ON crawl_run_automatic_admission_batches;
				DROP FUNCTION reject_batch_allocation()
			`,
			want: "allocate automatic admission batch",
		},
		{
			name:    "count probes",
			setup:   "ALTER TABLE verification_queue RENAME TO verification_queue_unavailable",
			cleanup: "ALTER TABLE verification_queue_unavailable RENAME TO verification_queue",
			want:    "count pending probes",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetAutomaticFixtureData(t, pool)
			_, runID := automaticPendingFixtureInPool(t, pool)
			if _, err := pool.Exec(ctx, test.setup); err != nil {
				t.Fatal(err)
			}
			defer executeSchemaCleanup(t, pool, test.cleanup)
			operationPool := newStoreSiblingPool(t, pool, "")
			defer operationPool.Close()
			store := newAutomaticAdmissionStore(t, operationPool, 1, 2)
			if _, err := store.PendingAutomaticCandidates(ctx, runID); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("stage error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPendingAutomaticCandidatesHandlesDeferredProbeFailure(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := newAutomaticAdmissionStore(t, pool, 1, 2)
	source := mustStoreOrigin(t, "https://source.example")
	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	runID := beginAutomaticAdmissionRun(t, store, source, 1)
	redirect := mustStoreOrigin(t, "https://redirect-only.example")
	if _, err := store.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{{Origin: redirect, Kind: discovery.KindRedirect}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION reject_deferred_probe() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'reject deferred probe'; END $$;
		CREATE TRIGGER reject_deferred_probe
			BEFORE INSERT ON verification_queue
			FOR EACH STATEMENT EXECUTE FUNCTION reject_deferred_probe()
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PendingAutomaticCandidates(ctx, runID); err == nil ||
		!strings.Contains(err.Error(), "admit deferred discovery probes") {
		t.Fatalf("deferred probe error = %v", err)
	}
}

func TestPendingAutomaticCandidatesHandlesListAndCollectionFailures(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := newAutomaticAdmissionStore(t, pool, 1, 1)
	source := mustStoreOrigin(t, "https://source.example")
	runID := beginAutomaticAdmissionRun(t, store, source, 1)
	if _, err := pool.Exec(ctx, `
		ALTER TABLE crawl_run_automatic_admission_batches
			RENAME TO original_automatic_admission_batches
	`); err != nil {
		t.Fatal(err)
	}
	defer executeSchemaCleanup(t, pool, `
		DROP TABLE IF EXISTS crawl_run_automatic_admission_batches;
		ALTER TABLE original_automatic_admission_batches
			RENAME TO crawl_run_automatic_admission_batches
	`)
	for _, test := range []struct {
		name            string
		candidateColumn string
		value           string
		want            string
	}{
		{
			name:            "list",
			candidateColumn: "json",
			value:           "'\"candidate\"'::json",
			want:            "list pending automatic candidates",
		},
		{
			name:            "collection",
			candidateColumn: "text",
			value:           "NULL",
			want:            "collect pending automatic candidates",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `
				DROP TABLE IF EXISTS crawl_run_automatic_admission_batches;
				CREATE TABLE crawl_run_automatic_admission_batches (
					admission_run_id bigint NOT NULL,
					candidate_origin `+test.candidateColumn+`,
					discovered_run_id bigint NOT NULL,
					outcome text NOT NULL DEFAULT 'pending'
				)
			`); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(
				ctx,
				`INSERT INTO crawl_run_automatic_admission_batches (
					admission_run_id, candidate_origin, discovered_run_id
				) VALUES ($1, `+test.value+`, $1)`,
				int64(runID),
			); err != nil {
				t.Fatal(err)
			}
			operationPool := newStoreSiblingPool(t, pool, "")
			defer operationPool.Close()
			operationStore := newAutomaticAdmissionStore(t, operationPool, 1, 1)
			if _, err := operationStore.PendingAutomaticCandidates(ctx, runID); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("pending error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompleteAutomaticCandidatesRejectsInvalidFailureCategory(t *testing.T) {
	_, store, runID, candidate := automaticCompletionFixture(t)
	err := store.CompleteAutomaticCandidates(
		context.Background(),
		runID,
		[]AutomaticCandidateResult{{
			Candidate:       candidate,
			FailureCategory: retry.Category("invalid"),
		}},
	)
	if !errors.Is(err, errInvalidDiscoveryCandidate) {
		t.Fatalf("invalid failure category error = %v", err)
	}
	candidate.Kind = discovery.KindRedirect
	err = store.CompleteAutomaticCandidates(
		context.Background(),
		runID,
		[]AutomaticCandidateResult{{Candidate: candidate}},
	)
	if !errors.Is(err, errInvalidDiscoveryCandidate) {
		t.Fatalf("redirect candidate error = %v", err)
	}
}

func TestCompleteAutomaticCandidatesHandlesClockAndCollectionFailures(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	t.Run("clock", func(t *testing.T) {
		resetAutomaticFixtureData(t, pool)
		store, runID, candidate := automaticCompletionFixtureInPool(t, pool)
		clockError := errors.New("completion clock failure")
		store.clock = &discoveryTestClock{err: clockError}
		err := store.CompleteAutomaticCandidates(
			ctx,
			runID,
			[]AutomaticCandidateResult{{Candidate: candidate}},
		)
		if !errors.Is(err, clockError) {
			t.Fatalf("clock error = %v", err)
		}
	})
	resetAutomaticFixtureData(t, pool)
	store, runID, candidate := automaticCompletionFixtureInPool(t, pool)
	if _, err := pool.Exec(ctx, `
		ALTER TABLE crawl_run_automatic_admission_batches
			DROP CONSTRAINT crawl_run_automatic_admission_batches_candidate_origin_fkey;
		ALTER TABLE crawl_run_automatic_admission_batches
			DROP CONSTRAINT crawl_run_automatic_admission_batches_pkey;
		ALTER TABLE crawl_run_automatic_admission_batches
			ALTER COLUMN candidate_origin DROP NOT NULL
	`); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "scan",
			value: "NULL",
			want:  "collect automatic admission batch",
		},
		{
			name:  "parse",
			value: "'bad'",
			want:  "invalid automatic admission candidate",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := pool.Exec(
				ctx,
				"UPDATE crawl_run_automatic_admission_batches SET candidate_origin = "+test.value,
			); err != nil {
				t.Fatal(err)
			}
			err := store.CompleteAutomaticCandidates(
				ctx,
				runID,
				[]AutomaticCandidateResult{{Candidate: candidate}},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("collection error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestAutomaticAdmissionReturnsAdvisoryLockTimeouts(t *testing.T) {
	ctx := context.Background()
	pool := newSerialStoreTestPool(t)
	for _, test := range []struct {
		name string
		call func(context.Context, *DiscoveryStore, discovery.CrawlRunID, discovery.Candidate) error
		want string
	}{
		{
			name: "pending",
			call: func(callContext context.Context, store *DiscoveryStore, runID discovery.CrawlRunID, _ discovery.Candidate) error {
				_, err := store.PendingAutomaticCandidates(callContext, runID)
				return err
			},
			want: "lock automatic batch allocation",
		},
		{
			name: "completion",
			call: func(callContext context.Context, store *DiscoveryStore, runID discovery.CrawlRunID, candidate discovery.Candidate) error {
				return store.CompleteAutomaticCandidates(
					callContext,
					runID,
					[]AutomaticCandidateResult{{Candidate: candidate}},
				)
			},
			want: "lock automatic admission",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetAutomaticFixtureData(t, pool)
			_, runID, candidate := automaticCompletionFixtureInPool(t, pool)
			store := newAutomaticAdmissionStore(
				t,
				newStoreSiblingPool(t, pool, "250ms"),
				1,
				2,
			)
			lockTransaction, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rollbackTestTransaction(t, lockTransaction)
			if _, err := lockTransaction.Exec(
				ctx,
				"SELECT pg_advisory_xact_lock($1)",
				automaticAdmissionAdvisoryLockKey,
			); err != nil {
				t.Fatal(err)
			}
			if err := test.call(ctx, store, runID, candidate); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("advisory lock error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompleteAutomaticCandidatesHandlesPolicyExistingAndCapacityFailures(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	tests := []struct {
		name    string
		setup   func(*testing.T, *pgxpool.Pool, discovery.Candidate)
		cleanup string
		want    string
	}{
		{
			name: "policy outcome",
			setup: func(t *testing.T, pool *pgxpool.Pool, candidate discovery.Candidate) {
				t.Helper()
				if _, err := pool.Exec(ctx, `
					INSERT INTO discovery_source_state (
						source_origin, crawl_blocked
					) VALUES ($1, true);
					CREATE FUNCTION reject_policy_outcome() RETURNS trigger
					LANGUAGE plpgsql AS $$
					BEGIN RAISE EXCEPTION 'reject policy outcome'; END $$;
					CREATE TRIGGER reject_policy_outcome
						BEFORE UPDATE ON crawl_run_automatic_admission_batches
						FOR EACH ROW EXECUTE FUNCTION reject_policy_outcome()
				`, pgx.QueryExecModeSimpleProtocol, candidate.Origin.String()); err != nil {
					t.Fatal(err)
				}
			},
			cleanup: `
				DROP TRIGGER reject_policy_outcome ON crawl_run_automatic_admission_batches;
				DROP FUNCTION reject_policy_outcome()
			`,
			want: "record policy-deferred admission",
		},
		{
			name: "existing outcome",
			setup: func(t *testing.T, pool *pgxpool.Pool, candidate discovery.Candidate) {
				t.Helper()
				if _, err := pool.Exec(ctx, `
					INSERT INTO discovery_source_state (
						source_origin, automatically_discovered
					) VALUES ($1, true);
					CREATE FUNCTION reject_existing_outcome() RETURNS trigger
					LANGUAGE plpgsql AS $$
					BEGIN RAISE EXCEPTION 'reject existing outcome'; END $$;
					CREATE TRIGGER reject_existing_outcome
						BEFORE UPDATE ON crawl_run_automatic_admission_batches
						FOR EACH ROW EXECUTE FUNCTION reject_existing_outcome()
				`, pgx.QueryExecModeSimpleProtocol, candidate.Origin.String()); err != nil {
					t.Fatal(err)
				}
			},
			cleanup: `
				DROP TRIGGER reject_existing_outcome ON crawl_run_automatic_admission_batches;
				DROP FUNCTION reject_existing_outcome()
			`,
			want: "record existing automatic source",
		},
		{
			name: "capacity outcome",
			setup: func(t *testing.T, pool *pgxpool.Pool, _ discovery.Candidate) {
				t.Helper()
				if _, err := pool.Exec(ctx, `
					INSERT INTO verification_queue (origin, available_at, mode)
					VALUES
						('https://full-one.example', statement_timestamp(), 'probe'),
						('https://full-two.example', statement_timestamp(), 'probe');
					CREATE FUNCTION reject_capacity_outcome() RETURNS trigger
					LANGUAGE plpgsql AS $$
					BEGIN RAISE EXCEPTION 'reject capacity outcome'; END $$;
					CREATE TRIGGER reject_capacity_outcome
						BEFORE UPDATE ON crawl_run_automatic_admission_batches
						FOR EACH ROW EXECUTE FUNCTION reject_capacity_outcome()
				`, pgx.QueryExecModeSimpleProtocol); err != nil {
					t.Fatal(err)
				}
			},
			cleanup: `
				DROP TRIGGER reject_capacity_outcome ON crawl_run_automatic_admission_batches;
				DROP FUNCTION reject_capacity_outcome()
			`,
			want: "record capacity-deferred admission",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetAutomaticFixtureData(t, pool)
			store, runID, candidate := automaticCompletionFixtureInPool(t, pool)
			test.setup(t, pool, candidate)
			defer executeSchemaCleanup(t, pool, test.cleanup)
			err := store.CompleteAutomaticCandidates(
				ctx,
				runID,
				[]AutomaticCandidateResult{{Candidate: candidate}},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("outcome error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompleteAutomaticCandidatesHandlesDatabaseStages(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	tests := []struct {
		name     string
		setup    string
		cleanup  string
		category retry.Category
		want     string
	}{
		{
			name:    "read run",
			setup:   "ALTER TABLE crawl_runs RENAME TO crawl_runs_unavailable",
			cleanup: "ALTER TABLE crawl_runs_unavailable RENAME TO crawl_runs",
			want:    "read automatic admission run",
		},
		{
			name:    "read exclusions",
			setup:   "ALTER TABLE crawl_domain_avoid_rules RENAME TO crawl_domain_avoid_rules_unavailable",
			cleanup: "ALTER TABLE crawl_domain_avoid_rules_unavailable RENAME TO crawl_domain_avoid_rules",
			want:    "read crawl domain avoid rules",
		},
		{
			name:    "count probes",
			setup:   "ALTER TABLE verification_queue RENAME TO verification_queue_unavailable",
			cleanup: "ALTER TABLE verification_queue_unavailable RENAME TO verification_queue",
			want:    "count pending probes",
		},
		{
			name:    "list batch",
			setup:   "ALTER TABLE crawl_run_automatic_admission_batches RENAME TO automatic_batches_unavailable",
			cleanup: "ALTER TABLE automatic_batches_unavailable RENAME TO crawl_run_automatic_admission_batches",
			want:    "collect automatic admission batch",
		},
		{
			name: "read retry streak",
			setup: `
				ALTER TABLE discovery_candidates
					ALTER COLUMN consecutive_failures DROP NOT NULL;
				UPDATE discovery_candidates SET consecutive_failures = NULL
			`,
			cleanup: `
				UPDATE discovery_candidates SET consecutive_failures = 0;
				ALTER TABLE discovery_candidates
					ALTER COLUMN consecutive_failures SET NOT NULL
			`,
			category: retry.CategoryHTTP429,
			want:     "read admission retry streak",
		},
		{
			name:     "defer retry",
			category: retry.CategoryHTTP429,
			setup: `
				CREATE FUNCTION reject_candidate_retry() RETURNS trigger
				LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'reject candidate retry'; END $$;
				CREATE TRIGGER reject_candidate_retry
					BEFORE UPDATE ON discovery_candidates
					FOR EACH ROW EXECUTE FUNCTION reject_candidate_retry()
			`,
			cleanup: `
				DROP TRIGGER reject_candidate_retry ON discovery_candidates;
				DROP FUNCTION reject_candidate_retry()
			`,
			want: "defer transient admission failure",
		},
		{
			name:     "record retry",
			category: retry.CategoryHTTP429,
			setup: `
				CREATE FUNCTION reject_batch_retry() RETURNS trigger
				LANGUAGE plpgsql AS $$
				BEGIN
					IF NEW.outcome = 'retry_deferred' THEN
						RAISE EXCEPTION 'reject batch retry';
					END IF;
					RETURN NEW;
				END $$;
				CREATE TRIGGER reject_batch_retry
					BEFORE UPDATE ON crawl_run_automatic_admission_batches
					FOR EACH ROW EXECUTE FUNCTION reject_batch_retry()
			`,
			cleanup: `
				DROP TRIGGER reject_batch_retry ON crawl_run_automatic_admission_batches;
				DROP FUNCTION reject_batch_retry()
			`,
			want: "record deferred admission outcome",
		},
		{
			name:     "network rejection",
			category: retry.CategoryUnsafeAddress,
			setup: `
				CREATE FUNCTION reject_network_outcome() RETURNS trigger
				LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'reject network outcome'; END $$;
				CREATE TRIGGER reject_network_outcome
					BEFORE UPDATE ON crawl_run_automatic_admission_batches
					FOR EACH ROW EXECUTE FUNCTION reject_network_outcome()
			`,
			cleanup: `
				DROP TRIGGER reject_network_outcome ON crawl_run_automatic_admission_batches;
				DROP FUNCTION reject_network_outcome()
			`,
			want: "record network-rejected admission",
		},
		{
			name: "reset retry",
			setup: `
				CREATE FUNCTION reject_retry_reset() RETURNS trigger
				LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'reject retry reset'; END $$;
				CREATE TRIGGER reject_retry_reset
					BEFORE UPDATE ON discovery_candidates
					FOR EACH ROW EXECUTE FUNCTION reject_retry_reset()
			`,
			cleanup: `
				DROP TRIGGER reject_retry_reset ON discovery_candidates;
				DROP FUNCTION reject_retry_reset()
			`,
			want: "reset admission retry streak",
		},
		{
			name:    "read state",
			setup:   "ALTER TABLE discovery_source_state RENAME TO discovery_source_state_unavailable",
			cleanup: "ALTER TABLE discovery_source_state_unavailable RENAME TO discovery_source_state",
			want:    "read candidate admission state",
		},
		{
			name: "schedule probe",
			setup: `
				CREATE FUNCTION reject_candidate_probe() RETURNS trigger
				LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'reject candidate probe'; END $$;
				CREATE TRIGGER reject_candidate_probe
					BEFORE INSERT ON verification_queue
					FOR EACH ROW EXECUTE FUNCTION reject_candidate_probe()
			`,
			cleanup: `
				DROP TRIGGER reject_candidate_probe ON verification_queue;
				DROP FUNCTION reject_candidate_probe()
			`,
			want: "schedule automatic candidate probe",
		},
		{
			name: "promote",
			setup: `
				CREATE FUNCTION reject_candidate_promotion() RETURNS trigger
				LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'reject candidate promotion'; END $$;
				CREATE TRIGGER reject_candidate_promotion
					BEFORE INSERT ON discovery_source_state
					FOR EACH ROW EXECUTE FUNCTION reject_candidate_promotion()
			`,
			cleanup: `
				DROP TRIGGER reject_candidate_promotion ON discovery_source_state;
				DROP FUNCTION reject_candidate_promotion()
			`,
			want: "promote automatic candidate",
		},
		{
			name: "record promotion",
			setup: `
				CREATE FUNCTION reject_promoted_outcome() RETURNS trigger
				LANGUAGE plpgsql AS $$
				BEGIN
					IF NEW.outcome = 'promoted' THEN
						RAISE EXCEPTION 'reject promoted outcome';
					END IF;
					RETURN NEW;
				END $$;
				CREATE TRIGGER reject_promoted_outcome
					BEFORE UPDATE ON crawl_run_automatic_admission_batches
					FOR EACH ROW EXECUTE FUNCTION reject_promoted_outcome()
			`,
			cleanup: `
				DROP TRIGGER reject_promoted_outcome ON crawl_run_automatic_admission_batches;
				DROP FUNCTION reject_promoted_outcome()
			`,
			want: "record promoted admission",
		},
		{
			name: "update promotion count",
			setup: `
				CREATE FUNCTION reject_promotion_count() RETURNS trigger
				LANGUAGE plpgsql AS $$
				BEGIN
					IF NEW.automatic_promotions <> OLD.automatic_promotions THEN
						RAISE EXCEPTION 'reject promotion count';
					END IF;
					RETURN NEW;
				END $$;
				CREATE TRIGGER reject_promotion_count
					BEFORE UPDATE ON crawl_runs
					FOR EACH ROW EXECUTE FUNCTION reject_promotion_count()
			`,
			cleanup: `
				DROP TRIGGER reject_promotion_count ON crawl_runs;
				DROP FUNCTION reject_promotion_count()
			`,
			want: "update automatic promotion count",
		},
		{
			name: "update original telemetry",
			setup: `
				CREATE FUNCTION reject_promotion_telemetry() RETURNS trigger
				LANGUAGE plpgsql AS $$
				BEGIN
					IF NEW.promotions_admitted IS DISTINCT FROM OLD.promotions_admitted
						OR NEW.promotions_deferred IS DISTINCT FROM OLD.promotions_deferred THEN
						RAISE EXCEPTION 'reject promotion telemetry';
					END IF;
					RETURN NEW;
				END $$;
				CREATE TRIGGER reject_promotion_telemetry
					BEFORE UPDATE ON crawl_runs
					FOR EACH ROW EXECUTE FUNCTION reject_promotion_telemetry()
			`,
			cleanup: `
				DROP TRIGGER reject_promotion_telemetry ON crawl_runs;
				DROP FUNCTION reject_promotion_telemetry()
			`,
			want: "update original-run promotion telemetry",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetAutomaticFixtureData(t, pool)
			_, runID, candidate := automaticCompletionFixtureInPool(t, pool)
			if _, err := pool.Exec(ctx, test.setup); err != nil {
				t.Fatal(err)
			}
			defer executeSchemaCleanup(t, pool, test.cleanup)
			operationPool := newStoreSiblingPool(t, pool, "")
			defer operationPool.Close()
			store := newAutomaticAdmissionStore(t, operationPool, 1, 2)
			err := store.CompleteAutomaticCandidates(
				ctx,
				runID,
				[]AutomaticCandidateResult{{
					Candidate:       candidate,
					FailureCategory: test.category,
				}},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("stage error = %v, want %q", err, test.want)
			}
		})
	}
}

func automaticPendingFixture(
	t *testing.T,
) (*pgxpool.Pool, *DiscoveryStore, discovery.CrawlRunID) {
	t.Helper()
	pool := newStoreTestPool(t)
	store, runID := automaticPendingFixtureInPool(t, pool)
	return pool, store, runID
}

func automaticPendingFixtureInPool(
	t *testing.T,
	pool *pgxpool.Pool,
) (*DiscoveryStore, discovery.CrawlRunID) {
	t.Helper()
	ctx := context.Background()
	store := newAutomaticAdmissionStore(t, pool, 1, 2)
	source := mustStoreOrigin(t, "https://source.example")
	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	runID := beginAutomaticAdmissionRun(t, store, source, 1)
	candidate := discovery.Candidate{
		Origin: mustStoreOrigin(t, "https://candidate.example"),
		Kind:   discovery.KindLink,
	}
	if _, err := store.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		[]discovery.Candidate{candidate},
	); err != nil {
		t.Fatal(err)
	}
	return store, runID
}

func automaticCompletionFixture(
	t *testing.T,
) (*pgxpool.Pool, *DiscoveryStore, discovery.CrawlRunID, discovery.Candidate) {
	t.Helper()
	pool, store, runID := automaticPendingFixture(t)
	pending, err := store.PendingAutomaticCandidates(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending candidates = %#v, want one", pending)
	}
	return pool, store, runID, pending[0]
}

func automaticCompletionFixtureInPool(
	t *testing.T,
	pool *pgxpool.Pool,
) (*DiscoveryStore, discovery.CrawlRunID, discovery.Candidate) {
	t.Helper()
	store, runID := automaticPendingFixtureInPool(t, pool)
	pending, err := store.PendingAutomaticCandidates(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending candidates = %#v, want one", pending)
	}
	return store, runID, pending[0]
}

func resetAutomaticFixtureData(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		TRUNCATE TABLE
			origins,
			discovery_candidates,
			discovery_source_state,
			crawl_runs,
			verification_queue
		RESTART IDENTITY CASCADE
	`); err != nil {
		t.Fatalf("reset automatic fixture data: %v", err)
	}
}

func executeSchemaCleanup(
	t *testing.T,
	pool *pgxpool.Pool,
	statement string,
) {
	t.Helper()
	if statement == "" {
		return
	}
	if _, err := pool.Exec(
		context.Background(),
		statement,
		pgx.QueryExecModeSimpleProtocol,
	); err != nil {
		t.Errorf("restore altered schema: %v", err)
	}
}

func newStoreSiblingPool(
	t *testing.T,
	parent *pgxpool.Pool,
	lockTimeout string,
) *pgxpool.Pool {
	t.Helper()
	config := parent.Config()
	config.MaxConns = 1
	config.MinConns = 0
	previousAfterConnect := config.AfterConnect
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		if previousAfterConnect != nil {
			if err := previousAfterConnect(ctx, connection); err != nil {
				return err
			}
		}
		if lockTimeout == "" {
			return nil
		}
		_, err := connection.Exec(
			ctx,
			"SELECT set_config('lock_timeout', $1, false)",
			lockTimeout,
		)
		return err
	}
	sibling, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("create isolated sibling pool: %v", err)
	}
	t.Cleanup(sibling.Close)
	if err := sibling.Ping(context.Background()); err != nil {
		t.Fatalf("connect isolated sibling pool: %v", err)
	}
	return sibling
}

func rollbackTestTransaction(t *testing.T, transaction pgx.Tx) {
	t.Helper()
	if err := transaction.Rollback(context.Background()); err != nil &&
		!errors.Is(err, pgx.ErrTxClosed) {
		t.Errorf("rollback test transaction: %v", err)
	}
}

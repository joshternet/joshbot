package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

func TestProcessorPauseReadersReportsSuccessAndDatabaseFailure(t *testing.T) {
	ctx := context.Background()
	var nilQueue *Queue
	if _, err := nilQueue.VerificationPaused(ctx); !errors.Is(err, errQueueUnavailable) {
		t.Fatalf("nil queue VerificationPaused() error = %v", err)
	}
	pool := newStoreTestPool(t)
	discoveryStore, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := NewQueue(pool, QueueConfig{
		LeaseDuration:     time.Minute,
		MinOriginInterval: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if paused, err := discoveryStore.DiscoveryPaused(ctx); err != nil || paused {
		t.Fatalf("DiscoveryPaused() = %t, %v", paused, err)
	}
	if paused, err := queue.VerificationPaused(ctx); err != nil || paused {
		t.Fatalf("VerificationPaused() = %t, %v", paused, err)
	}
	if _, err := pool.Exec(ctx, "DROP TABLE crawl_control"); err != nil {
		t.Fatal(err)
	}
	if _, err := discoveryStore.DiscoveryPaused(ctx); err == nil {
		t.Error("DiscoveryPaused() database error = nil")
	}
	if _, err := queue.VerificationPaused(ctx); err == nil {
		t.Error("VerificationPaused() database error = nil")
	}
}

func TestDiscoverySourceCompletionHandlesRetryStatesAndValidation(t *testing.T) {
	ctx := context.Background()
	var nilStore *DiscoveryStore
	if err := nilStore.CompleteDiscoverySourceRetry(
		ctx,
		originZero(),
		retry.CategoryNone,
		0,
	); !errors.Is(err, errDiscoveryStoreUnavailable) {
		t.Fatalf("nil store completion error = %v", err)
	}
	pool := newStoreTestPool(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := &discoveryTestClock{times: []time.Time{now, now.Add(time.Minute)}}
	store, err := newDiscoveryStoreWithConfig(pool, clock, AutomaticCrawlConfig{})
	if err != nil {
		t.Fatal(err)
	}
	source := mustStoreOrigin(t, "https://retry.example")
	if err := store.AddCrawlSeed(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteDiscoverySourceRetry(
		ctx,
		source,
		retry.CategoryHTTP429,
		10*time.Minute,
	); err != nil {
		t.Fatalf("transient completion: %v", err)
	}
	var failures int
	var category string
	var nextAttempt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT consecutive_failures, last_failure_category, next_attempt_at
		FROM discovery_source_state WHERE source_origin = $1
	`, source.String()).Scan(&failures, &category, &nextAttempt); err != nil {
		t.Fatal(err)
	}
	if failures != 1 || category != string(retry.CategoryHTTP429) ||
		!nextAttempt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("retry state = %d, %q, %v", failures, category, nextAttempt)
	}
	if err := store.CompleteDiscoverySource(ctx, source, retry.CategoryNone); err != nil {
		t.Fatalf("successful completion: %v", err)
	}
	var storedCategory *string
	var storedNext *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT consecutive_failures, last_failure_category, next_attempt_at
		FROM discovery_source_state WHERE source_origin = $1
	`, source.String()).Scan(&failures, &storedCategory, &storedNext); err != nil {
		t.Fatal(err)
	}
	if failures != 0 || storedCategory != nil || storedNext != nil {
		t.Fatalf("reset retry state = %d, %v, %v", failures, storedCategory, storedNext)
	}

	for _, call := range []func() error{
		func() error {
			return store.CompleteDiscoverySourceRetry(ctx, originZero(), retry.CategoryNone, 0)
		},
		func() error {
			return store.CompleteDiscoverySourceRetry(ctx, source, retry.Category("invalid"), 0)
		},
		func() error {
			return store.CompleteDiscoverySourceRetry(ctx, source, retry.CategoryNone, -time.Second)
		},
		func() error {
			return store.CompleteDiscoverySourceRetry(ctx, source, retry.CategoryNone, retry.MaxDelay+time.Second)
		},
	} {
		if err := call(); err == nil {
			t.Error("invalid discovery completion error = nil")
		}
	}
}

func TestDiscoverySourceCompletionReturnsClockAndDatabaseFailures(t *testing.T) {
	ctx := context.Background()
	source := mustStoreOrigin(t, "https://retry-errors.example")
	pool := newStoreTestPool(t)

	t.Run("clock", func(t *testing.T) {
		resetAutomaticFixtureData(t, pool)
		clockError := errors.New("test discovery clock failure")
		store, err := newDiscoveryStoreWithConfig(
			pool,
			&discoveryTestClock{err: clockError},
			AutomaticCrawlConfig{},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AddCrawlSeed(ctx, source); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteDiscoverySource(ctx, source, retry.CategoryNone); !errors.Is(err, clockError) {
			t.Fatalf("clock error = %v", err)
		}
	})

	for _, test := range []struct {
		name     string
		category retry.Category
		setup    string
		cleanup  string
	}{
		{
			name:     "non-transient update",
			category: retry.CategoryNone,
			setup:    "ALTER TABLE discovery_source_state RENAME TO discovery_source_state_unavailable",
			cleanup:  "ALTER TABLE discovery_source_state_unavailable RENAME TO discovery_source_state",
		},
		{
			name:     "transient streak",
			category: retry.CategoryHTTP429,
			setup:    "ALTER TABLE discovery_source_state RENAME TO discovery_source_state_unavailable",
			cleanup:  "ALTER TABLE discovery_source_state_unavailable RENAME TO discovery_source_state",
		},
		{
			name:     "transient schedule",
			category: retry.CategoryHTTP429,
			setup: `
				CREATE FUNCTION reject_retry_schedule() RETURNS trigger
				LANGUAGE plpgsql AS $$
				BEGIN
					IF NEW.next_attempt_at IS NOT NULL THEN
						RAISE EXCEPTION 'reject retry schedule';
					END IF;
					RETURN NEW;
				END $$;
				CREATE TRIGGER reject_retry_schedule
				BEFORE UPDATE ON discovery_source_state
				FOR EACH ROW EXECUTE FUNCTION reject_retry_schedule()
			`,
			cleanup: `
				DROP TRIGGER reject_retry_schedule ON discovery_source_state;
				DROP FUNCTION reject_retry_schedule()
			`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetAutomaticFixtureData(t, pool)
			setupStore, err := NewDiscoveryStore(pool)
			if err != nil {
				t.Fatal(err)
			}
			if err := setupStore.AddCrawlSeed(ctx, source); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, test.setup); err != nil {
				t.Fatal(err)
			}
			defer executeSchemaCleanup(t, pool, test.cleanup)
			operationPool := newStoreSiblingPool(t, pool, "")
			defer operationPool.Close()
			store, err := NewDiscoveryStore(operationPool)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.CompleteDiscoverySource(ctx, source, test.category); err == nil {
				t.Fatal("database error = nil")
			}
		})
	}
}

func TestAutomaticExclusionsHandleQueryAndCollectionFailures(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	t.Run("query", func(t *testing.T) {
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
		operationPool := newStoreSiblingPool(t, pool, "")
		defer operationPool.Close()
		store, err := NewDiscoveryStore(operationPool)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.automaticExclusions(ctx); err == nil ||
			!strings.Contains(err.Error(), "read crawl domain avoid rules") {
			t.Fatalf("query error = %v", err)
		}
	})
	t.Run("collection", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `
			ALTER TABLE crawl_domain_avoid_rules
				RENAME TO original_crawl_domain_avoid_rules;
			CREATE TABLE crawl_domain_avoid_rules (pattern text);
			INSERT INTO crawl_domain_avoid_rules (pattern) VALUES (NULL)
		`); err != nil {
			t.Fatal(err)
		}
		defer executeSchemaCleanup(t, pool, `
			DROP TABLE crawl_domain_avoid_rules;
			ALTER TABLE original_crawl_domain_avoid_rules
				RENAME TO crawl_domain_avoid_rules
		`)
		operationPool := newStoreSiblingPool(t, pool, "")
		defer operationPool.Close()
		store, err := NewDiscoveryStore(operationPool)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.automaticExclusions(ctx); err == nil ||
			!strings.Contains(err.Error(), "collect crawl domain avoid rules") {
			t.Fatalf("collection error = %v", err)
		}
	})
	t.Run("configured suffixes", func(t *testing.T) {
		store, err := newDiscoveryStoreWithConfig(
			pool,
			databaseQueueClock{},
			AutomaticCrawlConfig{ExcludedHostSuffixes: "configured.example"},
		)
		if err != nil {
			t.Fatal(err)
		}
		exclusions, err := store.automaticExclusions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		patterns := strings.Split(exclusions.ExcludedHostSuffixes, ",")
		foundConfigured := false
		for _, pattern := range patterns {
			if pattern == "configured.example" {
				foundConfigured = true
				break
			}
		}
		if !foundConfigured || exclusions.Enabled ||
			exclusions.MaxPendingProbes != 0 ||
			exclusions.MaxAutomaticPromotionsPerRun != 0 ||
			exclusions.RetryJitter != nil {
			t.Fatalf("automaticExclusions() = %#v", exclusions)
		}
	})
}

func TestExactCrawlBlockHandlesTransactionalFailures(t *testing.T) {
	ctx := context.Background()
	source := mustStoreOrigin(t, "https://blocked-errors.example")
	pool := newStoreTestPool(t)
	tests := []struct {
		name    string
		setup   string
		cleanup string
		want    string
	}{
		{
			name: "automatic exclusions",
			setup: `
				INSERT INTO discovery_source_state (
					source_origin, automatically_discovered
				) VALUES ('https://blocked-errors.example', true);
				ALTER TABLE crawl_domain_avoid_rules
					RENAME TO crawl_domain_avoid_rules_unavailable
			`,
			cleanup: "ALTER TABLE crawl_domain_avoid_rules_unavailable RENAME TO crawl_domain_avoid_rules",
			want:    "read crawl domain avoid rules",
		},
		{
			name: "effective update",
			setup: `
				CREATE FUNCTION reject_effective_block_update() RETURNS trigger
				LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'reject effective update'; END $$;
				CREATE TRIGGER reject_effective_block_update
				BEFORE UPDATE ON discovery_source_state
				FOR EACH ROW EXECUTE FUNCTION reject_effective_block_update()
			`,
			cleanup: `
				DROP TRIGGER reject_effective_block_update ON discovery_source_state;
				DROP FUNCTION reject_effective_block_update()
			`,
			want: "update effective crawl block",
		},
		{
			name: "probe removal",
			setup: `
				CREATE FUNCTION reject_probe_delete() RETURNS trigger
				LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'reject probe delete'; END $$;
				CREATE TRIGGER reject_probe_delete
				BEFORE DELETE ON verification_queue
				FOR EACH STATEMENT EXECUTE FUNCTION reject_probe_delete()
			`,
			cleanup: `
				DROP TRIGGER reject_probe_delete ON verification_queue;
				DROP FUNCTION reject_probe_delete()
			`,
			want: "remove blocked origin probe",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetAutomaticFixtureData(t, pool)
			if _, err := pool.Exec(ctx, test.setup); err != nil {
				t.Fatal(err)
			}
			defer executeSchemaCleanup(t, pool, test.cleanup)
			operationPool := newStoreSiblingPool(t, pool, "")
			defer operationPool.Close()
			store, err := NewDiscoveryStore(operationPool)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetCrawlBlocked(ctx, source, true); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("transactional block error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRecomputeAutomaticBlocksHandlesCollectionFailure(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	if _, err := pool.Exec(ctx, `
		DROP TABLE discovery_source_state CASCADE;
		CREATE TABLE discovery_source_state (
			source_origin text NOT NULL,
			operator_blocked boolean,
			crawl_blocked boolean,
			automatically_discovered boolean NOT NULL,
			seeded boolean NOT NULL
		);
		INSERT INTO discovery_source_state VALUES (
			'https://invalid-state.example', NULL, false, true, false
		)
	`); err != nil {
		t.Fatal(err)
	}
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		_, err := recomputeAutomaticCrawlBlocksTransaction(
			ctx,
			tx,
			AutomaticCrawlConfig{},
		)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "collect automatic crawl blocks") {
		t.Fatalf("collection error = %v", err)
	}
}

func TestDefaultAutomaticPromotionLimit(t *testing.T) {
	config := AutomaticCrawlConfig{}
	if got := config.maxAutomaticPromotionsPerRun(); got != defaultAutomaticPromotionsPerRun {
		t.Fatalf("default promotion limit = %d", got)
	}
	config.MaxAutomaticPromotionsPerRun = 7
	if got := config.maxAutomaticPromotionsPerRun(); got != 7 {
		t.Fatalf("configured promotion limit = %d", got)
	}
}

func TestQueueCompletionHandlesLockScanPolitenessAndZeroRows(t *testing.T) {
	pool := newStoreTestPool(t)
	t.Run("lock scan", func(t *testing.T) {
		resetAutomaticFixtureData(t, pool)
		fixture := newCompletionFixtureInPool(t, pool, QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		}, queueTestTime())
		if _, err := fixture.pool.Exec(fixture.ctx, `
			ALTER TABLE verification_queue
				ALTER COLUMN consecutive_failures DROP NOT NULL
		`); err != nil {
			t.Fatal(err)
		}
		defer executeSchemaCleanup(t, pool, `
			UPDATE verification_queue SET consecutive_failures = 0
				WHERE consecutive_failures IS NULL;
			ALTER TABLE verification_queue
				ALTER COLUMN consecutive_failures SET NOT NULL
		`)
		if _, err := fixture.pool.Exec(fixture.ctx, `
			UPDATE verification_queue SET consecutive_failures = NULL
			WHERE origin = $1
		`, fixture.source.String()); err != nil {
			t.Fatal(err)
		}
		fixture.queue.clock = fixedQueueClock{now: fixture.claimed.Add(time.Minute)}
		err := fixture.queue.CompleteVerification(
			fixture.ctx,
			fixture.lease,
			validCompletionResult(fixture.source),
			time.Hour,
		)
		if err == nil {
			t.Fatal("lock scan error = nil")
		}
	})

	t.Run("transient politeness", func(t *testing.T) {
		resetAutomaticFixtureData(t, pool)
		fixture := newCompletionFixtureInPool(t, pool, QueueConfig{
			LeaseDuration:     3 * time.Hour,
			MinOriginInterval: 2 * time.Hour,
		}, queueTestTime())
		completedAt := fixture.claimed.Add(time.Minute)
		fixture.queue.clock = fixedQueueClock{now: completedAt}
		result := declaration.Result{
			Outcome:         declaration.OutcomeUnavailable,
			Origin:          fixture.source,
			FailureCategory: retry.CategoryHTTP429,
		}
		if err := fixture.queue.CompleteVerification(
			fixture.ctx,
			fixture.lease,
			result,
			time.Hour,
		); err != nil {
			t.Fatal(err)
		}
		state := readCompletionQueueState(t, fixture)
		want := fixture.claimed.Add(2 * time.Hour)
		if !state.availableAt.Equal(want) || state.nextAttemptAt == nil ||
			!state.nextAttemptAt.Equal(want) {
			t.Fatalf("polite retry state = %#v, want %v", state, want)
		}
	})

	t.Run("suppressed mutation", func(t *testing.T) {
		resetAutomaticFixtureData(t, pool)
		fixture := newCompletionFixtureInPool(t, pool, QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		}, queueTestTime())
		if _, err := fixture.pool.Exec(fixture.ctx, `
			CREATE FUNCTION suppress_queue_update() RETURNS trigger
			LANGUAGE plpgsql AS $$
			BEGIN RETURN NULL; END $$;
			CREATE TRIGGER suppress_queue_update
				BEFORE UPDATE ON verification_queue
				FOR EACH ROW EXECUTE FUNCTION suppress_queue_update()
		`); err != nil {
			t.Fatal(err)
		}
		defer executeSchemaCleanup(t, pool, `
			DROP TRIGGER suppress_queue_update ON verification_queue;
			DROP FUNCTION suppress_queue_update()
		`)
		fixture.queue.clock = fixedQueueClock{now: fixture.claimed.Add(time.Minute)}
		err := fixture.queue.CompleteVerification(
			fixture.ctx,
			fixture.lease,
			validCompletionResult(fixture.source),
			time.Hour,
		)
		if !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("suppressed update error = %v", err)
		}
	})
}

func TestControlMutationAuditRollbackOnMissingAuditTable(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DROP TABLE operator_audit_events"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetProcessorPaused(
		ctx,
		"discovery",
		true,
		controlAudit("processor.pause", "discovery"),
	); err == nil {
		t.Fatal("missing audit table error = nil")
	}
	var paused bool
	if err := pool.QueryRow(
		ctx,
		"SELECT discovery_paused FROM crawl_control WHERE singleton",
	).Scan(&paused); err != nil {
		t.Fatal(err)
	}
	if paused {
		t.Error("processor state committed despite audit failure")
	}
}

func originZero() origin.Origin {
	return origin.Origin{}
}

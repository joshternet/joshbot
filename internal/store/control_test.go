package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/control"
)

func TestControlStoreMutationsCommitSuccessAudits(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	commander, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	audit := control.Audit{
		OccurredAt: time.Now().UTC(),
		Action:     "processor.pause",
		Target:     "discovery",
		Caller:     "192.0.2.1",
		Actor:      "operator-api",
		Result:     control.ResultSuccess,
		Reason:     "maintenance",
	}
	if err := commander.SetProcessorPaused(ctx, "discovery", true, audit); err != nil {
		t.Fatal(err)
	}
	if err := commander.AddDomainAvoid(ctx, "example.com", controlAudit("domain-avoid.add", "example.com")); err != nil {
		t.Fatal(err)
	}
	if err := commander.SetOriginBlocked(ctx, "https://blocked.example", true, controlAudit("origin.block", "https://blocked.example")); err != nil {
		t.Fatal(err)
	}
	if err := commander.SetOriginBlocked(ctx, "https://blocked.example", false, controlAudit("origin.allow", "https://blocked.example")); err != nil {
		t.Fatal(err)
	}
	if err := commander.RemoveDomainAvoid(ctx, "example.com", controlAudit("domain-avoid.remove", "example.com")); err != nil {
		t.Fatal(err)
	}

	var paused, blocked bool
	if err := pool.QueryRow(ctx, "SELECT discovery_paused FROM crawl_control WHERE singleton").Scan(&paused); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT crawl_blocked FROM discovery_source_state WHERE source_origin = 'https://blocked.example'").Scan(&blocked); err != nil {
		t.Fatal(err)
	}
	if !paused || blocked {
		t.Errorf("state = paused:%v blocked:%v", paused, blocked)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM operator_audit_events WHERE result = 'success'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("success audits = %d, want 5", count)
	}
}

func TestOperatorBlockMigrationConservativelyBackfills(t *testing.T) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)
	for _, migration := range []string{
		"migrations/0001_initial.sql",
		"migrations/0002_verification_queue.sql",
		"migrations/0003_discovery.sql",
		"migrations/0004_crawl_sources.sql",
		"migrations/0005_automatic_crawl_sources.sql",
		"migrations/0006_crawl_observability.sql",
		"migrations/0007_crawl_observability_permissions.sql",
		"migrations/0008_crawl_domain_avoid_rules.sql",
		"migrations/0009_automatic_admission.sql",
		"migrations/0010_retry_state.sql",
	} {
		applyRawStoreMigration(t, ctx, pool, migration)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_source_state (
			source_origin, automatically_discovered, crawl_blocked
		) VALUES
			('https://blocked.example', true, true),
			('https://allowed.example', true, false)
	`); err != nil {
		t.Fatal(err)
	}
	applyRawStoreMigration(
		t,
		ctx,
		pool,
		"migrations/0011_operator_audit_events.sql",
	)
	var blockedIntent, allowedIntent bool
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT operator_blocked FROM discovery_source_state
				WHERE source_origin = 'https://blocked.example'),
			(SELECT operator_blocked FROM discovery_source_state
				WHERE source_origin = 'https://allowed.example')
	`).Scan(&blockedIntent, &allowedIntent); err != nil {
		t.Fatal(err)
	}
	if !blockedIntent || allowedIntent {
		t.Errorf(
			"backfilled intents = blocked:%v allowed:%v",
			blockedIntent,
			allowedIntent,
		)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE discovery_source_state
		SET operator_blocked = true, crawl_blocked = false
		WHERE source_origin = 'https://allowed.example'
	`); err == nil {
		t.Error("inconsistent explicit and effective block state was accepted")
	}
}

func TestControlStoreRollsBackMutationWhenSuccessAuditFails(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	commander, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	invalid := controlAudit("processor.pause", "discovery")
	invalid.Actor = ""
	if err := commander.SetProcessorPaused(ctx, "discovery", true, invalid); err == nil {
		t.Fatal("mutation error = nil")
	}
	var paused bool
	if err := pool.QueryRow(ctx, "SELECT discovery_paused FROM crawl_control WHERE singleton").Scan(&paused); err != nil {
		t.Fatal(err)
	}
	if paused {
		t.Error("processor mutation committed without audit")
	}
}

func TestControlStoreRejectsInconsistentSuccessAudit(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	commander, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	audit := controlAudit("origin.allow", "https://example.com")
	if err := commander.SetOriginBlocked(
		ctx,
		"https://example.com",
		true,
		audit,
	); err == nil {
		t.Fatal("inconsistent success audit was accepted")
	}
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM discovery_source_state
			WHERE source_origin = 'https://example.com'
		)
	`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("mutation committed with inconsistent audit")
	}
}

func TestControlStorePersistsRejectedAudit(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	commander, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	audit := controlAudit("origin.block", "invalid")
	audit.Result = control.ResultRejected
	if err := commander.RecordRejected(ctx, audit); err != nil {
		t.Fatal(err)
	}
	var result string
	if err := pool.QueryRow(ctx, "SELECT result FROM operator_audit_events").Scan(&result); err != nil {
		t.Fatal(err)
	}
	if result != control.ResultRejected {
		t.Errorf("result = %q", result)
	}
}

func TestControlStoreDomainAvoidReconcilesAutomaticSources(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_source_state (
			source_origin, automatically_discovered, seeded, crawl_blocked
		) VALUES
			('https://auto.example.com', true, false, false),
			('https://seed.example.com', true, true, false),
			('https://other.test', true, false, false);
		INSERT INTO verification_queue (origin, available_at, mode) VALUES
			('https://auto.example.com', statement_timestamp(), 'probe'),
			('https://seed.example.com', statement_timestamp(), 'probe');
	`); err != nil {
		t.Fatal(err)
	}
	commander, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := commander.AddDomainAvoid(ctx, "example.com", controlAudit("domain-avoid.add", "example.com")); err != nil {
		t.Fatal(err)
	}
	var automaticBlocked, seedBlocked, otherBlocked bool
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT crawl_blocked FROM discovery_source_state WHERE source_origin = 'https://auto.example.com'),
			(SELECT crawl_blocked FROM discovery_source_state WHERE source_origin = 'https://seed.example.com'),
			(SELECT crawl_blocked FROM discovery_source_state WHERE source_origin = 'https://other.test')
	`).Scan(&automaticBlocked, &seedBlocked, &otherBlocked); err != nil {
		t.Fatal(err)
	}
	if !automaticBlocked || seedBlocked || otherBlocked {
		t.Errorf("blocked = automatic:%v seed:%v other:%v", automaticBlocked, seedBlocked, otherBlocked)
	}
	var autoProbe, seedProbe bool
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS(SELECT 1 FROM verification_queue WHERE origin = 'https://auto.example.com'),
			EXISTS(SELECT 1 FROM verification_queue WHERE origin = 'https://seed.example.com')
	`).Scan(&autoProbe, &seedProbe); err != nil {
		t.Fatal(err)
	}
	if autoProbe || !seedProbe {
		t.Errorf("probe state = automatic:%v seed:%v", autoProbe, seedProbe)
	}

	if err := commander.SetOriginBlocked(
		ctx,
		"https://auto.example.com",
		false,
		controlAudit("origin.allow", "https://auto.example.com"),
	); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT crawl_blocked
		FROM discovery_source_state
		WHERE source_origin = 'https://auto.example.com'
	`).Scan(&automaticBlocked); err != nil {
		t.Fatal(err)
	}
	if !automaticBlocked {
		t.Error("origin allow overrode active domain avoid rule")
	}
}

func TestControlStoreKeepsExactBlockAfterAvoidRemoval(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	commander, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	source := "https://blocked.example.com"
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_source_state (
			source_origin, automatically_discovered
		) VALUES ($1, true)
	`, source); err != nil {
		t.Fatal(err)
	}
	if err := commander.SetOriginBlocked(
		ctx, source, true, controlAudit("origin.block", source),
	); err != nil {
		t.Fatal(err)
	}
	if err := commander.AddDomainAvoid(
		ctx, "example.com", controlAudit("domain-avoid.add", "example.com"),
	); err != nil {
		t.Fatal(err)
	}
	if err := commander.RemoveDomainAvoid(
		ctx, "example.com", controlAudit("domain-avoid.remove", "example.com"),
	); err != nil {
		t.Fatal(err)
	}
	assertControlBlockState(t, ctx, pool, source, true, true)
}

func TestControlStoreRemoveOneOfMultipleAvoidRules(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	commander, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	source := "https://www.example.com"
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_source_state (
			source_origin, automatically_discovered
		) VALUES ($1, true)
	`, source); err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{"example.com", "www.example.com"} {
		if err := commander.AddDomainAvoid(
			ctx, pattern, controlAudit("domain-avoid.add", pattern),
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := commander.RemoveDomainAvoid(
		ctx, "example.com", controlAudit("domain-avoid.remove", "example.com"),
	); err != nil {
		t.Fatal(err)
	}
	assertControlBlockState(t, ctx, pool, source, false, true)
}

func TestControlStoreAllowWhileAvoidedThenRemoveUnblocks(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	commander, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	source := "https://allowed.example.com"
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_source_state (
			source_origin, automatically_discovered
		) VALUES ($1, true)
	`, source); err != nil {
		t.Fatal(err)
	}
	if err := commander.SetOriginBlocked(
		ctx, source, true, controlAudit("origin.block", source),
	); err != nil {
		t.Fatal(err)
	}
	if err := commander.AddDomainAvoid(
		ctx, "example.com", controlAudit("domain-avoid.add", "example.com"),
	); err != nil {
		t.Fatal(err)
	}
	if err := commander.SetOriginBlocked(
		ctx, source, false, controlAudit("origin.allow", source),
	); err != nil {
		t.Fatal(err)
	}
	assertControlBlockState(t, ctx, pool, source, false, true)
	if err := commander.RemoveDomainAvoid(
		ctx, "example.com", controlAudit("domain-avoid.remove", "example.com"),
	); err != nil {
		t.Fatal(err)
	}
	assertControlBlockState(t, ctx, pool, source, false, false)
}

func TestDiscoveryStorePreservesExplicitBlockAcrossReconciliation(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	discoveryStore, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{
			Enabled:              true,
			ExcludedHostSuffixes: "example.com",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	source := mustStoreOrigin(t, "https://explicit.example.com")
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_source_state (
			source_origin, automatically_discovered
		) VALUES ($1, true)
	`, source.String()); err != nil {
		t.Fatal(err)
	}
	if err := discoveryStore.SetCrawlBlocked(ctx, source, true); err != nil {
		t.Fatal(err)
	}
	if _, err := discoveryStore.ReconcileAutomaticCrawlPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	assertControlBlockState(t, ctx, pool, source.String(), true, true)
}

func TestOperatorAuditRowsAreAppendOnly(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	commander, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	audit := controlAudit("origin.block", "invalid")
	audit.Result = control.ResultRejected
	if err := commander.RecordRejected(ctx, audit); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"UPDATE operator_audit_events SET result = 'success'",
		"DELETE FROM operator_audit_events",
	} {
		if _, err := pool.Exec(ctx, statement); err == nil {
			t.Errorf("%s succeeded", statement)
		}
	}
}

func TestControlStoreValidatesDependenciesAndDatabaseFailure(t *testing.T) {
	if _, err := NewControlStore(nil); err == nil {
		t.Error("nil pool accepted")
	}
	pool := newStoreTestPool(t)
	commander, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if err := commander.RecordRejected(context.Background(), controlAudit("origin.block", "https://example.com")); err == nil {
		t.Error("closed pool error = nil")
	}
}

func TestControlStoreHandlesValidationAndMutationFailures(t *testing.T) {
	ctx := context.Background()
	testError := errors.New("test mutation failure")

	var nilStore *ControlStore
	if err := nilStore.RecordRejected(ctx, controlAudit("test", "test")); !errors.Is(err, errControlStoreUnavailable) {
		t.Errorf("nil store error = %v", err)
	}
	if err := nilStore.mutate(
		ctx,
		controlAudit("test", "test"),
		func(pgx.Tx) error { return nil },
	); !errors.Is(err, errControlStoreUnavailable) {
		t.Errorf("nil store mutation error = %v", err)
	}

	pool := newStoreTestPool(t)
	store, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	var nilContext context.Context
	if err := store.RecordRejected(nilContext, controlAudit("test", "test")); !errors.Is(err, errInvalidContext) {
		t.Errorf("nil context error = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.RecordRejected(canceled, controlAudit("test", "test")); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled context error = %v", err)
	}
	storeWithoutPool := &ControlStore{}
	if err := storeWithoutPool.RecordRejected(ctx, controlAudit("test", "test")); !errors.Is(err, errPoolUnavailable) {
		t.Errorf("nil pool error = %v", err)
	}

	if err := store.SetProcessorPaused(ctx, "other", true, controlAudit("processor.pause", "other")); !errors.Is(err, errInvalidServiceState) {
		t.Errorf("unknown processor error = %v", err)
	}
	if err := store.SetProcessorPaused(ctx, "verification", false, controlAudit("processor.resume", "verification")); err != nil {
		t.Fatalf("resume verification: %v", err)
	}
	if err := store.SetProcessorPaused(ctx, "verification", true, controlAudit("wrong", "verification")); err == nil {
		t.Error("inconsistent processor audit accepted")
	}
	for _, mutation := range []func() error{
		func() error { return store.AddDomainAvoid(ctx, "", controlAudit("domain-avoid.add", "")) },
		func() error {
			return store.RemoveDomainAvoid(ctx, "bad,rule", controlAudit("domain-avoid.remove", "bad,rule"))
		},
		func() error { return store.AddDomainAvoid(ctx, "example.com", controlAudit("wrong", "example.com")) },
		func() error { return store.RemoveDomainAvoid(ctx, "example.com", controlAudit("wrong", "example.com")) },
		func() error {
			return store.SetOriginBlocked(ctx, "not-an-origin", true, controlAudit("origin.block", "not-an-origin"))
		},
	} {
		if err := mutation(); err == nil {
			t.Error("invalid control mutation error = nil")
		}
	}
	rejected := controlAudit("test", "test")
	if err := store.RecordRejected(ctx, rejected); err == nil {
		t.Error("successful audit accepted by RecordRejected")
	}
	if err := store.mutate(ctx, controlAudit("test", "test"), nil); err == nil {
		t.Error("nil mutation accepted")
	}
	rejected.Result = control.ResultRejected
	if err := store.mutate(ctx, rejected, func(pgx.Tx) error { return nil }); err == nil {
		t.Error("rejected audit accepted by mutate")
	}
	if err := store.mutate(ctx, controlAudit("test", "test"), func(pgx.Tx) error {
		return testError
	}); !errors.Is(err, testError) {
		t.Errorf("mutation failure = %v", err)
	}
}

func TestControlStoreReturnsPostgresMutationFailures(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		setup string
		call  func(*ControlStore) error
	}{
		{
			name:  "processor update",
			setup: "DROP TABLE crawl_control",
			call: func(store *ControlStore) error {
				return store.SetProcessorPaused(ctx, "discovery", true, controlAudit("processor.pause", "discovery"))
			},
		},
		{
			name:  "processor singleton",
			setup: "DELETE FROM crawl_control",
			call: func(store *ControlStore) error {
				return store.SetProcessorPaused(ctx, "discovery", true, controlAudit("processor.pause", "discovery"))
			},
		},
		{
			name:  "add rule",
			setup: "DROP TABLE crawl_domain_avoid_rules",
			call: func(store *ControlStore) error {
				return store.AddDomainAvoid(ctx, "example.com", controlAudit("domain-avoid.add", "example.com"))
			},
		},
		{
			name:  "add reconciliation",
			setup: "DROP TABLE discovery_source_state CASCADE",
			call: func(store *ControlStore) error {
				return store.AddDomainAvoid(ctx, "example.com", controlAudit("domain-avoid.add", "example.com"))
			},
		},
		{
			name:  "remove rule",
			setup: "DROP TABLE crawl_domain_avoid_rules",
			call: func(store *ControlStore) error {
				return store.RemoveDomainAvoid(ctx, "example.com", controlAudit("domain-avoid.remove", "example.com"))
			},
		},
		{
			name: "remove reconciliation",
			setup: `
				INSERT INTO crawl_domain_avoid_rules (pattern) VALUES ('example.com');
				DROP TABLE discovery_source_state CASCADE
			`,
			call: func(store *ControlStore) error {
				return store.RemoveDomainAvoid(ctx, "example.com", controlAudit("domain-avoid.remove", "example.com"))
			},
		},
		{
			name:  "origin block",
			setup: "DROP TABLE discovery_source_state CASCADE",
			call: func(store *ControlStore) error {
				return store.SetOriginBlocked(ctx, "https://example.com", true, controlAudit("origin.block", "https://example.com"))
			},
		},
		{
			name:  "rejected audit",
			setup: "DROP TABLE operator_audit_events",
			call: func(store *ControlStore) error {
				audit := controlAudit("origin.block", "https://example.com")
				audit.Result = control.ResultRejected
				return store.RecordRejected(ctx, audit)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := newStoreTestPool(t)
			store, err := NewControlStore(pool)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, test.setup); err != nil {
				t.Fatalf("prepare failure: %v", err)
			}
			if err := test.call(store); err == nil {
				t.Fatal("database failure = nil")
			}
		})
	}
}

func controlAudit(action, target string) control.Audit {
	return control.Audit{
		OccurredAt: time.Now().UTC(),
		Action:     action,
		Target:     target,
		Caller:     "192.0.2.1",
		Actor:      control.DefaultActor,
		Result:     control.ResultSuccess,
	}
}

func assertControlBlockState(
	t *testing.T,
	ctx context.Context,
	pool interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	source string,
	wantOperatorBlocked bool,
	wantBlocked bool,
) {
	t.Helper()
	var operatorBlocked, blocked bool
	if err := pool.QueryRow(ctx, `
		SELECT operator_blocked, crawl_blocked
		FROM discovery_source_state
		WHERE source_origin = $1
	`, source).Scan(&operatorBlocked, &blocked); err != nil {
		t.Fatal(err)
	}
	if operatorBlocked != wantOperatorBlocked || blocked != wantBlocked {
		t.Errorf(
			"block state = operator:%v effective:%v, want operator:%v effective:%v",
			operatorBlocked,
			blocked,
			wantOperatorBlocked,
			wantBlocked,
		)
	}
}

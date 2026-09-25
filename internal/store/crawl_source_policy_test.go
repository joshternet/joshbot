package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
)

func TestCrawlSourcePolicyAndClassification(t *testing.T) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	discovered := mustStoreOrigin(t, "https://discovered.example")
	curated := mustStoreOrigin(t, "https://curated.example")
	first := time.Date(2026, 9, 1, 1, 2, 3, 0, time.UTC)
	last := first.Add(time.Hour)

	_, err := pool.Exec(ctx, `
		INSERT INTO discovery_candidates (origin, first_discovered_at, last_discovered_at)
		VALUES ($1, $2, $3)
	`, discovered.String(), first, last)
	if err != nil {
		t.Fatalf("seed discovery candidate: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO discovery_source_state
			(source_origin, seeded, automatically_discovered, crawl_blocked)
		VALUES ($1, false, true, false), ($2, true, false, false)
	`, discovered.String(), curated.String())
	if err != nil {
		t.Fatalf("seed source classifications: %v", err)
	}

	sourceStore, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf("NewDiscoveryStore() error = %v", err)
	}
	if err := sourceStore.SetCrawlBlocked(ctx, discovered, true); err != nil {
		t.Fatalf("SetCrawlBlocked(true) error = %v", err)
	}

	sources, err := sourceStore.CrawlSources(ctx)
	if err != nil {
		t.Fatalf("CrawlSources() error = %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("source count = %d, want 2", len(sources))
	}
	if sources[0].Origin.String() != curated.String() || !sources[0].Seeded || sources[0].AutomaticallyDiscovered || sources[0].Blocked {
		t.Errorf("curated source = %#v", sources[0])
	}
	if sources[1].Origin.String() != discovered.String() || sources[1].Seeded || !sources[1].AutomaticallyDiscovered || !sources[1].Blocked {
		t.Errorf("discovered source = %#v", sources[1])
	}
	if sources[1].FirstDiscoveredAt == nil || !sources[1].FirstDiscoveredAt.Equal(first) || sources[1].LastDiscoveredAt == nil || !sources[1].LastDiscoveredAt.Equal(last) {
		t.Errorf("discovery timestamps = %#v", sources[1])
	}

	if err := sourceStore.SetCrawlBlocked(ctx, discovered, false); err != nil {
		t.Fatalf("SetCrawlBlocked(false) error = %v", err)
	}
	_, err = NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{Enabled: true})
	if err != nil {
		t.Fatalf("construct automatic store: %v", err)
	}
	if _, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{Enabled: true, MaxPendingProbes: -1},
	); err == nil {
		t.Fatal("construct store with negative pending-probe limit: nil error")
	}
	if _, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{Enabled: true, MaxAutomaticPromotionsPerRun: -1},
	); err == nil {
		t.Fatal("construct store with negative promotion limit: nil error")
	}
	if _, err := NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		AutomaticCrawlConfig{Enabled: true, ExcludedHostSuffixes: ".invalid.example"},
	); err == nil {
		t.Fatal("construct store with invalid excluded host suffix: nil error")
	}
	for _, invalid := range []string{"*.example.com", "example.*.com", "example*"} {
		if _, err := NewDiscoveryStoreWithAutomaticCrawling(
			pool,
			AutomaticCrawlConfig{Enabled: true, ExcludedHostSuffixes: invalid},
		); err == nil {
			t.Fatalf("construct store with invalid wildcard %q: nil error", invalid)
		}
	}
}

func TestExactCrawlBlockRemovesNonRecurringVerificationWork(
	t *testing.T,
) {
	tests := []struct {
		name       string
		mode       string
		wantQueued bool
	}{
		{
			name:       "probe",
			mode:       "probe",
			wantQueued: false,
		},
		{
			name:       "reprobe",
			mode:       "reprobe",
			wantQueued: false,
		},
		{
			name:       "recurring",
			mode:       "recurring",
			wantQueued: true,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				ctx := context.Background()
				pool := newCrawlSourceMigrationTestPool(t)
				source := mustStoreOrigin(
					t,
					"https://"+test.mode+".example",
				)

				store, err := NewDiscoveryStore(pool)
				if err != nil {
					t.Fatalf(
						"NewDiscoveryStore() error = %v",
						err,
					)
				}

				_, err = pool.Exec(
					ctx,
					`
						INSERT INTO verification_queue (
							origin,
							available_at,
							mode
						)
						VALUES (
							$1,
							statement_timestamp(),
							$2
						)
					`,
					source.String(),
					test.mode,
				)
				if err != nil {
					t.Fatalf(
						"seed %s verification work: %v",
						test.mode,
						err,
					)
				}

				if err := store.SetCrawlBlocked(
					ctx,
					source,
					true,
				); err != nil {
					t.Fatalf(
						"SetCrawlBlocked(true) error = %v",
						err,
					)
				}

				var queued bool
				err = pool.QueryRow(
					ctx,
					`
						SELECT EXISTS (
							SELECT 1
							FROM verification_queue
							WHERE origin = $1
						)
					`,
					source.String(),
				).Scan(&queued)
				if err != nil {
					t.Fatalf(
						"read verification work: %v",
						err,
					)
				}

				if queued != test.wantQueued {
					t.Errorf(
						"queued = %t, want %t for mode %q",
						queued,
						test.wantQueued,
						test.mode,
					)
				}
			},
		)
	}
}

func TestReconcileAutomaticCrawlPolicyBlocksLegacyExcludedSources(t *testing.T) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	excluded := mustStoreOrigin(t, "https://legacy.blogspot.co.uk")
	curated := mustStoreOrigin(t, "https://curated.wordpress.com")
	allowed := mustStoreOrigin(t, "https://independent.example")

	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_source_state
			(source_origin, seeded, automatically_discovered)
		VALUES
			($1, false, true),
			($2, true, true),
			($3, false, true)
	`, excluded.String(), curated.String(), allowed.String()); err != nil {
		t.Fatalf("seed legacy automatic sources: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO verification_queue (origin, available_at, mode)
		VALUES
			($1, CURRENT_TIMESTAMP, 'reprobe'),
			($2, CURRENT_TIMESTAMP, 'probe'),
			($3, CURRENT_TIMESTAMP, 'probe')
	`, excluded.String(), curated.String(), allowed.String()); err != nil {
		t.Fatalf("seed legacy automatic probes: %v", err)
	}

	store, err := NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{
		Enabled:              true,
		ExcludedHostSuffixes: "blogspot.*,wordpress.com",
	})
	if err != nil {
		t.Fatalf("construct automatic store: %v", err)
	}

	changed, err := store.ReconcileAutomaticCrawlPolicy(ctx)
	if err != nil {
		t.Fatalf("ReconcileAutomaticCrawlPolicy() error = %v", err)
	}
	if changed != 1 {
		t.Fatalf("reconciled source count = %d, want 1", changed)
	}

	var excludedBlocked, curatedBlocked, allowedBlocked bool
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT crawl_blocked FROM discovery_source_state WHERE source_origin=$1),
			(SELECT crawl_blocked FROM discovery_source_state WHERE source_origin=$2),
			(SELECT crawl_blocked FROM discovery_source_state WHERE source_origin=$3)
	`, excluded.String(), curated.String(), allowed.String()).Scan(
		&excludedBlocked,
		&curatedBlocked,
		&allowedBlocked,
	); err != nil {
		t.Fatalf("read reconciled source policy: %v", err)
	}
	if !excludedBlocked || curatedBlocked || allowedBlocked {
		t.Fatalf(
			"blocked states = excluded:%t curated:%t allowed:%t",
			excludedBlocked,
			curatedBlocked,
			allowedBlocked,
		)
	}

	var queued []string
	rows, err := pool.Query(ctx, `SELECT origin FROM verification_queue ORDER BY origin`)
	if err != nil {
		t.Fatalf("read remaining probes: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan remaining probe: %v", err)
		}
		queued = append(queued, raw)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate remaining probes: %v", err)
	}
	want := []string{curated.String(), allowed.String()}
	if len(queued) != len(want) || queued[0] != want[0] || queued[1] != want[1] {
		t.Fatalf("remaining probes = %#v, want %#v", queued, want)
	}

	changed, err = store.ReconcileAutomaticCrawlPolicy(ctx)
	if err != nil || changed != 0 {
		t.Fatalf("second reconciliation = (%d, %v), want (0, nil)", changed, err)
	}
}

func TestDomainAvoidRulesControlAutomaticCrawlSources(t *testing.T) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	seed := mustStoreOrigin(t, "https://seed.example")
	legacy := mustStoreOrigin(t, "https://legacy.hosted.example")

	store, err := NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	commander, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddCrawlSeed(ctx, seed); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordDiscovery(ctx, seed, []discovery.Candidate{{Origin: legacy, Kind: discovery.KindLink}}); err != nil {
		t.Fatal(err)
	}
	admitResolvedAutomaticCandidates(t, ctx, store, seed)

	if err := commander.AddDomainAvoid(
		ctx,
		"hosted.example",
		controlAudit("domain-avoid.add", "hosted.example"),
	); err != nil {
		t.Fatalf("AddDomainAvoid() error = %v", err)
	}

	var blocked bool
	if err := pool.QueryRow(ctx, `SELECT crawl_blocked FROM discovery_source_state WHERE source_origin=$1`, legacy.String()).Scan(&blocked); err != nil {
		t.Fatal(err)
	}
	if !blocked {
		t.Fatal("legacy automatic source remains unblocked")
	}
	var queued int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM verification_queue WHERE origin=$1`, legacy.String()).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatalf("legacy automatic probe count = %d, want 0", queued)
	}

	var pattern string
	var createdAt time.Time
	if err := pool.QueryRow(
		ctx,
		`SELECT pattern, created_at FROM crawl_domain_avoid_rules WHERE pattern=$1`,
		"hosted.example",
	).Scan(&pattern, &createdAt); err != nil {
		t.Fatal(err)
	}
	if pattern != "hosted.example" || createdAt.IsZero() {
		t.Fatalf("stored avoid rule = %q, %v", pattern, createdAt)
	}

	if err := commander.RemoveDomainAvoid(
		ctx,
		"hosted.example",
		controlAudit("domain-avoid.remove", "hosted.example"),
	); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM crawl_domain_avoid_rules WHERE pattern=$1`,
		"hosted.example",
	).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("removed avoid rule remains listed")
	}
}

func TestDefaultDomainAvoidRulesRetainEvidenceWithoutPromotion(t *testing.T) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	seed := mustStoreOrigin(t, "https://seed.example")
	blocked := mustStoreOrigin(t, "https://writer.blogspot.com")
	allowed := mustStoreOrigin(t, "https://independent.example")
	store, err := NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddCrawlSeed(ctx, seed); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordDiscovery(ctx, seed, []discovery.Candidate{
		{Origin: blocked, Kind: discovery.KindLink},
		{Origin: allowed, Kind: discovery.KindLink},
	}); err != nil {
		t.Fatal(err)
	}
	admitResolvedAutomaticCandidates(t, ctx, store, seed)

	var candidates, blockedSources, allowedSources int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM discovery_candidates WHERE origin=$1),
		(SELECT count(*) FROM discovery_source_state WHERE source_origin=$1 AND automatically_discovered),
		(SELECT count(*) FROM discovery_source_state WHERE source_origin=$2 AND automatically_discovered)
	`, blocked.String(), allowed.String()).Scan(&candidates, &blockedSources, &allowedSources); err != nil {
		t.Fatal(err)
	}
	if candidates != 1 || blockedSources != 0 || allowedSources != 1 {
		t.Fatalf("candidate/blocked source/allowed source = %d/%d/%d, want 1/0/1", candidates, blockedSources, allowedSources)
	}
}

func TestDomainAvoidRulesRejectInvalidPatterns(t *testing.T) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	commander, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{"", "*.example.com", "example.*.com", "https://example.com", "one.example,two.example"} {
		if err := commander.AddDomainAvoid(
			ctx,
			pattern,
			controlAudit("domain-avoid.add", pattern),
		); err == nil {
			t.Errorf("AddDomainAvoid(%q) error = nil", pattern)
		}
		if err := commander.RemoveDomainAvoid(
			ctx,
			pattern,
			controlAudit("domain-avoid.remove", pattern),
		); err == nil {
			t.Errorf("RemoveDomainAvoid(%q) error = nil", pattern)
		}
	}
}

func TestAutomaticCrawlPolicyEmptyAndUnavailableRules(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		store, err := NewDiscoveryStore(newCrawlSourceMigrationTestPool(t))
		if err != nil {
			t.Fatal(err)
		}
		if changed, err := store.ReconcileAutomaticCrawlPolicy(context.Background()); err != nil || changed != 0 {
			t.Fatalf("disabled reconciliation = (%d, %v), want (0, nil)", changed, err)
		}
	})

	t.Run("empty", func(t *testing.T) {
		ctx := context.Background()
		pool := newCrawlSourceMigrationTestPool(t)
		if _, err := pool.Exec(ctx, `DELETE FROM crawl_domain_avoid_rules`); err != nil {
			t.Fatal(err)
		}
		store, err := NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if changed, err := store.ReconcileAutomaticCrawlPolicy(ctx); err != nil || changed != 0 {
			t.Fatalf("empty reconciliation = (%d, %v), want (0, nil)", changed, err)
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		ctx := context.Background()
		pool := newCrawlSourceMigrationTestPool(t)
		store, err := NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `DROP TABLE crawl_domain_avoid_rules`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReconcileAutomaticCrawlPolicy(ctx); err == nil {
			t.Error("ReconcileAutomaticCrawlPolicy() missing-rule-table error = nil")
		}
	})

	t.Run("no matching sources", func(t *testing.T) {
		ctx := context.Background()
		pool := newCrawlSourceMigrationTestPool(t)
		store, err := NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if changed, err := store.ReconcileAutomaticCrawlPolicy(ctx); err != nil || changed != 0 {
			t.Fatalf("no-match reconciliation = (%d, %v), want (0, nil)", changed, err)
		}
	})

	t.Run("missing source table", func(t *testing.T) {
		ctx := context.Background()
		pool := newCrawlSourceMigrationTestPool(t)
		store, err := NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `DROP TABLE discovery_source_state`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReconcileAutomaticCrawlPolicy(ctx); err == nil {
			t.Error("ReconcileAutomaticCrawlPolicy() missing-source-table error = nil")
		}
	})

	t.Run("invalid stored source", func(t *testing.T) {
		ctx := context.Background()
		pool := newCrawlSourceMigrationTestPool(t)
		store, err := NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `ALTER TABLE discovery_source_state DROP CONSTRAINT discovery_source_state_origin_check`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO discovery_source_state (source_origin, automatically_discovered) VALUES ('bad', true)`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReconcileAutomaticCrawlPolicy(ctx); err == nil {
			t.Error("ReconcileAutomaticCrawlPolicy() corrupt-source error = nil")
		}
	})

	t.Run("corrupt rule", func(t *testing.T) {
		ctx := context.Background()
		pool := newCrawlSourceMigrationTestPool(t)
		store, err := NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `DROP TABLE crawl_domain_avoid_rules`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `CREATE TABLE crawl_domain_avoid_rules (pattern TEXT, created_at TIMESTAMPTZ)`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO crawl_domain_avoid_rules (pattern) VALUES (NULL)`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReconcileAutomaticCrawlPolicy(ctx); err == nil {
			t.Error("ReconcileAutomaticCrawlPolicy() corrupt-rule error = nil")
		}
	})
}

func TestAutomaticCrawlFamilyPatterns(t *testing.T) {
	config := AutomaticCrawlConfig{ExcludedHostSuffixes: "blogspot.*,,example.com"}
	for _, hostname := range []string{"blogspot.com", "writer.blogspot.com", "writer.blogspot.co.uk"} {
		if !config.excludes(hostname) {
			t.Errorf("excludes(%q) = false, want true", hostname)
		}
	}
	for _, hostname := range []string{"notblogspot.com", "blogspotter.example"} {
		if config.excludes(hostname) {
			t.Errorf("excludes(%q) = true, want false", hostname)
		}
	}
	if !config.excludes("www.example.com") {
		t.Error("exact suffix did not exclude a subdomain")
	}
}

func TestRecordDiscoveryRetainsEvidenceWhenAvoidRulesAreUnavailable(t *testing.T) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	seed := mustStoreOrigin(t, "https://seed.example")
	store, err := NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddCrawlSeed(ctx, seed); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP TABLE crawl_domain_avoid_rules`); err != nil {
		t.Fatal(err)
	}
	_, err = store.RecordDiscovery(ctx, seed, []discovery.Candidate{{
		Origin: mustStoreOrigin(t, "https://candidate.example"),
		Kind:   discovery.KindLink,
	}})
	if err != nil {
		t.Fatalf("RecordDiscovery() error = %v, want durable evidence", err)
	}
	var evidence int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM discovery_candidates`,
	).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if evidence != 1 {
		t.Fatalf("discovery evidence count = %d, want 1", evidence)
	}
	runID := beginAutomaticAdmissionRun(
		t,
		store,
		seed,
		store.automatic.maxAutomaticPromotionsPerRun(),
	)
	if _, err := store.PendingAutomaticCandidates(ctx, runID); err == nil {
		t.Fatal("PendingAutomaticCandidates() missing-rule-table error = nil")
	}
}

func TestReconcileAutomaticCrawlPolicyFailures(t *testing.T) {
	t.Run("invalid context", func(t *testing.T) {
		store, err := NewDiscoveryStoreWithAutomaticCrawling(newCrawlSourceMigrationTestPool(t), AutomaticCrawlConfig{Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		var nilContext context.Context
		if _, err := store.ReconcileAutomaticCrawlPolicy(nilContext); err == nil {
			t.Error("ReconcileAutomaticCrawlPolicy(nil) error = nil")
		}
	})

	t.Run("corrupt source row", func(t *testing.T) {
		ctx := context.Background()
		pool := newCrawlSourceMigrationTestPool(t)
		store, err := NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `DROP TABLE discovery_source_state`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `CREATE TABLE discovery_source_state (source_origin TEXT, automatically_discovered BOOLEAN, seeded BOOLEAN, crawl_blocked BOOLEAN)`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO discovery_source_state VALUES (NULL, true, false, false)`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReconcileAutomaticCrawlPolicy(ctx); err == nil {
			t.Error("ReconcileAutomaticCrawlPolicy() corrupt-row error = nil")
		}
	})

	t.Run("block failure", func(t *testing.T) {
		ctx := context.Background()
		pool := newCrawlSourceMigrationTestPool(t)
		raw := "https://writer.blogspot.com"
		if _, err := pool.Exec(ctx, `INSERT INTO discovery_source_state (source_origin, automatically_discovered) VALUES ($1, true)`, raw); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_source_block() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'blocked'; END $$`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `CREATE TRIGGER reject_source_block BEFORE UPDATE ON discovery_source_state FOR EACH ROW EXECUTE FUNCTION reject_source_block()`); err != nil {
			t.Fatal(err)
		}
		store, err := NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReconcileAutomaticCrawlPolicy(ctx); err == nil {
			t.Error("ReconcileAutomaticCrawlPolicy() block failure = nil")
		}
	})

	t.Run("probe removal failure", func(t *testing.T) {
		ctx := context.Background()
		pool := newCrawlSourceMigrationTestPool(t)
		raw := "https://writer.blogspot.com"
		if _, err := pool.Exec(ctx, `INSERT INTO discovery_source_state (source_origin, automatically_discovered) VALUES ($1, true)`, raw); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO verification_queue (origin, available_at, mode) VALUES ($1, CURRENT_TIMESTAMP, 'probe')`, raw); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_probe_removal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'blocked'; END $$`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `CREATE TRIGGER reject_probe_removal BEFORE DELETE ON verification_queue FOR EACH ROW EXECUTE FUNCTION reject_probe_removal()`); err != nil {
			t.Fatal(err)
		}
		store, err := NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReconcileAutomaticCrawlPolicy(ctx); err == nil {
			t.Error("ReconcileAutomaticCrawlPolicy() probe-removal failure = nil")
		}
	})
}

func TestCrawlSourcePolicyValidatesInputsAndDatabaseFailures(t *testing.T) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	sourceStore, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.SetCrawlBlocked(ctx, origin.Origin{}, true); err == nil {
		t.Error("SetCrawlBlocked(empty) error = nil")
	}
	var nilContext context.Context
	if err := sourceStore.SetCrawlBlocked(nilContext, mustStoreOrigin(t, "https://example.com"), true); err == nil {
		t.Error("SetCrawlBlocked(nil context) error = nil")
	}
	if _, err := sourceStore.CrawlSources(nilContext); err == nil {
		t.Error("CrawlSources(nil context) error = nil")
	}
	if _, err := pool.Exec(ctx, "DROP TABLE discovery_source_state"); err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.SetCrawlBlocked(ctx, mustStoreOrigin(t, "https://example.com"), true); err == nil || !strings.Contains(err.Error(), "set crawl block") {
		t.Fatalf("SetCrawlBlocked(database failure) error = %v", err)
	}
	if _, err := sourceStore.CrawlSources(ctx); err == nil || !strings.Contains(err.Error(), "list crawl sources") {
		t.Fatalf("CrawlSources(database failure) error = %v", err)
	}
}

func TestCrawlSourcesRejectsCorruptStoredData(t *testing.T) {
	t.Run("invalid origin", func(t *testing.T) {
		ctx := context.Background()
		pool := newCrawlSourceMigrationTestPool(t)
		if _, err := pool.Exec(ctx, `INSERT INTO discovery_source_state (source_origin) VALUES ('not an origin')`); err != nil {
			t.Fatal(err)
		}
		sourceStore, err := NewDiscoveryStore(pool)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sourceStore.CrawlSources(ctx); err == nil || !strings.Contains(err.Error(), "invalid origin") {
			t.Fatalf("CrawlSources() error = %v", err)
		}
	})

	t.Run("scan failure", func(t *testing.T) {
		ctx := context.Background()
		pool := newCrawlSourceMigrationTestPool(t)
		if _, err := pool.Exec(ctx, "DROP TABLE discovery_edges; ALTER TABLE discovery_candidates ALTER COLUMN first_discovered_at TYPE text USING first_discovered_at::text, ALTER COLUMN last_discovered_at TYPE text USING last_discovered_at::text"); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO discovery_candidates VALUES ('https://example.com','bad','bad'); INSERT INTO discovery_source_state (source_origin, automatically_discovered) VALUES ('https://example.com',true)`); err != nil {
			t.Fatal(err)
		}
		sourceStore, err := NewDiscoveryStore(pool)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sourceStore.CrawlSources(ctx); err == nil || !strings.Contains(err.Error(), "collect crawl sources") {
			t.Fatalf("CrawlSources() error = %v", err)
		}
	})
}

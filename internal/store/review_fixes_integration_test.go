package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/retry"
)

func TestDiscoveryRetryDueBeforeRecrawlInterval(t *testing.T) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	start := queueTestTime()
	source := seedDiscoveryTestSource(t, pool, "https://retry.example", start)
	due := start.Add(6 * time.Minute)
	completed := due.Add(time.Minute)
	interval := 168 * time.Hour
	storage := newDiscoveryTestStore(t, pool, start, start.Add(time.Minute), due.Add(-time.Nanosecond), due, completed, completed.Add(time.Minute), completed.Add(interval))
	lease, found, err := storage.ClaimDiscoverySourceLease(ctx, "retry-worker", interval, 5*time.Minute)
	if err != nil || !found {
		t.Fatalf("initial claim: found=%v err=%v", found, err)
	}
	if err := storage.CompleteDiscoverySourceLeaseRetry(ctx, lease, retry.CategoryTransport, 0); err != nil {
		t.Fatal(err)
	}
	var next time.Time
	if err := pool.QueryRow(ctx, "SELECT next_attempt_at FROM discovery_source_state WHERE source_origin=$1", source.String()).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if !next.Equal(due) {
		t.Fatalf("retry due = %v, want %v", next, due)
	}
	if _, found, err := storage.ClaimDiscoverySourceLease(ctx, "retry-worker", interval, 5*time.Minute); err != nil || found {
		t.Fatalf("early retry: found=%v err=%v", found, err)
	}
	lease, found, err = storage.ClaimDiscoverySourceLease(ctx, "retry-worker", interval, 5*time.Minute)
	if err != nil || !found {
		t.Fatalf("due retry: found=%v err=%v", found, err)
	}
	if err := storage.CompleteDiscoverySourceLeaseRetry(ctx, lease, retry.CategoryNone, 0); err != nil {
		t.Fatal(err)
	}
	if _, found, err := storage.ClaimDiscoverySourceLease(ctx, "retry-worker", interval, 5*time.Minute); err != nil || found {
		t.Fatalf("successful cycle interval: found=%v err=%v", found, err)
	}
	if _, found, err := storage.ClaimDiscoverySourceLease(ctx, "retry-worker", interval, 5*time.Minute); err != nil || !found {
		t.Fatalf("normal recrawl due: found=%v err=%v", found, err)
	}
}

func TestControlMutationPreservesConfiguredHostExclusion(t *testing.T) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	raw := "https://host.excluded.example"
	if _, err := pool.Exec(ctx, "INSERT INTO discovery_source_state(source_origin,automatically_discovered)VALUES($1,true)", raw); err != nil {
		t.Fatal(err)
	}
	discovery, err := NewDiscoveryStoreWithAutomaticCrawling(pool, AutomaticCrawlConfig{Enabled: true, ExcludedHostSuffixes: "excluded.example"})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := discovery.ReconcileAutomaticCrawlPolicy(ctx); err != nil || changed != 1 {
		t.Fatalf("initial block changed=%d err=%v", changed, err)
	}
	commander, err := NewControlStoreWithExcludedHosts(pool, "excluded.example")
	if err != nil {
		t.Fatal(err)
	}
	operations := []struct {
		name string
		run  func() error
	}{
		{"add unrelated rule", func() error {
			return commander.AddDomainAvoid(ctx, "unrelated.example", controlAudit("domain-avoid.add", "unrelated.example"))
		}},
		{"remove unrelated rule", func() error {
			return commander.RemoveDomainAvoid(ctx, "unrelated.example", controlAudit("domain-avoid.remove", "unrelated.example"))
		}},
		{"allow excluded origin", func() error { return commander.SetOriginBlocked(ctx, raw, false, controlAudit("origin.allow", raw)) }},
	}
	for _, operation := range operations {
		if err := operation.run(); err != nil {
			t.Fatal(err)
		}
		var blocked bool
		if err := pool.QueryRow(ctx, "SELECT crawl_blocked FROM discovery_source_state WHERE source_origin=$1", raw).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if !blocked {
			t.Fatalf("%s cleared configured exclusion", operation.name)
		}
	}
	if _, err := NewControlStoreWithExcludedHosts(nil, ""); !errors.Is(err, errPoolUnavailable) {
		t.Fatalf("nil pool error = %v", err)
	}
	if _, err := NewControlStoreWithExcludedHosts(pool, "https://bad.example"); err == nil {
		t.Fatal("invalid configured exclusion accepted")
	}
}

func TestControlUnblockingResumesVerificationWork(t *testing.T) {
	for _, mode := range []string{"probe", "reprobe"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			pool := newCrawlSourceMigrationTestPool(t)
			raw := "https://unblock.example"
			now := queueTestTime()
			if _, err := pool.Exec(ctx, "INSERT INTO discovery_source_state(source_origin,automatically_discovered)VALUES($1,true)", raw); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO verification_queue(origin,available_at,mode,consecutive_failures,last_failure_category,next_attempt_at)VALUES($1,$2,$3,2,'http_5xx',$2)`, raw, now, mode); err != nil {
				t.Fatal(err)
			}
			commander, err := NewControlStore(pool)
			if err != nil {
				t.Fatal(err)
			}
			queue := newFixedQueue(t, pool, QueueConfig{LeaseDuration: 5 * time.Minute, MinOriginInterval: time.Nanosecond}, now)
			if err := commander.SetOriginBlocked(ctx, raw, true, controlAudit("origin.block", raw)); err != nil {
				t.Fatal(err)
			}
			if _, found, err := queue.Claim(ctx, "resumer"); err != nil || found {
				t.Fatalf("blocked work claimed: %v %v", found, err)
			}
			if err := commander.SetOriginBlocked(ctx, raw, false, controlAudit("origin.allow", raw)); err != nil {
				t.Fatal(err)
			}
			lease, found, err := queue.Claim(ctx, "resumer")
			if err != nil || !found || lease.Origin.String() != raw {
				t.Fatalf("resumed claim: %#v %v %v", lease, found, err)
			}
			var gotMode, category string
			var failures int
			var available, next time.Time
			if err := pool.QueryRow(ctx, `SELECT mode,consecutive_failures,last_failure_category,available_at,next_attempt_at FROM verification_queue WHERE origin=$1`, raw).Scan(&gotMode, &failures, &category, &available, &next); err != nil {
				t.Fatal(err)
			}
			if gotMode != mode || failures != 2 || category != "http_5xx" || !available.Equal(now) || !next.Equal(now) {
				t.Fatalf("suspended work changed: %s %d %s %v %v", gotMode, failures, category, available, next)
			}
		})
	}
}

func TestBlockedVerificationLeaseCannotRenewOrComplete(t *testing.T) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	source := mustStoreOrigin(t, "https://leased.example")
	now := queueTestTime()
	if _, err := pool.Exec(ctx, `INSERT INTO verification_queue(origin,available_at,mode)VALUES($1,$2,'probe')`, source.String(), now); err != nil {
		t.Fatal(err)
	}
	queue := newFixedQueue(t, pool, QueueConfig{LeaseDuration: 5 * time.Minute, MinOriginInterval: time.Nanosecond}, now)
	lease, found, err := queue.Claim(ctx, "owner")
	if err != nil || !found {
		t.Fatalf("claim: %v %v", found, err)
	}
	commander, err := NewControlStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := commander.SetOriginBlocked(ctx, source.String(), true, controlAudit("origin.block", source.String())); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Renew(ctx, lease); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("blocked renewal = %v", err)
	}
	result := validCompletionResult(source)
	if err := queue.CompleteVerification(ctx, lease, result, time.Hour); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("blocked completion = %v", err)
	}
	var observations int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM verification_observations WHERE origin=$1`, source.String()).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if observations != 0 {
		t.Fatal("blocked completion persisted an observation")
	}
	if err := commander.SetOriginBlocked(ctx, source.String(), false, controlAudit("origin.allow", source.String())); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Renew(ctx, lease); err != nil {
		t.Fatalf("resume active lease: %v", err)
	}
	if err := queue.CompleteVerification(ctx, lease, result, time.Hour); err != nil {
		t.Fatalf("resume completion: %v", err)
	}
}

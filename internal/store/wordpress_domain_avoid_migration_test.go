package store

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWordPressDomainAvoidMigrationRemovesWordPressRules(t *testing.T) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	assertDomainAvoidCount(t, ctx, pool, "wordpress.com", 0)
	assertDomainAvoidCount(t, ctx, pool, "wordpress.org", 0)
	assertDomainAvoidCount(t, ctx, pool, "blogspot.*", 1)
	assertDomainAvoidCount(t, ctx, pool, "centerblog.net", 1)
}

func TestWordPressDomainAvoidMigrationClearsWordPressBlocks(t *testing.T) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO crawl_domain_avoid_rules (pattern)
		VALUES ('wordpress.com'), ('wordpress.org')
	`); err != nil {
		t.Fatalf("restore wordpress avoid rules: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery_source_state (
			source_origin,
			automatically_discovered,
			operator_blocked,
			crawl_blocked
		) VALUES
			('https://blog.wordpress.com', true, true, true),
			('https://wordpress.org', true, true, true),
			('https://kept.example', true, true, true)
	`); err != nil {
		t.Fatalf("insert blocked sources: %v", err)
	}

	migration, err := embeddedMigrations.ReadFile(
		"migrations/0028_remove_wordpress_domain_avoid.sql",
	)
	if err != nil {
		t.Fatalf("read wordpress avoid migration: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		string(migration),
		pgx.QueryExecModeSimpleProtocol,
	); err != nil {
		t.Fatalf("reapply wordpress avoid migration: %v", err)
	}

	assertDomainAvoidCount(t, ctx, pool, "wordpress.com", 0)
	assertDomainAvoidCount(t, ctx, pool, "wordpress.org", 0)
	assertCrawlBlocked(t, ctx, pool, "https://blog.wordpress.com", false)
	assertCrawlBlocked(t, ctx, pool, "https://wordpress.org", false)
	assertCrawlBlocked(t, ctx, pool, "https://kept.example", true)
}

func assertDomainAvoidCount(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	pattern string,
	want int,
) {
	t.Helper()

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM crawl_domain_avoid_rules
		WHERE pattern = $1
	`, pattern).Scan(&count); err != nil {
		t.Fatalf("count pattern %q: %v", pattern, err)
	}
	if count != want {
		t.Fatalf("pattern %q count = %d, want %d", pattern, count, want)
	}
}

func assertCrawlBlocked(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	origin string,
	want bool,
) {
	t.Helper()

	var operatorBlocked bool
	var crawlBlocked bool
	if err := pool.QueryRow(ctx, `
		SELECT operator_blocked, crawl_blocked
		FROM discovery_source_state
		WHERE source_origin = $1
	`, origin).Scan(&operatorBlocked, &crawlBlocked); err != nil {
		t.Fatalf("read block state for %s: %v", origin, err)
	}
	if operatorBlocked != want || crawlBlocked != want {
		t.Fatalf(
			"%s operator_blocked=%t crawl_blocked=%t, want both %t",
			origin,
			operatorBlocked,
			crawlBlocked,
			want,
		)
	}
}

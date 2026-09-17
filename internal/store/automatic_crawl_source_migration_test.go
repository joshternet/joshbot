package store

import (
	"context"
	"testing"
)

func TestAutomaticCrawlSourceMigrationPreservesExistingClassification(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	for _, migration := range []string{
		"migrations/0001_initial.sql",
		"migrations/0002_verification_queue.sql",
		"migrations/0003_discovery.sql",
		"migrations/0004_crawl_sources.sql",
	} {
		applyRawStoreMigration(t, ctx, pool, migration)
	}

	const source = "https://seed.example"
	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_state (
				source_origin,
				seeded
			)
			VALUES ($1, true)
		`,
		source,
	)
	if err != nil {
		t.Fatalf("insert existing source: %v", err)
	}

	applyRawStoreMigration(
		t,
		ctx,
		pool,
		"migrations/0005_automatic_crawl_sources.sql",
	)

	var (
		seeded                  bool
		automaticallyDiscovered bool
		crawlBlocked            bool
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				seeded,
				automatically_discovered,
				crawl_blocked
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		source,
	).Scan(
		&seeded,
		&automaticallyDiscovered,
		&crawlBlocked,
	)
	if err != nil {
		t.Fatalf("query migrated source: %v", err)
	}

	if !seeded {
		t.Error("seeded = false, want true")
	}

	if automaticallyDiscovered {
		t.Error("automatically_discovered = true, want false")
	}

	if crawlBlocked {
		t.Error("crawl_blocked = true, want false")
	}
}

func TestAutomaticCrawlSourceMigrationAllowsIndependentClassification(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate() error = %v, want nil", err)
	}

	const source = "https://discovered.example"
	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_state (
				source_origin,
				automatically_discovered,
				crawl_blocked
			)
			VALUES ($1, true, true)
		`,
		source,
	)
	if err != nil {
		t.Fatalf("insert automatic source: %v", err)
	}

	var originCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM origins
			WHERE origin = $1
		`,
		source,
	).Scan(&originCount)
	if err != nil {
		t.Fatalf("count verification origins: %v", err)
	}

	if originCount != 0 {
		t.Errorf("verification origin count = %d, want 0", originCount)
	}
}

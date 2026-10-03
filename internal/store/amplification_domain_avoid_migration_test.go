package store

import (
	"context"
	"testing"
)

func TestAmplificationDomainAvoidMigrationSeedsCenterblog(t *testing.T) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM crawl_domain_avoid_rules
		WHERE pattern = 'centerblog.net'
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("centerblog.net rules = %d, want 1", count)
	}
}

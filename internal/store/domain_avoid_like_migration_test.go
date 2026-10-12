package store

import (
	"context"
	"testing"
)

func TestDomainAvoidMigrationRejectsLikeMetacharacters(t *testing.T) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	for _, pattern := range []string{
		"foo_bar.example",
		"foo%bar.example",
		`foo\bar.example`,
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO crawl_domain_avoid_rules (pattern)
			VALUES ($1)
		`, pattern); err == nil {
			t.Fatalf(
				"pattern %q accepted, want CHECK rejection",
				pattern,
			)
		}
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO crawl_domain_avoid_rules (pattern)
		VALUES ('safe.example')
	`); err != nil {
		t.Fatalf(
			"safe pattern insert error = %v",
			err,
		)
	}
}

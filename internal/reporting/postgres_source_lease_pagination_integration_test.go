package reporting

import (
	"context"
	"testing"
	"time"
)

func TestPostgresReaderSourcesPageReturnsDiscoveryLease(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)

	seedReportingFixture(
		t,
		pool,
	)

	claimedAt := time.Date(
		2026,
		time.September,
		27,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	expiresAt := claimedAt.Add(
		5 * time.Minute,
	)

	_, err := pool.Exec(
		ctx,
		`
			UPDATE discovery_source_state
			SET
				lease_generation = 7,
				lease_owner = 'discovery-worker-1',
				last_claimed_at = $1,
				lease_expires_at = $2
			WHERE source_origin = 'https://auto.example'
		`,
		claimedAt,
		expiresAt,
	)
	if err != nil {
		t.Fatalf(
			"update discovery source lease: %v",
			err,
		)
	}

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{
			AutomaticCrawlEnabled: true,
			MaxPendingProbes:      1000,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	page, err := reader.SourcesPage(
		ctx,
		sourceQuery{
			Limit:  10,
			Origin: "https://auto.example",
		},
	)
	if err != nil {
		t.Fatalf(
			"SourcesPage() error = %v",
			err,
		)
	}

	if len(page.Items) != 1 {
		t.Fatalf(
			"SourcesPage() length = %d, want 1",
			len(page.Items),
		)
	}

	source := page.Items[0]

	if source.Origin != "https://auto.example" {
		t.Fatalf(
			"source origin = %q, want https://auto.example",
			source.Origin,
		)
	}

	if source.LeaseGeneration != 7 {
		t.Fatalf(
			"lease generation = %d, want 7",
			source.LeaseGeneration,
		)
	}

	if source.LeaseOwner != "discovery-worker-1" {
		t.Fatalf(
			"lease owner = %q, want discovery-worker-1",
			source.LeaseOwner,
		)
	}

	if source.LeaseExpiresAt == nil ||
		!source.LeaseExpiresAt.Equal(expiresAt) {
		t.Fatalf(
			"lease expires at = %v, want %v",
			source.LeaseExpiresAt,
			expiresAt,
		)
	}

	if source.LastClaimedAt == nil ||
		!source.LastClaimedAt.Equal(claimedAt) {
		t.Fatalf(
			"last claimed at = %v, want %v",
			source.LastClaimedAt,
			claimedAt,
		)
	}

	if source.LeaseOwner == "worker-1" {
		t.Fatal(
			"discovery lease owner was confused with verification queue owner",
		)
	}
}

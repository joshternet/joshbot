package reporting

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPostgresReaderReturnsSourceDetail(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)

	fixture := seedReportingFixture(
		t,
		pool,
	)

	now := time.Now().
		UTC().
		Truncate(time.Microsecond)

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_edges (
				source_origin,
				candidate_origin,
				kind,
				first_discovered_at,
				last_discovered_at
			)
			VALUES (
				'https://seed.example',
				'https://auto.example',
				'link',
				$1,
				$2
			)
		`,
		now.Add(-24*time.Hour),
		now.Add(-time.Hour),
	)
	if err != nil {
		t.Fatalf(
			"insert discovery edge: %v",
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

	seeded, found, err := reader.Source(
		ctx,
		"https://seed.example",
		10,
	)
	if err != nil {
		t.Fatalf(
			"Source(seed) error = %v",
			err,
		)
	}

	if !found {
		t.Fatal(
			"Source(seed) found = false, want true",
		)
	}

	if !seeded.Source.Seeded ||
		!seeded.Source.Verified ||
		!seeded.Source.CrawlEligible {
		t.Errorf(
			"seed source = %#v",
			seeded.Source,
		)
	}

	if seeded.Queue == nil ||
		seeded.Queue.Mode != "recurring" ||
		seeded.Queue.LeaseOwner != "" {
		t.Errorf(
			"seed queue = %#v",
			seeded.Queue,
		)
	}

	if seeded.LatestCrawl == nil ||
		seeded.LatestCrawl.ID != fixture.crawlID ||
		seeded.LatestCrawl.Outcome != "complete" {
		t.Errorf(
			"seed latest crawl = %#v",
			seeded.LatestCrawl,
		)
	}

	if seeded.LatestRobotsObservation == nil ||
		seeded.LatestRobotsObservation.CrawlRunID !=
			fixture.crawlID ||
		seeded.LatestRobotsObservation.Decision !=
			"allowed" {
		t.Errorf(
			"seed robots observation = %#v",
			seeded.LatestRobotsObservation,
		)
	}

	if seeded.DiscoveredBy == nil {
		t.Error(
			"seed DiscoveredBy = nil, want empty array",
		)
	}

	if len(seeded.DiscoveredBy) != 0 {
		t.Errorf(
			"seed DiscoveredBy = %#v, want empty",
			seeded.DiscoveredBy,
		)
	}

	automatic, found, err := reader.Source(
		ctx,
		"https://auto.example",
		1,
	)
	if err != nil {
		t.Fatalf(
			"Source(auto) error = %v",
			err,
		)
	}

	if !found {
		t.Fatal(
			"Source(auto) found = false, want true",
		)
	}

	if !automatic.Source.AutomaticallyDiscovered ||
		automatic.Source.Blocked ||
		!automatic.Source.CrawlEligible {
		t.Errorf(
			"automatic source = %#v",
			automatic.Source,
		)
	}

	if automatic.Queue == nil ||
		automatic.Queue.Mode != "probe" ||
		automatic.Queue.LeaseOwner != "worker-1" {
		t.Errorf(
			"automatic queue = %#v",
			automatic.Queue,
		)
	}

	if automatic.LatestCrawl != nil {
		t.Errorf(
			"automatic LatestCrawl = %#v, want nil",
			automatic.LatestCrawl,
		)
	}

	if automatic.LatestRobotsObservation != nil {
		t.Errorf(
			"automatic LatestRobotsObservation = %#v, want nil",
			automatic.LatestRobotsObservation,
		)
	}

	if len(automatic.DiscoveredBy) != 1 {
		t.Fatalf(
			"automatic DiscoveredBy = %d, want 1",
			len(automatic.DiscoveredBy),
		)
	}

	if automatic.DiscoveredBy[0].SourceOrigin !=
		"https://seed.example" ||
		automatic.DiscoveredBy[0].Kind != "link" {
		t.Errorf(
			"automatic provenance = %#v",
			automatic.DiscoveredBy[0],
		)
	}

	blocked, found, err := reader.Source(
		ctx,
		"https://blocked.example",
		10,
	)
	if err != nil {
		t.Fatalf(
			"Source(blocked) error = %v",
			err,
		)
	}

	if !found {
		t.Fatal(
			"Source(blocked) found = false, want true",
		)
	}

	if !blocked.Source.Blocked ||
		blocked.Source.CrawlEligible {
		t.Errorf(
			"blocked source = %#v",
			blocked.Source,
		)
	}

	if blocked.Queue != nil ||
		blocked.LatestCrawl != nil ||
		blocked.LatestRobotsObservation != nil {
		t.Errorf(
			"blocked detail = %#v",
			blocked,
		)
	}

	missing, found, err := reader.Source(
		ctx,
		"https://missing.example",
		10,
	)
	if err != nil {
		t.Fatalf(
			"Source(missing) error = %v",
			err,
		)
	}

	if found {
		t.Error(
			"Source(missing) found = true, want false",
		)
	}

	if missing.Source.Origin != "" ||
		missing.Queue != nil ||
		missing.LatestCrawl != nil {
		t.Errorf(
			"Source(missing) = %#v, want zero value",
			missing,
		)
	}
}

func TestPostgresReaderSourceRejectsInvalidRequests(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{
			MaxPendingProbes: 1000,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	if _, _, err := reader.Source(
		ctx,
		"https://example.com",
		0,
	); !errors.Is(err, errInvalidLimit) {
		t.Errorf(
			"Source(limit=0) error = %v, want %v",
			err,
			errInvalidLimit,
		)
	}

	if _, _, err := reader.Source(
		ctx,
		"not-an-origin",
		10,
	); !errors.Is(
		err,
		errInvalidSourceOrigin,
	) {
		t.Errorf(
			"Source(invalid origin) error = %v, want %v",
			err,
			errInvalidSourceOrigin,
		)
	}
}

func TestPostgresReaderSourcePreservesBaseFailure(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{
			MaxPendingProbes: 1000,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	pool.Close()

	_, found, err := reader.Source(
		ctx,
		"https://example.com",
		10,
	)
	if err == nil {
		t.Fatal(
			"Source() error = nil, want database failure",
		)
	}

	if found {
		t.Error(
			"Source() found = true, want false",
		)
	}
}

func TestPostgresReaderSourcePreservesProvenanceFailure(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)

	seedReportingFixture(
		t,
		pool,
	)

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{
			MaxPendingProbes: 1000,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		"DROP TABLE discovery_edges",
	); err != nil {
		t.Fatalf(
			"drop discovery_edges: %v",
			err,
		)
	}

	_, found, err := reader.Source(
		ctx,
		"https://seed.example",
		10,
	)
	if err == nil {
		t.Fatal(
			"Source() error = nil, want provenance failure",
		)
	}

	if found {
		t.Error(
			"Source() found = true, want false on provenance failure",
		)
	}
}

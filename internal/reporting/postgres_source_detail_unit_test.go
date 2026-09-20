package reporting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPostgresReaderSourceWithoutDatabase(
	t *testing.T,
) {
	now := time.Date(
		2026,
		time.September,
		18,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	firstDiscovered := now.Add(-48 * time.Hour)
	lastDiscovered := now.Add(-24 * time.Hour)
	queueAvailable := now.Add(time.Hour)
	leaseExpires := now.Add(30 * time.Minute)
	lastClaimed := now.Add(-time.Minute)
	crawlFinished := now.Add(-10 * time.Minute)
	provenanceFirst := now.Add(-72 * time.Hour)
	provenanceLast := now.Add(-36 * time.Hour)

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			rowResults: []fakeRow{
				{
					values: unitSourceDetailValues(
						now,
						&firstDiscovered,
						&lastDiscovered,
						queueAvailable,
						&leaseExpires,
						&lastClaimed,
						&crawlFinished,
					),
				},
			},
			queryResults: []fakeQueryResult{
				{
					rows: newFakeRows(
						[]any{
							"https://discoverer.example",
							"link",
							provenanceFirst,
							provenanceLast,
						},
					),
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	detail, found, err := reader.Source(
		context.Background(),
		"https://example.com",
		10,
	)
	if err != nil {
		t.Fatalf(
			"Source() error = %v",
			err,
		)
	}

	if !found {
		t.Fatal(
			"Source() found = false, want true",
		)
	}

	if detail.Source.Origin !=
		"https://example.com" ||
		!detail.Source.Seeded ||
		!detail.Source.AutomaticallyDiscovered ||
		detail.Source.Blocked ||
		!detail.Source.Verified ||
		!detail.Source.CrawlEligible {
		t.Fatalf(
			"Source().Source = %#v",
			detail.Source,
		)
	}

	if detail.Queue == nil {
		t.Fatal(
			"Source().Queue = nil",
		)
	}

	if detail.Queue.Origin !=
		"https://example.com" ||
		detail.Queue.Mode != "probe" ||
		detail.Queue.LeaseGeneration != 3 ||
		detail.Queue.LeaseOwner != "worker-1" {
		t.Fatalf(
			"Source().Queue = %#v",
			detail.Queue,
		)
	}

	if detail.LatestCrawl == nil {
		t.Fatal(
			"Source().LatestCrawl = nil",
		)
	}

	if detail.LatestCrawl.ID != 42 ||
		detail.LatestCrawl.SourceOrigin !=
			"https://example.com" ||
		detail.LatestCrawl.Outcome != "complete" ||
		detail.LatestCrawl.PagesAttempted != 4 ||
		detail.LatestCrawl.PagesParsed != 3 ||
		detail.LatestCrawl.CandidatesDiscovered != 2 {
		t.Fatalf(
			"Source().LatestCrawl = %#v",
			detail.LatestCrawl,
		)
	}

	if detail.LatestRobotsObservation == nil {
		t.Fatal(
			"Source().LatestRobotsObservation = nil",
		)
	}

	if detail.LatestRobotsObservation.CrawlRunID != 42 ||
		detail.LatestRobotsObservation.Sequence != 2 ||
		detail.LatestRobotsObservation.Decision != "allowed" {
		t.Fatalf(
			"Source().LatestRobotsObservation = %#v",
			detail.LatestRobotsObservation,
		)
	}

	if len(detail.DiscoveredBy) != 1 {
		t.Fatalf(
			"Source().DiscoveredBy length = %d, want 1",
			len(detail.DiscoveredBy),
		)
	}

	if detail.DiscoveredBy[0].SourceOrigin !=
		"https://discoverer.example" ||
		detail.DiscoveredBy[0].Kind != "link" ||
		!detail.DiscoveredBy[0].
			FirstDiscoveredAt.Equal(
			provenanceFirst,
		) ||
		!detail.DiscoveredBy[0].
			LastDiscoveredAt.Equal(
			provenanceLast,
		) {
		t.Fatalf(
			"Source().DiscoveredBy[0] = %#v",
			detail.DiscoveredBy[0],
		)
	}
}

func TestPostgresReaderSourceRejectsInvalidInputWithoutDatabase(
	t *testing.T,
) {
	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	if _, _, err := reader.Source(
		context.Background(),
		"https://example.com",
		0,
	); !errors.Is(
		err,
		errInvalidLimit,
	) {
		t.Fatalf(
			"Source(limit=0) error = %v",
			err,
		)
	}

	if _, _, err := reader.Source(
		context.Background(),
		"not-an-origin",
		1,
	); !errors.Is(
		err,
		errInvalidSourceOrigin,
	) {
		t.Fatalf(
			"Source(invalid origin) error = %v",
			err,
		)
	}
}

func TestPostgresReaderSourceNotFoundWithoutDatabase(
	t *testing.T,
) {
	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			rowResults: []fakeRow{
				{
					err: pgx.ErrNoRows,
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	detail, found, err := reader.Source(
		context.Background(),
		"https://missing.example",
		1,
	)
	if err != nil {
		t.Fatalf(
			"Source() error = %v",
			err,
		)
	}

	if found {
		t.Fatal(
			"Source() found = true, want false",
		)
	}

	if detail.Source.Origin != "" ||
		detail.Queue != nil ||
		detail.LatestCrawl != nil ||
		detail.LatestRobotsObservation != nil ||
		len(detail.DiscoveredBy) != 0 {
		t.Fatalf(
			"Source() detail = %#v, want zero value",
			detail,
		)
	}
}

func TestPostgresReaderSourceBaseFailureWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test source detail failure",
	)

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			rowResults: []fakeRow{
				{
					err: testErr,
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	_, found, err := reader.Source(
		context.Background(),
		"https://example.com",
		1,
	)

	if found {
		t.Fatal(
			"Source() found = true, want false",
		)
	}

	if !errors.Is(
		err,
		testErr,
	) ||
		!strings.Contains(
			err.Error(),
			"reporting: read source detail",
		) {
		t.Fatalf(
			"Source() error = %v",
			err,
		)
	}
}

func TestPostgresReaderSourceProvenanceQueryFailureWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test source provenance query failure",
	)
	now := time.Now().UTC()

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			rowResults: []fakeRow{
				{
					values: unitSourceDetailValues(
						now,
						nil,
						nil,
						time.Time{},
						nil,
						nil,
						nil,
					),
				},
			},
			queryResults: []fakeQueryResult{
				{
					err: testErr,
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	detail, found, err := reader.Source(
		context.Background(),
		"https://example.com",
		1,
	)

	if found {
		t.Fatal(
			"Source() found = true, want false",
		)
	}

	if detail.Source.Origin != "" {
		t.Fatalf(
			"Source() detail = %#v, want zero value on provenance failure",
			detail,
		)
	}

	if !errors.Is(
		err,
		testErr,
	) ||
		!strings.Contains(
			err.Error(),
			"reporting: read source provenance",
		) {
		t.Fatalf(
			"Source() error = %v",
			err,
		)
	}
}

func TestPostgresReaderSourceProvenanceCollectFailureWithoutDatabase(
	t *testing.T,
) {
	now := time.Now().UTC()

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			rowResults: []fakeRow{
				{
					values: unitSourceDetailValues(
						now,
						nil,
						nil,
						time.Time{},
						nil,
						nil,
						nil,
					),
				},
			},
			queryResults: []fakeQueryResult{
				{
					rows: newFakeRows(
						[]any{
							"https://discoverer.example",
						},
					),
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	detail, found, err := reader.Source(
		context.Background(),
		"https://example.com",
		1,
	)

	if found {
		t.Fatal(
			"Source() found = true, want false",
		)
	}

	if detail.Source.Origin != "" {
		t.Fatalf(
			"Source() detail = %#v, want zero value on provenance failure",
			detail,
		)
	}

	if err == nil {
		t.Fatal(
			"Source() error = nil, want collection failure",
		)
	}
}

func unitSourceDetailValues(
	now time.Time,
	firstDiscovered *time.Time,
	lastDiscovered *time.Time,
	queueAvailable time.Time,
	leaseExpires *time.Time,
	lastClaimed *time.Time,
	crawlFinished *time.Time,
) []any {
	return []any{
		"https://example.com",
		true,
		true,
		false,
		true,
		true,
		firstDiscovered,
		lastDiscovered,

		true,
		"https://example.com",
		"probe",
		queueAvailable,
		int64(3),
		"worker-1",
		leaseExpires,
		lastClaimed,

		true,
		int64(42),
		"https://example.com",
		now.Add(-time.Hour),
		crawlFinished,
		"complete",
		"frontier_exhausted",
		4,
		3,
		2,
		false,
		2,
		32,
		int64(1 << 20),
		int64(250),
		5,
		int64(5000),

		true,
		int64(42),
		2,
		now.Add(-45 * time.Minute),
		"allowed",
	}
}

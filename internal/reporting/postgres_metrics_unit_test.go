package reporting

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type metricsQueryCapture struct {
	*fakeQueryer

	query string
}

func (queryer *metricsQueryCapture) QueryRow(
	ctx context.Context,
	query string,
	args ...any,
) pgx.Row {
	queryer.query = query

	return queryer.fakeQueryer.QueryRow(
		ctx,
		query,
		args...,
	)
}

func TestPostgresReaderMetricsWithoutDatabase(
	t *testing.T,
) {
	queryer := &metricsQueryCapture{
		fakeQueryer: &fakeQueryer{
			rowResults: []fakeRow{
				{
					values: []any{
						int64(1),
						int64(2),
						int64(3),
						int64(4),
						int64(5),
						int64(6),
						int64(7),
						int64(8),
						int64(9),
						int64(10),
						int64(11),
					},
				},
			},
		},
	}

	reader, err := newPostgresReader(
		queryer,
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)
	if err != nil {
		t.Fatalf(
			"newPostgresReader() error = %v",
			err,
		)
	}

	metrics, err := reader.Metrics(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"Metrics() error = %v",
			err,
		)
	}

	want := OperationalMetrics{
		Candidates:          1,
		DiscoveryEdges:      2,
		CrawlRuns:           3,
		UnfinishedCrawlRuns: 4,
		PagesAttempted:      5,
		PagesParsed:         6,
		OriginsFound:        7,
		OriginsPromoted:     8,
		OriginsDeferred:     9,
		Failures:            10,
		RobotsDenials:       11,
	}

	if metrics != want {
		t.Fatalf(
			"Metrics() = %#v, want %#v",
			metrics,
			want,
		)
	}

	normalizedQuery := strings.Join(
		strings.Fields(queryer.query),
		" ",
	)

	if !strings.Contains(
		normalizedQuery,
		"(SELECT count(*) FROM crawl_runs WHERE finished_at IS NULL)",
	) {
		t.Fatalf(
			"Metrics() query does not count unfinished crawl runs: %s",
			normalizedQuery,
		)
	}
}

func TestPostgresReaderMetricsFailureWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test metrics failure",
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

	metrics, err := reader.Metrics(
		context.Background(),
	)

	if metrics != (OperationalMetrics{}) {
		t.Fatalf(
			"Metrics() = %#v, want zero",
			metrics,
		)
	}

	if !errors.Is(
		err,
		testErr,
	) {
		t.Fatalf(
			"Metrics() error = %v, want %v",
			err,
			testErr,
		)
	}

	if !strings.Contains(
		err.Error(),
		"reporting: read metrics",
	) {
		t.Fatalf(
			"Metrics() error = %v, want reporting context",
			err,
		)
	}
}

package reporting

import (
	"context"
	"errors"
	"reflect"
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
			queryResults: []fakeQueryResult{
				{
					rows: newFakeRows(
						[]any{
							"page_failure_category",
							"timeout",
							int64(2),
						},
						[]any{
							"http_status",
							"403",
							int64(3),
						},
						[]any{
							"verification_queue_failure_category",
							"robots_temporary",
							int64(4),
						},
					),
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
		PageFailureCategories: []MetricBreakdown{
			{
				Label: "timeout",
				Count: 2,
			},
		},
		HTTPStatuses: []MetricBreakdown{
			{
				Label: "403",
				Count: 3,
			},
		},
		VerificationQueueFailureCategories: []MetricBreakdown{
			{
				Label: "robots_temporary",
				Count: 4,
			},
		},
	}

	if !reflect.DeepEqual(
		metrics,
		want,
	) {
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

	if queryer.queryIndex != 1 {
		t.Fatalf(
			"Metrics() breakdown query count = %d, want 1",
			queryer.queryIndex,
		)
	}
}

func TestPostgresReaderMetricBreakdownFailuresWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test metric breakdown failure",
	)

	scanRows := newFakeRows(
		[]any{
			"page_failure_category",
		},
	)

	iterationRows := newFakeRows()
	iterationRows.err = testErr

	tests := []struct {
		name        string
		queryResult fakeQueryResult
		wantContext string
		wantCause   bool
	}{
		{
			name: "query failure",
			queryResult: fakeQueryResult{
				err: testErr,
			},
			wantContext: "reporting: read metric breakdowns",
			wantCause:   true,
		},
		{
			name: "scan failure",
			queryResult: fakeQueryResult{
				rows: scanRows,
			},
			wantContext: "reporting: scan metric breakdown",
		},
		{
			name: "unknown breakdown kind",
			queryResult: fakeQueryResult{
				rows: newFakeRows(
					[]any{
						"unknown",
						"value",
						int64(1),
					},
				),
			},
			wantContext: `reporting: unknown metric breakdown kind "unknown"`,
		},
		{
			name: "iteration failure",
			queryResult: fakeQueryResult{
				rows: iterationRows,
			},
			wantContext: "reporting: iterate metric breakdowns",
			wantCause:   true,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				reader := mustUnitPostgresReader(
					t,
					&fakeQueryer{
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
						queryResults: []fakeQueryResult{
							test.queryResult,
						},
					},
					PostgresConfig{
						MaxPendingProbes: 1,
					},
				)

				metrics, err := reader.Metrics(
					context.Background(),
				)

				if !reflect.DeepEqual(
					metrics,
					OperationalMetrics{},
				) {
					t.Fatalf(
						"Metrics() = %#v, want zero",
						metrics,
					)
				}

				if err == nil {
					t.Fatal(
						"Metrics() error = nil, want non-nil",
					)
				}

				if !strings.Contains(
					err.Error(),
					test.wantContext,
				) {
					t.Fatalf(
						"Metrics() error = %v, want context %q",
						err,
						test.wantContext,
					)
				}

				if test.wantCause &&
					!errors.Is(
						err,
						testErr,
					) {
					t.Fatalf(
						"Metrics() error = %v, want %v",
						err,
						testErr,
					)
				}
			},
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

	if !reflect.DeepEqual(
		metrics,
		OperationalMetrics{},
	) {
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

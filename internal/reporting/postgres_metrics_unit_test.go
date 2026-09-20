package reporting

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPostgresReaderMetricsWithoutDatabase(
	t *testing.T,
) {
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
					},
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
	if err != nil {
		t.Fatalf(
			"Metrics() error = %v",
			err,
		)
	}

	want := OperationalMetrics{
		Candidates:      1,
		DiscoveryEdges:  2,
		CrawlRuns:       3,
		PagesAttempted:  4,
		PagesParsed:     5,
		OriginsFound:    6,
		OriginsPromoted: 7,
		OriginsDeferred: 8,
		Failures:        9,
		RobotsDenials:   10,
	}

	if metrics != want {
		t.Fatalf(
			"Metrics() = %#v, want %#v",
			metrics,
			want,
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

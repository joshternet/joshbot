package reporting

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const (
	metricKindPageFailureCategory              = "page_failure_category"
	metricKindHTTPStatus                       = "http_status"
	metricKindVerificationQueueFailureCategory = "verification_queue_failure_category"
)

// MetricBreakdown is one labeled count within an operational metric family.
type MetricBreakdown struct {
	Label string
	Count int64
}

// OperationalMetrics contains durable crawler operational measurements.
type OperationalMetrics struct {
	Candidates                         int64
	DiscoveryEdges                     int64
	CrawlRuns                          int64
	UnfinishedCrawlRuns                int64
	PagesAttempted                     int64
	PagesParsed                        int64
	OriginsFound                       int64
	OriginsPromoted                    int64
	OriginsDeferred                    int64
	Failures                           int64
	RobotsDenials                      int64
	PageFailureCategories              []MetricBreakdown
	HTTPStatuses                       []MetricBreakdown
	VerificationQueueFailureCategories []MetricBreakdown
}

type metricsReader interface {
	Metrics(context.Context) (OperationalMetrics, error)
}

type metricBreakdownRow struct {
	Kind  string
	Label string
	Count int64
}

// Metrics returns aggregate gauges over the retained durable telemetry window.
func (reader *PostgresReader) Metrics(
	ctx context.Context,
) (OperationalMetrics, error) {
	var metrics OperationalMetrics
	err := reader.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM discovery_candidates),
			(SELECT count(*) FROM discovery_edges),
			(SELECT count(*) FROM crawl_runs),
			(SELECT count(*) FROM crawl_runs WHERE finished_at IS NULL),
			(SELECT COALESCE(sum(pages_attempted), 0) FROM crawl_runs),
			(SELECT COALESCE(sum(pages_parsed), 0) FROM crawl_runs),
			(SELECT COALESCE(sum(candidates_discovered), 0) FROM crawl_runs),
			(SELECT COALESCE(sum(promotions_admitted), 0) FROM crawl_runs),
			(SELECT COALESCE(sum(promotions_deferred), 0) FROM crawl_runs),
			(SELECT COALESCE(sum(pages_failed), 0) FROM crawl_runs),
			(SELECT COALESCE(sum(pages_blocked), 0) FROM crawl_runs)
	`).Scan(
		&metrics.Candidates,
		&metrics.DiscoveryEdges,
		&metrics.CrawlRuns,
		&metrics.UnfinishedCrawlRuns,
		&metrics.PagesAttempted,
		&metrics.PagesParsed,
		&metrics.OriginsFound,
		&metrics.OriginsPromoted,
		&metrics.OriginsDeferred,
		&metrics.Failures,
		&metrics.RobotsDenials,
	)
	if err != nil {
		return OperationalMetrics{}, fmt.Errorf(
			"reporting: read metrics: %w",
			err,
		)
	}

	rows, err := reader.pool.Query(
		ctx,
		`
			SELECT metric, label, value
			FROM (
				SELECT
					$1::text AS metric,
					failure_category AS label,
					count(*)::bigint AS value
				FROM crawl_page_attempts
				WHERE failure_category <> 'none'
				GROUP BY failure_category

				UNION ALL

				SELECT
					$2::text,
					status_code::text,
					count(*)::bigint
				FROM crawl_page_attempts
				WHERE status_code IS NOT NULL
				GROUP BY status_code

				UNION ALL

				SELECT
					$3::text,
					last_failure_category,
					count(*)::bigint
				FROM verification_queue
				WHERE last_failure_category IS NOT NULL
				GROUP BY last_failure_category
			) AS breakdowns
			ORDER BY metric, label
		`,
		metricKindPageFailureCategory,
		metricKindHTTPStatus,
		metricKindVerificationQueueFailureCategory,
	)
	if err != nil {
		return OperationalMetrics{}, fmt.Errorf(
			"reporting: read metric breakdowns: %w",
			err,
		)
	}

	breakdowns, err := pgx.CollectRows(
		rows,
		func(
			row pgx.CollectableRow,
		) (metricBreakdownRow, error) {
			var breakdown metricBreakdownRow

			err := row.Scan(
				&breakdown.Kind,
				&breakdown.Label,
				&breakdown.Count,
			)

			return breakdown, err
		},
	)
	if err != nil {
		return OperationalMetrics{}, fmt.Errorf(
			"reporting: read metric breakdown rows: %w",
			err,
		)
	}

	for _, row := range breakdowns {
		breakdown := MetricBreakdown{
			Label: row.Label,
			Count: row.Count,
		}

		switch row.Kind {
		case metricKindPageFailureCategory:
			metrics.PageFailureCategories = append(
				metrics.PageFailureCategories,
				breakdown,
			)
		case metricKindHTTPStatus:
			metrics.HTTPStatuses = append(
				metrics.HTTPStatuses,
				breakdown,
			)
		case metricKindVerificationQueueFailureCategory:
			metrics.VerificationQueueFailureCategories = append(
				metrics.VerificationQueueFailureCategories,
				breakdown,
			)
		}
	}

	return metrics, nil
}

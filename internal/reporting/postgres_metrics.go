package reporting

import (
	"context"
	"fmt"
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

	rows, err := reader.pool.Query(ctx, `
		SELECT metric, label, value
		FROM (
			SELECT
				'page_failure_category'::text AS metric,
				failure_category AS label,
				count(*)::bigint AS value
			FROM crawl_page_attempts
			WHERE failure_category <> 'none'
			GROUP BY failure_category

			UNION ALL

			SELECT
				'http_status'::text,
				status_code::text,
				count(*)::bigint
			FROM crawl_page_attempts
			WHERE status_code IS NOT NULL
			GROUP BY status_code

			UNION ALL

			SELECT
				'verification_queue_failure_category'::text,
				last_failure_category,
				count(*)::bigint
			FROM verification_queue
			WHERE last_failure_category IS NOT NULL
			GROUP BY last_failure_category
		) AS breakdowns
		ORDER BY metric, label
	`)
	if err != nil {
		return OperationalMetrics{}, fmt.Errorf(
			"reporting: read metric breakdowns: %w",
			err,
		)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			kind  string
			label string
			count int64
		)

		if err := rows.Scan(
			&kind,
			&label,
			&count,
		); err != nil {
			return OperationalMetrics{}, fmt.Errorf(
				"reporting: scan metric breakdown: %w",
				err,
			)
		}

		breakdown := MetricBreakdown{
			Label: label,
			Count: count,
		}

		switch kind {
		case "page_failure_category":
			metrics.PageFailureCategories = append(
				metrics.PageFailureCategories,
				breakdown,
			)
		case "http_status":
			metrics.HTTPStatuses = append(
				metrics.HTTPStatuses,
				breakdown,
			)
		case "verification_queue_failure_category":
			metrics.VerificationQueueFailureCategories = append(
				metrics.VerificationQueueFailureCategories,
				breakdown,
			)
		default:
			return OperationalMetrics{}, fmt.Errorf(
				"reporting: unknown metric breakdown kind %q",
				kind,
			)
		}
	}

	if err := rows.Err(); err != nil {
		return OperationalMetrics{}, fmt.Errorf(
			"reporting: iterate metric breakdowns: %w",
			err,
		)
	}

	return metrics, nil
}

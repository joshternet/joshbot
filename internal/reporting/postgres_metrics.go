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
	metricKindPersonalSiteQualification        = "personal_site_qualification"
	metricKindPersonalSiteAssessment           = "personal_site_assessment"
	metricKindPersonalSiteTruncated            = "personal_site_truncated"
	metricKindPersonalSiteAwaiting             = "personal_site_awaiting"
	metricKindAutomaticPromotionScoreBucket    = "automatic_promotion_score_bucket"
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
	PersonalSiteQualifications         []MetricBreakdown
	PersonalSiteAssessments            []MetricBreakdown
	PersonalSiteTruncated              []MetricBreakdown
	PersonalSiteAwaiting               []MetricBreakdown
	AutomaticPromotionsByScoreBucket   []MetricBreakdown
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

				UNION ALL

				SELECT
					$4::text,
					personal_site_qualification_outcome,
					count(*)::bigint
				FROM discovery_candidates
				WHERE personal_site_qualification_outcome IS NOT NULL
				GROUP BY personal_site_qualification_outcome

				UNION ALL

				SELECT
					$5::text,
					CASE
						WHEN personal_site_score < 20 THEN '0-19'
						WHEN personal_site_score < 40 THEN '20-39'
						WHEN personal_site_score < 60 THEN '40-59'
						WHEN personal_site_score < 80 THEN '60-79'
						ELSE '80-100'
					END,
					count(*)::bigint
				FROM discovery_candidates
				WHERE personal_site_qualification_outcome = 'assessed'
					AND personal_site_score IS NOT NULL
				GROUP BY 2

				UNION ALL

				SELECT
					$6::text,
					CASE
						WHEN candidate.personal_site_score IS NULL THEN 'unassessed'
						WHEN candidate.personal_site_score < 20 THEN '0-19'
						WHEN candidate.personal_site_score < 40 THEN '20-39'
						WHEN candidate.personal_site_score < 60 THEN '40-59'
						WHEN candidate.personal_site_score < 80 THEN '60-79'
						ELSE '80-100'
					END,
					count(*)::bigint
				FROM crawl_run_automatic_admission_batches AS batch
				JOIN discovery_candidates AS candidate
					ON candidate.origin = batch.candidate_origin
				WHERE batch.outcome = 'promoted'
				GROUP BY 2

				UNION ALL

				SELECT
					$7::text,
					CASE
						WHEN personal_site_truncated THEN 'truncated'
						ELSE 'full'
					END,
					count(*)::bigint
				FROM discovery_candidates
				WHERE personal_site_qualification_outcome = 'assessed'
				GROUP BY 2

				UNION ALL

				SELECT
					$8::text,
					CASE
						WHEN personal_site_next_attempt_at IS NULL
							OR personal_site_next_attempt_at
								<= statement_timestamp()
						THEN 'due'
						ELSE 'scheduled'
					END,
					count(*)::bigint
				FROM discovery_candidates
				WHERE personal_site_qualification_outcome IS NULL
					OR personal_site_qualification_outcome <> 'assessed'
					OR personal_site_assessment_version IS DISTINCT FROM 1
				GROUP BY 2
			) AS breakdowns
			ORDER BY metric, label
		`,
		metricKindPageFailureCategory,
		metricKindHTTPStatus,
		metricKindVerificationQueueFailureCategory,
		metricKindPersonalSiteQualification,
		metricKindPersonalSiteAssessment,
		metricKindAutomaticPromotionScoreBucket,
		metricKindPersonalSiteTruncated,
		metricKindPersonalSiteAwaiting,
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
		case metricKindPersonalSiteQualification:
			metrics.PersonalSiteQualifications = append(
				metrics.PersonalSiteQualifications,
				breakdown,
			)
		case metricKindPersonalSiteAssessment:
			metrics.PersonalSiteAssessments = append(
				metrics.PersonalSiteAssessments,
				breakdown,
			)
		case metricKindPersonalSiteTruncated:
			metrics.PersonalSiteTruncated = append(
				metrics.PersonalSiteTruncated,
				breakdown,
			)
		case metricKindPersonalSiteAwaiting:
			metrics.PersonalSiteAwaiting = append(
				metrics.PersonalSiteAwaiting,
				breakdown,
			)
		case metricKindAutomaticPromotionScoreBucket:
			metrics.AutomaticPromotionsByScoreBucket = append(
				metrics.AutomaticPromotionsByScoreBucket,
				breakdown,
			)
		}
	}

	return metrics, nil
}

package reporting

import (
	"context"
	"fmt"
)

// OperationalMetrics contains retained durable crawler measurements.
type OperationalMetrics struct {
	Candidates          int64
	DiscoveryEdges      int64
	CrawlRuns           int64
	UnfinishedCrawlRuns int64
	PagesAttempted      int64
	PagesParsed         int64
	OriginsFound        int64
	OriginsPromoted     int64
	OriginsDeferred     int64
	Failures            int64
	RobotsDenials       int64
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
		return OperationalMetrics{}, fmt.Errorf("reporting: read metrics: %w", err)
	}
	return metrics, nil
}

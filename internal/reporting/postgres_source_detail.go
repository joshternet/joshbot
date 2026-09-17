package reporting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/origin"
)

// Source returns one crawl source and its operational detail.
func (reader *PostgresReader) Source(
	ctx context.Context,
	rawOrigin string,
	provenanceLimit int,
) (SourceDetail, bool, error) {
	if !validReaderLimit(provenanceLimit) {
		return SourceDetail{},
			false,
			errInvalidLimit
	}

	parsed, err := origin.Parse(rawOrigin)
	if err != nil || parsed.String() == "" {
		return SourceDetail{},
			false,
			errInvalidSourceOrigin
	}

	canonicalOrigin := parsed.String()

	detail, found, err := reader.sourceDetailBase(
		ctx,
		canonicalOrigin,
	)
	if err != nil || !found {
		return detail, found, err
	}

	provenance, err := reader.sourceProvenance(
		ctx,
		canonicalOrigin,
		provenanceLimit,
	)
	if err != nil {
		return SourceDetail{},
			false,
			err
	}

	detail.DiscoveredBy = append(
		[]DiscoveryProvenance{},
		provenance...,
	)

	return detail, true, nil
}

func (reader *PostgresReader) sourceDetailBase(
	ctx context.Context,
	sourceOrigin string,
) (SourceDetail, bool, error) {
	var (
		detail SourceDetail

		queuePresent         bool
		queueOrigin          string
		queueMode            string
		queueAvailableAt     time.Time
		queueLeaseGeneration int64
		queueLeaseOwner      string
		queueLeaseExpiresAt  *time.Time
		queueLastClaimedAt   *time.Time

		crawlPresent                  bool
		crawlID                       int64
		crawlSourceOrigin             string
		crawlStartedAt                time.Time
		crawlFinishedAt               *time.Time
		crawlOutcome                  string
		crawlStopReason               string
		crawlPagesAttempted           int
		crawlPagesParsed              int
		crawlCandidatesDiscovered     int
		crawlBudgetExhausted          bool
		crawlMaxDepth                 int
		crawlMaxPages                 int
		crawlMaxPageBytes             int64
		crawlRequestDelayMilliseconds int64
		crawlRedirectLimit            int
		crawlPageTimeoutMilliseconds  int64

		robotsPresent  bool
		robotsRunID    int64
		robotsSequence int
		robotsObserved time.Time
		robotsDecision string
	)

	err := reader.pool.QueryRow(
		ctx,
		`
			SELECT
				state.source_origin,
				state.seeded,
				state.automatically_discovered,
				state.crawl_blocked,
				COALESCE(
					effective.outcome = 'valid',
					false
				) AS verified,
				NOT state.crawl_blocked
					AND (
						state.seeded
						OR state.automatically_discovered
						OR COALESCE(
							effective.outcome = 'valid',
							false
						)
					) AS crawl_eligible,
				candidate.first_discovered_at,
				candidate.last_discovered_at,

				queue.origin IS NOT NULL,
				COALESCE(queue.origin, ''),
				COALESCE(queue.mode, ''),
				COALESCE(
					queue.available_at,
					'epoch'::timestamptz
				),
				COALESCE(queue.lease_generation, 0),
				COALESCE(queue.lease_owner, ''),
				queue.lease_expires_at,
				queue.last_claimed_at,

				latest_crawl.id IS NOT NULL,
				COALESCE(latest_crawl.id, 0),
				COALESCE(latest_crawl.source_origin, ''),
				COALESCE(
					latest_crawl.started_at,
					'epoch'::timestamptz
				),
				latest_crawl.finished_at,
				COALESCE(latest_crawl.outcome, ''),
				COALESCE(latest_crawl.stop_reason, ''),
				COALESCE(latest_crawl.pages_attempted, 0),
				COALESCE(latest_crawl.pages_parsed, 0),
				COALESCE(
					latest_crawl.candidates_discovered,
					0
				),
				COALESCE(
					latest_crawl.budget_exhausted,
					false
				),
				COALESCE(latest_crawl.max_depth, 0),
				COALESCE(latest_crawl.max_pages, 0),
				COALESCE(latest_crawl.max_page_bytes, 0),
				COALESCE(
					latest_crawl.
						request_delay_milliseconds,
					0
				),
				COALESCE(latest_crawl.redirect_limit, 0),
				COALESCE(
					latest_crawl.
						page_timeout_milliseconds,
					0
				),

				latest_robots.run_id IS NOT NULL,
				COALESCE(latest_robots.run_id, 0),
				COALESCE(latest_robots.sequence, 0),
				COALESCE(
					latest_robots.started_at,
					'epoch'::timestamptz
				),
				COALESCE(
					latest_robots.robots_decision,
					''
				)
			FROM discovery_source_state AS state
			LEFT JOIN discovery_candidates AS candidate
				ON candidate.origin = state.source_origin
			LEFT JOIN LATERAL (
				SELECT observation.outcome
				FROM verification_observations
					AS observation
				WHERE observation.origin =
						state.source_origin
					AND observation.outcome IN (
						'valid',
						'absent',
						'invalid',
						'unsupported_version',
						'cross_origin_redirect'
					)
				ORDER BY
					observation.observed_at DESC,
					observation.id DESC
				LIMIT 1
			) AS effective ON true
			LEFT JOIN verification_queue AS queue
				ON queue.origin = state.source_origin
			LEFT JOIN LATERAL (
				SELECT
					run.id,
					run.source_origin,
					run.started_at,
					run.finished_at,
					run.outcome,
					run.stop_reason,
					run.pages_attempted,
					run.pages_parsed,
					run.candidates_discovered,
					run.budget_exhausted,
					run.max_depth,
					run.max_pages,
					run.max_page_bytes,
					run.request_delay_milliseconds,
					run.redirect_limit,
					run.page_timeout_milliseconds
				FROM crawl_runs AS run
				WHERE run.source_origin =
						state.source_origin
				ORDER BY
					run.started_at DESC,
					run.id DESC
				LIMIT 1
			) AS latest_crawl ON true
			LEFT JOIN LATERAL (
				SELECT
					attempt.run_id,
					attempt.sequence,
					attempt.started_at,
					attempt.robots_decision
				FROM crawl_page_attempts AS attempt
				JOIN crawl_runs AS run
					ON run.id = attempt.run_id
				WHERE run.source_origin =
						state.source_origin
				ORDER BY
					attempt.started_at DESC,
					attempt.run_id DESC,
					attempt.sequence DESC
				LIMIT 1
			) AS latest_robots ON true
			WHERE state.source_origin = $1
		`,
		sourceOrigin,
	).Scan(
		&detail.Source.Origin,
		&detail.Source.Seeded,
		&detail.Source.AutomaticallyDiscovered,
		&detail.Source.Blocked,
		&detail.Source.Verified,
		&detail.Source.CrawlEligible,
		&detail.Source.FirstDiscoveredAt,
		&detail.Source.LastDiscoveredAt,

		&queuePresent,
		&queueOrigin,
		&queueMode,
		&queueAvailableAt,
		&queueLeaseGeneration,
		&queueLeaseOwner,
		&queueLeaseExpiresAt,
		&queueLastClaimedAt,

		&crawlPresent,
		&crawlID,
		&crawlSourceOrigin,
		&crawlStartedAt,
		&crawlFinishedAt,
		&crawlOutcome,
		&crawlStopReason,
		&crawlPagesAttempted,
		&crawlPagesParsed,
		&crawlCandidatesDiscovered,
		&crawlBudgetExhausted,
		&crawlMaxDepth,
		&crawlMaxPages,
		&crawlMaxPageBytes,
		&crawlRequestDelayMilliseconds,
		&crawlRedirectLimit,
		&crawlPageTimeoutMilliseconds,

		&robotsPresent,
		&robotsRunID,
		&robotsSequence,
		&robotsObserved,
		&robotsDecision,
	)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return SourceDetail{}, false, nil
	case err != nil:
		return SourceDetail{},
			false,
			fmt.Errorf(
				"reporting: read source detail: %w",
				err,
			)
	}

	if queuePresent {
		detail.Queue = &QueueItem{
			Origin:          queueOrigin,
			Mode:            queueMode,
			AvailableAt:     queueAvailableAt,
			LeaseGeneration: queueLeaseGeneration,
			LeaseOwner:      queueLeaseOwner,
			LeaseExpiresAt:  queueLeaseExpiresAt,
			LastClaimedAt:   queueLastClaimedAt,
		}
	}

	if crawlPresent {
		detail.LatestCrawl = &CrawlRun{
			ID:                       crawlID,
			SourceOrigin:             crawlSourceOrigin,
			StartedAt:                crawlStartedAt,
			FinishedAt:               crawlFinishedAt,
			Outcome:                  crawlOutcome,
			StopReason:               crawlStopReason,
			PagesAttempted:           crawlPagesAttempted,
			PagesParsed:              crawlPagesParsed,
			CandidatesDiscovered:     crawlCandidatesDiscovered,
			BudgetExhausted:          crawlBudgetExhausted,
			MaxDepth:                 crawlMaxDepth,
			MaxPages:                 crawlMaxPages,
			MaxPageBytes:             crawlMaxPageBytes,
			RequestDelayMilliseconds: crawlRequestDelayMilliseconds,
			RedirectLimit:            crawlRedirectLimit,
			PageTimeoutMilliseconds:  crawlPageTimeoutMilliseconds,
		}
	}

	if robotsPresent {
		detail.LatestRobotsObservation =
			&RobotsObservation{
				CrawlRunID: robotsRunID,
				Sequence:   robotsSequence,
				ObservedAt: robotsObserved,
				Decision:   robotsDecision,
			}
	}

	return detail, true, nil
}

func (reader *PostgresReader) sourceProvenance(
	ctx context.Context,
	sourceOrigin string,
	limit int,
) ([]DiscoveryProvenance, error) {
	rows, err := reader.pool.Query(
		ctx,
		`
			SELECT
				source_origin,
				kind,
				first_discovered_at,
				last_discovered_at
			FROM discovery_edges
			WHERE candidate_origin = $1
			ORDER BY
				last_discovered_at DESC,
				source_origin,
				kind
			LIMIT $2
		`,
		sourceOrigin,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"reporting: read source provenance: %w",
			err,
		)
	}

	return pgx.CollectRows(
		rows,
		func(
			row pgx.CollectableRow,
		) (DiscoveryProvenance, error) {
			var provenance DiscoveryProvenance

			err := row.Scan(
				&provenance.SourceOrigin,
				&provenance.Kind,
				&provenance.FirstDiscoveredAt,
				&provenance.LastDiscoveredAt,
			)

			return provenance, err
		},
	)
}

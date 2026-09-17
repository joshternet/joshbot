package reporting

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// SourcesPage returns one stable keyset-paginated page of crawl sources,
// ordered and advanced by canonical origin.
func (reader *PostgresReader) SourcesPage(
	ctx context.Context,
	query sourceQuery,
) (sourcePage, error) {
	fetchLimit, err := pageLimit(query.Limit)
	if err != nil {
		return sourcePage{}, err
	}

	cursorOrigin := ""
	if query.Cursor != nil {
		cursorOrigin = query.Cursor.Origin
	}

	rows, err := reader.pool.Query(
		ctx,
		`
			WITH classified AS (
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
					candidate.last_discovered_at
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
			)
			SELECT
				source_origin,
				seeded,
				automatically_discovered,
				crawl_blocked,
				verified,
				crawl_eligible,
				first_discovered_at,
				last_discovered_at
			FROM classified
			WHERE ($1::text = '' OR source_origin = $1)
				AND ($2::boolean IS NULL OR seeded = $2)
				AND (
					$3::boolean IS NULL
					OR automatically_discovered = $3
				)
				AND ($4::boolean IS NULL OR verified = $4)
				AND ($5::boolean IS NULL OR crawl_blocked = $5)
				AND (
					$6::boolean IS NULL
					OR crawl_eligible = $6
				)
				AND ($7::text = '' OR source_origin > $7)
			ORDER BY source_origin
			LIMIT $8
		`,
		query.Origin,
		query.Seeded,
		query.Automatic,
		query.Verified,
		query.Blocked,
		query.CrawlEligible,
		cursorOrigin,
		fetchLimit,
	)
	if err != nil {
		return sourcePage{}, fmt.Errorf(
			"reporting: read source page: %w",
			err,
		)
	}

	items, err := pgx.CollectRows(
		rows,
		func(
			row pgx.CollectableRow,
		) (CrawlSource, error) {
			var source CrawlSource

			err := row.Scan(
				&source.Origin,
				&source.Seeded,
				&source.AutomaticallyDiscovered,
				&source.Blocked,
				&source.Verified,
				&source.CrawlEligible,
				&source.FirstDiscoveredAt,
				&source.LastDiscoveredAt,
			)

			return source, err
		},
	)
	if err != nil {
		return sourcePage{}, fmt.Errorf(
			"reporting: collect source page: %w",
			err,
		)
	}

	return trimSourcePage(
		items,
		query.Limit,
	), nil
}

// CrawlsPage returns one stable newest-first page of crawl runs.
func (reader *PostgresReader) CrawlsPage(
	ctx context.Context,
	query crawlQuery,
) (crawlPage, error) {
	fetchLimit, err := pageLimit(query.Limit)
	if err != nil {
		return crawlPage{}, err
	}

	var cursorStartedAt *time.Time
	var cursorID int64
	if query.Cursor != nil {
		cursorStartedAt = &query.Cursor.StartedAt
		cursorID = query.Cursor.ID
	}

	rows, err := reader.pool.Query(
		ctx,
		`
			SELECT
				id,
				source_origin,
				started_at,
				finished_at,
				outcome,
				stop_reason,
				pages_attempted,
				pages_parsed,
				candidates_discovered,
				budget_exhausted,
				max_depth,
				max_pages,
				max_page_bytes,
				request_delay_milliseconds,
				redirect_limit,
				page_timeout_milliseconds,
				promotions_admitted,
				promotions_deferred,
				failure_category,
				pages_blocked,
				pages_failed,
				urls_found,
				urls_enqueued,
				frontier_remaining
			FROM crawl_runs
			WHERE ($1::text = '' OR source_origin = $1)
				AND ($2::text = '' OR outcome = $2)
				AND (
					$3::timestamptz IS NULL
					OR (started_at, id) < ($3, $4::bigint)
				)
			ORDER BY started_at DESC, id DESC
			LIMIT $5
		`,
		query.Origin,
		query.Outcome,
		cursorStartedAt,
		cursorID,
		fetchLimit,
	)
	if err != nil {
		return crawlPage{}, fmt.Errorf(
			"reporting: read crawl page: %w",
			err,
		)
	}

	items, err := pgx.CollectRows(
		rows,
		func(
			row pgx.CollectableRow,
		) (CrawlRun, error) {
			return scanCrawlRun(row)
		},
	)
	if err != nil {
		return crawlPage{}, fmt.Errorf(
			"reporting: collect crawl page: %w",
			err,
		)
	}

	return trimCrawlPage(
		items,
		query.Limit,
	), nil
}

// QueuePage returns one stable oldest-available-first page of current queue
// entries. Leased means the lease is currently active, matching Status.
func (reader *PostgresReader) QueuePage(
	ctx context.Context,
	query queueQuery,
) (queuePage, error) {
	fetchLimit, err := pageLimit(query.Limit)
	if err != nil {
		return queuePage{}, err
	}

	var cursorAvailableAt *time.Time
	cursorOrigin := ""
	if query.Cursor != nil {
		cursorAvailableAt = &query.Cursor.AvailableAt
		cursorOrigin = query.Cursor.Origin
	}

	rows, err := reader.pool.Query(
		ctx,
		`
			SELECT
				origin,
				mode,
				available_at,
				lease_generation,
				COALESCE(lease_owner, ''),
				lease_expires_at,
				last_claimed_at
			FROM verification_queue
			WHERE ($1::text = '' OR origin = $1)
				AND ($2::text = '' OR mode = $2)
				AND (
					$3::boolean IS NULL
					OR COALESCE(
						lease_owner IS NOT NULL
							AND lease_expires_at >
								statement_timestamp(),
						false
					) = $3
				)
				AND (
					$4::timestamptz IS NULL
					OR (available_at, origin) > ($4, $5)
				)
			ORDER BY available_at, origin
			LIMIT $6
		`,
		query.Origin,
		query.Mode,
		query.Leased,
		cursorAvailableAt,
		cursorOrigin,
		fetchLimit,
	)
	if err != nil {
		return queuePage{}, fmt.Errorf(
			"reporting: read queue page: %w",
			err,
		)
	}

	items, err := pgx.CollectRows(
		rows,
		func(
			row pgx.CollectableRow,
		) (QueueItem, error) {
			var item QueueItem

			err := row.Scan(
				&item.Origin,
				&item.Mode,
				&item.AvailableAt,
				&item.LeaseGeneration,
				&item.LeaseOwner,
				&item.LeaseExpiresAt,
				&item.LastClaimedAt,
			)

			return item, err
		},
	)
	if err != nil {
		return queuePage{}, fmt.Errorf(
			"reporting: collect queue page: %w",
			err,
		)
	}

	return trimQueuePage(
		items,
		query.Limit,
	), nil
}

// QueueEventsPage returns one stable newest-first page of durable queue
// transitions.
func (reader *PostgresReader) QueueEventsPage(
	ctx context.Context,
	query queueEventQuery,
) (queueEventPage, error) {
	fetchLimit, err := pageLimit(query.Limit)
	if err != nil {
		return queueEventPage{}, err
	}

	var cursorOccurredAt *time.Time
	var cursorID int64
	if query.Cursor != nil {
		cursorOccurredAt = &query.Cursor.OccurredAt
		cursorID = query.Cursor.ID
	}

	rows, err := reader.pool.Query(
		ctx,
		`
			SELECT
				id,
				origin,
				occurred_at,
				event,
				mode,
				available_at,
				lease_owner,
				lease_generation,
				lease_expires_at
			FROM verification_queue_events
			WHERE ($1::text = '' OR origin = $1)
				AND ($2::text = '' OR event = $2)
				AND ($3::text = '' OR mode = $3)
				AND (
					$4::timestamptz IS NULL
					OR (occurred_at, id) < ($4, $5::bigint)
				)
			ORDER BY occurred_at DESC, id DESC
			LIMIT $6
		`,
		query.Origin,
		query.Event,
		query.Mode,
		cursorOccurredAt,
		cursorID,
		fetchLimit,
	)
	if err != nil {
		return queueEventPage{}, fmt.Errorf(
			"reporting: read queue event page: %w",
			err,
		)
	}

	items, err := pgx.CollectRows(
		rows,
		func(
			row pgx.CollectableRow,
		) (QueueEvent, error) {
			var event QueueEvent

			err := row.Scan(
				&event.ID,
				&event.Origin,
				&event.OccurredAt,
				&event.Event,
				&event.Mode,
				&event.AvailableAt,
				&event.LeaseOwner,
				&event.LeaseGeneration,
				&event.LeaseExpiresAt,
			)

			return event, err
		},
	)
	if err != nil {
		return queueEventPage{}, fmt.Errorf(
			"reporting: collect queue event page: %w",
			err,
		)
	}

	return trimQueueEventPage(
		items,
		query.Limit,
	), nil
}

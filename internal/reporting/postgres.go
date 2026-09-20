package reporting

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/database"
)

var (
	errPostgresPoolUnavailable = errors.New(
		"reporting: PostgreSQL pool is unavailable",
	)
	errInvalidPostgresConfig = errors.New(
		"reporting: PostgreSQL configuration is invalid",
	)
	errInvalidCrawlID = errors.New(
		"reporting: crawl ID is invalid",
	)
)

// PostgresConfig contains operational reporting configuration.
//
// AutomaticCrawlEnabled and MaxPendingProbes come from the same runtime
// configuration used by discovery so reporting describes the effective
// backpressure policy rather than attempting to infer it from database state.
type PostgresConfig struct {
	AutomaticCrawlEnabled bool
	MaxPendingProbes      int
}

// PostgresReader implements Reader using JoshBot's private PostgreSQL
// operational state.
//
// The caller owns the supplied pool and remains responsible for closing it.
type PostgresReader struct {
	pool   database.Queryer
	config PostgresConfig
}

// NewPostgresReader constructs the PostgreSQL reporting read model.
func NewPostgresReader(
	pool *pgxpool.Pool,
	config PostgresConfig,
) (*PostgresReader, error) {
	if pool == nil {
		return nil, errPostgresPoolUnavailable
	}

	return newPostgresReader(
		pool,
		config,
	)
}

func newPostgresReader(
	pool database.Queryer,
	config PostgresConfig,
) (*PostgresReader, error) {
	if pool == nil {
		return nil, errPostgresPoolUnavailable
	}

	if !validPostgresConfig(config) {
		return nil, errInvalidPostgresConfig
	}

	return &PostgresReader{
		pool:   pool,
		config: config,
	}, nil
}

// Status returns the aggregate point-in-time crawler state.
func (reader *PostgresReader) Status(
	ctx context.Context,
) (Status, error) {
	var status Status

	err := reader.pool.QueryRow(
		ctx,
		`
			WITH source_classification AS (
				SELECT
					state.seeded,
					state.automatically_discovered,
					state.crawl_blocked,
					COALESCE(
						effective.outcome = 'valid',
						false
					) AS verified
				FROM discovery_source_state AS state
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
				control.discovery_paused,
				control.verification_paused,
				control.updated_at,

				(
					SELECT COUNT(*)
					FROM verification_queue
				),
				(
					SELECT COUNT(*)
					FROM verification_queue
					WHERE mode = 'probe'
				),
				(
					SELECT COUNT(*)
					FROM verification_queue
					WHERE mode = 'recurring'
				),
				(
					SELECT COUNT(*)
					FROM verification_queue
					WHERE lease_owner IS NOT NULL
						AND lease_expires_at >
							statement_timestamp()
				),
				(
					SELECT MIN(available_at)
					FROM verification_queue
				),

				(
					SELECT COUNT(*)
					FROM source_classification
				),
				(
					SELECT COUNT(*)
					FROM source_classification
					WHERE seeded
				),
				(
					SELECT COUNT(*)
					FROM source_classification
					WHERE automatically_discovered
				),
				(
					SELECT COUNT(*)
					FROM source_classification
					WHERE verified
				),
				(
					SELECT COUNT(*)
					FROM source_classification
					WHERE crawl_blocked
				),
				(
					SELECT COUNT(*)
					FROM source_classification
					WHERE NOT crawl_blocked
						AND (
							seeded
							OR automatically_discovered
							OR verified
						)
				)
			FROM crawl_control AS control
			WHERE control.singleton
		`,
	).Scan(
		&status.Control.DiscoveryPaused,
		&status.Control.VerificationPaused,
		&status.Control.UpdatedAt,

		&status.Queue.Total,
		&status.Queue.Probe,
		&status.Queue.Recurring,
		&status.Queue.Leased,
		&status.Queue.OldestAvailableAt,

		&status.Sources.Total,
		&status.Sources.Seeded,
		&status.Sources.Automatic,
		&status.Sources.Verified,
		&status.Sources.Blocked,
		&status.Sources.CrawlEligible,
	)
	if err != nil {
		return Status{}, fmt.Errorf(
			"reporting: read status: %w",
			err,
		)
	}

	status.Backpressure = BackpressureStatus{
		AutomaticCrawlEnabled: reader.config.AutomaticCrawlEnabled,
		PendingProbes:         status.Queue.Probe,
		MaxPendingProbes: int64(
			reader.config.MaxPendingProbes,
		),
	}

	status.Backpressure.Active =
		status.Backpressure.AutomaticCrawlEnabled &&
			status.Backpressure.PendingProbes >=
				status.Backpressure.MaxPendingProbes

	services, err := reader.Services(ctx)
	if err != nil {
		return Status{}, err
	}

	status.Services = services

	return status, nil
}

// Sources returns private crawl-source classifications in deterministic origin
// order.
func (reader *PostgresReader) Sources(
	ctx context.Context,
	limit int,
) ([]CrawlSource, error) {
	if !validReaderLimit(limit) {
		return nil, errInvalidLimit
	}

	rows, err := reader.pool.Query(
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
			ORDER BY state.source_origin
			LIMIT $1
		`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"reporting: read sources: %w",
			err,
		)
	}

	return pgx.CollectRows(
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
}

// Crawls returns recent crawl runs ordered newest first.
func (reader *PostgresReader) Crawls(
	ctx context.Context,
	limit int,
) ([]CrawlRun, error) {
	if !validReaderLimit(limit) {
		return nil, errInvalidLimit
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
			ORDER BY started_at DESC, id DESC
			LIMIT $1
		`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"reporting: read crawl runs: %w",
			err,
		)
	}

	return pgx.CollectRows(
		rows,
		func(
			row pgx.CollectableRow,
		) (CrawlRun, error) {
			return scanCrawlRun(row)
		},
	)
}

// Crawl returns one crawl and its ordered sanitized page attempts.
func (reader *PostgresReader) Crawl(
	ctx context.Context,
	id int64,
) (CrawlDetail, bool, error) {
	if id <= 0 {
		return CrawlDetail{},
			false,
			errInvalidCrawlID
	}

	run, err := scanCrawlRun(
		reader.pool.QueryRow(
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
				WHERE id = $1
			`,
			id,
		),
	)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return CrawlDetail{},
			false,
			nil
	case err != nil:
		return CrawlDetail{},
			false,
			fmt.Errorf(
				"reporting: read crawl: %w",
				err,
			)
	}

	pages, err := reader.crawlPages(
		ctx,
		id,
	)
	if err != nil {
		return CrawlDetail{},
			false,
			err
	}

	return CrawlDetail{
		Run:   run,
		Pages: pages,
	}, true, nil
}

func (reader *PostgresReader) crawlPages(
	ctx context.Context,
	id int64,
) ([]PageAttempt, error) {
	rows, err := reader.pool.Query(
		ctx,
		`
			SELECT
				sequence,
				requested_url,
				final_url,
				depth,
				started_at,
				duration_milliseconds,
				status_code,
				response_bytes,
				content_type,
				redirect_count,
				robots_decision,
				internal_link_count,
				external_link_count,
				outcome,
				failure_category,
				urls_found,
				urls_enqueued
			FROM crawl_page_attempts
			WHERE run_id = $1
			ORDER BY sequence
		`,
		id,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"reporting: read crawl pages: %w",
			err,
		)
	}

	return pgx.CollectRows(
		rows,
		func(
			row pgx.CollectableRow,
		) (PageAttempt, error) {
			var attempt PageAttempt

			err := row.Scan(
				&attempt.Sequence,
				&attempt.RequestedURL,
				&attempt.FinalURL,
				&attempt.Depth,
				&attempt.StartedAt,
				&attempt.DurationMillis,
				&attempt.StatusCode,
				&attempt.ResponseBytes,
				&attempt.ContentType,
				&attempt.RedirectCount,
				&attempt.RobotsDecision,
				&attempt.InternalLinkCount,
				&attempt.ExternalLinkCount,
				&attempt.Outcome,
				&attempt.FailureCategory,
				&attempt.URLsFound,
				&attempt.URLsEnqueued,
			)

			return attempt, err
		},
	)
}

// Queue returns the current durable verification queue.
func (reader *PostgresReader) Queue(
	ctx context.Context,
	limit int,
) ([]QueueItem, error) {
	if !validReaderLimit(limit) {
		return nil, errInvalidLimit
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
			ORDER BY available_at, origin
			LIMIT $1
		`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"reporting: read queue: %w",
			err,
		)
	}

	return pgx.CollectRows(
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
}

// QueueEvents returns recent durable verification queue transitions.
func (reader *PostgresReader) QueueEvents(
	ctx context.Context,
	limit int,
) ([]QueueEvent, error) {
	if !validReaderLimit(limit) {
		return nil, errInvalidLimit
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
			ORDER BY occurred_at DESC, id DESC
			LIMIT $1
		`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"reporting: read queue events: %w",
			err,
		)
	}

	return pgx.CollectRows(
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
}

// Services returns all current service heartbeats in deterministic order.
func (reader *PostgresReader) Services(
	ctx context.Context,
) ([]ServiceStatus, error) {
	rows, err := reader.pool.Query(
		ctx,
		`
			SELECT
				service,
				instance_id,
				state,
				current_origin,
				message,
				started_at,
				updated_at
			FROM crawl_service_heartbeats
			ORDER BY service, instance_id
		`,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"reporting: read services: %w",
			err,
		)
	}

	return pgx.CollectRows(
		rows,
		func(
			row pgx.CollectableRow,
		) (ServiceStatus, error) {
			var service ServiceStatus

			err := row.Scan(
				&service.Service,
				&service.InstanceID,
				&service.State,
				&service.CurrentOrigin,
				&service.Message,
				&service.StartedAt,
				&service.UpdatedAt,
			)

			return service, err
		},
	)
}

type rowScanner interface {
	Scan(...any) error
}

func scanCrawlRun(
	row rowScanner,
) (CrawlRun, error) {
	var run CrawlRun

	err := row.Scan(
		&run.ID,
		&run.SourceOrigin,
		&run.StartedAt,
		&run.FinishedAt,
		&run.Outcome,
		&run.StopReason,
		&run.PagesAttempted,
		&run.PagesParsed,
		&run.CandidatesDiscovered,
		&run.BudgetExhausted,
		&run.MaxDepth,
		&run.MaxPages,
		&run.MaxPageBytes,
		&run.RequestDelayMilliseconds,
		&run.RedirectLimit,
		&run.PageTimeoutMilliseconds,
		&run.PromotionsAdmitted,
		&run.PromotionsDeferred,
		&run.FailureCategory,
		&run.PagesBlocked,
		&run.PagesFailed,
		&run.URLsFound,
		&run.URLsEnqueued,
		&run.FrontierRemaining,
	)

	return run, err
}

func validPostgresConfig(
	config PostgresConfig,
) bool {
	return config.MaxPendingProbes > 0
}

func validReaderLimit(
	limit int,
) bool {
	return limit >= 1 &&
		limit <= maxLimit
}

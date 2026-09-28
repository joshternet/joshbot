package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var errInvalidOperationalHistoryRetention = errors.New(
	"store: operational history retention is invalid",
)

// PurgeOperationalHistory removes disposable verification and service history
// older than retention while preserving the observations required to
// reconstruct current declaration state.
//
// For every known origin, the newest observation and newest authoritative
// observation are retained regardless of age. Durable participation,
// declaration-check, discovery, reprobe, and operator-audit state are not
// removed by this cleanup.
func (s *DiscoveryStore) PurgeOperationalHistory(
	ctx context.Context,
	retention time.Duration,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}

	if retention <= 0 {
		return errInvalidOperationalHistoryRetention
	}

	err := pgx.BeginFunc(
		ctx,
		s.pool,
		func(tx pgx.Tx) error {
			if _, err := tx.Exec(
				ctx,
				`
					WITH protected_observations AS (
						SELECT latest.id
						FROM origins AS stored_origin
						CROSS JOIN LATERAL (
							SELECT observation.id
							FROM verification_observations
								AS observation
							WHERE observation.origin =
								stored_origin.origin
							ORDER BY
								observation.observed_at DESC,
								observation.id DESC
							LIMIT 1
						) AS latest

						UNION

						SELECT authoritative.id
						FROM origins AS stored_origin
						CROSS JOIN LATERAL (
							SELECT observation.id
							FROM verification_observations
								AS observation
							WHERE observation.origin =
									stored_origin.origin
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
						) AS authoritative
					)
					DELETE FROM verification_observations
						AS observation
					WHERE observation.observed_at <
							transaction_timestamp() -
							make_interval(
								secs => $1::double precision
							)
						AND NOT EXISTS (
							SELECT 1
							FROM protected_observations
							WHERE protected_observations.id =
								observation.id
						)
				`,
				retention.Seconds(),
			); err != nil {
				return fmt.Errorf(
					"delete expired verification observations: %w",
					err,
				)
			}

			if _, err := tx.Exec(
				ctx,
				`
					DELETE FROM verification_queue_events
					WHERE occurred_at <
						transaction_timestamp() -
						make_interval(
							secs => $1::double precision
						)
				`,
				retention.Seconds(),
			); err != nil {
				return fmt.Errorf(
					"delete expired verification queue events: %w",
					err,
				)
			}

			if _, err := tx.Exec(
				ctx,
				`
					DELETE FROM crawl_service_heartbeats
					WHERE updated_at <
						transaction_timestamp() -
						make_interval(
							secs => $1::double precision
						)
				`,
				retention.Seconds(),
			); err != nil {
				return fmt.Errorf(
					"delete stale service heartbeats: %w",
					err,
				)
			}

			return nil
		},
	)
	if err != nil {
		return fmt.Errorf(
			"store: purge operational history: %w",
			err,
		)
	}

	return nil
}

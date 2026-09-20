package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/retry"
)

// CompleteDiscoverySourceLeaseRetry completes an authoritative
// discovery-source lease while honoring a bounded remote Retry-After delay.
//
// last_attempted_at advances only here, after the crawl has actually completed.
// The active lease generation and expiration are checked before retry state is
// changed so a stale claimant cannot complete over a newer claim.
func (s *DiscoveryStore) CompleteDiscoverySourceLeaseRetry(
	ctx context.Context,
	lease discovery.CrawlSourceLease,
	category retry.Category,
	retryAfter time.Duration,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}

	if !validDiscoverySourceLease(lease) {
		return errInvalidDiscoverySourceLease
	}

	if !category.Valid() ||
		retryAfter < 0 ||
		retryAfter > retry.MaxDelay {
		return errInvalidDiscoveryCandidate
	}

	err := pgx.BeginFunc(
		ctx,
		s.pool,
		func(tx pgx.Tx) error {
			completedAt, err := s.clock.NowTransaction(
				ctx,
				tx,
			)
			if err != nil {
				return fmt.Errorf(
					"store: read discovery clock: %w",
					err,
				)
			}
			completedAt = completedAt.UTC()

			var consecutiveFailures int
			err = tx.QueryRow(
				ctx,
				`
					SELECT consecutive_failures
					FROM discovery_source_state
					WHERE source_origin = $1
						AND lease_generation = $2
						AND lease_expires_at > $3
					FOR UPDATE
				`,
				lease.Origin.String(),
				lease.Generation,
				completedAt,
			).Scan(
				&consecutiveFailures,
			)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrDiscoverySourceLeaseLost
			}

			if err != nil {
				return fmt.Errorf(
					"store: lock discovery source lease: %w",
					err,
				)
			}

			transient := category.Transient()
			nextFailures := 0
			nextAttemptAt := completedAt

			if transient {
				nextFailures =
					consecutiveFailures + 1
				nextAttemptAt = completedAt.Add(
					s.retryPolicy.Delay(
						nextFailures,
						retryAfter,
					),
				).UTC()
			}

			commandTag, err := tx.Exec(
				ctx,
				`
					UPDATE discovery_source_state
					SET
						last_attempted_at = $3,
						consecutive_failures = $4,
						last_failure_category =
							CASE
								WHEN $5::boolean
								THEN $6
								ELSE NULL
							END,
						next_attempt_at =
							CASE
								WHEN $5::boolean
								THEN $7::timestamptz
								ELSE NULL
							END,
						lease_expires_at = NULL
					WHERE source_origin = $1
						AND lease_generation = $2
						AND lease_expires_at > $3
				`,
				lease.Origin.String(),
				lease.Generation,
				completedAt,
				nextFailures,
				transient,
				string(category),
				nextAttemptAt,
			)
			if err != nil {
				return fmt.Errorf(
					"store: update discovery source completion: %w",
					err,
				)
			}

			if commandTag.RowsAffected() != 1 {
				return ErrDiscoverySourceLeaseLost
			}

			return nil
		},
	)
	if err != nil {
		return fmt.Errorf(
			"store: complete discovery source lease: %w",
			err,
		)
	}

	return nil
}

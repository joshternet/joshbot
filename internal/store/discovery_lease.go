package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
)

var (
	// ErrDiscoverySourceLeaseLost means the caller no longer has authority
	// over the discovery source it previously claimed.
	ErrDiscoverySourceLeaseLost = errors.New(
		"store: discovery source lease lost",
	)

	errInvalidDiscoveryLeaseDuration = errors.New(
		"store: discovery lease duration is invalid",
	)
	errInvalidDiscoverySourceLease = errors.New(
		"store: discovery source lease is invalid",
	)
)

// ClaimDiscoverySourceLease leases one due crawl source without consuming its
// discovery interval.
//
// last_attempted_at records completed crawl attempts only. Claiming instead
// advances lease_generation and records a renewable expiration. If the
// claimant disappears, the source becomes eligible again after the lease
// expires.
func (s *DiscoveryStore) ClaimDiscoverySourceLease(
	ctx context.Context,
	interval time.Duration,
	leaseDuration time.Duration,
) (discovery.CrawlSourceLease, bool, error) {
	if err := s.validate(ctx); err != nil {
		return discovery.CrawlSourceLease{}, false, err
	}

	if interval <= 0 {
		return discovery.CrawlSourceLease{}, false,
			errInvalidDiscoveryInterval
	}

	if leaseDuration <= 0 {
		return discovery.CrawlSourceLease{}, false,
			errInvalidDiscoveryLeaseDuration
	}

	var (
		lease discovery.CrawlSourceLease
		found bool
	)

	err := pgx.BeginFunc(
		ctx,
		s.pool,
		func(tx pgx.Tx) error {
			now, err := s.clock.NowTransaction(
				ctx,
				tx,
			)
			if err != nil {
				return fmt.Errorf(
					"store: read discovery clock: %w",
					err,
				)
			}

			now = now.UTC()
			expiresAt := now.Add(leaseDuration).UTC()

			if _, err := tx.Exec(
				ctx,
				"SELECT pg_advisory_xact_lock($1)",
				discoveryClaimAdvisoryLockKey,
			); err != nil {
				return fmt.Errorf(
					"store: lock discovery claim: %w",
					err,
				)
			}

			var paused bool
			if err := tx.QueryRow(
				ctx,
				"SELECT discovery_paused FROM crawl_control WHERE singleton",
			).Scan(&paused); err != nil {
				return fmt.Errorf(
					"store: read crawl control: %w",
					err,
				)
			}
			if paused {
				return nil
			}

			var storedOrigin string
			scanErr := tx.QueryRow(
				ctx,
				`
					WITH verified_candidate AS (
						SELECT
							stored_origin.origin
								AS source_origin,
							source_state.last_attempted_at,
							COALESCE(
								source_state.seeded,
								false
							) AS seeded
						FROM origins AS stored_origin
						JOIN LATERAL (
							SELECT observation.outcome
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
						) AS effective
							ON effective.outcome = 'valid'
						LEFT JOIN discovery_source_state
							AS source_state
							ON source_state.source_origin =
								stored_origin.origin
						WHERE NOT COALESCE(
								source_state.seeded,
								false
							)
							AND NOT COALESCE(
								source_state.crawl_blocked,
								false
							)
							AND (
								source_state.next_attempt_at IS NULL
								OR source_state.next_attempt_at <= $1
							)
							AND (
								source_state.last_attempted_at
								IS NULL
								OR source_state.last_attempted_at
								<= (
									$1::timestamptz -
									make_interval(
										secs =>
										$2::double precision
									)
								)
							)
							AND (
								source_state.lease_expires_at
								IS NULL
								OR source_state.lease_expires_at
								<= $1
							)
						ORDER BY
							source_state.last_attempted_at
								ASC NULLS FIRST,
							stored_origin.origin ASC
						FOR UPDATE OF stored_origin
							SKIP LOCKED
						LIMIT 1
					),
					seeded_candidate AS (
						SELECT
							source_state.source_origin,
							source_state.last_attempted_at,
							source_state.seeded
						FROM discovery_source_state
							AS source_state
						WHERE NOT source_state.crawl_blocked
							AND (
								source_state.seeded
								OR (
									$3::boolean
									AND source_state.
									automatically_discovered
									AND (
										SELECT count(*)
										FROM (
											SELECT 1
											FROM verification_queue
											WHERE mode = 'probe'
											LIMIT $4::bigint
										) AS pending_probe
									) < $4::bigint
								)
							)
							AND (
								source_state.next_attempt_at IS NULL
								OR source_state.next_attempt_at <= $1
							)
							AND (
								source_state.last_attempted_at
								IS NULL
								OR source_state.last_attempted_at
								<= (
									$1::timestamptz -
									make_interval(
										secs =>
										$2::double precision
									)
								)
							)
							AND (
								source_state.lease_expires_at
								IS NULL
								OR source_state.lease_expires_at
								<= $1
							)
						ORDER BY
							source_state.last_attempted_at
								ASC NULLS FIRST,
							source_state.source_origin ASC
						FOR UPDATE OF source_state
							SKIP LOCKED
						LIMIT 1
					),
					candidate AS (
						SELECT
							source_origin,
							last_attempted_at,
							seeded
						FROM verified_candidate

						UNION ALL

						SELECT
							source_origin,
							last_attempted_at,
							seeded
						FROM seeded_candidate

						ORDER BY
							last_attempted_at
								ASC NULLS FIRST,
							source_origin ASC
						LIMIT 1
					)
					INSERT INTO discovery_source_state (
						source_origin,
						seeded,
						lease_generation,
						last_claimed_at,
						lease_expires_at
					)
					SELECT
						candidate.source_origin,
						candidate.seeded,
						1,
						$1::timestamptz,
						$5::timestamptz
					FROM candidate
					ON CONFLICT (source_origin) DO UPDATE
					SET
						lease_generation =
							discovery_source_state.
							lease_generation + 1,
						last_claimed_at =
							EXCLUDED.last_claimed_at,
						lease_expires_at =
							EXCLUDED.lease_expires_at
					RETURNING
						source_origin,
						lease_generation,
						last_claimed_at,
						lease_expires_at
				`,
				now,
				interval.Seconds(),
				s.automatic.Enabled,
				s.automatic.maxPendingProbes(),
				expiresAt,
			).Scan(
				&storedOrigin,
				&lease.Generation,
				&lease.ClaimedAt,
				&lease.ExpiresAt,
			)

			if errors.Is(scanErr, pgx.ErrNoRows) {
				return nil
			}

			if scanErr != nil {
				return scanErr
			}

			source, err := origin.Parse(storedOrigin)
			if err != nil {
				return fmt.Errorf(
					"store: invalid discovery source: %w",
					err,
				)
			}

			lease.Origin = source
			lease.ClaimedAt = lease.ClaimedAt.UTC()
			lease.ExpiresAt = lease.ExpiresAt.UTC()
			found = true

			return nil
		},
	)
	if err != nil {
		return discovery.CrawlSourceLease{}, false, fmt.Errorf(
			"store: claim discovery source lease: %w",
			err,
		)
	}

	return lease, found, nil
}

// RenewDiscoverySourceLease extends an authoritative discovery-source lease.
//
// Renewal preserves the source, generation, and original claim time. It never
// shortens the current expiration. A lease that has expired or whose
// generation has been superseded cannot be renewed.
func (s *DiscoveryStore) RenewDiscoverySourceLease(
	ctx context.Context,
	lease discovery.CrawlSourceLease,
	leaseDuration time.Duration,
) (discovery.CrawlSourceLease, error) {
	if err := s.validate(ctx); err != nil {
		return discovery.CrawlSourceLease{}, err
	}

	if !validDiscoverySourceLease(lease) {
		return discovery.CrawlSourceLease{},
			errInvalidDiscoverySourceLease
	}

	if leaseDuration <= 0 {
		return discovery.CrawlSourceLease{},
			errInvalidDiscoveryLeaseDuration
	}

	renewed := discovery.CrawlSourceLease{
		Origin: lease.Origin,
	}

	err := pgx.BeginFunc(
		ctx,
		s.pool,
		func(tx pgx.Tx) error {
			now, err := s.clock.NowTransaction(
				ctx,
				tx,
			)
			if err != nil {
				return fmt.Errorf(
					"store: read discovery clock: %w",
					err,
				)
			}

			now = now.UTC()
			candidateExpiresAt := now.Add(
				leaseDuration,
			).UTC()

			err = tx.QueryRow(
				ctx,
				`
					UPDATE discovery_source_state
					SET lease_expires_at = GREATEST(
						lease_expires_at,
						$4
					)
					WHERE source_origin = $1
						AND lease_generation = $2
						AND lease_expires_at > $3
					RETURNING
						lease_generation,
						last_claimed_at,
						lease_expires_at
				`,
				lease.Origin.String(),
				lease.Generation,
				now,
				candidateExpiresAt,
			).Scan(
				&renewed.Generation,
				&renewed.ClaimedAt,
				&renewed.ExpiresAt,
			)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrDiscoverySourceLeaseLost
			}

			if err != nil {
				return fmt.Errorf(
					"store: update discovery source lease: %w",
					err,
				)
			}

			renewed.ClaimedAt = renewed.ClaimedAt.UTC()
			renewed.ExpiresAt = renewed.ExpiresAt.UTC()

			return nil
		},
	)
	if err != nil {
		return discovery.CrawlSourceLease{}, fmt.Errorf(
			"store: renew discovery source lease: %w",
			err,
		)
	}

	return renewed, nil
}

func validDiscoverySourceLease(
	lease discovery.CrawlSourceLease,
) bool {
	return lease.Origin.String() != "" &&
		lease.Generation > 0 &&
		!lease.ClaimedAt.IsZero() &&
		!lease.ExpiresAt.IsZero() &&
		lease.ExpiresAt.After(lease.ClaimedAt)
}

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
	errInvalidDiscoveryLeaseOwner = errors.New(
		"store: discovery lease owner is invalid",
	)
	errInvalidDiscoverySourceLease = errors.New(
		"store: discovery source lease is invalid",
	)
)

// ClaimDiscoverySourceLease leases one due crawl source without consuming its
// discovery interval.
//
// Active discovery work comes only from discovery_source_schedule. Durable
// source classification and history do not independently create crawl work.
//
// Curated seeds and currently verified participants remain claimable while
// automatic expansion is paused or verification-queue backpressure is active.
// Other scheduled sources observe those automatic-expansion controls.
//
// last_attempted_at records completed crawl attempts only. Claiming instead
// advances lease_generation and records a renewable expiration. If the
// claimant disappears, the source becomes eligible again after the lease
// expires.
func (s *DiscoveryStore) ClaimDiscoverySourceLease(
	ctx context.Context,
	leaseOwner string,
	interval time.Duration,
	leaseDuration time.Duration,
) (discovery.CrawlSourceLease, bool, error) {
	if err := s.validate(ctx); err != nil {
		return discovery.CrawlSourceLease{}, false, err
	}

	if !validQueueWorkerID(leaseOwner) {
		return discovery.CrawlSourceLease{}, false,
			errInvalidDiscoveryLeaseOwner
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

			var (
				discoveryPaused          bool
				automaticExpansionPaused bool
			)

			if err := tx.QueryRow(
				ctx,
				`
					SELECT
						discovery_paused,
						automatic_expansion_paused
					FROM crawl_control
					WHERE singleton
				`,
			).Scan(
				&discoveryPaused,
				&automaticExpansionPaused,
			); err != nil {
				return fmt.Errorf(
					"store: read crawl control: %w",
					err,
				)
			}

			if discoveryPaused {
				return nil
			}

			automaticCrawlEnabled :=
				s.automatic.Enabled &&
					!automaticExpansionPaused

			var storedOrigin string

			scanErr := tx.QueryRow(
				ctx,
				`
					WITH scheduled_candidate AS (
						SELECT
							schedule.source_origin,
							source_state.last_attempted_at,
							COALESCE(
								source_state.seeded,
								false
							) AS seeded
						FROM discovery_source_schedule
							AS schedule
						LEFT JOIN discovery_source_state
							AS source_state
							ON source_state.source_origin =
								schedule.source_origin
						LEFT JOIN LATERAL (
							SELECT observation.outcome
							FROM verification_observations
								AS observation
							WHERE observation.origin =
								schedule.source_origin
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
						CROSS JOIN LATERAL (
							SELECT count(*) AS count
							FROM (
								SELECT 1
								FROM verification_queue
								WHERE mode = 'probe'
								LIMIT $4::bigint
							) AS pending_probe
						) AS pending_probes
						WHERE NOT COALESCE(
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
								source_state.lease_expires_at IS NULL
								OR source_state.lease_expires_at <= $1
							)
							AND (
								COALESCE(
								source_state.seeded,
								false
								)
								OR effective.outcome = 'valid'
								OR (
								$3::boolean
								AND pending_probes.count <
								$4::bigint
								)
							)
						ORDER BY
							source_state.last_attempted_at
								ASC NULLS FIRST,
							schedule.source_origin ASC
						FOR UPDATE OF schedule
							SKIP LOCKED
						LIMIT 1
					)
					INSERT INTO discovery_source_state (
						source_origin,
						seeded,
						lease_generation,
						lease_owner,
						last_claimed_at,
						lease_expires_at
					)
					SELECT
						source_origin,
						seeded,
						1,
						$6::text,
						$1::timestamptz,
						$5::timestamptz
					FROM scheduled_candidate
					ON CONFLICT (source_origin) DO UPDATE
					SET
						lease_generation =
							discovery_source_state.
								lease_generation + 1,
						lease_owner =
							EXCLUDED.lease_owner,
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
				automaticCrawlEnabled,
				s.automatic.maxPendingProbes(),
				expiresAt,
				leaseOwner,
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

			if _, err := tx.Exec(
				ctx,
				`
					UPDATE crawl_runs
					SET
						finished_at = $2,
						outcome = 'canceled',
						stop_reason = 'lease_reclaimed'
					WHERE source_origin = $1
						AND outcome = 'running'
						AND finished_at IS NULL
				`,
				source.String(),
				now,
			); err != nil {
				return fmt.Errorf(
					"store: cancel abandoned crawl runs: %w",
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

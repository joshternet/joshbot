package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
)

const maxDiscoveryCandidatesPerSource = 1024

var (
	errDiscoveryStoreUnavailable = errors.New(
		"store: discovery store is unavailable",
	)
	errDiscoveryClockUnavailable = errors.New(
		"store: discovery clock is unavailable",
	)
	errInvalidDiscoveryInterval = errors.New(
		"store: discovery interval is invalid",
	)
	errInvalidDiscoveryCandidate = errors.New(
		"store: discovery candidate is invalid",
	)
	errDiscoverySourceUnknown = errors.New(
		"store: discovery source is unknown",
	)
)

// DiscoveryStore coordinates discovery scheduling and private provenance.
//
// The caller retains ownership of the PostgreSQL pool.
type DiscoveryStore struct {
	pool  *pgxpool.Pool
	clock transactionQueueClock
}

// NewDiscoveryStore constructs a discovery store.
func NewDiscoveryStore(
	pool *pgxpool.Pool,
) (*DiscoveryStore, error) {
	return newDiscoveryStore(
		pool,
		databaseQueueClock{},
	)
}

func newDiscoveryStore(
	pool *pgxpool.Pool,
	clock transactionQueueClock,
) (*DiscoveryStore, error) {
	if pool == nil {
		return nil, errPoolUnavailable
	}

	if clock == nil {
		return nil, errDiscoveryClockUnavailable
	}

	return &DiscoveryStore{
		pool:  pool,
		clock: clock,
	}, nil
}

// ClaimDiscoverySource claims and marks one due, effectively verified source.
func (s *DiscoveryStore) ClaimDiscoverySource(
	ctx context.Context,
	interval time.Duration,
) (origin.Origin, bool, error) {
	if err := s.validate(ctx); err != nil {
		return origin.Origin{}, false, err
	}

	if interval <= 0 {
		return origin.Origin{}, false,
			errInvalidDiscoveryInterval
	}

	var (
		claimed origin.Origin
		found   bool
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

			var storedOrigin string
			err = tx.QueryRow(
				ctx,
				`
					WITH candidate AS (
						SELECT stored_origin.origin
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
							ON discovery_source_state.source_origin =
								stored_origin.origin
						WHERE
							discovery_source_state.last_attempted_at
								IS NULL
							OR discovery_source_state.last_attempted_at
								<= (
									$1::timestamptz -
									make_interval(
										secs =>
											$2::double precision
									)
								)
						ORDER BY
							discovery_source_state.last_attempted_at
								ASC NULLS FIRST,
							stored_origin.origin ASC
						FOR UPDATE OF stored_origin SKIP LOCKED
						LIMIT 1
					)
					INSERT INTO discovery_source_state (
						source_origin,
						last_attempted_at
					)
					SELECT
						candidate.origin,
						$1::timestamptz
					FROM candidate
					ON CONFLICT (source_origin) DO UPDATE
					SET last_attempted_at =
						EXCLUDED.last_attempted_at
					RETURNING source_origin
				`,
				now.UTC(),
				interval.Seconds(),
			).Scan(&storedOrigin)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}

			if err != nil {
				return err
			}

			claimed, err = origin.Parse(storedOrigin)
			if err != nil {
				return fmt.Errorf(
					"store: invalid discovery source: %w",
					err,
				)
			}

			found = true

			return nil
		},
	)
	if err != nil {
		return origin.Origin{}, false, fmt.Errorf(
			"store: claim discovery source: %w",
			err,
		)
	}

	return claimed, found, nil
}

// RecordDiscovery atomically records one bounded candidate batch.
//
// Existing queue rows remain completely untouched. A candidate receives a
// probe only when it currently has no queue row.
func (s *DiscoveryStore) RecordDiscovery(
	ctx context.Context,
	source origin.Origin,
	candidates []discovery.Candidate,
) (discovery.RecordResult, error) {
	if err := s.validate(ctx); err != nil {
		return discovery.RecordResult{}, err
	}

	prepared, err := prepareDiscoveryCandidates(
		source,
		candidates,
	)
	if err != nil {
		return discovery.RecordResult{}, err
	}

	if len(prepared) == 0 {
		return discovery.RecordResult{}, nil
	}

	candidateOrigins := make(
		[]string,
		len(prepared),
	)
	candidateKinds := make(
		[]string,
		len(prepared),
	)

	for index, candidate := range prepared {
		candidateOrigins[index] =
			candidate.Origin.String()
		candidateKinds[index], _ =
			discoveryKindText(candidate.Kind)
	}

	accepted := 0

	err = pgx.BeginFunc(
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

			var (
				sourceCount   int
				edgeCount     int
				scheduleCount int
			)

			err = tx.QueryRow(
				ctx,
				`
					WITH locked_source AS (
						SELECT origin
						FROM origins
						WHERE origin = $1
						FOR UPDATE
					),
					input AS (
						SELECT
							values.candidate_origin,
							values.kind
						FROM unnest(
							$2::text[],
							$3::text[]
						) AS values(
							candidate_origin,
							kind
						)
						CROSS JOIN locked_source
					),
					existing AS (
						SELECT DISTINCT candidate_origin
						FROM discovery_edges
						WHERE source_origin = $1
					),
					capacity AS (
						SELECT GREATEST(
							$5 - count(*),
							0
						)::bigint AS remaining
						FROM existing
					),
					existing_input AS (
						SELECT
							input.candidate_origin,
							input.kind
						FROM input
						JOIN existing
							USING (candidate_origin)
					),
					new_input AS (
						SELECT
							input.candidate_origin,
							input.kind
						FROM input
						WHERE NOT EXISTS (
							SELECT 1
							FROM existing
							WHERE existing.candidate_origin =
								input.candidate_origin
						)
						ORDER BY input.candidate_origin
						LIMIT (
							SELECT remaining
							FROM capacity
						)
					),
					accepted AS (
						SELECT
							candidate_origin,
							kind
						FROM existing_input

						UNION ALL

						SELECT
							candidate_origin,
							kind
						FROM new_input
					),
					stored_candidates AS (
						INSERT INTO discovery_candidates (
							origin,
							first_discovered_at,
							last_discovered_at
						)
						SELECT
							candidate_origin,
							$4,
							$4
						FROM accepted
						ON CONFLICT (origin) DO UPDATE
						SET
							first_discovered_at = LEAST(
								discovery_candidates.
									first_discovered_at,
								EXCLUDED.first_discovered_at
							),
							last_discovered_at = GREATEST(
								discovery_candidates.
									last_discovered_at,
								EXCLUDED.last_discovered_at
							)
						RETURNING origin
					),
					stored_edges AS (
						INSERT INTO discovery_edges (
							source_origin,
							candidate_origin,
							kind,
							first_discovered_at,
							last_discovered_at
						)
						SELECT
							$1,
							accepted.candidate_origin,
							accepted.kind,
							$4,
							$4
						FROM accepted
						JOIN stored_candidates
							ON stored_candidates.origin =
								accepted.candidate_origin
						ON CONFLICT (
							source_origin,
							candidate_origin,
							kind
						) DO UPDATE
						SET
							first_discovered_at = LEAST(
								discovery_edges.
									first_discovered_at,
								EXCLUDED.first_discovered_at
							),
							last_discovered_at = GREATEST(
								discovery_edges.
									last_discovered_at,
								EXCLUDED.last_discovered_at
							)
						RETURNING candidate_origin
					),
					scheduled_probes AS (
						INSERT INTO verification_queue (
							origin,
							available_at,
							mode
						)
						SELECT
							accepted.candidate_origin,
							$4,
							'probe'
						FROM accepted
						ON CONFLICT (origin) DO NOTHING
						RETURNING origin
					)
					SELECT
						(
							SELECT count(*)
							FROM locked_source
						),
						(
							SELECT count(*)
							FROM accepted
						),
						(
							SELECT count(*)
							FROM stored_edges
						),
						(
							SELECT count(*)
							FROM scheduled_probes
						)
				`,
				source.String(),
				candidateOrigins,
				candidateKinds,
				now.UTC(),
				maxDiscoveryCandidatesPerSource,
			).Scan(
				&sourceCount,
				&accepted,
				&edgeCount,
				&scheduleCount,
			)
			if err != nil {
				return err
			}

			if sourceCount != 1 {
				return errDiscoverySourceUnknown
			}

			if edgeCount != accepted {
				return errors.New(
					"store: discovery edge write was incomplete",
				)
			}

			_ = scheduleCount

			return nil
		},
	)
	if errors.Is(err, errDiscoverySourceUnknown) {
		return discovery.RecordResult{},
			errDiscoverySourceUnknown
	}

	if err != nil {
		return discovery.RecordResult{}, fmt.Errorf(
			"store: record discovery: %w",
			err,
		)
	}

	return discovery.RecordResult{
		Accepted: accepted,
		Dropped:  len(prepared) - accepted,
	}, nil
}

func (s *DiscoveryStore) validate(
	ctx context.Context,
) error {
	if s == nil {
		return errDiscoveryStoreUnavailable
	}

	if ctx == nil {
		return errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if s.pool == nil {
		return errPoolUnavailable
	}

	if s.clock == nil {
		return errDiscoveryClockUnavailable
	}

	return nil
}

func prepareDiscoveryCandidates(
	source origin.Origin,
	candidates []discovery.Candidate,
) ([]discovery.Candidate, error) {
	if source.String() == "" {
		return nil, errInvalidOrigin
	}

	if len(candidates) > discovery.MaxCandidates {
		return nil, errInvalidDiscoveryCandidate
	}

	prepared := append(
		[]discovery.Candidate(nil),
		candidates...,
	)
	seen := make(
		map[origin.Origin]struct{},
		len(prepared),
	)

	for _, candidate := range prepared {
		if candidate.Origin.String() == "" ||
			candidate.Origin == source {
			return nil, errInvalidDiscoveryCandidate
		}

		if _, known := discoveryKindText(
			candidate.Kind,
		); !known {
			return nil, errInvalidDiscoveryCandidate
		}

		if _, duplicate := seen[candidate.Origin]; duplicate {
			return nil, errInvalidDiscoveryCandidate
		}

		seen[candidate.Origin] = struct{}{}
	}

	sort.Slice(
		prepared,
		func(left int, right int) bool {
			return prepared[left].Origin.String() <
				prepared[right].Origin.String()
		},
	)

	return prepared, nil
}

func discoveryKindText(
	kind discovery.Kind,
) (string, bool) {
	switch kind {
	case discovery.KindLink:
		return "link", true
	case discovery.KindRedirect:
		return "redirect", true
	default:
		return "", false
	}
}

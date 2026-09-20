package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/database"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

const discoveryClaimAdvisoryLockKey int64 = 0x4a6f7368436c6169
const automaticAdmissionAdvisoryLockKey int64 = 0x4a6f736841646d69

const defaultAutomaticCrawlMaxPendingProbes = 1000
const defaultAutomaticPromotionsPerRun = 100

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
	pool        database.Postgres
	clock       transactionQueueClock
	automatic   AutomaticCrawlConfig
	retryPolicy retry.Policy
}

// AutomaticCrawlConfig controls promotion of discovered origins into crawl
// sources. A disabled configuration preserves the original curated and
// verified-source behavior.
type AutomaticCrawlConfig struct {
	Enabled                      bool
	MaxPendingProbes             int
	MaxAutomaticPromotionsPerRun int
	ExcludedHostSuffixes         string
	RetryJitter                  retry.Jitter
}

// CrawlSource describes the private operational classification of an origin.
type CrawlSource struct {
	Origin                  origin.Origin
	Seeded                  bool
	AutomaticallyDiscovered bool
	Blocked                 bool
	Verified                bool
	FirstDiscoveredAt       *time.Time
	LastDiscoveredAt        *time.Time
}

// AutomaticCandidateResult is one typed network admission decision.
type AutomaticCandidateResult struct {
	Candidate       discovery.Candidate
	FailureCategory retry.Category
}

func (s *DiscoveryStore) automaticExclusions(ctx context.Context) (AutomaticCrawlConfig, error) {
	rows, err := s.pool.Query(ctx, `SELECT pattern FROM crawl_domain_avoid_rules ORDER BY pattern`)
	if err != nil {
		return AutomaticCrawlConfig{}, fmt.Errorf("store: read crawl domain avoid rules: %w", err)
	}
	patterns, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (string, error) {
		var pattern string
		err := row.Scan(&pattern)
		return pattern, err
	})
	if err != nil {
		return AutomaticCrawlConfig{}, fmt.Errorf("store: collect crawl domain avoid rules: %w", err)
	}
	configured := strings.TrimSpace(s.automatic.ExcludedHostSuffixes)
	if configured != "" {
		patterns = append(patterns, configured)
	}
	return AutomaticCrawlConfig{ExcludedHostSuffixes: strings.Join(patterns, ",")}, nil
}

// SetCrawlBlocked applies or removes the operator deny policy for an origin.
// Blocking preserves its seed, discovery, verification, and history records.
func (s *DiscoveryStore) SetCrawlBlocked(
	ctx context.Context,
	source origin.Origin,
	blocked bool,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}
	if source.String() == "" {
		return errInvalidOrigin
	}

	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return setExactCrawlBlockTransaction(
			ctx,
			tx,
			source,
			blocked,
			s.automatic,
		)
	})
	if err != nil {
		return fmt.Errorf("store: set crawl block: %w", err)
	}
	return nil
}

// CrawlSources returns every origin with private crawl-source state, including
// its independent curated, automatic, blocked, and verified classifications.
func (s *DiscoveryStore) CrawlSources(
	ctx context.Context,
) ([]CrawlSource, error) {
	if err := s.validate(ctx); err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT
			state.source_origin,
			state.seeded,
			state.automatically_discovered,
			state.crawl_blocked,
			COALESCE(effective.outcome = 'valid', false),
			candidate.first_discovered_at,
			candidate.last_discovered_at
		FROM discovery_source_state AS state
		LEFT JOIN discovery_candidates AS candidate
			ON candidate.origin = state.source_origin
		LEFT JOIN LATERAL (
			SELECT observation.outcome
			FROM verification_observations AS observation
			WHERE observation.origin = state.source_origin
				AND observation.outcome IN (
					'valid', 'absent', 'invalid', 'unsupported_version',
					'cross_origin_redirect'
				)
			ORDER BY observation.observed_at DESC, observation.id DESC
			LIMIT 1
		) AS effective ON true
		ORDER BY state.source_origin
	`)
	if err != nil {
		return nil, fmt.Errorf("store: list crawl sources: %w", err)
	}
	defer rows.Close()

	sources, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (CrawlSource, error) {
		var raw string
		var source CrawlSource
		if err := row.Scan(
			&raw,
			&source.Seeded,
			&source.AutomaticallyDiscovered,
			&source.Blocked,
			&source.Verified,
			&source.FirstDiscoveredAt,
			&source.LastDiscoveredAt,
		); err != nil {
			return CrawlSource{}, err
		}
		parsed, err := origin.Parse(raw)
		if err != nil {
			return CrawlSource{}, fmt.Errorf("invalid origin: %w", err)
		}
		source.Origin = parsed
		return source, nil
	})
	if err != nil {
		return nil, fmt.Errorf("store: collect crawl sources: %w", err)
	}
	return sources, nil
}

// ReconcileAutomaticCrawlPolicy applies the current automatic-source
// exclusions to sources promoted by an earlier process configuration.
// Curated seeds remain operator-controlled. Discovery provenance and history
// are retained, while excluded automatic sources are blocked and any pending
// probe work for them is removed.
func (s *DiscoveryStore) ReconcileAutomaticCrawlPolicy(
	ctx context.Context,
) (int, error) {
	if err := s.validate(ctx); err != nil {
		return 0, err
	}
	if !s.automatic.Enabled {
		return 0, nil
	}
	changed := 0
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		changed, err = recomputeAutomaticCrawlBlocksTransaction(
			ctx,
			tx,
			s.automatic,
		)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("store: reconcile automatic crawl policy: %w", err)
	}
	return changed, nil
}

// NewDiscoveryStore constructs a discovery store.
func NewDiscoveryStore(
	pool *pgxpool.Pool,
) (*DiscoveryStore, error) {
	if pool == nil {
		return newDiscoveryStore(
			nil,
			databaseQueueClock{},
		)
	}

	return newDiscoveryStore(
		pool,
		databaseQueueClock{},
	)
}

// NewDiscoveryStoreWithAutomaticCrawling constructs a discovery store that
// can promote discovered origins into crawl sources under queue backpressure.
func NewDiscoveryStoreWithAutomaticCrawling(
	pool *pgxpool.Pool,
	config AutomaticCrawlConfig,
) (*DiscoveryStore, error) {
	if pool == nil {
		return newDiscoveryStoreWithConfig(
			nil,
			databaseQueueClock{},
			config,
		)
	}

	return newDiscoveryStoreWithConfig(
		pool,
		databaseQueueClock{},
		config,
	)
}

func newDiscoveryStore(
	pool database.Postgres,
	clock transactionQueueClock,
) (*DiscoveryStore, error) {
	return newDiscoveryStoreWithConfig(
		pool,
		clock,
		AutomaticCrawlConfig{},
	)
}

func newDiscoveryStoreWithConfig(
	pool database.Postgres,
	clock transactionQueueClock,
	config AutomaticCrawlConfig,
) (*DiscoveryStore, error) {
	if pool == nil {
		return nil, errPoolUnavailable
	}

	if clock == nil {
		return nil, errDiscoveryClockUnavailable
	}

	if config.MaxPendingProbes < 0 {
		return nil, errInvalidQueueConfig
	}
	if config.MaxAutomaticPromotionsPerRun < 0 {
		return nil, errInvalidQueueConfig
	}
	if !validExcludedHostSuffixes(config.ExcludedHostSuffixes) {
		return nil, errInvalidQueueConfig
	}

	return &DiscoveryStore{
		pool:        pool,
		clock:       clock,
		automatic:   config,
		retryPolicy: retry.NewPolicy(config.RetryJitter),
	}, nil
}

// AddCrawlSeed marks an origin as an explicitly curated crawl source.
//
// Adding a seed does not create verification observations or verification
// queue work. Existing crawl scheduling state is preserved.
func (s *DiscoveryStore) AddCrawlSeed(
	ctx context.Context,
	source origin.Origin,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}

	if source.String() == "" {
		return errInvalidOrigin
	}

	_, err := s.pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_state (
				source_origin,
				seeded
			)
			VALUES ($1, true)
			ON CONFLICT (source_origin) DO UPDATE
			SET seeded = true
		`,
		source.String(),
	)
	if err != nil {
		return fmt.Errorf(
			"store: add crawl seed: %w",
			err,
		)
	}

	return nil
}

// RemoveCrawlSeed removes explicit seed status from an origin.
//
// Verification observations, candidate provenance, queue state, and crawl
// scheduling history remain unchanged.
func (s *DiscoveryStore) RemoveCrawlSeed(
	ctx context.Context,
	source origin.Origin,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}

	if source.String() == "" {
		return errInvalidOrigin
	}

	_, err := s.pool.Exec(
		ctx,
		`
			UPDATE discovery_source_state
			SET seeded = false
			WHERE source_origin = $1
		`,
		source.String(),
	)
	if err != nil {
		return fmt.Errorf(
			"store: remove crawl seed: %w",
			err,
		)
	}

	return nil
}

// CrawlSeeds returns explicitly curated seeds in canonical order.
func (s *DiscoveryStore) CrawlSeeds(
	ctx context.Context,
) ([]origin.Origin, error) {
	if err := s.validate(ctx); err != nil {
		return nil, err
	}

	var stored []string
	err := s.pool.QueryRow(
		ctx,
		`
			SELECT COALESCE(
				array_agg(
					source_origin
					ORDER BY source_origin
				),
				ARRAY[]::text[]
			)
			FROM discovery_source_state
			WHERE seeded
		`,
	).Scan(&stored)
	if err != nil {
		return nil, fmt.Errorf(
			"store: list crawl seeds: %w",
			err,
		)
	}

	seeds := make(
		[]origin.Origin,
		len(stored),
	)

	for index, rawOrigin := range stored {
		seed, err := origin.Parse(rawOrigin)
		if err != nil {
			return nil, fmt.Errorf(
				"store: invalid crawl seed: %w",
				err,
			)
		}

		seeds[index] = seed
	}

	return seeds, nil
}

func (config AutomaticCrawlConfig) maxPendingProbes() int {
	if config.MaxPendingProbes > 0 {
		return config.MaxPendingProbes
	}

	return defaultAutomaticCrawlMaxPendingProbes
}

func (config AutomaticCrawlConfig) maxAutomaticPromotionsPerRun() int {
	if config.MaxAutomaticPromotionsPerRun > 0 {
		return config.MaxAutomaticPromotionsPerRun
	}
	return defaultAutomaticPromotionsPerRun
}

// RecordDiscovery durably records one complete candidate-evidence batch before
// attempting bounded probe admission. Admission failure never rolls back valid
// candidates or provenance edges.
func (s *DiscoveryStore) RecordDiscovery(
	ctx context.Context,
	source origin.Origin,
	candidates []discovery.Candidate,
) (discovery.RecordResult, error) {
	return s.recordDiscovery(ctx, 0, source, candidates)
}

// RecordDiscoveryForRun records the same durable evidence while also
// attributing every candidate kind to one active crawl run.
func (s *DiscoveryStore) RecordDiscoveryForRun(
	ctx context.Context,
	runID discovery.CrawlRunID,
	source origin.Origin,
	candidates []discovery.Candidate,
) (discovery.RecordResult, error) {
	if runID <= 0 {
		return discovery.RecordResult{}, errInvalidCrawlRun
	}
	return s.recordDiscovery(ctx, runID, source, candidates)
}

func (s *DiscoveryStore) recordDiscovery(
	ctx context.Context,
	runID discovery.CrawlRunID,
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
	var recordedAt time.Time

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
			recordedAt = now.UTC()

			var (
				sourceCount int
				runCount    int
			)

			err = tx.QueryRow(
				ctx,
				`
					WITH locked_verification_source AS (
						SELECT
							origin AS source_origin
						FROM origins
						WHERE origin = $1
						FOR UPDATE
					),
					locked_crawl_source AS (
						SELECT source_origin
						FROM discovery_source_state
						WHERE source_origin = $1
						FOR UPDATE
					),
					locked_source AS (
						SELECT source_origin
						FROM locked_verification_source

						UNION

						SELECT source_origin
						FROM locked_crawl_source
					),
					locked_run AS (
						SELECT id
						FROM crawl_runs
						WHERE id = $5 AND finished_at IS NULL
						FOR UPDATE
					),
					valid_run AS (
						SELECT 1 WHERE $5::bigint = 0

						UNION ALL

						SELECT 1 FROM locked_run
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
						CROSS JOIN valid_run
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
						FROM input
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
							input.candidate_origin,
							input.kind,
							$4,
							$4
						FROM input
						JOIN stored_candidates
							ON stored_candidates.origin =
								input.candidate_origin
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
					stored_run_candidates AS (
						INSERT INTO crawl_run_discovery_candidates (
							run_id,
							candidate_origin,
							kind
						)
						SELECT
							$5,
							input.candidate_origin,
							input.kind
						FROM input
						JOIN stored_candidates
							ON stored_candidates.origin =
								input.candidate_origin
						WHERE $5::bigint > 0
						ON CONFLICT (run_id, candidate_origin, kind)
							DO UPDATE SET kind = EXCLUDED.kind
						RETURNING candidate_origin
					)
					SELECT
						(
							SELECT count(*)
							FROM locked_source
						),
						(
							SELECT count(*) FROM valid_run
						),
						(
							SELECT count(*)
							FROM input
						)
				`,
				source.String(),
				candidateOrigins,
				candidateKinds,
				recordedAt,
				int64(runID),
			).Scan(
				&sourceCount,
				&runCount,
				&accepted,
			)
			if err != nil {
				return err
			}

			if sourceCount != 1 {
				return errDiscoverySourceUnknown
			}
			if runCount != 1 {
				return errUnknownCrawlRun
			}

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

	if !s.automatic.Enabled {
		if err := s.scheduleProbeCandidates(ctx, prepared, recordedAt); err != nil {
			return discovery.RecordResult{}, err
		}
	}

	return discovery.RecordResult{
		Accepted: accepted,
	}, nil
}

func (s *DiscoveryStore) scheduleProbeCandidates(
	ctx context.Context,
	candidates []discovery.Candidate,
	availableAt time.Time,
) error {
	exclusions, err := s.automaticExclusions(ctx)
	if err != nil {
		return err
	}
	eligible := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if !exclusions.excludes(candidate.Origin.Hostname()) {
			eligible = append(eligible, candidate.Origin.String())
		}
	}
	if len(eligible) == 0 {
		return nil
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(
			ctx,
			"SELECT pg_advisory_xact_lock($1)",
			automaticAdmissionAdvisoryLockKey,
		); err != nil {
			return fmt.Errorf("store: lock probe admission: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO verification_queue (origin, available_at, mode)
			SELECT candidate.origin, $2, 'probe'
			FROM unnest($1::text[]) AS candidate(origin)
			WHERE NOT EXISTS (
				SELECT 1 FROM verification_queue
				WHERE verification_queue.origin = candidate.origin
			)
			ORDER BY candidate.origin
			LIMIT GREATEST(
				$3::bigint - (
					SELECT count(*) FROM verification_queue
					WHERE mode = 'probe'
				),
				0
			)
			ON CONFLICT (origin) DO NOTHING
		`, eligible, availableAt, s.automatic.maxPendingProbes()); err != nil {
			return fmt.Errorf("store: admit discovery probes: %w", err)
		}
		return nil
	})
}

// PendingAutomaticCandidates allocates and returns one durable, bounded
// admission batch for runID. Deferred candidates are assigned to later runs in
// canonical order, while redirect-only candidates remain probe-only.
func (s *DiscoveryStore) PendingAutomaticCandidates(
	ctx context.Context,
	runID discovery.CrawlRunID,
) ([]discovery.Candidate, error) {
	if err := s.validate(ctx); err != nil {
		return nil, err
	}
	if !s.automatic.Enabled {
		return nil, nil
	}
	if runID <= 0 {
		return nil, errInvalidCrawlRun
	}

	var pending []discovery.Candidate
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(
			ctx,
			"SELECT pg_advisory_xact_lock($1)",
			automaticAdmissionAdvisoryLockKey,
		); err != nil {
			return fmt.Errorf("store: lock automatic batch allocation: %w", err)
		}
		now, err := s.clock.NowTransaction(ctx, tx)
		if err != nil {
			return fmt.Errorf("store: read automatic admission clock: %w", err)
		}

		var maxPromotions, batchCount int
		if err := tx.QueryRow(ctx, `
			SELECT
				run.max_automatic_promotions,
				(
					SELECT count(*)
					FROM crawl_run_automatic_admission_batches AS batch
					WHERE batch.admission_run_id = run.id
				)
			FROM crawl_runs AS run
			WHERE run.id = $1 AND finished_at IS NULL
			FOR UPDATE
		`, int64(runID)).Scan(&maxPromotions, &batchCount); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errUnknownCrawlRun
			}
			return fmt.Errorf("read automatic admission run: %w", err)
		}

		exclusions, err := automaticExclusionsTransaction(ctx, tx, s.automatic)
		if err != nil {
			return err
		}
		patterns := automaticExclusionPatterns(exclusions)
		remainingBatch := maxPromotions - batchCount
		if remainingBatch > 0 {
			if _, err := tx.Exec(ctx, `
				WITH candidate_pool AS (
					SELECT
						candidate.origin,
						COALESCE(
							MIN(run_candidate.run_id),
							$1::bigint
						) AS discovered_run_id
					FROM discovery_candidates AS candidate
					LEFT JOIN crawl_run_discovery_candidates AS run_candidate
						ON run_candidate.candidate_origin = candidate.origin
					LEFT JOIN discovery_source_state AS source_state
						ON source_state.source_origin = candidate.origin
					WHERE EXISTS (
						SELECT 1 FROM discovery_edges AS edge
						WHERE edge.candidate_origin = candidate.origin
							AND edge.kind = 'link'
					)
						AND (
							candidate.next_attempt_at IS NULL
							OR candidate.next_attempt_at <= $4
						)
						AND NOT COALESCE(
							source_state.automatically_discovered,
							false
						)
						AND NOT COALESCE(source_state.crawl_blocked, false)
						AND NOT EXISTS (
							SELECT 1
							FROM crawl_run_automatic_admission_batches AS prior
							WHERE prior.candidate_origin = candidate.origin
								AND prior.outcome IN (
								'promoted',
								'network_rejected',
								'existing'
								)
						)
						AND NOT EXISTS (
							SELECT 1
							FROM crawl_run_automatic_admission_batches AS current
							WHERE current.admission_run_id = $1
								AND current.candidate_origin = candidate.origin
						)
						AND NOT EXISTS (
							SELECT 1
							FROM unnest($2::text[]) AS excluded(pattern)
							CROSS JOIN LATERAL (
								SELECT lower(trim(
								both '[]' from substring(
								candidate.origin
								from '^https?://(\[[^]]+\]|[^:]+)'
								)
								)) AS hostname
							) AS parsed
							WHERE (
								right(excluded.pattern, 2) = '.*'
								AND (
								parsed.hostname LIKE
								left(
								excluded.pattern,
								length(excluded.pattern) - 2
								) || '.%'
								OR parsed.hostname LIKE
								'%.' || left(
								excluded.pattern,
								length(excluded.pattern) - 2
								) || '.%'
								)
							) OR (
								right(excluded.pattern, 2) <> '.*'
								AND (
								parsed.hostname = excluded.pattern
								OR parsed.hostname LIKE
								'%.' || excluded.pattern
								)
							)
						)
					GROUP BY candidate.origin
					ORDER BY candidate.origin
					LIMIT $3
				)
				INSERT INTO crawl_run_automatic_admission_batches (
					admission_run_id,
					candidate_origin,
					discovered_run_id
				)
				SELECT $1, origin, discovered_run_id
				FROM candidate_pool
				ON CONFLICT (admission_run_id, candidate_origin) DO NOTHING
			`, int64(runID), patterns, remainingBatch, now.UTC()); err != nil {
				return fmt.Errorf("allocate automatic admission batch: %w", err)
			}
		}

		var pendingProbes int
		if err := tx.QueryRow(
			ctx,
			`SELECT count(*) FROM verification_queue WHERE mode = 'probe'`,
		).Scan(&pendingProbes); err != nil {
			return fmt.Errorf("count pending probes: %w", err)
		}
		remainingProbes := s.automatic.maxPendingProbes() - pendingProbes
		if remainingProbes > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO verification_queue (origin, available_at, mode)
				SELECT candidate.origin, $3, 'probe'
				FROM discovery_candidates AS candidate
				LEFT JOIN discovery_source_state AS source_state
					ON source_state.source_origin = candidate.origin
				WHERE NOT COALESCE(source_state.crawl_blocked, false)
					AND EXISTS (
						SELECT 1
						FROM discovery_edges AS redirect_edge
						WHERE redirect_edge.candidate_origin = candidate.origin
							AND redirect_edge.kind = 'redirect'
					)
					AND NOT EXISTS (
						SELECT 1
						FROM discovery_edges AS link_edge
						WHERE link_edge.candidate_origin = candidate.origin
							AND link_edge.kind = 'link'
					)
					AND NOT EXISTS (
						SELECT 1 FROM verification_queue AS queue
						WHERE queue.origin = candidate.origin
					)
					AND NOT EXISTS (
						SELECT 1
						FROM unnest($1::text[]) AS excluded(pattern)
						CROSS JOIN LATERAL (
							SELECT lower(trim(
								both '[]' from substring(
								candidate.origin
								from '^https?://(\[[^]]+\]|[^:]+)'
								)
							)) AS hostname
						) AS parsed
						WHERE (
							right(excluded.pattern, 2) = '.*'
							AND (
								parsed.hostname LIKE
								left(
								excluded.pattern,
								length(excluded.pattern) - 2
								) || '.%'
								OR parsed.hostname LIKE
								'%.' || left(
								excluded.pattern,
								length(excluded.pattern) - 2
								) || '.%'
							)
						) OR (
							right(excluded.pattern, 2) <> '.*'
							AND (
								parsed.hostname = excluded.pattern
								OR parsed.hostname LIKE
								'%.' || excluded.pattern
							)
						)
					)
				ORDER BY candidate.origin
				LIMIT $2
				ON CONFLICT (origin) DO NOTHING
			`, patterns, remainingProbes, now.UTC()); err != nil {
				return fmt.Errorf("admit deferred discovery probes: %w", err)
			}
		}

		rows, err := tx.Query(ctx, `
			SELECT candidate_origin
			FROM crawl_run_automatic_admission_batches
			WHERE admission_run_id = $1 AND outcome = 'pending'
			ORDER BY candidate_origin
		`, int64(runID))
		if err != nil {
			return fmt.Errorf("list pending automatic candidates: %w", err)
		}
		pending, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (discovery.Candidate, error) {
			var rawOrigin string
			if err := row.Scan(&rawOrigin); err != nil {
				return discovery.Candidate{}, err
			}
			candidateOrigin, err := origin.Parse(rawOrigin)
			if err != nil {
				return discovery.Candidate{}, err
			}
			return discovery.Candidate{
				Origin: candidateOrigin,
				Kind:   discovery.KindLink,
			}, nil
		})
		if err != nil {
			return fmt.Errorf("collect pending automatic candidates: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("store: prepare automatic candidates: %w", err)
	}
	return pending, nil
}

// AdmitAutomaticCandidates atomically applies one crawl run's immutable
// promotion budget and the strict global pending-probe capacity. Callers must
// include link candidates only after a fresh all-address netguard resolution.
func (s *DiscoveryStore) AdmitAutomaticCandidates(
	ctx context.Context,
	runID discovery.CrawlRunID,
	candidates []discovery.Candidate,
) error {
	results := make([]AutomaticCandidateResult, len(candidates))
	for index, candidate := range candidates {
		results[index] = AutomaticCandidateResult{Candidate: candidate}
	}
	return s.CompleteAutomaticCandidates(ctx, runID, results)
}

// CompleteAutomaticCandidates persists typed resolution decisions before
// applying automatic-admission budgets.
func (s *DiscoveryStore) CompleteAutomaticCandidates(
	ctx context.Context,
	runID discovery.CrawlRunID,
	results []AutomaticCandidateResult,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}
	if !s.automatic.Enabled {
		return nil
	}
	if runID <= 0 {
		return errInvalidCrawlRun
	}
	candidates := make([]discovery.Candidate, len(results))
	for index, result := range results {
		candidates[index] = result.Candidate
	}
	prepared, err := prepareAdmissionCandidates(candidates)
	if err != nil {
		return err
	}
	decisions := make(map[origin.Origin]retry.Category, len(prepared))
	for index, candidate := range candidates {
		if candidate.Kind != discovery.KindLink {
			return errInvalidDiscoveryCandidate
		}
		category := results[index].FailureCategory
		if category != retry.CategoryNone &&
			category != retry.CategoryUnsafeAddress &&
			!category.Transient() {
			return errInvalidDiscoveryCandidate
		}
		decisions[candidate.Origin] = category
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(
			ctx,
			"SELECT pg_advisory_xact_lock($1)",
			automaticAdmissionAdvisoryLockKey,
		); err != nil {
			return fmt.Errorf("store: lock automatic admission: %w", err)
		}

		var maxPromotions, promotions int
		if err := tx.QueryRow(ctx, `
			SELECT max_automatic_promotions, automatic_promotions
			FROM crawl_runs
			WHERE id = $1 AND finished_at IS NULL
			FOR UPDATE
		`, int64(runID)).Scan(&maxPromotions, &promotions); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errUnknownCrawlRun
			}
			return fmt.Errorf("read automatic admission run: %w", err)
		}

		exclusions, err := automaticExclusionsTransaction(ctx, tx, s.automatic)
		if err != nil {
			return err
		}
		var pendingProbes int
		if err := tx.QueryRow(
			ctx,
			`SELECT count(*) FROM verification_queue WHERE mode = 'probe'`,
		).Scan(&pendingProbes); err != nil {
			return fmt.Errorf("count pending probes: %w", err)
		}
		now, err := s.clock.NowTransaction(ctx, tx)
		if err != nil {
			return fmt.Errorf("store: read discovery clock: %w", err)
		}

		var batch []string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(
				array_agg(candidate_origin ORDER BY candidate_origin),
				ARRAY[]::text[]
			)
			FROM crawl_run_automatic_admission_batches
			WHERE admission_run_id = $1 AND outcome = 'pending'
		`, int64(runID)).Scan(&batch); err != nil {
			return fmt.Errorf("collect automatic admission batch: %w", err)
		}

		for _, rawOrigin := range batch {
			candidateOrigin, err := origin.Parse(rawOrigin)
			if err != nil {
				return fmt.Errorf("invalid automatic admission candidate: %w", err)
			}
			category, resolved := decisions[candidateOrigin]
			if resolved && category.Transient() {
				var failures int
				if err := tx.QueryRow(ctx, `
					SELECT consecutive_failures
					FROM discovery_candidates
					WHERE origin = $1
					FOR UPDATE
				`, rawOrigin).Scan(&failures); err != nil {
					return fmt.Errorf("read admission retry streak: %w", err)
				}
				failures++
				nextAttempt := now.UTC().Add(s.retryPolicy.Delay(failures, 0))
				if _, err := tx.Exec(ctx, `
					UPDATE discovery_candidates
					SET
						consecutive_failures = $2,
						last_failure_category = $3,
						next_attempt_at = $4
					WHERE origin = $1;
				`, rawOrigin, failures, string(category), nextAttempt); err != nil {
					return fmt.Errorf("defer transient admission failure: %w", err)
				}
				if _, err := tx.Exec(ctx, `
					UPDATE crawl_run_automatic_admission_batches
					SET
						outcome = 'retry_deferred',
						consecutive_failures = $3,
						last_failure_category = $4,
						next_attempt_at = $5
					WHERE admission_run_id = $1 AND candidate_origin = $2
				`, int64(runID), rawOrigin, failures, string(category), nextAttempt); err != nil {
					return fmt.Errorf("record deferred admission outcome: %w", err)
				}
				continue
			}
			if !resolved || category == retry.CategoryUnsafeAddress {
				if _, err := tx.Exec(ctx, `
					UPDATE crawl_run_automatic_admission_batches
					SET outcome = 'network_rejected'
					WHERE admission_run_id = $1 AND candidate_origin = $2
				`, int64(runID), rawOrigin); err != nil {
					return fmt.Errorf("record network-rejected admission: %w", err)
				}
				continue
			}
			if _, err := tx.Exec(ctx, `
				UPDATE discovery_candidates
				SET
					consecutive_failures = 0,
					last_failure_category = NULL,
					next_attempt_at = NULL
				WHERE origin = $1
			`, rawOrigin); err != nil {
				return fmt.Errorf("reset admission retry streak: %w", err)
			}
			var (
				automaticallyDiscovered bool
				crawlBlocked            bool
				queueExists             bool
			)
			if err := tx.QueryRow(ctx, `
				SELECT
					COALESCE((
						SELECT automatically_discovered
						FROM discovery_source_state
						WHERE source_origin = $1
					), false),
					COALESCE((
						SELECT crawl_blocked
						FROM discovery_source_state
						WHERE source_origin = $1
					), false),
					EXISTS (
						SELECT 1 FROM verification_queue WHERE origin = $1
					)
			`, rawOrigin).Scan(
				&automaticallyDiscovered,
				&crawlBlocked,
				&queueExists,
			); err != nil {
				return fmt.Errorf("read candidate admission state: %w", err)
			}
			if crawlBlocked || exclusions.excludes(candidateOrigin.Hostname()) {
				if _, err := tx.Exec(ctx, `
					UPDATE crawl_run_automatic_admission_batches
					SET outcome = 'policy_deferred'
					WHERE admission_run_id = $1 AND candidate_origin = $2
				`, int64(runID), rawOrigin); err != nil {
					return fmt.Errorf("record policy-deferred admission: %w", err)
				}
				continue
			}
			if automaticallyDiscovered {
				if _, err := tx.Exec(ctx, `
					UPDATE crawl_run_automatic_admission_batches
					SET outcome = 'existing'
					WHERE admission_run_id = $1 AND candidate_origin = $2
				`, int64(runID), rawOrigin); err != nil {
					return fmt.Errorf("record existing automatic source: %w", err)
				}
				continue
			}
			if !queueExists && pendingProbes < s.automatic.maxPendingProbes() {
				if _, err := tx.Exec(ctx, `
					INSERT INTO verification_queue (origin, available_at, mode)
					VALUES ($1, $2, 'probe')
					ON CONFLICT (origin) DO NOTHING
				`, rawOrigin, now.UTC()); err != nil {
					return fmt.Errorf("schedule automatic candidate probe: %w", err)
				}
				queueExists = true
				pendingProbes++
			}
			if !queueExists || promotions >= maxPromotions {
				if _, err := tx.Exec(ctx, `
					UPDATE crawl_run_automatic_admission_batches
					SET outcome = 'capacity_deferred'
					WHERE admission_run_id = $1 AND candidate_origin = $2
				`, int64(runID), rawOrigin); err != nil {
					return fmt.Errorf("record capacity-deferred admission: %w", err)
				}
				continue
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO discovery_source_state (
					source_origin,
					automatically_discovered
				) VALUES ($1, true)
				ON CONFLICT (source_origin) DO UPDATE
				SET automatically_discovered = true
			`, rawOrigin); err != nil {
				return fmt.Errorf("promote automatic candidate: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE crawl_run_automatic_admission_batches
				SET outcome = 'promoted'
				WHERE admission_run_id = $1 AND candidate_origin = $2
			`, int64(runID), rawOrigin); err != nil {
				return fmt.Errorf("record promoted admission: %w", err)
			}
			promotions++
		}
		if _, err := tx.Exec(ctx, `
			UPDATE crawl_runs
			SET automatic_promotions = $2
			WHERE id = $1
		`, int64(runID), promotions); err != nil {
			return fmt.Errorf("update automatic promotion count: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			WITH affected AS (
				SELECT DISTINCT discovered_run_id AS run_id
				FROM crawl_run_automatic_admission_batches
				WHERE admission_run_id = $1
			),
			admitted AS (
				SELECT discovered_run_id AS run_id,
					count(DISTINCT candidate_origin) AS count
				FROM crawl_run_automatic_admission_batches
				WHERE discovered_run_id IN (SELECT run_id FROM affected)
					AND outcome = 'promoted'
				GROUP BY discovered_run_id
			),
			discovered AS (
				SELECT candidate.run_id, count(*) AS count
				FROM crawl_run_discovery_candidates AS candidate
				WHERE candidate.run_id IN (SELECT run_id FROM affected)
					AND candidate.kind = 'link'
					AND candidate.run_id = (
						SELECT min(original.run_id)
						FROM crawl_run_discovery_candidates AS original
						WHERE original.candidate_origin =
								candidate.candidate_origin
							AND original.kind = 'link'
					)
				GROUP BY candidate.run_id
			)
			UPDATE crawl_runs AS run
			SET promotions_admitted = COALESCE(admitted.count, 0),
				promotions_deferred = GREATEST(
					COALESCE(discovered.count, 0) -
						COALESCE(admitted.count, 0),
					0
				)
			FROM affected
			LEFT JOIN admitted ON admitted.run_id = affected.run_id
			LEFT JOIN discovered ON discovered.run_id = affected.run_id
			WHERE run.id = affected.run_id
		`, int64(runID)); err != nil {
			return fmt.Errorf("update original-run promotion telemetry: %w", err)
		}
		return nil
	})
}

func automaticExclusionsTransaction(
	ctx context.Context,
	tx pgx.Tx,
	config AutomaticCrawlConfig,
) (AutomaticCrawlConfig, error) {
	rows, err := tx.Query(ctx, `SELECT pattern FROM crawl_domain_avoid_rules ORDER BY pattern`)
	if err != nil {
		return AutomaticCrawlConfig{}, fmt.Errorf("store: read crawl domain avoid rules: %w", err)
	}
	patterns, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (string, error) {
		var pattern string
		err := row.Scan(&pattern)
		return pattern, err
	})
	if err != nil {
		return AutomaticCrawlConfig{}, fmt.Errorf("store: collect crawl domain avoid rules: %w", err)
	}
	if configured := strings.TrimSpace(config.ExcludedHostSuffixes); configured != "" {
		patterns = append(patterns, configured)
	}
	return AutomaticCrawlConfig{ExcludedHostSuffixes: strings.Join(patterns, ",")}, nil
}

func automaticExclusionPatterns(config AutomaticCrawlConfig) []string {
	var patterns []string
	for _, pattern := range strings.Split(config.ExcludedHostSuffixes, ",") {
		if pattern = strings.TrimSpace(pattern); pattern != "" {
			patterns = append(patterns, pattern)
		}
	}
	return patterns
}

func prepareAdmissionCandidates(
	candidates []discovery.Candidate,
) ([]discovery.Candidate, error) {
	prepared := append([]discovery.Candidate(nil), candidates...)
	seen := make(map[origin.Origin]struct{}, len(prepared))
	for _, candidate := range prepared {
		if candidate.Origin.String() == "" {
			return nil, errInvalidDiscoveryCandidate
		}
		if _, known := discoveryKindText(candidate.Kind); !known {
			return nil, errInvalidDiscoveryCandidate
		}
		if _, duplicate := seen[candidate.Origin]; duplicate {
			return nil, errInvalidDiscoveryCandidate
		}
		seen[candidate.Origin] = struct{}{}
	}
	sort.Slice(prepared, func(left, right int) bool {
		return prepared[left].Origin.String() < prepared[right].Origin.String()
	})
	return prepared, nil
}

func validExcludedHostSuffixes(value string) bool {
	for _, suffix := range strings.Split(value, ",") {
		suffix = strings.TrimSpace(suffix)
		if suffix == "" {
			continue
		}
		family := strings.TrimSuffix(suffix, ".*")
		if strings.ToLower(suffix) != suffix || strings.HasPrefix(suffix, ".") ||
			strings.HasSuffix(family, ".") || family == "" ||
			strings.ContainsAny(family, "/:@?#*") ||
			(strings.Contains(suffix, "*") && suffix != family+".*") {
			return false
		}
	}
	return true
}

func (config AutomaticCrawlConfig) excludes(hostname string) bool {
	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
	for _, suffix := range strings.Split(config.ExcludedHostSuffixes, ",") {
		suffix = strings.TrimSpace(suffix)
		if suffix == "" {
			continue
		}
		if family := strings.TrimSuffix(suffix, ".*"); family != suffix {
			if strings.HasPrefix(hostname, family+".") ||
				strings.Contains(hostname, "."+family+".") {
				return true
			}
			continue
		}
		if hostname == suffix || strings.HasSuffix(hostname, "."+suffix) {
			return true
		}
	}
	return false
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

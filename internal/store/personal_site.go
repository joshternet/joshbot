package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/domainavoid"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/personalsite"
)

const personalSiteClaimDuration = 2 * time.Minute

// PersonalSiteQualifyClaim is one atomically leased qualification attempt.
type PersonalSiteQualifyClaim struct {
	Origin     origin.Origin
	Generation int64
	Owner      string
	ExpiresAt  time.Time
}

var (
	// ErrPersonalSiteClaimLost means the claim expired or was taken over.
	ErrPersonalSiteClaimLost = errors.New(
		"store: personal site qualification claim lost",
	)
	errInvalidPersonalSiteClaimOwner = errors.New(
		"store: personal site claim owner is invalid",
	)
)

// ClaimPersonalSiteQualifications leases up to limit due qualification
// attempts. Expired claims are reclaimable. At most one active claim exists
// per origin.
//
// @param ctx - Request context.
// @param owner - Worker identity for fencing.
// @param limit - Maximum claims to take.
// @returns Claimed origins with generations.
func (s *DiscoveryStore) ClaimPersonalSiteQualifications(
	ctx context.Context,
	owner string,
	limit int,
) ([]PersonalSiteQualifyClaim, error) {
	if err := s.validate(ctx); err != nil {
		return nil, err
	}
	if !validQueueWorkerID(owner) {
		return nil, errInvalidPersonalSiteClaimOwner
	}
	if limit <= 0 || !s.automatic.Enabled {
		return nil, nil
	}

	exclusions, err := s.automaticExclusions(ctx)
	if err != nil {
		return nil, err
	}
	patterns := domainavoid.NonRegexPatterns(
		automaticExclusionPatterns(exclusions),
	)

	var claims []PersonalSiteQualifyClaim
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		now, clockErr := s.clock.NowTransaction(ctx, tx)
		if clockErr != nil {
			return fmt.Errorf("store: read personal site clock: %w", clockErr)
		}
		expiresAt := now.Add(personalSiteClaimDuration)

		rows, queryErr := tx.Query(ctx, `
			WITH due AS (
				SELECT candidate.origin
				FROM discovery_candidates AS candidate
				LEFT JOIN discovery_source_state AS source_state
					ON source_state.source_origin = candidate.origin
				LEFT JOIN discovery_source_schedule AS schedule
					ON schedule.source_origin = candidate.origin
				LEFT JOIN LATERAL (
					SELECT
						observation.observed_at,
						observation.outcome
					FROM verification_observations AS observation
					WHERE observation.origin = candidate.origin
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
				WHERE EXISTS (
					SELECT 1 FROM discovery_edges AS edge
					WHERE edge.candidate_origin = candidate.origin
						AND edge.kind = 'link'
				)
					AND NOT COALESCE(source_state.crawl_blocked, false)
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
						WHERE `+domainAvoidHostnameMatchSQL+`
					)
					AND (
						candidate.personal_site_next_attempt_at IS NULL
						OR candidate.personal_site_next_attempt_at <= $3
					)
					AND (
						candidate.personal_site_claim_expires_at IS NULL
						OR candidate.personal_site_claim_expires_at <= $3
					)
					AND (
						(
							candidate.personal_site_rescore_requested_at
								IS NOT NULL
						)
						OR (
							NOT COALESCE(
								source_state.automatically_discovered,
								false
							)
							AND schedule.source_origin IS NULL
							AND (
								effective.observed_at IS NULL
								OR (
									effective.outcome <> 'valid'
									AND candidate.last_discovered_at >
										effective.observed_at
								)
							)
							AND NOT EXISTS (
								SELECT 1
								FROM discovery_candidate_admission_state
									AS durable
								WHERE durable.candidate_origin =
									candidate.origin
							)
							AND (
								candidate.personal_site_qualification_outcome
									IS NULL
								OR candidate.personal_site_qualification_outcome
									<> 'assessed'
								OR candidate.personal_site_assessment_version
									IS DISTINCT FROM $2
							)
						)
					)
				ORDER BY
					candidate.personal_site_next_attempt_at
						ASC NULLS FIRST,
					candidate.origin ASC
				LIMIT $4
				FOR UPDATE OF candidate SKIP LOCKED
			)
			UPDATE discovery_candidates AS candidate
			SET
				personal_site_claim_owner = $5,
				personal_site_claim_generation =
					candidate.personal_site_claim_generation + 1,
				personal_site_claim_expires_at = $6
			FROM due
			WHERE candidate.origin = due.origin
			RETURNING
				candidate.origin,
				candidate.personal_site_claim_generation,
				candidate.personal_site_claim_expires_at
		`, patterns, personalsite.AssessmentVersion, now.UTC(), limit, owner, expiresAt.UTC())
		if queryErr != nil {
			return fmt.Errorf(
				"store: claim personal site qualifications: %w",
				queryErr,
			)
		}
		defer rows.Close()

		claims = make([]PersonalSiteQualifyClaim, 0, limit)
		for rows.Next() {
			var (
				raw        string
				generation int64
				expires    time.Time
			)
			if scanErr := rows.Scan(&raw, &generation, &expires); scanErr != nil {
				return fmt.Errorf(
					"store: scan personal site claim: %w",
					scanErr,
				)
			}
			parsed, parseErr := origin.Parse(raw)
			if parseErr != nil {
				return fmt.Errorf(
					"store: parse personal site claim: %w",
					parseErr,
				)
			}
			claims = append(claims, PersonalSiteQualifyClaim{
				Origin:     parsed,
				Generation: generation,
				Owner:      owner,
				ExpiresAt:  expires.UTC(),
			})
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// CompletePersonalSiteQualification persists an attempt behind a claim fence.
// Expired or stolen claims return errPersonalSiteClaimLost without writing.
//
// @param ctx - Request context.
// @param claim - Lease returned by ClaimPersonalSiteQualifications.
// @param qualification - Attempt result.
// @returns error when validation, fencing, or persistence fails.
func (s *DiscoveryStore) CompletePersonalSiteQualification(
	ctx context.Context,
	claim PersonalSiteQualifyClaim,
	qualification personalsite.Qualification,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}
	if claim.Origin.String() == "" ||
		claim.Owner == "" ||
		claim.Generation < 1 {
		return errInvalidPersonalSiteClaimOwner
	}
	if !validPersonalSiteOutcome(qualification.Outcome) {
		return fmt.Errorf(
			"store: invalid personal site outcome %q",
			qualification.Outcome,
		)
	}

	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		now, clockErr := s.clock.NowTransaction(ctx, tx)
		if clockErr != nil {
			return fmt.Errorf("store: read personal site clock: %w", clockErr)
		}

		var (
			previousClass   *string
			previousCount   int
			previousScore   *int
			previousVersion *int
		)
		if err := tx.QueryRow(ctx, `
			SELECT
				personal_site_retry_class,
				personal_site_failure_count,
				personal_site_score,
				personal_site_assessment_version
			FROM discovery_candidates
			WHERE origin = $1
				AND personal_site_claim_owner = $2
				AND personal_site_claim_generation = $3
				AND personal_site_claim_expires_at > $4
			FOR UPDATE
		`, claim.Origin.String(), claim.Owner, claim.Generation, now.UTC()).Scan(
			&previousClass,
			&previousCount,
			&previousScore,
			&previousVersion,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrPersonalSiteClaimLost
			}
			return fmt.Errorf("store: load personal site claim: %w", err)
		}

		score, version, reasons, truncated, httpStatus, nextAttempt, failureCount, retryClass, err :=
			personalSitePersistState(
				qualification,
				previousClass,
				previousCount,
				now.UTC(),
			)
		if err != nil {
			return err
		}

		tag, execErr := tx.Exec(ctx, `
			UPDATE discovery_candidates
			SET
				personal_site_score = $4,
				personal_site_assessment_version = $5,
				personal_site_assessed_at = $6,
				personal_site_qualification_outcome = $7,
				personal_site_reason_codes = $8::jsonb,
				personal_site_truncated = $9,
				personal_site_http_status = $10,
				personal_site_failure_count = $11,
				personal_site_retry_class = $12,
				personal_site_next_attempt_at = $13,
				personal_site_last_attempt_at = $6,
				personal_site_claim_owner = NULL,
				personal_site_claim_expires_at = NULL,
				personal_site_rescore_requested_at = CASE
					WHEN personal_site_rescore_requested_at IS NOT NULL
						AND personal_site_claim_expires_at IS NOT NULL
						AND personal_site_rescore_requested_at >
							(
								personal_site_claim_expires_at
								- $14::interval
							)
					THEN personal_site_rescore_requested_at
					ELSE NULL
				END
			WHERE origin = $1
				AND personal_site_claim_owner = $2
				AND personal_site_claim_generation = $3
		`,
			claim.Origin.String(),
			claim.Owner,
			claim.Generation,
			score,
			version,
			now.UTC(),
			string(qualification.Outcome),
			reasons,
			truncated,
			httpStatus,
			failureCount,
			retryClass,
			nextAttempt,
			personalSiteClaimDuration.String(),
		)
		if execErr != nil {
			return fmt.Errorf(
				"store: complete personal site qualification: %w",
				execErr,
			)
		}
		if tag.RowsAffected() == 0 {
			return ErrPersonalSiteClaimLost
		}
		return nil
	})
}

// UpsertPersonalSiteAssessment replaces the latest private assessment without
// a claim fence. Prefer CompletePersonalSiteQualification for workers.
//
// @param ctx - Request context.
// @param candidate - Canonical origin key.
// @param qualification - Latest qualification result.
// @returns error when validation or persistence fails.
func (s *DiscoveryStore) UpsertPersonalSiteAssessment(
	ctx context.Context,
	candidate origin.Origin,
	qualification personalsite.Qualification,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}
	if candidate.String() == "" {
		return errInvalidOrigin
	}
	if !validPersonalSiteOutcome(qualification.Outcome) {
		return fmt.Errorf(
			"store: invalid personal site outcome %q",
			qualification.Outcome,
		)
	}

	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		now, clockErr := s.clock.NowTransaction(ctx, tx)
		if clockErr != nil {
			return fmt.Errorf("store: read personal site clock: %w", clockErr)
		}

		var (
			previousClass *string
			previousCount int
		)
		if err := tx.QueryRow(ctx, `
			SELECT
				personal_site_retry_class,
				personal_site_failure_count
			FROM discovery_candidates
			WHERE origin = $1
			FOR UPDATE
		`, candidate.String()).Scan(&previousClass, &previousCount); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errInvalidDiscoveryCandidate
			}
			return fmt.Errorf("store: load personal site row: %w", err)
		}

		score, version, reasons, truncated, httpStatus, nextAttempt, failureCount, retryClass, err :=
			personalSitePersistState(
				qualification,
				previousClass,
				previousCount,
				now.UTC(),
			)
		if err != nil {
			return err
		}

		tag, execErr := tx.Exec(ctx, `
			UPDATE discovery_candidates
			SET
				personal_site_score = $2,
				personal_site_assessment_version = $3,
				personal_site_assessed_at = $4,
				personal_site_qualification_outcome = $5,
				personal_site_reason_codes = $6::jsonb,
				personal_site_truncated = $7,
				personal_site_http_status = $8,
				personal_site_failure_count = $9,
				personal_site_retry_class = $10,
				personal_site_next_attempt_at = $11,
				personal_site_last_attempt_at = $4,
				personal_site_claim_owner = NULL,
				personal_site_claim_expires_at = NULL
			WHERE origin = $1
		`,
			candidate.String(),
			score,
			version,
			now.UTC(),
			string(qualification.Outcome),
			reasons,
			truncated,
			httpStatus,
			failureCount,
			retryClass,
			nextAttempt,
		)
		if execErr != nil {
			return fmt.Errorf(
				"store: upsert personal site assessment: %w",
				execErr,
			)
		}
		if tag.RowsAffected() == 0 {
			return errInvalidDiscoveryCandidate
		}
		return nil
	})
}

// CandidatesNeedingPersonalSiteAssessment returns due origins that still need
// a current-version assessment without claiming them.
//
// @param ctx - Request context.
// @param limit - Maximum origins to return.
// @returns Origins in due order.
func (s *DiscoveryStore) CandidatesNeedingPersonalSiteAssessment(
	ctx context.Context,
	limit int,
) ([]origin.Origin, error) {
	if err := s.validate(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 || !s.automatic.Enabled {
		return nil, nil
	}

	exclusions, err := s.automaticExclusions(ctx)
	if err != nil {
		return nil, err
	}
	patterns := domainavoid.NonRegexPatterns(
		automaticExclusionPatterns(exclusions),
	)

	rows, err := s.pool.Query(ctx, `
		SELECT candidate.origin
		FROM discovery_candidates AS candidate
		LEFT JOIN discovery_source_state AS source_state
			ON source_state.source_origin = candidate.origin
		LEFT JOIN discovery_source_schedule AS schedule
			ON schedule.source_origin = candidate.origin
		LEFT JOIN LATERAL (
			SELECT
				observation.observed_at,
				observation.outcome
			FROM verification_observations AS observation
			WHERE observation.origin = candidate.origin
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
		WHERE EXISTS (
			SELECT 1 FROM discovery_edges AS edge
			WHERE edge.candidate_origin = candidate.origin
				AND edge.kind = 'link'
		)
			AND NOT COALESCE(source_state.crawl_blocked, false)
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
				WHERE `+domainAvoidHostnameMatchSQL+`
			)
			AND (
				candidate.personal_site_next_attempt_at IS NULL
				OR candidate.personal_site_next_attempt_at
					<= statement_timestamp()
			)
			AND (
				candidate.personal_site_claim_expires_at IS NULL
				OR candidate.personal_site_claim_expires_at
					<= statement_timestamp()
			)
			AND (
				(
					candidate.personal_site_rescore_requested_at IS NOT NULL
				)
				OR (
					NOT COALESCE(
						source_state.automatically_discovered,
						false
					)
					AND schedule.source_origin IS NULL
					AND (
						effective.observed_at IS NULL
						OR (
							effective.outcome <> 'valid'
							AND candidate.last_discovered_at >
								effective.observed_at
						)
					)
					AND NOT EXISTS (
						SELECT 1
						FROM discovery_candidate_admission_state AS durable
						WHERE durable.candidate_origin = candidate.origin
					)
					AND (
						candidate.personal_site_qualification_outcome IS NULL
						OR candidate.personal_site_qualification_outcome
							<> 'assessed'
						OR candidate.personal_site_assessment_version
							IS DISTINCT FROM $2
					)
				)
			)
		ORDER BY
			candidate.personal_site_next_attempt_at ASC NULLS FIRST,
			candidate.origin ASC
		LIMIT $3
	`, patterns, personalsite.AssessmentVersion, limit)
	if err != nil {
		return nil, fmt.Errorf(
			"store: list personal site assessment candidates: %w",
			err,
		)
	}
	defer rows.Close()

	origins := make([]origin.Origin, 0, limit)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf(
				"store: scan personal site assessment candidate: %w",
				err,
			)
		}
		parsed, parseErr := origin.Parse(raw)
		if parseErr != nil {
			return nil, fmt.Errorf(
				"store: parse personal site assessment candidate: %w",
				parseErr,
			)
		}
		origins = append(origins, parsed)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"store: iterate personal site assessment candidates: %w",
			err,
		)
	}
	return origins, nil
}

func personalSitePersistState(
	qualification personalsite.Qualification,
	previousClass *string,
	previousCount int,
	now time.Time,
) (
	score *int,
	version *int,
	reasons []byte,
	truncated bool,
	httpStatus *int,
	nextAttempt *time.Time,
	failureCount int,
	retryClass *string,
	err error,
) {
	truncated = qualification.Truncated
	if qualification.HTTPStatus != 0 {
		status := qualification.HTTPStatus
		httpStatus = &status
	}

	if qualification.Outcome == personalsite.QualificationAssessed {
		clamped := qualification.Assessment.Score
		if clamped < 0 || clamped > 100 {
			return nil, nil, nil, false, nil, nil, 0, nil, fmt.Errorf(
				"store: personal site score %d out of range",
				clamped,
			)
		}
		score = &clamped
		assessmentVersion := qualification.Assessment.Version
		if assessmentVersion == 0 {
			assessmentVersion = personalsite.AssessmentVersion
		}
		version = &assessmentVersion
		codes := qualification.Assessment.Reasons
		if codes == nil {
			codes = []personalsite.ReasonCode{}
		}
		reasons, _ = json.Marshal(codes)
		failureCount = 0
		return score, version, reasons, truncated, httpStatus, nil, failureCount, nil, nil
	}

	// Failed requalification clears any prior score so stale versions cannot admit.
	reasons = nil
	score = nil
	version = nil

	nextClass := personalsite.RetryClassFor(qualification)
	priorClass := personalsite.RetryClassNone
	if previousClass != nil {
		priorClass = personalsite.RetryClass(*previousClass)
	}
	failureCount = personalsite.NextFailureStep(
		priorClass,
		previousCount,
		nextClass,
	)
	delay := personalsite.RetryDelay(
		nextClass,
		failureCount,
		qualification.RetryAfter,
	)
	due := now.Add(delay)
	nextAttempt = &due
	classValue := string(nextClass)
	retryClass = &classValue
	return score, version, reasons, truncated, httpStatus, nextAttempt, failureCount, retryClass, nil
}

func (s *DiscoveryStore) backfillPersonalSiteAssessmentsForTests(
	ctx context.Context,
) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE discovery_candidates
		SET
			personal_site_score = 50,
			personal_site_assessment_version = $1,
			personal_site_assessed_at = statement_timestamp(),
			personal_site_qualification_outcome = 'assessed',
			personal_site_reason_codes = '[]'::jsonb,
			personal_site_truncated = false,
			personal_site_http_status = NULL,
			personal_site_failure_count = 0,
			personal_site_retry_class = NULL,
			personal_site_next_attempt_at = NULL,
			personal_site_last_attempt_at = statement_timestamp(),
			personal_site_claim_owner = NULL,
			personal_site_claim_expires_at = NULL
		WHERE personal_site_qualification_outcome IS DISTINCT FROM 'assessed'
			OR personal_site_assessment_version IS DISTINCT FROM $1
			OR personal_site_score IS NULL
	`, personalsite.AssessmentVersion)
	if err != nil {
		return fmt.Errorf(
			"store: backfill personal site assessments for tests: %w",
			err,
		)
	}
	return nil
}

func validPersonalSiteOutcome(
	outcome personalsite.QualificationOutcome,
) bool {
	switch outcome {
	case personalsite.QualificationAssessed,
		personalsite.QualificationRobotsDenied,
		personalsite.QualificationTimeout,
		personalsite.QualificationUnsupportedContent,
		personalsite.QualificationTooLarge,
		personalsite.QualificationDNSFailure,
		personalsite.QualificationHTTPError,
		personalsite.QualificationNetworkRejected,
		personalsite.QualificationParserFailure,
		personalsite.QualificationUnavailable:
		return true
	default:
		return false
	}
}

func automaticAdmissionAllocationQuery(
	runID int64,
	patterns []string,
	remainingBatch int,
	now time.Time,
) (string, []any) {
	sql := `
		WITH eligible AS (
			SELECT
				candidate.origin,
				COALESCE(
					MIN(run_candidate.run_id),
					$1::bigint
				) AS discovered_run_id,
				MAX(
					COALESCE(
						candidate.personal_site_override_score,
						candidate.personal_site_score
					)
				) AS effective_personal_site_score
			FROM discovery_candidates AS candidate
			LEFT JOIN crawl_run_discovery_candidates AS run_candidate
				ON run_candidate.candidate_origin = candidate.origin
			LEFT JOIN discovery_source_state AS source_state
				ON source_state.source_origin = candidate.origin
			LEFT JOIN discovery_source_schedule AS schedule
				ON schedule.source_origin = candidate.origin
			LEFT JOIN LATERAL (
				SELECT
					observation.observed_at,
					observation.outcome
				FROM verification_observations AS observation
				WHERE observation.origin = candidate.origin
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
				AND schedule.source_origin IS NULL
				AND (
					effective.observed_at IS NULL
					OR (
						effective.outcome <> 'valid'
						AND candidate.last_discovered_at >
							effective.observed_at
					)
				)
				AND NOT EXISTS (
					SELECT 1
					FROM discovery_candidate_admission_state AS durable
					WHERE durable.candidate_origin = candidate.origin
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
					WHERE ` + domainAvoidHostnameMatchSQL + `
				)
				AND candidate.personal_site_qualification_outcome = 'assessed'
				AND candidate.personal_site_assessment_version = $5
				AND candidate.personal_site_score IS NOT NULL
			GROUP BY candidate.origin
		),
		candidate_pool AS (
			SELECT origin, discovered_run_id
			FROM eligible
			ORDER BY
				effective_personal_site_score DESC,
				origin ASC
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
	`
	return sql, []any{
		runID,
		patterns,
		remainingBatch,
		now,
		personalsite.AssessmentVersion,
	}
}

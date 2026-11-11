package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/joshternet/joshbot/internal/domainavoid"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/personalsite"
)

// UpsertPersonalSiteAssessment replaces the latest private assessment for
// candidate. Unavailable outcomes clear score and reason codes.
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

	var (
		score   *int
		version *int
		reasons []byte
		outcome = string(qualification.Outcome)
	)

	if qualification.Outcome == personalsite.QualificationAssessed {
		clamped := qualification.Assessment.Score
		if clamped < 0 || clamped > 100 {
			return fmt.Errorf(
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
		// ReasonCode is a string alias; encoding cannot fail.
		reasons, _ = json.Marshal(codes)
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE discovery_candidates
		SET
			personal_site_score = $2,
			personal_site_assessment_version = $3,
			personal_site_assessed_at = statement_timestamp(),
			personal_site_qualification_outcome = $4,
			personal_site_reason_codes = $5::jsonb
		WHERE origin = $1
	`, candidate.String(), score, version, outcome, reasons)
	if err != nil {
		return fmt.Errorf(
			"store: upsert personal site assessment: %w",
			err,
		)
	}
	if tag.RowsAffected() == 0 {
		return errInvalidDiscoveryCandidate
	}
	return nil
}

// CandidatesNeedingPersonalSiteAssessment returns eligible automatic-
// admission candidates that still need scoring for the current version.
//
// @param ctx - Request context.
// @param limit - Maximum origins to return.
// @returns Origins in canonical order.
func (s *DiscoveryStore) CandidatesNeedingPersonalSiteAssessment(
	ctx context.Context,
	limit int,
) ([]origin.Origin, error) {
	if err := s.validate(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}
	if !s.automatic.Enabled {
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
			AND (
				candidate.next_attempt_at IS NULL
				OR candidate.next_attempt_at <= statement_timestamp()
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
				candidate.personal_site_qualification_outcome IS NULL
				OR (
					candidate.personal_site_qualification_outcome = 'assessed'
					AND candidate.personal_site_assessment_version
						IS DISTINCT FROM $2
				)
			)
		ORDER BY candidate.origin
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

// explorationSlotCount returns the anti-starvation share of a batch.
func explorationSlotCount(remainingBatch int) int {
	if remainingBatch < 5 {
		return 0
	}
	exploration := remainingBatch / 10
	if exploration < 1 {
		return 1
	}
	return exploration
}

func automaticAdmissionAllocationQuery(
	mode personalsite.ScoringMode,
	runID int64,
	patterns []string,
	remainingBatch int,
	now time.Time,
) (string, []any) {
	eligibleCTE := `
		eligible AS (
			SELECT
				candidate.origin,
				COALESCE(
					MIN(run_candidate.run_id),
					$1::bigint
				) AS discovered_run_id,
				MIN(candidate.first_discovered_at) AS first_discovered_at,
				MAX(candidate.personal_site_score) AS personal_site_score
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
			GROUP BY candidate.origin
		)`

	if !mode.OrdersAdmission() {
		sql := `
			WITH ` + eligibleCTE + `,
			candidate_pool AS (
				SELECT origin, discovered_run_id
				FROM eligible
				ORDER BY origin
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
		return sql, []any{runID, patterns, remainingBatch, now}
	}

	exploration := explorationSlotCount(remainingBatch)
	scoredLimit := remainingBatch - exploration
	sql := `
		WITH ` + eligibleCTE + `,
		exploration AS (
			SELECT origin, discovered_run_id
			FROM eligible
			ORDER BY first_discovered_at ASC, origin ASC
			LIMIT $3
		),
		scored AS (
			SELECT origin, discovered_run_id
			FROM eligible
			WHERE origin NOT IN (SELECT origin FROM exploration)
			ORDER BY personal_site_score DESC NULLS LAST, origin ASC
			LIMIT $5
		),
		candidate_pool AS (
			SELECT origin, discovered_run_id FROM exploration
			UNION ALL
			SELECT origin, discovered_run_id FROM scored
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
		exploration,
		now,
		scoredLimit,
	}
}

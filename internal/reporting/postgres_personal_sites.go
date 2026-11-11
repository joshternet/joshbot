package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/personalsite"
)

const personalSiteSelectSQL = `
	SELECT
		candidate.origin,
		candidate.personal_site_qualification_outcome,
		candidate.personal_site_score,
		COALESCE(
			candidate.personal_site_override_score,
			candidate.personal_site_score
		) AS effective_score,
		candidate.personal_site_assessment_version,
		candidate.personal_site_assessed_at,
		candidate.personal_site_truncated,
		candidate.personal_site_reason_codes,
		candidate.personal_site_next_attempt_at,
		candidate.personal_site_last_attempt_at,
		candidate.personal_site_failure_count,
		candidate.personal_site_http_status,
		candidate.personal_site_retry_class,
		candidate.personal_site_override_score,
		candidate.personal_site_override_reason,
		candidate.personal_site_override_actor,
		candidate.personal_site_override_updated_at,
		candidate.personal_site_override_revision,
		candidate.personal_site_rescore_requested_at IS NOT NULL
			AS rescore_pending,
		COALESCE(durable.outcome, '') AS admission_state,
		CASE
			WHEN COALESCE(source_state.crawl_blocked, false)
				THEN 'blocked'
			WHEN source_state.lease_expires_at IS NOT NULL
				AND source_state.lease_expires_at > statement_timestamp()
				THEN 'leased'
			WHEN schedule.source_origin IS NOT NULL
				AND (
					source_state.next_attempt_at IS NULL
					OR source_state.next_attempt_at
						<= statement_timestamp()
				)
				THEN 'due'
			WHEN schedule.source_origin IS NOT NULL
				THEN 'scheduled'
			ELSE 'unscheduled'
		END AS crawl_state,
		(
			candidate.personal_site_qualification_outcome = 'assessed'
			AND candidate.personal_site_assessment_version = $1
			AND candidate.personal_site_score IS NOT NULL
			AND NOT COALESCE(source_state.automatically_discovered, false)
			AND NOT COALESCE(source_state.crawl_blocked, false)
			AND schedule.source_origin IS NULL
			AND durable.candidate_origin IS NULL
		) AS admission_eligible
	FROM discovery_candidates AS candidate
	LEFT JOIN discovery_source_state AS source_state
		ON source_state.source_origin = candidate.origin
	LEFT JOIN discovery_source_schedule AS schedule
		ON schedule.source_origin = candidate.origin
	LEFT JOIN discovery_candidate_admission_state AS durable
		ON durable.candidate_origin = candidate.origin
`

// PersonalSitesPage returns a keyset page of personal-site candidates.
func (reader *PostgresReader) PersonalSitesPage(
	ctx context.Context,
	query personalSiteQuery,
) (personalSitePage, error) {
	fetchLimit, err := pageLimit(query.Limit)
	if err != nil {
		return personalSitePage{}, err
	}
	limit := fetchLimit - 1
	args := []any{personalsite.AssessmentVersion}
	filters := `
		WHERE EXISTS (
			SELECT 1 FROM discovery_edges AS edge
			WHERE edge.candidate_origin = candidate.origin
				AND edge.kind = 'link'
		)
	`
	argIndex := 2
	if query.Origin != "" {
		filters += fmt.Sprintf(" AND candidate.origin = $%d", argIndex)
		args = append(args, query.Origin)
		argIndex++
	}
	if query.Outcome != "" {
		filters += fmt.Sprintf(
			" AND candidate.personal_site_qualification_outcome = $%d",
			argIndex,
		)
		args = append(args, query.Outcome)
		argIndex++
	}
	if query.Awaiting != nil {
		if *query.Awaiting {
			filters += `
				AND (
					candidate.personal_site_qualification_outcome IS NULL
					OR candidate.personal_site_qualification_outcome
						<> 'assessed'
					OR candidate.personal_site_assessment_version
						IS DISTINCT FROM $1
					OR candidate.personal_site_rescore_requested_at
						IS NOT NULL
				)
			`
		} else {
			filters += `
				AND candidate.personal_site_qualification_outcome = 'assessed'
				AND candidate.personal_site_assessment_version = $1
				AND candidate.personal_site_rescore_requested_at IS NULL
			`
		}
	}
	if query.Cursor != nil {
		if query.Cursor.HasScore {
			filters += fmt.Sprintf(`
				AND (
					COALESCE(
						candidate.personal_site_override_score,
						candidate.personal_site_score
					) < $%d
					OR (
						COALESCE(
							candidate.personal_site_override_score,
							candidate.personal_site_score
						) = $%d
						AND candidate.origin > $%d
					)
					OR COALESCE(
						candidate.personal_site_override_score,
						candidate.personal_site_score
					) IS NULL
				)
			`, argIndex, argIndex, argIndex+1)
			args = append(args, *query.Cursor.EffectiveScore, query.Cursor.Origin)
			argIndex += 2
		} else {
			filters += fmt.Sprintf(`
				AND COALESCE(
					candidate.personal_site_override_score,
					candidate.personal_site_score
				) IS NULL
				AND candidate.origin > $%d
			`, argIndex)
			args = append(args, query.Cursor.Origin)
			argIndex++
		}
	}

	sql := personalSiteSelectSQL + filters + `
		ORDER BY
			COALESCE(
				candidate.personal_site_override_score,
				candidate.personal_site_score
			) DESC NULLS LAST,
			candidate.origin ASC
		LIMIT $` + fmt.Sprint(argIndex)
	args = append(args, fetchLimit)

	rows, err := reader.pool.Query(ctx, sql, args...)
	if err != nil {
		return personalSitePage{}, fmt.Errorf(
			"reporting: list personal sites: %w",
			err,
		)
	}
	items, err := pgx.CollectRows(
		rows,
		func(row pgx.CollectableRow) (PersonalSite, error) {
			return scanPersonalSiteRow(row)
		},
	)
	if err != nil {
		return personalSitePage{}, fmt.Errorf(
			"reporting: collect personal sites: %w",
			err,
		)
	}
	page := personalSitePage{Items: items}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextCursor = encodePersonalSiteCursor(
			page.Items[len(page.Items)-1],
		)
	}
	return page, nil
}

// PersonalSite returns one candidate assessment detail.
func (reader *PostgresReader) PersonalSite(
	ctx context.Context,
	rawOrigin string,
) (PersonalSite, bool, error) {
	parsed, err := origin.Parse(rawOrigin)
	if err != nil {
		return PersonalSite{}, false, errInvalidOriginFilter
	}
	row := reader.pool.QueryRow(
		ctx,
		personalSiteSelectSQL+`
			WHERE candidate.origin = $2
		`,
		personalsite.AssessmentVersion,
		parsed.String(),
	)
	site, err := scanPersonalSiteRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonalSite{}, false, nil
	}
	if err != nil {
		return PersonalSite{}, false, fmt.Errorf(
			"reporting: read personal site: %w",
			err,
		)
	}
	return site, true, nil
}

func scanPersonalSiteRow(row rowScanner) (PersonalSite, error) {
	var (
		site       PersonalSite
		reasonJSON []byte
		rescore    bool
	)
	err := row.Scan(
		&site.Origin,
		&site.QualificationOutcome,
		&site.CalculatedScore,
		&site.EffectiveScore,
		&site.AssessmentVersion,
		&site.AssessedAt,
		&site.Truncated,
		&reasonJSON,
		&site.NextAttemptAt,
		&site.LastAttemptAt,
		&site.FailureCount,
		&site.HTTPStatus,
		&site.RetryClass,
		&site.OverrideScore,
		&site.OverrideReason,
		&site.OverrideActor,
		&site.OverrideUpdatedAt,
		&site.OverrideRevision,
		&rescore,
		&site.AdmissionState,
		&site.CrawlState,
		&site.AdmissionEligible,
	)
	if err != nil {
		return PersonalSite{}, err
	}
	site.RescorePending = rescore
	if len(reasonJSON) > 0 {
		if err := json.Unmarshal(reasonJSON, &site.ReasonCodes); err != nil {
			return PersonalSite{}, fmt.Errorf(
				"reporting: decode personal site reasons: %w",
				err,
			)
		}
	}
	enrichPersonalSite(&site)
	return site, nil
}

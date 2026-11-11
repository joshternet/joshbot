package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/control"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/personalsite"
)

var (
	// ErrPersonalSiteOverrideConflict means expected_revision did not match.
	ErrPersonalSiteOverrideConflict = control.ErrConflict
	// ErrPersonalSiteCandidateUnknown means the origin is not a candidate.
	ErrPersonalSiteCandidateUnknown = control.ErrNotFound
	// ErrPersonalSiteCandidateBlocked means the origin is crawl-blocked.
	ErrPersonalSiteCandidateBlocked = control.ErrForbidden
	// ErrOriginCrawlUnknown means no discovery_source_state row exists.
	ErrOriginCrawlUnknown = control.ErrNotFound
	// ErrOriginCrawlBlocked means the origin is blocked from crawling.
	ErrOriginCrawlBlocked = control.ErrForbidden
)

const (
	controlMutationOK             = "ok"
	controlMutationAlreadyPending = "already_pending"
	controlMutationActive         = "active"
)

// SetPersonalSiteOverride sets or replaces a manual score override.
//
// @param ctx - Request context.
// @param rawOrigin - Canonical origin string.
// @param score - Override score 0-100.
// @param expectedRevision - Current revision; 0 when creating the first override.
// @param audit - Success audit payload.
// @returns Conflict when expectedRevision does not match.
func (s *ControlStore) SetPersonalSiteOverride(
	ctx context.Context,
	rawOrigin string,
	score int,
	expectedRevision int64,
	audit control.Audit,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}
	parsed, err := origin.Parse(rawOrigin)
	if err != nil {
		return errInvalidOrigin
	}
	if score < 0 || score > 100 {
		return fmt.Errorf("store: invalid personal site override score")
	}
	if audit.Reason == "" {
		return fmt.Errorf("store: personal site override reason required")
	}
	if !controlAuditMatches(
		audit,
		"personal-site.override.set",
		parsed.String(),
	) {
		return errors.New("store: override audit is inconsistent")
	}

	return s.mutate(ctx, audit, func(tx pgx.Tx) error {
		var (
			revision        int64
			blocked         bool
			outcome         *string
			version         *int
			calculatedScore *int
		)
		err := tx.QueryRow(ctx, `
			SELECT
				candidate.personal_site_override_revision,
				COALESCE(source_state.crawl_blocked, false),
				candidate.personal_site_qualification_outcome,
				candidate.personal_site_assessment_version,
				candidate.personal_site_score
			FROM discovery_candidates AS candidate
			LEFT JOIN discovery_source_state AS source_state
				ON source_state.source_origin = candidate.origin
			WHERE candidate.origin = $1
			FOR UPDATE OF candidate
		`, parsed.String()).Scan(
			&revision,
			&blocked,
			&outcome,
			&version,
			&calculatedScore,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPersonalSiteCandidateUnknown
		}
		if err != nil {
			return fmt.Errorf("store: load personal site override: %w", err)
		}
		if blocked {
			return ErrPersonalSiteCandidateBlocked
		}
		if outcome == nil ||
			*outcome != string(personalsite.QualificationAssessed) ||
			version == nil ||
			*version != personalsite.AssessmentVersion ||
			calculatedScore == nil {
			return ErrPersonalSiteCandidateBlocked
		}
		if revision != expectedRevision {
			return ErrPersonalSiteOverrideConflict
		}

		tag, execErr := tx.Exec(ctx, `
			UPDATE discovery_candidates
			SET
				personal_site_override_score = $2,
				personal_site_override_reason = $3,
				personal_site_override_actor = $4,
				personal_site_override_updated_at = statement_timestamp(),
				personal_site_override_revision =
					personal_site_override_revision + 1
			WHERE origin = $1
				AND personal_site_override_revision = $5
		`, parsed.String(), score, audit.Reason, audit.Actor, expectedRevision)
		if execErr != nil {
			return fmt.Errorf("store: set personal site override: %w", execErr)
		}
		if tag.RowsAffected() != 1 {
			return ErrPersonalSiteOverrideConflict
		}
		return nil
	})
}

// ClearPersonalSiteOverride removes a manual score override.
//
// @param ctx - Request context.
// @param rawOrigin - Canonical origin string.
// @param expectedRevision - Current override revision.
// @param audit - Success audit payload.
// @returns Conflict when revision mismatches or no override exists.
func (s *ControlStore) ClearPersonalSiteOverride(
	ctx context.Context,
	rawOrigin string,
	expectedRevision int64,
	audit control.Audit,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}
	parsed, err := origin.Parse(rawOrigin)
	if err != nil {
		return errInvalidOrigin
	}
	if expectedRevision < 1 {
		return ErrPersonalSiteOverrideConflict
	}
	if !controlAuditMatches(
		audit,
		"personal-site.override.clear",
		parsed.String(),
	) {
		return errors.New("store: clear override audit is inconsistent")
	}

	return s.mutate(ctx, audit, func(tx pgx.Tx) error {
		tag, execErr := tx.Exec(ctx, `
			UPDATE discovery_candidates
			SET
				personal_site_override_score = NULL,
				personal_site_override_reason = NULL,
				personal_site_override_actor = NULL,
				personal_site_override_updated_at = NULL
			WHERE origin = $1
				AND personal_site_override_revision = $2
				AND personal_site_override_score IS NOT NULL
		`, parsed.String(), expectedRevision)
		if execErr != nil {
			return fmt.Errorf("store: clear personal site override: %w", execErr)
		}
		if tag.RowsAffected() != 1 {
			var exists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS(
					SELECT 1 FROM discovery_candidates WHERE origin = $1
				)
			`, parsed.String()).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrPersonalSiteCandidateUnknown
			}
			return ErrPersonalSiteOverrideConflict
		}
		return nil
	})
}

// RequestPersonalSiteRescore marks a candidate due for operator reassessment.
//
// @param ctx - Request context.
// @param rawOrigin - Canonical origin string.
// @param audit - Success audit payload.
// @returns Status "ok" or "already_pending".
func (s *ControlStore) RequestPersonalSiteRescore(
	ctx context.Context,
	rawOrigin string,
	audit control.Audit,
) (string, error) {
	if err := s.validate(ctx); err != nil {
		return "", err
	}
	parsed, err := origin.Parse(rawOrigin)
	if err != nil {
		return "", errInvalidOrigin
	}
	if !controlAuditMatches(
		audit,
		"personal-site.rescore.request",
		parsed.String(),
	) {
		return "", errors.New("store: rescore audit is inconsistent")
	}

	status := controlMutationOK
	err = s.mutate(ctx, audit, func(tx pgx.Tx) error {
		if err := rejectDomainAvoidedOrigin(ctx, tx, parsed, s.automatic); err != nil {
			return err
		}

		var (
			blocked      bool
			pending      *time.Time
			claimExpires *time.Time
			now          time.Time
			exists       bool
		)
		err := tx.QueryRow(ctx, `
			SELECT
				true,
				COALESCE(source_state.crawl_blocked, false),
				candidate.personal_site_rescore_requested_at,
				candidate.personal_site_claim_expires_at,
				statement_timestamp()
			FROM discovery_candidates AS candidate
			LEFT JOIN discovery_source_state AS source_state
				ON source_state.source_origin = candidate.origin
			WHERE candidate.origin = $1
			FOR UPDATE OF candidate
		`, parsed.String()).Scan(
			&exists,
			&blocked,
			&pending,
			&claimExpires,
			&now,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPersonalSiteCandidateUnknown
		}
		if err != nil {
			return fmt.Errorf("store: load personal site rescore: %w", err)
		}
		if blocked {
			return ErrPersonalSiteCandidateBlocked
		}
		if claimExpires != nil && claimExpires.After(now) {
			status = controlMutationActive
			return nil
		}
		if pending != nil {
			status = controlMutationAlreadyPending
			return nil
		}

		tag, execErr := tx.Exec(ctx, `
			UPDATE discovery_candidates
			SET
				personal_site_rescore_requested_at = statement_timestamp(),
				personal_site_next_attempt_at = NULL
			WHERE origin = $1
				AND personal_site_rescore_requested_at IS NULL
				AND (
					personal_site_claim_expires_at IS NULL
					OR personal_site_claim_expires_at <= statement_timestamp()
				)
		`, parsed.String())
		if execErr != nil {
			return fmt.Errorf("store: request personal site rescore: %w", execErr)
		}
		if tag.RowsAffected() != 1 {
			status = controlMutationAlreadyPending
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return status, nil
}

// RequestOriginCrawl ensures schedule membership and makes a known source due.
//
// @param ctx - Request context.
// @param rawOrigin - Canonical origin string.
// @param audit - Success audit payload.
// @returns Status "ok", "already_pending", or "active".
func (s *ControlStore) RequestOriginCrawl(
	ctx context.Context,
	rawOrigin string,
	audit control.Audit,
) (string, error) {
	if err := s.validate(ctx); err != nil {
		return "", err
	}
	parsed, err := origin.Parse(rawOrigin)
	if err != nil {
		return "", errInvalidOrigin
	}
	if !controlAuditMatches(
		audit,
		"origin.crawl.request",
		parsed.String(),
	) {
		return "", errors.New("store: crawl request audit is inconsistent")
	}

	status := controlMutationOK
	err = s.mutate(ctx, audit, func(tx pgx.Tx) error {
		if err := rejectDomainAvoidedOrigin(ctx, tx, parsed, s.automatic); err != nil {
			return err
		}

		var (
			blocked      bool
			leaseExpires *time.Time
			now          time.Time
		)
		if err := tx.QueryRow(ctx, `
			SELECT statement_timestamp()
		`).Scan(&now); err != nil {
			return fmt.Errorf("store: read crawl request clock: %w", err)
		}

		err := tx.QueryRow(ctx, `
			SELECT
				crawl_blocked,
				lease_expires_at
			FROM discovery_source_state
			WHERE source_origin = $1
			FOR UPDATE
		`, parsed.String()).Scan(&blocked, &leaseExpires)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOriginCrawlUnknown
		}
		if err != nil {
			return fmt.Errorf("store: load crawl source: %w", err)
		}
		if blocked {
			return ErrOriginCrawlBlocked
		}
		if leaseExpires != nil && leaseExpires.After(now) {
			status = controlMutationActive
			return nil
		}

		if err := lockDiscoveryScheduleOrigin(ctx, tx, parsed.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO discovery_source_schedule (source_origin)
			VALUES ($1)
			ON CONFLICT (source_origin) DO NOTHING
		`, parsed.String()); err != nil {
			return fmt.Errorf("store: ensure crawl schedule: %w", err)
		}

		tag, execErr := tx.Exec(ctx, `
			UPDATE discovery_source_state
			SET
				next_attempt_at = $2,
				lease_owner = NULL,
				lease_expires_at = NULL
			WHERE source_origin = $1
				AND (
					lease_expires_at IS NULL
					OR lease_expires_at <= $2
				)
		`, parsed.String(), now.UTC())
		if execErr != nil {
			return fmt.Errorf("store: nudge crawl source: %w", execErr)
		}
		if tag.RowsAffected() != 1 {
			status = controlMutationAlreadyPending
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return status, nil
}

func rejectDomainAvoidedOrigin(
	ctx context.Context,
	tx pgx.Tx,
	candidate origin.Origin,
	automatic AutomaticCrawlConfig,
) error {
	exclusions, err := automaticExclusionsTransaction(ctx, tx, automatic)
	if err != nil {
		return err
	}
	if exclusions.excludes(candidate.Hostname()) {
		return ErrPersonalSiteCandidateBlocked
	}
	return nil
}

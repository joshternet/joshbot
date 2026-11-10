package store

import (
	"fmt"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/retry"
)

// probeFailureSuppressionInterval is how long discovery withholds re-admission
// after a non-transient probe removal. Aligns with retry.MaxDelay.
const probeFailureSuppressionInterval = 24 * time.Hour

// transientFailureCategoriesSQL lists categories matching
// retry.Category.Transient() for use in admission SQL.
const transientFailureCategoriesSQL = `
	'dns', 'transport', 'timeout', 'robots_temporary',
	'http_408', 'http_429', 'http_5xx',
	'declaration_unavailable', 'processor', 'store'
`

// probeAdmissionNotSuppressedSQL returns AND predicates that block probe
// enqueue while a candidate cooldown is active or a recent non-transient
// unavailable observation still applies for the origin.
//
// @param originExpr - SQL expression for the origin text (for example
//
//	`candidate.origin`)
//
// @param nowExpr - SQL expression for the comparison clock (for example
//
//	`statement_timestamp()` or `$2`)
//
// @returns SQL fragment beginning with AND
func probeAdmissionNotSuppressedSQL(
	originExpr string,
	nowExpr string,
) string {
	return fmt.Sprintf(`
		AND NOT EXISTS (
			SELECT 1
			FROM discovery_candidates AS suppress_candidate
			WHERE suppress_candidate.origin = %s
				AND suppress_candidate.next_attempt_at IS NOT NULL
				AND suppress_candidate.next_attempt_at > %s
		)
		AND NOT EXISTS (
			SELECT 1
			FROM verification_observations AS suppress_observation
			WHERE suppress_observation.origin = %s
				AND suppress_observation.outcome = 'unavailable'
				AND suppress_observation.failure_category IS NOT NULL
				AND suppress_observation.failure_category NOT IN (%s)
				AND suppress_observation.observed_at >= COALESCE(
					(
						SELECT last_discovered_at
						FROM discovery_candidates AS discovered
						WHERE discovered.origin = %s
					),
					'-infinity'::timestamptz
				)
				AND suppress_observation.observed_at >
					%s - interval '24 hours'
		)`,
		originExpr,
		nowExpr,
		originExpr,
		transientFailureCategoriesSQL,
		originExpr,
		nowExpr,
	)
}

// resolvedFailureCategory returns the category persisted for an observation,
// applying the unavailable default used by CompleteVerification.
//
// @param result - completed verification result
// @returns typed category, possibly declaration_unavailable for bare unavailable
func resolvedFailureCategory(
	result declaration.Result,
) retry.Category {
	failureCategory := result.FailureCategory
	if result.Outcome == declaration.OutcomeUnavailable &&
		failureCategory == retry.CategoryNone {
		return retry.CategoryDeclarationUnavailable
	}

	return failureCategory
}

// observationFailureCategoryValue returns a nullable DB value for the
// observation failure_category column.
//
// @param category - resolved failure category
// @returns nil for CategoryNone, otherwise the category string
func observationFailureCategoryValue(
	category retry.Category,
) any {
	if category == retry.CategoryNone {
		return nil
	}

	return string(category)
}

package store

import (
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/retry"
)

func TestResolvedFailureCategoryDefaultsUnavailable(
	t *testing.T,
) {
	got := resolvedFailureCategory(declaration.Result{
		Outcome: declaration.OutcomeUnavailable,
	})
	if got != retry.CategoryDeclarationUnavailable {
		t.Fatalf(
			"resolvedFailureCategory() = %q, want %q",
			got,
			retry.CategoryDeclarationUnavailable,
		)
	}

	got = resolvedFailureCategory(declaration.Result{
		Outcome: declaration.OutcomeUnavailable,
		FailureCategory: retry.
			CategoryUnsupportedOrigin,
	})
	if got != retry.CategoryUnsupportedOrigin {
		t.Fatalf(
			"resolvedFailureCategory() = %q, want %q",
			got,
			retry.CategoryUnsupportedOrigin,
		)
	}
}

func TestObservationFailureCategoryValue(
	t *testing.T,
) {
	if got := observationFailureCategoryValue(
		retry.CategoryNone,
	); got != nil {
		t.Fatalf(
			"observationFailureCategoryValue(none) = %#v, want nil",
			got,
		)
	}

	got := observationFailureCategoryValue(
		retry.CategoryUnsupportedOrigin,
	)
	value, ok := got.(string)
	if !ok ||
		value != string(retry.CategoryUnsupportedOrigin) {
		t.Fatalf(
			"observationFailureCategoryValue() = %#v, want %q",
			got,
			retry.CategoryUnsupportedOrigin,
		)
	}
}

func TestProbeAdmissionNotSuppressedSQL(
	t *testing.T,
) {
	sql := probeAdmissionNotSuppressedSQL(
		"candidate.origin",
		"$2::timestamptz",
	)
	for _, want := range []string{
		"discovery_candidates AS suppress_candidate",
		"verification_observations AS suppress_observation",
		"declaration_unavailable",
		"failure_category NOT IN",
		"$2::timestamptz",
		"candidate.origin",
		"outcome = 'unavailable'",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf(
				"probeAdmissionNotSuppressedSQL() missing %q in %s",
				want,
				sql,
			)
		}
	}
}

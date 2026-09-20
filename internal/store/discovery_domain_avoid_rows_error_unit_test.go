package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDomainAvoidRulesValidationWithoutDatabase(
	t *testing.T,
) {
	var store *DiscoveryStore

	rules, err := store.DomainAvoidRules(
		context.Background(),
	)

	if rules != nil {
		t.Fatalf(
			"DomainAvoidRules() = %#v, want nil",
			rules,
		)
	}

	if !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Fatalf(
			"DomainAvoidRules() error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}
}

func TestDomainAvoidRulesRowsErrorWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test domain rules rows failure",
	)

	database := &discoveryUnitDatabase{
		queryResults: []discoveryUnitQueryResult{
			{
				rows: &discoveryUnitRows{
					err: testErr,
				},
			},
		},
	}

	store := discoverySimpleDatabaseStore(
		database,
	)

	rules, err := store.DomainAvoidRules(
		context.Background(),
	)

	if rules != nil {
		t.Fatalf(
			"DomainAvoidRules() = %#v, want nil",
			rules,
		)
	}

	if !errors.Is(err, testErr) ||
		!strings.Contains(
			err.Error(),
			"store: collect crawl domain avoid rules",
		) {
		t.Fatalf(
			"DomainAvoidRules() error = %v",
			err,
		)
	}
}

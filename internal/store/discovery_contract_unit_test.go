package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/testutil"
)

func TestDiscoveryStoreConstructorsWithoutDatabase(
	t *testing.T,
) {
	pool := new(pgxpool.Pool)
	clock := databaseQueueClock{}

	if _, err := NewDiscoveryStore(nil); !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"NewDiscoveryStore(nil) error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}

	store, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v",
			err,
		)
	}

	if store == nil {
		t.Fatal(
			"NewDiscoveryStore() = nil",
		)
	}

	if _, err := NewDiscoveryStoreWithAutomaticCrawling(
		nil,
		AutomaticCrawlConfig{},
	); !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"NewDiscoveryStoreWithAutomaticCrawling(nil) error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}

	automaticConfig := AutomaticCrawlConfig{
		Enabled:                      true,
		MaxPendingProbes:             17,
		MaxAutomaticPromotionsPerRun: 9,
		ExcludedHostSuffixes:         "example.com,blogspot.*",
	}

	store, err = NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		automaticConfig,
	)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStoreWithAutomaticCrawling() error = %v",
			err,
		)
	}

	if store == nil {
		t.Fatal(
			"NewDiscoveryStoreWithAutomaticCrawling() = nil",
		)
	}

	if _, err := newDiscoveryStore(
		nil,
		clock,
	); !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"newDiscoveryStore(nil) error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}

	if _, err := newDiscoveryStore(
		pool,
		nil,
	); !errors.Is(
		err,
		errDiscoveryClockUnavailable,
	) {
		t.Fatalf(
			"newDiscoveryStore(nil clock) error = %v, want %v",
			err,
			errDiscoveryClockUnavailable,
		)
	}

	store, err = newDiscoveryStore(
		pool,
		clock,
	)
	if err != nil {
		t.Fatalf(
			"newDiscoveryStore() error = %v",
			err,
		)
	}

	if store == nil {
		t.Fatal(
			"newDiscoveryStore() = nil",
		)
	}

	if _, err := newDiscoveryStoreWithConfig(
		nil,
		clock,
		AutomaticCrawlConfig{},
	); !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"newDiscoveryStoreWithConfig(nil pool) error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}

	if _, err := newDiscoveryStoreWithConfig(
		pool,
		nil,
		AutomaticCrawlConfig{},
	); !errors.Is(
		err,
		errDiscoveryClockUnavailable,
	) {
		t.Fatalf(
			"newDiscoveryStoreWithConfig(nil clock) error = %v, want %v",
			err,
			errDiscoveryClockUnavailable,
		)
	}

	if _, err := newDiscoveryStoreWithConfig(
		pool,
		clock,
		AutomaticCrawlConfig{
			MaxPendingProbes: -1,
		},
	); !errors.Is(
		err,
		errInvalidQueueConfig,
	) {
		t.Fatalf(
			"newDiscoveryStoreWithConfig(negative pending) error = %v, want %v",
			err,
			errInvalidQueueConfig,
		)
	}

	if _, err := newDiscoveryStoreWithConfig(
		pool,
		clock,
		AutomaticCrawlConfig{
			MaxAutomaticPromotionsPerRun: -1,
		},
	); !errors.Is(
		err,
		errInvalidQueueConfig,
	) {
		t.Fatalf(
			"newDiscoveryStoreWithConfig(negative promotions) error = %v, want %v",
			err,
			errInvalidQueueConfig,
		)
	}

	if _, err := newDiscoveryStoreWithConfig(
		pool,
		clock,
		AutomaticCrawlConfig{
			ExcludedHostSuffixes: "Example.com",
		},
	); !errors.Is(
		err,
		errInvalidQueueConfig,
	) {
		t.Fatalf(
			"newDiscoveryStoreWithConfig(invalid exclusion) error = %v, want %v",
			err,
			errInvalidQueueConfig,
		)
	}

	store, err = newDiscoveryStoreWithConfig(
		pool,
		clock,
		automaticConfig,
	)
	if err != nil {
		t.Fatalf(
			"newDiscoveryStoreWithConfig() error = %v",
			err,
		)
	}

	if store == nil {
		t.Fatal(
			"newDiscoveryStoreWithConfig() = nil",
		)
	}
}

func TestDiscoveryStoreValidateWithoutDatabase(
	t *testing.T,
) {
	var nilStore *DiscoveryStore

	if err := nilStore.validate(
		context.Background(),
	); !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Fatalf(
			"nil validate() error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}

	store := &DiscoveryStore{
		pool:  new(pgxpool.Pool),
		clock: databaseQueueClock{},
	}

	if err := store.validate(nil); !errors.Is(
		err,
		errInvalidContext,
	) {
		t.Fatalf(
			"validate(nil) error = %v, want %v",
			err,
			errInvalidContext,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := store.validate(ctx); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf(
			"validate(canceled) error = %v, want %v",
			err,
			context.Canceled,
		)
	}

	if err := (&DiscoveryStore{
		clock: databaseQueueClock{},
	}).validate(
		context.Background(),
	); !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"validate(nil pool) error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}

	if err := (&DiscoveryStore{
		pool: new(pgxpool.Pool),
	}).validate(
		context.Background(),
	); !errors.Is(
		err,
		errDiscoveryClockUnavailable,
	) {
		t.Fatalf(
			"validate(nil clock) error = %v, want %v",
			err,
			errDiscoveryClockUnavailable,
		)
	}

	if err := store.validate(
		context.Background(),
	); err != nil {
		t.Fatalf(
			"validate() error = %v",
			err,
		)
	}
}

func TestAutomaticCrawlConfigDefaultsWithoutDatabase(
	t *testing.T,
) {
	if got := (AutomaticCrawlConfig{}).
		maxPendingProbes(); got !=
		defaultAutomaticCrawlMaxPendingProbes {
		t.Fatalf(
			"maxPendingProbes() = %d, want %d",
			got,
			defaultAutomaticCrawlMaxPendingProbes,
		)
	}

	if got := (AutomaticCrawlConfig{
		MaxPendingProbes: 17,
	}).maxPendingProbes(); got != 17 {
		t.Fatalf(
			"maxPendingProbes() = %d, want 17",
			got,
		)
	}

	if got := (AutomaticCrawlConfig{}).
		maxAutomaticPromotionsPerRun(); got !=
		defaultAutomaticPromotionsPerRun {
		t.Fatalf(
			"maxAutomaticPromotionsPerRun() = %d, want %d",
			got,
			defaultAutomaticPromotionsPerRun,
		)
	}

	if got := (AutomaticCrawlConfig{
		MaxAutomaticPromotionsPerRun: 23,
	}).maxAutomaticPromotionsPerRun(); got != 23 {
		t.Fatalf(
			"maxAutomaticPromotionsPerRun() = %d, want 23",
			got,
		)
	}
}

func TestAutomaticExclusionPatternsWithoutDatabase(
	t *testing.T,
) {
	got := automaticExclusionPatterns(
		AutomaticCrawlConfig{
			ExcludedHostSuffixes: " example.com, ,blogspot.*, foo.test ",
		},
	)

	want := []string{
		"example.com",
		"blogspot.*",
		"foo.test",
	}

	if !slices.Equal(got, want) {
		t.Fatalf(
			"automaticExclusionPatterns() = %#v, want %#v",
			got,
			want,
		)
	}

	if got := automaticExclusionPatterns(
		AutomaticCrawlConfig{},
	); len(got) != 0 {
		t.Fatalf(
			"automaticExclusionPatterns(empty) = %#v, want empty",
			got,
		)
	}
}

func TestPrepareAdmissionCandidatesWithoutDatabase(
	t *testing.T,
) {
	exampleOrg := mustStoreOrigin(
		t,
		"https://example.org",
	)
	exampleCom := mustStoreOrigin(
		t,
		"https://example.com",
	)

	input := []discovery.Candidate{
		{
			Origin: exampleOrg,
			Kind:   discovery.KindRedirect,
		},
		{
			Origin: exampleCom,
			Kind:   discovery.KindLink,
		},
	}

	got, err := prepareAdmissionCandidates(input)
	if err != nil {
		t.Fatalf(
			"prepareAdmissionCandidates() error = %v",
			err,
		)
	}

	if len(got) != 2 {
		t.Fatalf(
			"prepareAdmissionCandidates() length = %d, want 2",
			len(got),
		)
	}

	if got[0].Origin != exampleCom ||
		got[0].Kind != discovery.KindLink ||
		got[1].Origin != exampleOrg ||
		got[1].Kind != discovery.KindRedirect {
		t.Fatalf(
			"prepareAdmissionCandidates() = %#v",
			got,
		)
	}

	if input[0].Origin != exampleOrg ||
		input[1].Origin != exampleCom {
		t.Fatalf(
			"prepareAdmissionCandidates() mutated input = %#v",
			input,
		)
	}

	tests := []struct {
		name       string
		candidates []discovery.Candidate
	}{
		{
			name: "zero origin",
			candidates: []discovery.Candidate{
				{
					Origin: origin.Origin{},
					Kind:   discovery.KindLink,
				},
			},
		},
		{
			name: "unknown kind",
			candidates: []discovery.Candidate{
				{
					Origin: exampleCom,
					Kind:   discovery.Kind(255),
				},
			},
		},
		{
			name: "duplicate",
			candidates: []discovery.Candidate{
				{
					Origin: exampleCom,
					Kind:   discovery.KindLink,
				},
				{
					Origin: exampleCom,
					Kind:   discovery.KindRedirect,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				got, err := prepareAdmissionCandidates(
					test.candidates,
				)

				if got != nil {
					t.Fatalf(
						"prepareAdmissionCandidates() = %#v, want nil",
						got,
					)
				}

				if !errors.Is(
					err,
					errInvalidDiscoveryCandidate,
				) {
					t.Fatalf(
						"prepareAdmissionCandidates() error = %v, want %v",
						err,
						errInvalidDiscoveryCandidate,
					)
				}
			},
		)
	}
}

func TestValidExcludedHostSuffixesWithoutDatabase(
	t *testing.T,
) {
	valid := []string{
		"",
		"example.com",
		"example.com,blogspot.*",
		"foo-bar.example",
		" example.com , blogspot.* ",
	}

	for _, value := range valid {
		if !validExcludedHostSuffixes(value) {
			t.Errorf(
				"validExcludedHostSuffixes(%q) = false",
				value,
			)
		}
	}

	invalid := []string{
		"Example.com",
		".example.com",
		"example.com.",
		"https://example.com",
		"example.com/path",
		"user@example.com",
		"example.com?x=1",
		"example.com#fragment",
		"*",
		"blogspot.*.example",
	}

	for _, value := range invalid {
		if validExcludedHostSuffixes(value) {
			t.Errorf(
				"validExcludedHostSuffixes(%q) = true",
				value,
			)
		}
	}
}

func TestPrepareDiscoveryCandidatesWithoutDatabase(
	t *testing.T,
) {
	source := mustStoreOrigin(
		t,
		"https://source.example",
	)
	exampleOrg := mustStoreOrigin(
		t,
		"https://example.org",
	)
	exampleCom := mustStoreOrigin(
		t,
		"https://example.com",
	)

	input := []discovery.Candidate{
		{
			Origin: exampleOrg,
			Kind:   discovery.KindRedirect,
		},
		{
			Origin: exampleCom,
			Kind:   discovery.KindLink,
		},
	}

	got, err := prepareDiscoveryCandidates(
		source,
		input,
	)
	if err != nil {
		t.Fatalf(
			"prepareDiscoveryCandidates() error = %v",
			err,
		)
	}

	if len(got) != 2 {
		t.Fatalf(
			"prepareDiscoveryCandidates() length = %d, want 2",
			len(got),
		)
	}

	if got[0].Origin != exampleCom ||
		got[0].Kind != discovery.KindLink ||
		got[1].Origin != exampleOrg ||
		got[1].Kind != discovery.KindRedirect {
		t.Fatalf(
			"prepareDiscoveryCandidates() = %#v",
			got,
		)
	}

	if input[0].Origin != exampleOrg ||
		input[1].Origin != exampleCom {
		t.Fatalf(
			"prepareDiscoveryCandidates() mutated input = %#v",
			input,
		)
	}

	if got, err := prepareDiscoveryCandidates(
		origin.Origin{},
		nil,
	); got != nil ||
		!errors.Is(
			err,
			errInvalidOrigin,
		) {
		t.Fatalf(
			"prepareDiscoveryCandidates(zero source) = %#v, %v",
			got,
			err,
		)
	}

	tests := []struct {
		name       string
		candidates []discovery.Candidate
	}{
		{
			name: "zero candidate",
			candidates: []discovery.Candidate{
				{
					Origin: origin.Origin{},
					Kind:   discovery.KindLink,
				},
			},
		},
		{
			name: "source candidate",
			candidates: []discovery.Candidate{
				{
					Origin: source,
					Kind:   discovery.KindLink,
				},
			},
		},
		{
			name: "unknown kind",
			candidates: []discovery.Candidate{
				{
					Origin: exampleCom,
					Kind:   discovery.Kind(255),
				},
			},
		},
		{
			name: "duplicate",
			candidates: []discovery.Candidate{
				{
					Origin: exampleCom,
					Kind:   discovery.KindLink,
				},
				{
					Origin: exampleCom,
					Kind:   discovery.KindRedirect,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				got, err := prepareDiscoveryCandidates(
					source,
					test.candidates,
				)

				if got != nil {
					t.Fatalf(
						"prepareDiscoveryCandidates() = %#v, want nil",
						got,
					)
				}

				if !errors.Is(
					err,
					errInvalidDiscoveryCandidate,
				) {
					t.Fatalf(
						"prepareDiscoveryCandidates() error = %v, want %v",
						err,
						errInvalidDiscoveryCandidate,
					)
				}
			},
		)
	}
}

func TestDiscoveryKindTextWithoutDatabase(
	t *testing.T,
) {
	text, known := discoveryKindText(
		discovery.KindLink,
	)
	if !known || text != "link" {
		t.Fatalf(
			"discoveryKindText(link) = %q, %t",
			text,
			known,
		)
	}

	text, known = discoveryKindText(
		discovery.KindRedirect,
	)
	if !known || text != "redirect" {
		t.Fatalf(
			"discoveryKindText(redirect) = %q, %t",
			text,
			known,
		)
	}

	text, known = discoveryKindText(
		discovery.Kind(255),
	)
	if known || text != "" {
		t.Fatalf(
			"discoveryKindText(unknown) = %q, %t",
			text,
			known,
		)
	}
}

func TestDiscoveryContractGolden(
	t *testing.T,
) {
	var snapshot bytes.Buffer

	fmt.Fprintln(
		&snapshot,
		"=== automatic defaults ===",
	)
	fmt.Fprintf(
		&snapshot,
		"pending=%d\n",
		(AutomaticCrawlConfig{}).
			maxPendingProbes(),
	)
	fmt.Fprintf(
		&snapshot,
		"promotions=%d\n",
		(AutomaticCrawlConfig{}).
			maxAutomaticPromotionsPerRun(),
	)

	fmt.Fprintln(
		&snapshot,
		"\n=== exclusion patterns ===",
	)

	patterns := automaticExclusionPatterns(
		AutomaticCrawlConfig{
			ExcludedHostSuffixes: " example.com, ,blogspot.*, foo.test ",
		},
	)

	for _, pattern := range patterns {
		fmt.Fprintf(
			&snapshot,
			"pattern=%s\n",
			pattern,
		)
	}

	fmt.Fprintln(
		&snapshot,
		"\n=== exclusion syntax ===",
	)

	for _, value := range []string{
		"",
		"example.com",
		"example.com,blogspot.*",
		"Example.com",
		".example.com",
		"example.com.",
		"*",
		"blogspot.*.example",
	} {
		fmt.Fprintf(
			&snapshot,
			"%q=%t\n",
			value,
			validExcludedHostSuffixes(value),
		)
	}

	fmt.Fprintln(
		&snapshot,
		"\n=== exclusion matching ===",
	)

	exclusions := AutomaticCrawlConfig{
		ExcludedHostSuffixes: "example.com,blogspot.*",
	}

	for _, hostname := range []string{
		"example.com",
		"www.example.com",
		"other.test",
		"blogspot.com",
		"foo.blogspot.co.uk",
	} {
		fmt.Fprintf(
			&snapshot,
			"%s=%t\n",
			hostname,
			exclusions.excludes(hostname),
		)
	}

	fmt.Fprintln(
		&snapshot,
		"\n=== discovery kinds ===",
	)

	for _, kind := range []discovery.Kind{
		discovery.KindLink,
		discovery.KindRedirect,
		discovery.Kind(255),
	} {
		text, known := discoveryKindText(kind)

		fmt.Fprintf(
			&snapshot,
			"%d=%q,%t\n",
			kind,
			text,
			known,
		)
	}

	source := mustStoreOrigin(
		t,
		"https://source.example",
	)
	exampleOrg := mustStoreOrigin(
		t,
		"https://example.org",
	)
	exampleCom := mustStoreOrigin(
		t,
		"https://example.com",
	)

	candidates := []discovery.Candidate{
		{
			Origin: exampleOrg,
			Kind:   discovery.KindRedirect,
		},
		{
			Origin: exampleCom,
			Kind:   discovery.KindLink,
		},
	}

	admission, err :=
		prepareAdmissionCandidates(candidates)
	if err != nil {
		t.Fatalf(
			"prepareAdmissionCandidates() error = %v",
			err,
		)
	}

	fmt.Fprintln(
		&snapshot,
		"\n=== admission order ===",
	)

	for _, candidate := range admission {
		kind, _ := discoveryKindText(
			candidate.Kind,
		)

		fmt.Fprintf(
			&snapshot,
			"%s %s\n",
			candidate.Origin.String(),
			kind,
		)
	}

	prepared, err :=
		prepareDiscoveryCandidates(
			source,
			candidates,
		)
	if err != nil {
		t.Fatalf(
			"prepareDiscoveryCandidates() error = %v",
			err,
		)
	}

	fmt.Fprintln(
		&snapshot,
		"\n=== discovery order ===",
	)

	for _, candidate := range prepared {
		kind, _ := discoveryKindText(
			candidate.Kind,
		)

		fmt.Fprintf(
			&snapshot,
			"%s %s\n",
			candidate.Origin.String(),
			kind,
		)
	}

	if err := testutil.CheckGolden(
		"testdata/golden/discovery-contract.golden",
		snapshot.Bytes(),
	); err != nil {
		t.Fatalf(
			"discovery contract golden error = %v",
			err,
		)
	}
}

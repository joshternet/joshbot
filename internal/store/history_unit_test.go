package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
)

func TestStoreObservationsWithoutDatabase(
	t *testing.T,
) {
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	first := time.Date(
		2026,
		time.September,
		18,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	second := first.Add(-time.Hour)

	version := 1
	affirmed := "affirmed"

	store := newStore(
		&storeFakePostgres{
			queryResults: []storeFakeQueryResult{
				{
					rows: newStoreFakeRows(
						[]any{
							first,
							"valid",
							&version,
							&affirmed,
						},
						[]any{
							second,
							"absent",
							nil,
							nil,
						},
					),
				},
			},
		},
	)

	observations, err := store.Observations(
		context.Background(),
		source,
		2,
	)
	if err != nil {
		t.Fatalf(
			"Observations() error = %v",
			err,
		)
	}

	if len(observations) != 2 {
		t.Fatalf(
			"Observations() length = %d, want 2",
			len(observations),
		)
	}

	if observations[0].Outcome !=
		declaration.OutcomeValid ||
		!observations[0].ObservedAt.Equal(first) ||
		observations[0].Declaration.Version != 1 ||
		observations[0].Declaration.Identity !=
			declaration.IdentityAffirmed {
		t.Fatalf(
			"Observations()[0] = %#v",
			observations[0],
		)
	}

	if observations[1].Outcome !=
		declaration.OutcomeAbsent ||
		!observations[1].ObservedAt.Equal(second) ||
		observations[1].Declaration !=
			(declaration.Declaration{}) {
		t.Fatalf(
			"Observations()[1] = %#v",
			observations[1],
		)
	}
}

func TestStoreObservationsValidationFailuresWithoutDatabase(
	t *testing.T,
) {
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	t.Run(
		"store validation",
		func(t *testing.T) {
			store := newStore(nil)

			observations, err := store.Observations(
				context.Background(),
				source,
				1,
			)

			if observations != nil {
				t.Fatalf(
					"Observations() = %#v, want nil",
					observations,
				)
			}

			if !errors.Is(
				err,
				errPoolUnavailable,
			) {
				t.Fatalf(
					"Observations() error = %v, want %v",
					err,
					errPoolUnavailable,
				)
			}
		},
	)

	t.Run(
		"origin",
		func(t *testing.T) {
			store := newStore(
				&storeFakePostgres{},
			)

			var emptySource origin.Origin

			observations, err := store.Observations(
				context.Background(),
				emptySource,
				1,
			)

			if observations != nil {
				t.Fatalf(
					"Observations() = %#v, want nil",
					observations,
				)
			}

			if !errors.Is(
				err,
				errInvalidOrigin,
			) {
				t.Fatalf(
					"Observations() error = %v, want %v",
					err,
					errInvalidOrigin,
				)
			}
		},
	)

	t.Run(
		"low limit",
		func(t *testing.T) {
			store := newStore(
				&storeFakePostgres{},
			)

			observations, err := store.Observations(
				context.Background(),
				source,
				0,
			)

			if observations != nil {
				t.Fatalf(
					"Observations() = %#v, want nil",
					observations,
				)
			}

			if !errors.Is(
				err,
				errInvalidHistoryLimit,
			) {
				t.Fatalf(
					"Observations() error = %v, want %v",
					err,
					errInvalidHistoryLimit,
				)
			}
		},
	)

	t.Run(
		"high limit",
		func(t *testing.T) {
			store := newStore(
				&storeFakePostgres{},
			)

			observations, err := store.Observations(
				context.Background(),
				source,
				MaxObservationHistory+1,
			)

			if observations != nil {
				t.Fatalf(
					"Observations() = %#v, want nil",
					observations,
				)
			}

			if !errors.Is(
				err,
				errInvalidHistoryLimit,
			) {
				t.Fatalf(
					"Observations() error = %v, want %v",
					err,
					errInvalidHistoryLimit,
				)
			}
		},
	)
}

func TestStoreObservationsDatabaseFailuresWithoutDatabase(
	t *testing.T,
) {
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)
	testErr := errors.New(
		"test observation history failure",
	)

	t.Run(
		"query",
		func(t *testing.T) {
			store := newStore(
				&storeFakePostgres{
					queryResults: []storeFakeQueryResult{
						{
							err: testErr,
						},
					},
				},
			)

			observations, err := store.Observations(
				context.Background(),
				source,
				1,
			)

			if observations != nil {
				t.Fatalf(
					"Observations() = %#v, want nil",
					observations,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"Observations() error = %v, want %v",
					err,
					testErr,
				)
			}
		},
	)

	t.Run(
		"scan",
		func(t *testing.T) {
			store := newStore(
				&storeFakePostgres{
					queryResults: []storeFakeQueryResult{
						{
							rows: newStoreFakeRows(
								[]any{
									time.Now().UTC(),
								},
							),
						},
					},
				},
			)

			observations, err := store.Observations(
				context.Background(),
				source,
				1,
			)

			if observations != nil {
				t.Fatalf(
					"Observations() = %#v, want nil",
					observations,
				)
			}

			if err == nil {
				t.Fatal(
					"Observations() error = nil, want scan failure",
				)
			}
		},
	)
}

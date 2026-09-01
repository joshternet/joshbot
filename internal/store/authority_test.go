package store

import (
	"context"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
)

func TestUnavailablePreservesVerifiedState(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := New(pool)
	source := mustStoreOrigin(t, "https://example.com")

	t1 := time.Date(
		2026,
		time.August,
		30,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	t2 := time.Date(
		2026,
		time.August,
		30,
		13,
		0,
		0,
		0,
		time.UTC,
	)

	valid := declaration.Result{
		Outcome: declaration.OutcomeValid,
		Origin:  source,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityAffirmed,
		},
	}
	unavailable := declaration.Result{
		Outcome: declaration.OutcomeUnavailable,
		Origin:  source,
	}

	if err := store.RecordVerification(
		ctx,
		t1,
		valid,
	); err != nil {
		t.Fatalf(
			"record valid verification: %v",
			err,
		)
	}

	if err := store.RecordVerification(
		ctx,
		t2,
		unavailable,
	); err != nil {
		t.Fatalf(
			"record unavailable verification: %v",
			err,
		)
	}

	got, found, err := store.OriginState(ctx, source)
	if err != nil {
		t.Fatalf("OriginState() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("OriginState() found = false, want true")
	}

	assertStoreObservation(
		t,
		got.Latest,
		Observation{
			Outcome:    declaration.OutcomeUnavailable,
			ObservedAt: t2,
		},
	)

	if got.Effective.State != StateVerified {
		t.Errorf(
			"effective state = %v, want StateVerified",
			got.Effective.State,
		)
	}

	assertStoreObservation(
		t,
		got.Effective.Observation,
		Observation{
			Outcome:    declaration.OutcomeValid,
			ObservedAt: t1,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityAffirmed,
			},
		},
	)

	history, err := store.Observations(ctx, source, 10)
	if err != nil {
		t.Fatalf(
			"Observations() error = %v, want nil",
			err,
		)
	}

	if len(history) != 2 {
		t.Fatalf(
			"Observations() length = %d, want 2",
			len(history),
		)
	}

	assertStoreObservation(
		t,
		history[0],
		Observation{
			Outcome:    declaration.OutcomeUnavailable,
			ObservedAt: t2,
		},
	)
	assertStoreObservation(
		t,
		history[1],
		Observation{
			Outcome:    declaration.OutcomeValid,
			ObservedAt: t1,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityAffirmed,
			},
		},
	)

	var unavailableRows int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_observations
			WHERE outcome = 'unavailable'
				AND version IS NULL
				AND identity IS NULL
		`,
	).Scan(&unavailableRows)
	if err != nil {
		t.Fatalf(
			"count unavailable observations: %v",
			err,
		)
	}

	if unavailableRows != 1 {
		t.Errorf(
			"stored unavailable rows = %d, want 1",
			unavailableRows,
		)
	}
}

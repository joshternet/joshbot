package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
)

func TestStoreReturnsBoundedObservationHistory(
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
	t3 := time.Date(
		2026,
		time.August,
		30,
		14,
		0,
		0,
		0,
		time.UTC,
	)

	records := []struct {
		observedAt time.Time
		identity   declaration.Identity
	}{
		{
			observedAt: t2,
			identity:   declaration.IdentityAffirmed,
		},
		{
			observedAt: t1,
			identity:   declaration.IdentityUndeclared,
		},
		{
			observedAt: t3,
			identity:   declaration.IdentityDeclined,
		},
	}

	for _, record := range records {
		err := store.RecordVerification(
			ctx,
			record.observedAt,
			declaration.Result{
				Outcome: declaration.OutcomeValid,
				Origin:  source,
				Declaration: declaration.Declaration{
					Version:  1,
					Identity: record.identity,
				},
			},
		)
		if err != nil {
			t.Fatalf(
				"RecordVerification(%v) error = %v, want nil",
				record.observedAt,
				err,
			)
		}
	}

	want := []Observation{
		{
			Outcome:    declaration.OutcomeValid,
			ObservedAt: t3,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityDeclined,
			},
		},
		{
			Outcome:    declaration.OutcomeValid,
			ObservedAt: t2,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityAffirmed,
			},
		},
		{
			Outcome:    declaration.OutcomeValid,
			ObservedAt: t1,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityUndeclared,
			},
		},
	}

	got, err := store.Observations(ctx, source, 10)
	if err != nil {
		t.Fatalf(
			"Observations() error = %v, want nil",
			err,
		)
	}

	if len(got) != len(want) {
		t.Fatalf(
			"Observations() length = %d, want %d",
			len(got),
			len(want),
		)
	}

	for index := range want {
		assertStoreObservation(
			t,
			got[index],
			want[index],
		)
	}

	limited, err := store.Observations(ctx, source, 2)
	if err != nil {
		t.Fatalf(
			"Observations(limit 2) error = %v, want nil",
			err,
		)
	}

	if len(limited) != 2 {
		t.Fatalf(
			"Observations(limit 2) length = %d, want 2",
			len(limited),
		)
	}

	for index := range limited {
		assertStoreObservation(
			t,
			limited[index],
			want[index],
		)
	}
}

func TestStoreObservationsPreservesCanceledContext(
	t *testing.T,
) {
	pool := newStoreTestPool(t)
	store := New(pool)
	source := mustStoreOrigin(t, "https://example.com")

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	got, err := store.Observations(ctx, source, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf(
			"Observations() error = %v, want context.Canceled",
			err,
		)
	}

	if got != nil {
		t.Errorf(
			"Observations() = %#v, want nil",
			got,
		)
	}
}

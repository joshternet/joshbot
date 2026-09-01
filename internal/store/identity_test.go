package store

import (
	"context"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
)

func TestStoreRecordsValidIdentityStates(t *testing.T) {
	tests := []struct {
		name           string
		identity       declaration.Identity
		storedIdentity string
	}{
		{
			name:           "undeclared",
			identity:       declaration.IdentityUndeclared,
			storedIdentity: "undeclared",
		},
		{
			name:           "affirmed",
			identity:       declaration.IdentityAffirmed,
			storedIdentity: "affirmed",
		},
		{
			name:           "declined",
			identity:       declaration.IdentityDeclined,
			storedIdentity: "declined",
		},
	}

	observedAt := time.Date(
		2026,
		time.August,
		30,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := newStoreTestPool(t)
			store := New(pool)
			source := mustStoreOrigin(
				t,
				"https://example.com",
			)
			result := declaration.Result{
				Outcome: declaration.OutcomeValid,
				Origin:  source,
				Declaration: declaration.Declaration{
					Version:  1,
					Identity: test.identity,
				},
			}

			if err := store.RecordVerification(
				ctx,
				observedAt,
				result,
			); err != nil {
				t.Fatalf(
					"RecordVerification() error = %v, want nil",
					err,
				)
			}

			got, found, err := store.OriginState(
				ctx,
				source,
			)
			if err != nil {
				t.Fatalf(
					"OriginState() error = %v, want nil",
					err,
				)
			}

			if !found {
				t.Fatal(
					"OriginState() found = false, want true",
				)
			}

			wantObservation := Observation{
				Outcome:    declaration.OutcomeValid,
				ObservedAt: observedAt,
				Declaration: declaration.Declaration{
					Version:  1,
					Identity: test.identity,
				},
			}

			assertStoreObservation(
				t,
				got.Latest,
				wantObservation,
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
				wantObservation,
			)

			var storedIdentity string
			err = pool.QueryRow(
				ctx,
				`
					SELECT identity
					FROM verification_observations
				`,
			).Scan(&storedIdentity)
			if err != nil {
				t.Fatalf(
					"query stored identity: %v",
					err,
				)
			}

			if storedIdentity != test.storedIdentity {
				t.Errorf(
					"stored identity = %q, want %q",
					storedIdentity,
					test.storedIdentity,
				)
			}
		})
	}
}

package store

import (
	"context"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
)

func TestStoreNonAuthoritativeOutcomeKeepsUnknownState(
	t *testing.T,
) {
	tests := []struct {
		name    string
		outcome declaration.Outcome
	}{
		{
			name:    "unavailable",
			outcome: declaration.OutcomeUnavailable,
		},
		{
			name:    "robots denied",
			outcome: declaration.OutcomeRobotsDenied,
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
			store := New(newStoreTestPool(t))
			source := mustStoreOrigin(
				t,
				"https://example.com",
			)

			recordStoreResult(
				t,
				store,
				observedAt,
				declaration.Result{
					Outcome: test.outcome,
					Origin:  source,
				},
			)

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

			assertStoreObservation(
				t,
				got.Latest,
				Observation{
					Outcome:    test.outcome,
					ObservedAt: observedAt,
				},
			)

			if got.Effective.State != StateUnknown {
				t.Errorf(
					"effective state = %v, want StateUnknown",
					got.Effective.State,
				)
			}

			assertStoreObservation(
				t,
				got.Effective.Observation,
				Observation{},
			)
		})
	}
}

func TestRobotsDeniedPreservesVerifiedState(
	t *testing.T,
) {
	ctx := context.Background()
	store := New(newStoreTestPool(t))
	source := mustStoreOrigin(t, "https://example.com")
	t1, t2, _ := storeSemanticTimes()

	recordStoreResult(
		t,
		store,
		t1,
		validStoreResult(
			source,
			declaration.IdentityAffirmed,
		),
	)
	recordStoreResult(
		t,
		store,
		t2,
		declaration.Result{
			Outcome: declaration.OutcomeRobotsDenied,
			Origin:  source,
		},
	)

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
			Outcome:    declaration.OutcomeRobotsDenied,
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
}

func TestStoreAuthoritativeOutcomeEstablishesNotVerified(
	t *testing.T,
) {
	tests := []struct {
		name    string
		outcome declaration.Outcome
	}{
		{
			name:    "absent",
			outcome: declaration.OutcomeAbsent,
		},
		{
			name:    "invalid",
			outcome: declaration.OutcomeInvalid,
		},
		{
			name:    "unsupported version",
			outcome: declaration.OutcomeUnsupportedVersion,
		},
		{
			name:    "cross-origin redirect",
			outcome: declaration.OutcomeCrossOriginRedirect,
		},
	}

	t1, t2, _ := storeSemanticTimes()

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store := New(newStoreTestPool(t))
			source := mustStoreOrigin(
				t,
				"https://example.com",
			)

			recordStoreResult(
				t,
				store,
				t1,
				validStoreResult(
					source,
					declaration.IdentityAffirmed,
				),
			)
			recordStoreResult(
				t,
				store,
				t2,
				declaration.Result{
					Outcome: test.outcome,
					Origin:  source,
				},
			)

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

			wantEffective := Observation{
				Outcome:    test.outcome,
				ObservedAt: t2,
			}

			assertStoreObservation(
				t,
				got.Latest,
				wantEffective,
			)

			if got.Effective.State != StateNotVerified {
				t.Errorf(
					"effective state = %v, want StateNotVerified",
					got.Effective.State,
				)
			}

			assertStoreObservation(
				t,
				got.Effective.Observation,
				wantEffective,
			)

			history, err := store.Observations(
				ctx,
				source,
				10,
			)
			if err != nil {
				t.Fatalf(
					"Observations() error = %v, want nil",
					err,
				)
			}

			if len(history) != 2 {
				t.Fatalf(
					"history length = %d, want 2",
					len(history),
				)
			}

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
		})
	}
}

func TestStoreLaterValidObservationRestoresVerification(
	t *testing.T,
) {
	ctx := context.Background()
	store := New(newStoreTestPool(t))
	source := mustStoreOrigin(t, "https://example.com")
	t1, t2, t3 := storeSemanticTimes()

	recordStoreResult(
		t,
		store,
		t1,
		validStoreResult(
			source,
			declaration.IdentityAffirmed,
		),
	)
	recordStoreResult(
		t,
		store,
		t2,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  source,
		},
	)
	recordStoreResult(
		t,
		store,
		t3,
		validStoreResult(
			source,
			declaration.IdentityDeclined,
		),
	)

	got, found, err := store.OriginState(ctx, source)
	if err != nil {
		t.Fatalf("OriginState() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("OriginState() found = false, want true")
	}

	want := Observation{
		Outcome:    declaration.OutcomeValid,
		ObservedAt: t3,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityDeclined,
		},
	}

	assertStoreObservation(t, got.Latest, want)

	if got.Effective.State != StateVerified {
		t.Errorf(
			"effective state = %v, want StateVerified",
			got.Effective.State,
		)
	}

	assertStoreObservation(
		t,
		got.Effective.Observation,
		want,
	)
}

func TestStoreUsesObservationTimeInsteadOfInsertionOrder(
	t *testing.T,
) {
	ctx := context.Background()
	store := New(newStoreTestPool(t))
	source := mustStoreOrigin(t, "https://example.com")
	t1, t2, t3 := storeSemanticTimes()

	recordStoreResult(
		t,
		store,
		t3,
		declaration.Result{
			Outcome: declaration.OutcomeUnavailable,
			Origin:  source,
		},
	)
	recordStoreResult(
		t,
		store,
		t1,
		validStoreResult(
			source,
			declaration.IdentityAffirmed,
		),
	)
	recordStoreResult(
		t,
		store,
		t2,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  source,
		},
	)

	got, found, err := store.OriginState(ctx, source)
	if err != nil {
		t.Fatalf("OriginState() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("OriginState() found = false, want true")
	}

	if !got.FirstObservedAt.Equal(t1) {
		t.Errorf(
			"first observed at = %v, want %v",
			got.FirstObservedAt,
			t1,
		)
	}

	assertStoreObservation(
		t,
		got.Latest,
		Observation{
			Outcome:    declaration.OutcomeUnavailable,
			ObservedAt: t3,
		},
	)

	if got.Effective.State != StateNotVerified {
		t.Errorf(
			"effective state = %v, want StateNotVerified",
			got.Effective.State,
		)
	}

	assertStoreObservation(
		t,
		got.Effective.Observation,
		Observation{
			Outcome:    declaration.OutcomeAbsent,
			ObservedAt: t2,
		},
	)
}

func TestStoreBreaksTimestampTiesUsingGeneratedID(
	t *testing.T,
) {
	ctx := context.Background()
	store := New(newStoreTestPool(t))
	source := mustStoreOrigin(t, "https://example.com")
	t1, _, _ := storeSemanticTimes()

	recordStoreResult(
		t,
		store,
		t1,
		validStoreResult(
			source,
			declaration.IdentityAffirmed,
		),
	)
	recordStoreResult(
		t,
		store,
		t1,
		validStoreResult(
			source,
			declaration.IdentityDeclined,
		),
	)

	got, found, err := store.OriginState(ctx, source)
	if err != nil {
		t.Fatalf("OriginState() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("OriginState() found = false, want true")
	}

	wantLatest := Observation{
		Outcome:    declaration.OutcomeValid,
		ObservedAt: t1,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityDeclined,
		},
	}

	assertStoreObservation(t, got.Latest, wantLatest)
	assertStoreObservation(
		t,
		got.Effective.Observation,
		wantLatest,
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
			"history length = %d, want 2",
			len(history),
		)
	}

	assertStoreObservation(t, history[0], wantLatest)
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
}

func TestStoreOriginStateDistinguishesUnrecordedOrigin(
	t *testing.T,
) {
	ctx := context.Background()
	store := New(newStoreTestPool(t))
	source := mustStoreOrigin(t, "https://example.com")

	_, found, err := store.OriginState(ctx, source)
	if err != nil {
		t.Fatalf("OriginState() error = %v, want nil", err)
	}

	if found {
		t.Fatal("OriginState() found = true, want false")
	}

	history, err := store.Observations(ctx, source, 10)
	if err != nil {
		t.Fatalf(
			"Observations() error = %v, want nil",
			err,
		)
	}

	if len(history) != 0 {
		t.Errorf(
			"Observations() length = %d, want 0",
			len(history),
		)
	}
}

func recordStoreResult(
	t *testing.T,
	store *Store,
	observedAt time.Time,
	result declaration.Result,
) {
	t.Helper()

	if err := store.RecordVerification(
		context.Background(),
		observedAt,
		result,
	); err != nil {
		t.Fatalf(
			"RecordVerification() error = %v, want nil",
			err,
		)
	}
}

func validStoreResult(
	source origin.Origin,
	identity declaration.Identity,
) declaration.Result {
	return declaration.Result{
		Outcome: declaration.OutcomeValid,
		Origin:  source,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: identity,
		},
	}
}

func storeSemanticTimes() (
	time.Time,
	time.Time,
	time.Time,
) {
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

	return t1,
		t1.Add(time.Hour),
		t1.Add(2 * time.Hour)
}

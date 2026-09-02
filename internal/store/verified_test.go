package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/declaration"
)

func TestVerifiedOriginsReturnsAllIdentityStatesInOriginOrder(
	t *testing.T,
) {
	store := New(newStoreTestPool(t))
	observedAt, _, _ := storeSemanticTimes()

	inputs := []struct {
		rawURL   string
		identity declaration.Identity
	}{
		{
			rawURL:   "https://example.net",
			identity: declaration.IdentityAffirmed,
		},
		{
			rawURL:   "https://example.org",
			identity: declaration.IdentityDeclined,
		},
		{
			rawURL:   "https://example.com",
			identity: declaration.IdentityUndeclared,
		},
	}

	for _, input := range inputs {
		source := mustStoreOrigin(t, input.rawURL)
		recordStoreResult(
			t,
			store,
			observedAt,
			validStoreResult(source, input.identity),
		)
	}

	got, err := store.VerifiedOrigins(context.Background())
	if err != nil {
		t.Fatalf(
			"VerifiedOrigins() error = %v, want nil",
			err,
		)
	}

	want := []VerifiedOrigin{
		{
			Origin: mustStoreOrigin(
				t,
				"https://example.com",
			),
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityUndeclared,
			},
		},
		{
			Origin: mustStoreOrigin(
				t,
				"https://example.net",
			),
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityAffirmed,
			},
		},
		{
			Origin: mustStoreOrigin(
				t,
				"https://example.org",
			),
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityDeclined,
			},
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf(
			"VerifiedOrigins() = %#v, want %#v",
			got,
			want,
		)
	}
}

func TestVerifiedOriginsUsesEffectiveAuthoritativeState(
	t *testing.T,
) {
	tests := []struct {
		name         string
		laterOutcome declaration.Outcome
		wantIncluded bool
	}{
		{
			name:         "unavailable preserves participant",
			laterOutcome: declaration.OutcomeUnavailable,
			wantIncluded: true,
		},
		{
			name:         "robots denial preserves participant",
			laterOutcome: declaration.OutcomeRobotsDenied,
			wantIncluded: true,
		},
		{
			name:         "absent removes participant",
			laterOutcome: declaration.OutcomeAbsent,
		},
		{
			name:         "invalid removes participant",
			laterOutcome: declaration.OutcomeInvalid,
		},
		{
			name: "unsupported version removes participant",
			laterOutcome: declaration.
				OutcomeUnsupportedVersion,
		},
		{
			name: "cross-origin redirect removes participant",
			laterOutcome: declaration.
				OutcomeCrossOriginRedirect,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := New(newStoreTestPool(t))
			source := mustStoreOrigin(
				t,
				"https://example.com",
			)
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
					Outcome: test.laterOutcome,
					Origin:  source,
				},
			)

			got, err := store.VerifiedOrigins(
				context.Background(),
			)
			if err != nil {
				t.Fatalf(
					"VerifiedOrigins() error = %v, want nil",
					err,
				)
			}

			if (len(got) == 1) != test.wantIncluded {
				t.Errorf(
					"VerifiedOrigins() length = %d, included want %v",
					len(got),
					test.wantIncluded,
				)
			}

			state, found, err := store.OriginState(
				context.Background(),
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

			stateVerified :=
				state.Effective.State == StateVerified
			if stateVerified != test.wantIncluded {
				t.Errorf(
					"OriginState() verified = %v, want %v",
					stateVerified,
					test.wantIncluded,
				)
			}
		})
	}
}

func TestVerifiedOriginsExcludesUnknownState(
	t *testing.T,
) {
	store := New(newStoreTestPool(t))
	observedAt, _, _ := storeSemanticTimes()

	outcomes := []declaration.Outcome{
		declaration.OutcomeUnavailable,
		declaration.OutcomeRobotsDenied,
	}

	for index, outcome := range outcomes {
		source := mustStoreOrigin(
			t,
			[]string{
				"https://example.com",
				"https://example.net",
			}[index],
		)
		recordStoreResult(
			t,
			store,
			observedAt,
			declaration.Result{
				Outcome: outcome,
				Origin:  source,
			},
		)
	}

	got, err := store.VerifiedOrigins(context.Background())
	if err != nil {
		t.Fatalf(
			"VerifiedOrigins() error = %v, want nil",
			err,
		)
	}

	if len(got) != 0 {
		t.Errorf(
			"VerifiedOrigins() = %#v, want empty",
			got,
		)
	}
}

func TestVerifiedOriginsUsesObservationTimeNotInsertionOrder(
	t *testing.T,
) {
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

	got, err := store.VerifiedOrigins(context.Background())
	if err != nil {
		t.Fatalf(
			"VerifiedOrigins() error = %v, want nil",
			err,
		)
	}

	if len(got) != 0 {
		t.Errorf(
			"VerifiedOrigins() = %#v, want empty",
			got,
		)
	}
}

func TestVerifiedOriginsBreaksTimestampTiesUsingGeneratedID(
	t *testing.T,
) {
	tests := []struct {
		name         string
		first        declaration.Result
		second       declaration.Result
		wantIncluded bool
		wantIdentity declaration.Identity
	}{
		{
			name: "later absent wins",
			first: declaration.Result{
				Outcome: declaration.OutcomeValid,
				Declaration: declaration.Declaration{
					Version:  1,
					Identity: declaration.IdentityAffirmed,
				},
			},
			second: declaration.Result{
				Outcome: declaration.OutcomeAbsent,
			},
		},
		{
			name: "later valid wins",
			first: declaration.Result{
				Outcome: declaration.OutcomeAbsent,
			},
			second: declaration.Result{
				Outcome: declaration.OutcomeValid,
				Declaration: declaration.Declaration{
					Version:  1,
					Identity: declaration.IdentityDeclined,
				},
			},
			wantIncluded: true,
			wantIdentity: declaration.IdentityDeclined,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := New(newStoreTestPool(t))
			source := mustStoreOrigin(
				t,
				"https://example.com",
			)
			observedAt, _, _ := storeSemanticTimes()

			test.first.Origin = source
			test.second.Origin = source

			recordStoreResult(
				t,
				store,
				observedAt,
				test.first,
			)
			recordStoreResult(
				t,
				store,
				observedAt,
				test.second,
			)

			got, err := store.VerifiedOrigins(
				context.Background(),
			)
			if err != nil {
				t.Fatalf(
					"VerifiedOrigins() error = %v, want nil",
					err,
				)
			}

			if (len(got) == 1) != test.wantIncluded {
				t.Fatalf(
					"VerifiedOrigins() length = %d, included want %v",
					len(got),
					test.wantIncluded,
				)
			}

			if test.wantIncluded &&
				got[0].Declaration.Identity !=
					test.wantIdentity {
				t.Errorf(
					"identity = %v, want %v",
					got[0].Declaration.Identity,
					test.wantIdentity,
				)
			}

			state, found, err := store.OriginState(
				context.Background(),
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

			stateVerified :=
				state.Effective.State == StateVerified
			if stateVerified != test.wantIncluded {
				t.Errorf(
					"OriginState() verified = %v, want %v",
					stateVerified,
					test.wantIncluded,
				)
			}
		})
	}
}

func TestVerifiedOriginsValidatesStoreAndContext(
	t *testing.T,
) {
	pool := newStoreTestPool(t)
	validStore := New(pool)

	canceledContext, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	var nilStore *Store

	tests := []struct {
		name  string
		store *Store
		ctx   context.Context
		want  error
	}{
		{
			name:  "nil store",
			store: nilStore,
			ctx:   context.Background(),
			want:  errStoreUnavailable,
		},
		{
			name:  "nil context",
			store: validStore,
			ctx:   nil,
			want:  errInvalidContext,
		},
		{
			name:  "canceled context",
			store: validStore,
			ctx:   canceledContext,
			want:  context.Canceled,
		},
		{
			name:  "nil pool",
			store: New(nil),
			ctx:   context.Background(),
			want:  errPoolUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.store.VerifiedOrigins(test.ctx)
			if !errors.Is(err, test.want) {
				t.Errorf(
					"VerifiedOrigins() error = %v, want %v",
					err,
					test.want,
				)
			}

			if got != nil {
				t.Errorf(
					"VerifiedOrigins() = %#v, want nil",
					got,
				)
			}
		})
	}
}

func TestVerifiedOriginsReturnsDatabaseFailure(
	t *testing.T,
) {
	pool := newStoreTestPool(t)
	store := New(pool)
	pool.Close()

	got, err := store.VerifiedOrigins(context.Background())
	if err == nil {
		t.Fatal(
			"VerifiedOrigins() error = nil, want non-nil",
		)
	}

	if got != nil {
		t.Errorf(
			"VerifiedOrigins() = %#v, want nil",
			got,
		)
	}
}

func TestVerifiedOriginsRejectsInvalidStoredOrigin(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := New(pool)
	observedAt := time.Date(
		2026,
		time.September,
		1,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO origins (
				origin,
				first_observed_at
			)
			VALUES ($1, $2)
		`,
		"not a valid origin",
		observedAt,
	)
	if err != nil {
		t.Fatalf("insert origin: %v", err)
	}

	_, err = pool.Exec(
		ctx,
		`
			INSERT INTO verification_observations (
				origin,
				observed_at,
				outcome,
				version,
				identity
			)
			VALUES ($1, $2, 'valid', 1, 'affirmed')
		`,
		"not a valid origin",
		observedAt,
	)
	if err != nil {
		t.Fatalf("insert observation: %v", err)
	}

	got, err := store.VerifiedOrigins(ctx)
	if !errors.Is(err, errInvalidStoredVerifiedOrigin) {
		t.Errorf(
			"VerifiedOrigins() error = %v, want %v",
			err,
			errInvalidStoredVerifiedOrigin,
		)
	}

	if got != nil {
		t.Errorf(
			"VerifiedOrigins() = %#v, want nil",
			got,
		)
	}
}

func TestScanVerifiedOriginRejectsInvalidStoredValues(
	t *testing.T,
) {
	scanFailure := errors.New("scan failed")

	_, err := scanVerifiedOrigin(
		storedVerifiedOriginRow{
			scanError: scanFailure,
		},
	)
	if !errors.Is(err, scanFailure) {
		t.Errorf(
			"scan error = %v, want %v",
			err,
			scanFailure,
		)
	}

	tests := []struct {
		name string
		row  storedVerifiedOriginRow
	}{
		{
			name: "invalid origin",
			row: storedVerifiedOriginRow{
				origin:   "invalid",
				version:  1,
				identity: "affirmed",
			},
		},
		{
			name: "invalid version",
			row: storedVerifiedOriginRow{
				origin:   "https://example.com",
				version:  2,
				identity: "affirmed",
			},
		},
		{
			name: "invalid identity",
			row: storedVerifiedOriginRow{
				origin:   "https://example.com",
				version:  1,
				identity: "unknown",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := scanVerifiedOrigin(test.row)
			if !errors.Is(
				err,
				errInvalidStoredVerifiedOrigin,
			) {
				t.Errorf(
					"scan error = %v, want %v",
					err,
					errInvalidStoredVerifiedOrigin,
				)
			}
		})
	}
}

type storedVerifiedOriginRow struct {
	origin    string
	version   int
	identity  string
	scanError error
}

func (row storedVerifiedOriginRow) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}

func (row storedVerifiedOriginRow) Scan(
	destinations ...any,
) error {
	if row.scanError != nil {
		return row.scanError
	}

	*destinations[0].(*string) = row.origin
	*destinations[1].(*int) = row.version
	*destinations[2].(*string) = row.identity

	return nil
}

func (row storedVerifiedOriginRow) Values() ([]any, error) {
	if row.scanError != nil {
		return nil, row.scanError
	}

	return []any{
		row.origin,
		row.version,
		row.identity,
	}, nil
}

func (row storedVerifiedOriginRow) RawValues() [][]byte {
	return nil
}

//lint:file-ignore SA1012 Intentional negative tests verify defensive nil-context rejection; production callers must never pass a nil context.
package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

func TestStoreConstructorsWithoutDatabase(
	t *testing.T,
) {
	nilPoolStore := New(nil)
	if nilPoolStore == nil {
		t.Fatal("New(nil) = nil")
	}

	if err := nilPoolStore.validate(
		context.Background(),
	); !errors.Is(err, errPoolUnavailable) {
		t.Fatalf(
			"New(nil).validate() error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}

	store := New(new(pgxpool.Pool))
	if store == nil {
		t.Fatal("New(pool) = nil")
	}

	if err := store.validate(
		context.Background(),
	); err != nil {
		t.Fatalf(
			"New(pool).validate() error = %v",
			err,
		)
	}
}

func TestStoreValidateWithoutDatabase(
	t *testing.T,
) {
	var nilStore *Store

	if err := nilStore.validate(
		context.Background(),
	); !errors.Is(err, errStoreUnavailable) {
		t.Fatalf(
			"nil Store.validate() error = %v, want %v",
			err,
			errStoreUnavailable,
		)
	}

	store := newStore(
		&storeFakePostgres{},
	)

	if err := store.validate(nil); !errors.Is(
		err,
		errInvalidContext,
	) {
		t.Fatalf(
			"Store.validate(nil) error = %v, want %v",
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
			"Store.validate(canceled) error = %v",
			err,
		)
	}

	if err := newStore(nil).validate(
		context.Background(),
	); !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"Store.validate(nil pool) error = %v",
			err,
		)
	}

	if err := store.validate(
		context.Background(),
	); err != nil {
		t.Fatalf(
			"Store.validate() error = %v",
			err,
		)
	}
}

func TestStoreRecordVerificationWithoutDatabase(
	t *testing.T,
) {
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)
	observedAt := time.Date(
		2026,
		time.September,
		18,
		12,
		0,
		0,
		0,
		time.FixedZone("test", -7*60*60),
	)

	tx := &storeFakeTx{}
	store := newStore(
		&storeFakePostgres{
			beginTx: tx,
		},
	)

	err := store.RecordVerification(
		context.Background(),
		observedAt,
		declaration.Result{
			Outcome: declaration.OutcomeValid,
			Origin:  source,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityAffirmed,
			},
		},
	)
	if err != nil {
		t.Fatalf(
			"RecordVerification() error = %v",
			err,
		)
	}

	if !tx.committed {
		t.Fatal(
			"RecordVerification() did not commit transaction",
		)
	}
}

func TestStoreRecordVerificationRejectsInvalidInputWithoutDatabase(
	t *testing.T,
) {
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)
	observedAt := time.Now().UTC()

	store := newStore(
		&storeFakePostgres{},
	)

	tests := []struct {
		name       string
		observedAt time.Time
		result     declaration.Result
		want       error
	}{
		{
			name:       "origin",
			observedAt: observedAt,
			result: declaration.Result{
				Outcome: declaration.OutcomeAbsent,
			},
			want: errInvalidOrigin,
		},
		{
			name: "observed time",
			result: declaration.Result{
				Outcome: declaration.OutcomeAbsent,
				Origin:  source,
			},
			want: errInvalidObservedAt,
		},
		{
			name:       "result",
			observedAt: observedAt,
			result: declaration.Result{
				Outcome: declaration.OutcomeValid,
				Origin:  source,
				Declaration: declaration.Declaration{
					Version: 2,
				},
			},
			want: errInvalidResult,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				err := store.RecordVerification(
					context.Background(),
					test.observedAt,
					test.result,
				)

				if !errors.Is(
					err,
					test.want,
				) {
					t.Fatalf(
						"RecordVerification() error = %v, want %v",
						err,
						test.want,
					)
				}
			},
		)
	}
}

func TestStoreRecordVerificationTransactionFailuresWithoutDatabase(
	t *testing.T,
) {
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)
	result := declaration.Result{
		Outcome: declaration.OutcomeAbsent,
		Origin:  source,
	}
	observedAt := time.Now().UTC()
	testErr := errors.New(
		"test transaction failure",
	)

	t.Run(
		"begin",
		func(t *testing.T) {
			store := newStore(
				&storeFakePostgres{
					beginErr: testErr,
				},
			)

			err := store.RecordVerification(
				context.Background(),
				observedAt,
				result,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: record verification",
				) {
				t.Fatalf(
					"RecordVerification() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"exec",
		func(t *testing.T) {
			tx := &storeFakeTx{
				execErr: testErr,
			}
			store := newStore(
				&storeFakePostgres{
					beginTx: tx,
				},
			)

			err := store.RecordVerification(
				context.Background(),
				observedAt,
				result,
			)

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"RecordVerification() error = %v",
					err,
				)
			}

			if !tx.rolledBack {
				t.Fatal(
					"RecordVerification() did not roll back failed transaction",
				)
			}
		},
	)
}

func TestStoreOriginStateUnknownWithoutDatabase(
	t *testing.T,
) {
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)
	firstObserved := time.Date(
		2026,
		time.September,
		18,
		10,
		0,
		0,
		0,
		time.UTC,
	)
	latestObserved := firstObserved.Add(
		time.Hour,
	)

	store := newStore(
		&storeFakePostgres{
			rowResults: []storeFakeRow{
				{
					values: []any{
						firstObserved,
						latestObserved,
						"unavailable",
						nil,
						nil,
						nil,
						nil,
						nil,
						nil,
					},
				},
			},
		},
	)

	state, found, err := store.OriginState(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"OriginState() error = %v",
			err,
		)
	}

	if !found {
		t.Fatal(
			"OriginState() found = false, want true",
		)
	}

	if state.Origin != source ||
		!state.FirstObservedAt.Equal(
			firstObserved,
		) ||
		state.Latest.Outcome !=
			declaration.OutcomeUnavailable ||
		state.Effective.State != StateUnknown {
		t.Fatalf(
			"OriginState() = %#v",
			state,
		)
	}
}

func TestStoreOriginStateVerifiedWithoutDatabase(
	t *testing.T,
) {
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)
	firstObserved := time.Date(
		2026,
		time.September,
		18,
		10,
		0,
		0,
		0,
		time.UTC,
	)
	latestObserved := firstObserved.Add(
		time.Hour,
	)

	version := 1
	identity := "affirmed"
	effectiveObserved := latestObserved
	effectiveOutcome := "valid"

	store := newStore(
		&storeFakePostgres{
			rowResults: []storeFakeRow{
				{
					values: []any{
						firstObserved,
						latestObserved,
						"valid",
						&version,
						&identity,
						&effectiveObserved,
						&effectiveOutcome,
						&version,
						&identity,
					},
				},
			},
		},
	)

	state, found, err := store.OriginState(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"OriginState() error = %v",
			err,
		)
	}

	if !found {
		t.Fatal(
			"OriginState() found = false, want true",
		)
	}

	if state.Latest.Declaration.Version != 1 ||
		state.Latest.Declaration.Identity !=
			declaration.IdentityAffirmed {
		t.Fatalf(
			"OriginState().Latest = %#v",
			state.Latest,
		)
	}

	if state.Effective.State != StateVerified ||
		state.Effective.Observation.Outcome !=
			declaration.OutcomeValid ||
		state.Effective.Observation.Declaration.Identity !=
			declaration.IdentityAffirmed {
		t.Fatalf(
			"OriginState().Effective = %#v",
			state.Effective,
		)
	}
}

func TestStoreOriginStateFailuresWithoutDatabase(
	t *testing.T,
) {
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	t.Run(
		"invalid origin",
		func(t *testing.T) {
			store := newStore(
				&storeFakePostgres{},
			)

			var empty origin.Origin

			_, found, err := store.OriginState(
				context.Background(),
				empty,
			)

			if found ||
				!errors.Is(
					err,
					errInvalidOrigin,
				) {
				t.Fatalf(
					"OriginState() found = %t, error = %v",
					found,
					err,
				)
			}
		},
	)

	t.Run(
		"not found",
		func(t *testing.T) {
			store := newStore(
				&storeFakePostgres{
					rowResults: []storeFakeRow{
						{
							err: pgx.ErrNoRows,
						},
					},
				},
			)

			state, found, err := store.OriginState(
				context.Background(),
				source,
			)

			if err != nil ||
				found ||
				state != (OriginState{}) {
				t.Fatalf(
					"OriginState() = %#v, %t, %v",
					state,
					found,
					err,
				)
			}
		},
	)

	t.Run(
		"query",
		func(t *testing.T) {
			testErr := errors.New(
				"test origin state failure",
			)
			store := newStore(
				&storeFakePostgres{
					rowResults: []storeFakeRow{
						{
							err: testErr,
						},
					},
				},
			)

			_, found, err := store.OriginState(
				context.Background(),
				source,
			)

			if found ||
				!errors.Is(
					err,
					testErr,
				) ||
				!strings.Contains(
					err.Error(),
					"store: query origin state",
				) {
				t.Fatalf(
					"OriginState() found = %t, error = %v",
					found,
					err,
				)
			}
		},
	)
}

func TestValidVerificationResult(
	t *testing.T,
) {
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	tests := []struct {
		name   string
		result declaration.Result
		want   bool
	}{
		{
			name: "unknown outcome",
			result: declaration.Result{
				Outcome: declaration.Outcome(255),
				Origin:  source,
			},
		},
		{
			name: "valid",
			result: declaration.Result{
				Outcome: declaration.OutcomeValid,
				Origin:  source,
				Declaration: declaration.Declaration{
					Version:  1,
					Identity: declaration.IdentityAffirmed,
				},
			},
			want: true,
		},
		{
			name: "valid wrong version",
			result: declaration.Result{
				Outcome: declaration.OutcomeValid,
				Origin:  source,
				Declaration: declaration.Declaration{
					Version:  2,
					Identity: declaration.IdentityAffirmed,
				},
			},
		},
		{
			name: "valid bad identity",
			result: declaration.Result{
				Outcome: declaration.OutcomeValid,
				Origin:  source,
				Declaration: declaration.Declaration{
					Version:  1,
					Identity: declaration.Identity(255),
				},
			},
		},
		{
			name: "valid failure category",
			result: declaration.Result{
				Outcome: declaration.OutcomeValid,
				Origin:  source,
				Declaration: declaration.Declaration{
					Version:  1,
					Identity: declaration.IdentityAffirmed,
				},
				FailureCategory: retry.CategoryDNS,
			},
		},
		{
			name: "valid retry after",
			result: declaration.Result{
				Outcome: declaration.OutcomeValid,
				Origin:  source,
				Declaration: declaration.Declaration{
					Version:  1,
					Identity: declaration.IdentityAffirmed,
				},
				RetryAfter: time.Second,
			},
		},
		{
			name: "non-valid declaration",
			result: declaration.Result{
				Outcome: declaration.OutcomeAbsent,
				Origin:  source,
				Declaration: declaration.Declaration{
					Version: 1,
				},
			},
		},
		{
			name: "authoritative non-valid",
			result: declaration.Result{
				Outcome: declaration.OutcomeAbsent,
				Origin:  source,
			},
			want: true,
		},
		{
			name: "authoritative failure category",
			result: declaration.Result{
				Outcome:         declaration.OutcomeAbsent,
				Origin:          source,
				FailureCategory: retry.CategoryDNS,
			},
		},
		{
			name: "authoritative retry after",
			result: declaration.Result{
				Outcome:    declaration.OutcomeAbsent,
				Origin:     source,
				RetryAfter: time.Second,
			},
		},
		{
			name: "unavailable",
			result: declaration.Result{
				Outcome:         declaration.OutcomeUnavailable,
				Origin:          source,
				FailureCategory: retry.CategoryDNS,
				RetryAfter:      retry.MaxDelay,
			},
			want: true,
		},
		{
			name: "unavailable invalid category",
			result: declaration.Result{
				Outcome: declaration.OutcomeUnavailable,
				Origin:  source,
				FailureCategory: retry.Category(
					"invalid",
				),
			},
		},
		{
			name: "unavailable negative retry",
			result: declaration.Result{
				Outcome:         declaration.OutcomeUnavailable,
				Origin:          source,
				FailureCategory: retry.CategoryDNS,
				RetryAfter:      -time.Second,
			},
		},
		{
			name: "unavailable excessive retry",
			result: declaration.Result{
				Outcome:         declaration.OutcomeUnavailable,
				Origin:          source,
				FailureCategory: retry.CategoryDNS,
				RetryAfter: retry.MaxDelay +
					time.Second,
			},
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				got := validVerificationResult(
					test.result,
				)

				if got != test.want {
					t.Fatalf(
						"validVerificationResult() = %t, want %t",
						got,
						test.want,
					)
				}
			},
		)
	}
}

func TestStoreValueHelpers(
	t *testing.T,
) {
	for _, identity := range []declaration.Identity{
		declaration.IdentityUndeclared,
		declaration.IdentityAffirmed,
		declaration.IdentityDeclined,
	} {
		if !validIdentity(identity) {
			t.Errorf(
				"validIdentity(%v) = false",
				identity,
			)
		}
	}

	if validIdentity(
		declaration.Identity(255),
	) {
		t.Error(
			"validIdentity(invalid) = true",
		)
	}

	version, identity := storedDeclarationValues(
		declaration.Result{
			Outcome: declaration.OutcomeValid,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityDeclined,
			},
		},
	)

	if version != 1 ||
		identity != "declined" {
		t.Fatalf(
			"storedDeclarationValues(valid) = %#v, %#v",
			version,
			identity,
		)
	}

	version, identity = storedDeclarationValues(
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
		},
	)

	if version != nil ||
		identity != nil {
		t.Fatalf(
			"storedDeclarationValues(absent) = %#v, %#v",
			version,
			identity,
		)
	}

	observedAt := time.Date(
		2026,
		time.September,
		18,
		12,
		0,
		0,
		0,
		time.FixedZone("test", -7*60*60),
	)

	empty := observationFromStored(
		observedAt,
		"absent",
		nil,
		nil,
	)

	if empty.Outcome !=
		declaration.OutcomeAbsent ||
		!empty.ObservedAt.Equal(
			observedAt,
		) ||
		empty.Declaration !=
			(declaration.Declaration{}) {
		t.Fatalf(
			"observationFromStored(empty) = %#v",
			empty,
		)
	}

	storedVersion := 1
	storedIdentity := "affirmed"

	full := observationFromStored(
		observedAt,
		"valid",
		&storedVersion,
		&storedIdentity,
	)

	if full.Outcome !=
		declaration.OutcomeValid ||
		full.Declaration.Version != 1 ||
		full.Declaration.Identity !=
			declaration.IdentityAffirmed {
		t.Fatalf(
			"observationFromStored(full) = %#v",
			full,
		)
	}

	if effectiveStateForOutcome(
		declaration.OutcomeValid,
	) != StateVerified {
		t.Error(
			"effectiveStateForOutcome(valid) != StateVerified",
		)
	}

	if effectiveStateForOutcome(
		declaration.OutcomeAbsent,
	) != StateNotVerified {
		t.Error(
			"effectiveStateForOutcome(absent) != StateNotVerified",
		)
	}
}

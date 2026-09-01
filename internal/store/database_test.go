package store

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
)

func TestStoreRejectsInvalidRecordInputs(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := New(pool)
	source := mustStoreOrigin(t, "https://example.com")
	observedAt, _, _ := storeSemanticTimes()
	valid := validStoreResult(
		source,
		declaration.IdentityAffirmed,
	)

	canceledContext, cancel := context.WithCancel(ctx)
	cancel()

	var nilStore *Store

	tests := []struct {
		name       string
		store      *Store
		ctx        context.Context
		observedAt time.Time
		result     declaration.Result
		want       error
	}{
		{
			name:       "nil store",
			store:      nilStore,
			ctx:        ctx,
			observedAt: observedAt,
			result:     valid,
			want:       errStoreUnavailable,
		},
		{
			name:       "nil context",
			store:      store,
			ctx:        nil,
			observedAt: observedAt,
			result:     valid,
			want:       errInvalidContext,
		},
		{
			name:       "canceled context",
			store:      store,
			ctx:        canceledContext,
			observedAt: observedAt,
			result:     valid,
			want:       context.Canceled,
		},
		{
			name:       "nil pool",
			store:      New(nil),
			ctx:        ctx,
			observedAt: observedAt,
			result:     valid,
			want:       errPoolUnavailable,
		},
		{
			name:       "zero origin",
			store:      store,
			ctx:        ctx,
			observedAt: observedAt,
			result: declaration.Result{
				Outcome: declaration.OutcomeValid,
				Declaration: declaration.Declaration{
					Version:  1,
					Identity: declaration.IdentityAffirmed,
				},
			},
			want: errInvalidOrigin,
		},
		{
			name:       "zero observation time",
			store:      store,
			ctx:        ctx,
			observedAt: time.Time{},
			result:     valid,
			want:       errInvalidObservedAt,
		},
		{
			name:       "unknown outcome",
			store:      store,
			ctx:        ctx,
			observedAt: observedAt,
			result: declaration.Result{
				Outcome: declaration.Outcome(255),
				Origin:  source,
			},
			want: errInvalidResult,
		},
		{
			name:       "valid with wrong version",
			store:      store,
			ctx:        ctx,
			observedAt: observedAt,
			result: declaration.Result{
				Outcome: declaration.OutcomeValid,
				Origin:  source,
				Declaration: declaration.Declaration{
					Version:  2,
					Identity: declaration.IdentityAffirmed,
				},
			},
			want: errInvalidResult,
		},
		{
			name:       "valid with unknown identity",
			store:      store,
			ctx:        ctx,
			observedAt: observedAt,
			result: declaration.Result{
				Outcome: declaration.OutcomeValid,
				Origin:  source,
				Declaration: declaration.Declaration{
					Version:  1,
					Identity: declaration.Identity(255),
				},
			},
			want: errInvalidResult,
		},
		{
			name:       "non-valid with declaration",
			store:      store,
			ctx:        ctx,
			observedAt: observedAt,
			result: declaration.Result{
				Outcome: declaration.OutcomeAbsent,
				Origin:  source,
				Declaration: declaration.Declaration{
					Version:  1,
					Identity: declaration.IdentityAffirmed,
				},
			},
			want: errInvalidResult,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.store.RecordVerification(
				test.ctx,
				test.observedAt,
				test.result,
			)
			if !errors.Is(err, test.want) {
				t.Errorf(
					"RecordVerification() error = %v, want %v",
					err,
					test.want,
				)
			}
		})
	}
}

func TestOriginStateRejectsInvalidInputsAndPoolFailure(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := New(pool)
	source := mustStoreOrigin(t, "https://example.com")

	var nilStore *Store
	_, _, err := nilStore.OriginState(ctx, source)
	if !errors.Is(err, errStoreUnavailable) {
		t.Errorf(
			"nil Store OriginState() error = %v, want %v",
			err,
			errStoreUnavailable,
		)
	}

	_, _, err = store.OriginState(
		ctx,
		mustStoreOriginZero(),
	)
	if !errors.Is(err, errInvalidOrigin) {
		t.Errorf(
			"zero origin error = %v, want %v",
			err,
			errInvalidOrigin,
		)
	}

	canceledContext, cancel := context.WithCancel(ctx)
	cancel()

	_, _, err = store.OriginState(
		canceledContext,
		source,
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"canceled context error = %v, want context.Canceled",
			err,
		)
	}

	pool.Close()

	_, found, err := store.OriginState(ctx, source)
	if err == nil {
		t.Error(
			"OriginState() error = nil, want non-nil",
		)
	}

	if found {
		t.Error(
			"OriginState() found = true, want false",
		)
	}
}

func TestObservationsRejectsInvalidInputsAndPoolFailure(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := New(pool)
	source := mustStoreOrigin(t, "https://example.com")

	got, err := store.Observations(
		ctx,
		mustStoreOriginZero(),
		1,
	)
	if !errors.Is(err, errInvalidOrigin) {
		t.Errorf(
			"zero origin error = %v, want %v",
			err,
			errInvalidOrigin,
		)
	}

	if got != nil {
		t.Errorf(
			"zero origin observations = %#v, want nil",
			got,
		)
	}

	for _, limit := range []int{
		0,
		-1,
		MaxObservationHistory + 1,
	} {
		got, err = store.Observations(
			ctx,
			source,
			limit,
		)
		if !errors.Is(err, errInvalidHistoryLimit) {
			t.Errorf(
				"Observations(limit %d) error = %v, want %v",
				limit,
				err,
				errInvalidHistoryLimit,
			)
		}

		if got != nil {
			t.Errorf(
				"Observations(limit %d) = %#v, want nil",
				limit,
				got,
			)
		}
	}

	pool.Close()

	got, err = store.Observations(ctx, source, 1)
	if err == nil {
		t.Error(
			"Observations() error = nil, want non-nil",
		)
	}

	if got != nil {
		t.Errorf(
			"Observations() = %#v, want nil",
			got,
		)
	}
}

func TestRecordVerificationRollsBackOriginWhenObservationFails(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := New(pool)
	source := mustStoreOrigin(t, "https://example.com")
	observedAt, _, _ := storeSemanticTimes()

	_, err := pool.Exec(
		ctx,
		`
			ALTER TABLE verification_observations
			ADD CONSTRAINT test_reject_valid_observation
			CHECK (outcome <> 'valid')
		`,
	)
	if err != nil {
		t.Fatalf(
			"add deterministic failure constraint: %v",
			err,
		)
	}

	err = store.RecordVerification(
		ctx,
		observedAt,
		validStoreResult(
			source,
			declaration.IdentityAffirmed,
		),
	)
	if err == nil {
		t.Fatal(
			"RecordVerification() error = nil, want non-nil",
		)
	}

	var originCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM origins
			WHERE origin = $1
		`,
		source.String(),
	).Scan(&originCount)
	if err != nil {
		t.Fatalf("count origins: %v", err)
	}

	if originCount != 0 {
		t.Errorf(
			"origin count = %d, want 0",
			originCount,
		)
	}

	var observationCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_observations
		`,
	).Scan(&observationCount)
	if err != nil {
		t.Fatalf("count observations: %v", err)
	}

	if observationCount != 0 {
		t.Errorf(
			"observation count = %d, want 0",
			observationCount,
		)
	}
}

func TestStoreDatabaseConstraintsRejectImpossibleRows(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	source := mustStoreOrigin(t, "https://example.com")
	observedAt, _, _ := storeSemanticTimes()

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO origins (
				origin,
				first_observed_at
			)
			VALUES ($1, $2)
		`,
		source.String(),
		observedAt,
	)
	if err != nil {
		t.Fatalf("insert origin: %v", err)
	}

	tests := []struct {
		name     string
		origin   string
		outcome  string
		version  any
		identity any
	}{
		{
			name:     "unknown outcome",
			origin:   source.String(),
			outcome:  "mystery",
			version:  nil,
			identity: nil,
		},
		{
			name:     "invalid identity",
			origin:   source.String(),
			outcome:  "valid",
			version:  1,
			identity: "maybe",
		},
		{
			name:     "valid with null version",
			origin:   source.String(),
			outcome:  "valid",
			version:  nil,
			identity: "affirmed",
		},
		{
			name:     "valid with null identity",
			origin:   source.String(),
			outcome:  "valid",
			version:  1,
			identity: nil,
		},
		{
			name:     "valid with unsupported version",
			origin:   source.String(),
			outcome:  "valid",
			version:  2,
			identity: "affirmed",
		},
		{
			name:     "absent with identity",
			origin:   source.String(),
			outcome:  "absent",
			version:  nil,
			identity: "affirmed",
		},
		{
			name:     "unavailable with version",
			origin:   source.String(),
			outcome:  "unavailable",
			version:  1,
			identity: nil,
		},
		{
			name:     "robots denied with identity",
			origin:   source.String(),
			outcome:  "robots_denied",
			version:  nil,
			identity: "declined",
		},
		{
			name:     "orphan observation",
			origin:   "https://example.net",
			outcome:  "absent",
			version:  nil,
			identity: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := pool.Exec(
				ctx,
				`
					INSERT INTO verification_observations (
						origin,
						observed_at,
						outcome,
						version,
						identity
					)
					VALUES ($1, $2, $3, $4, $5)
				`,
				test.origin,
				observedAt,
				test.outcome,
				test.version,
				test.identity,
			)
			if err == nil {
				t.Error(
					"constraint violation error = nil, want non-nil",
				)
			}
		})
	}

	var observationCount int
	err = pool.QueryRow(
		ctx,
		"SELECT count(*) FROM verification_observations",
	).Scan(&observationCount)
	if err != nil {
		t.Fatalf("count observations: %v", err)
	}

	if observationCount != 0 {
		t.Errorf(
			"observation count = %d, want 0",
			observationCount,
		)
	}
}

func TestStoreConcurrentRecordingPreservesAllObservations(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := New(pool)
	source := mustStoreOrigin(t, "https://example.com")
	firstObservedAt, _, _ := storeSemanticTimes()

	const observationCount = 16

	var waitGroup sync.WaitGroup
	errorsChannel := make(
		chan error,
		observationCount,
	)

	for index := 0; index < observationCount; index++ {
		observedAt := firstObservedAt.Add(
			time.Duration(index) * time.Minute,
		)

		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()

			errorsChannel <- store.RecordVerification(
				ctx,
				observedAt,
				validStoreResult(
					source,
					declaration.IdentityAffirmed,
				),
			)
		}()
	}

	waitGroup.Wait()
	close(errorsChannel)

	for err := range errorsChannel {
		if err != nil {
			t.Fatalf(
				"concurrent RecordVerification() error = %v",
				err,
			)
		}
	}

	var storedOriginCount int
	err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM origins
			WHERE origin = $1
		`,
		source.String(),
	).Scan(&storedOriginCount)
	if err != nil {
		t.Fatalf("count origins: %v", err)
	}

	if storedOriginCount != 1 {
		t.Errorf(
			"origin count = %d, want 1",
			storedOriginCount,
		)
	}

	var storedObservationCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_observations
			WHERE origin = $1
		`,
		source.String(),
	).Scan(&storedObservationCount)
	if err != nil {
		t.Fatalf("count observations: %v", err)
	}

	if storedObservationCount != observationCount {
		t.Errorf(
			"observation count = %d, want %d",
			storedObservationCount,
			observationCount,
		)
	}

	got, found, err := store.OriginState(ctx, source)
	if err != nil {
		t.Fatalf("OriginState() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("OriginState() found = false, want true")
	}

	if !got.FirstObservedAt.Equal(firstObservedAt) {
		t.Errorf(
			"first observed at = %v, want %v",
			got.FirstObservedAt,
			firstObservedAt,
		)
	}

	wantLatestAt := firstObservedAt.Add(
		(observationCount - 1) * time.Minute,
	)

	assertStoreObservation(
		t,
		got.Latest,
		Observation{
			Outcome:    declaration.OutcomeValid,
			ObservedAt: wantLatestAt,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityAffirmed,
			},
		},
	)

	if got.Effective.State != StateVerified {
		t.Errorf(
			"effective state = %v, want StateVerified",
			got.Effective.State,
		)
	}
}

func TestPostgreSQLIntegrationConfigured(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	var versionText string
	if err := pool.QueryRow(
		ctx,
		"SHOW server_version_num",
	).Scan(&versionText); err != nil {
		t.Fatalf(
			"query PostgreSQL version: %v",
			err,
		)
	}

	versionNumber, err := strconv.Atoi(versionText)
	if err != nil {
		t.Fatalf(
			"parse PostgreSQL version %q: %v",
			versionText,
			err,
		)
	}

	if versionNumber/10000 != 18 {
		t.Errorf(
			"PostgreSQL major version = %d, want 18",
			versionNumber/10000,
		)
	}

	var databaseName string
	if err := pool.QueryRow(
		ctx,
		"SELECT current_database()",
	).Scan(&databaseName); err != nil {
		t.Fatalf(
			"query current database: %v",
			err,
		)
	}

	if databaseName == "" {
		t.Error(
			"current database name is empty",
		)
	}
}

func TestInitialMigrationIsForwardOnly(t *testing.T) {
	pool := newStoreTestPool(t)

	migration, err := os.ReadFile(
		"migrations/0001_initial.sql",
	)
	if err != nil {
		t.Fatalf("read initial migration: %v", err)
	}

	_, err = pool.Exec(
		context.Background(),
		string(migration),
		pgx.QueryExecModeSimpleProtocol,
	)
	if err == nil {
		t.Fatal(
			"reapply initial migration error = nil, want non-nil",
		)
	}
}

func TestStoreSchemaPersistsOnlySemanticColumns(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	tests := []struct {
		table string
		want  []string
	}{
		{
			table: "origins",
			want: []string{
				"origin",
				"first_observed_at",
			},
		},
		{
			table: "verification_observations",
			want: []string{
				"id",
				"origin",
				"observed_at",
				"outcome",
				"version",
				"identity",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.table, func(t *testing.T) {
			rows, err := pool.Query(
				ctx,
				`
					SELECT column_name
					FROM information_schema.columns
					WHERE table_schema = current_schema()
						AND table_name = $1
					ORDER BY ordinal_position
				`,
				test.table,
			)
			if err != nil {
				t.Fatalf(
					"query %s columns: %v",
					test.table,
					err,
				)
			}

			got, err := pgx.CollectRows(
				rows,
				func(
					row pgx.CollectableRow,
				) (string, error) {
					var column string
					if err := row.Scan(&column); err != nil {
						return "", err
					}

					return column, nil
				},
			)
			if err != nil {
				t.Fatalf(
					"collect %s columns: %v",
					test.table,
					err,
				)
			}

			if !slices.Equal(got, test.want) {
				t.Errorf(
					"%s columns = %v, want %v",
					test.table,
					got,
					test.want,
				)
			}
		})
	}
}

func TestStoreSchemaHasRequiredObservationIndexes(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	rows, err := pool.Query(
		ctx,
		`
			SELECT indexname
			FROM pg_indexes
			WHERE schemaname = current_schema()
				AND tablename = 'verification_observations'
				AND indexname <> 'verification_observations_pkey'
			ORDER BY indexname
		`,
	)
	if err != nil {
		t.Fatalf("query observation indexes: %v", err)
	}

	got, err := pgx.CollectRows(
		rows,
		func(row pgx.CollectableRow) (string, error) {
			var indexName string
			if err := row.Scan(&indexName); err != nil {
				return "", err
			}

			return indexName, nil
		},
	)
	if err != nil {
		t.Fatalf("collect observation indexes: %v", err)
	}

	want := []string{
		"verification_observations_authoritative_order_idx",
		"verification_observations_origin_order_idx",
	}
	if !slices.Equal(got, want) {
		t.Errorf(
			"observation indexes = %v, want %v",
			got,
			want,
		)
	}
}

func mustStoreOriginZero() origin.Origin {
	return origin.Origin{}
}

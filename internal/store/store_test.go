package store

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
)

var storeSchemaSequence uint64

func TestStoreRecordsValidAffirmedVerification(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := New(pool)

	source := mustStoreOrigin(t, "https://example.com")
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
	result := declaration.Result{
		Outcome: declaration.OutcomeValid,
		Origin:  source,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityAffirmed,
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

	got, found, err := store.OriginState(ctx, source)
	if err != nil {
		t.Fatalf("OriginState() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("OriginState() found = false, want true")
	}

	if got.Origin != source {
		t.Errorf(
			"OriginState() origin = %q, want %q",
			got.Origin,
			source,
		)
	}

	if !got.FirstObservedAt.Equal(observedAt) {
		t.Errorf(
			"first observed at = %v, want %v",
			got.FirstObservedAt,
			observedAt,
		)
	}

	assertStoreObservation(
		t,
		got.Latest,
		Observation{
			Outcome:    declaration.OutcomeValid,
			ObservedAt: observedAt,
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

	assertStoreObservation(
		t,
		got.Effective.Observation,
		Observation{
			Outcome:    declaration.OutcomeValid,
			ObservedAt: observedAt,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityAffirmed,
			},
		},
	)

	var (
		storedOrigin   string
		storedAt       time.Time
		storedOutcome  string
		storedVersion  int
		storedIdentity string
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				origin,
				observed_at,
				outcome,
				version,
				identity
			FROM verification_observations
		`,
	).Scan(
		&storedOrigin,
		&storedAt,
		&storedOutcome,
		&storedVersion,
		&storedIdentity,
	)
	if err != nil {
		t.Fatalf(
			"query stored observation: %v",
			err,
		)
	}

	if storedOrigin != source.String() {
		t.Errorf(
			"stored origin = %q, want %q",
			storedOrigin,
			source,
		)
	}

	if !storedAt.Equal(observedAt) {
		t.Errorf(
			"stored observed_at = %v, want %v",
			storedAt,
			observedAt,
		)
	}

	if storedOutcome != "valid" {
		t.Errorf(
			"stored outcome = %q, want %q",
			storedOutcome,
			"valid",
		)
	}

	if storedVersion != 1 {
		t.Errorf(
			"stored version = %d, want 1",
			storedVersion,
		)
	}

	if storedIdentity != "affirmed" {
		t.Errorf(
			"stored identity = %q, want %q",
			storedIdentity,
			"affirmed",
		)
	}
}

func newStoreTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("JOSHBOT_TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv(
			"JOSHBOT_REQUIRE_DATABASE_TESTS",
		) == "1" {
			t.Fatal(
				"JOSHBOT_TEST_DATABASE_URL is required",
			)
		}

		t.Skip(
			"JOSHBOT_TEST_DATABASE_URL is not configured",
		)
	}

	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}

	adminPool, err := pgxpool.NewWithConfig(
		context.Background(),
		adminConfig,
	)
	if err != nil {
		t.Fatal("cannot create test database pool")
	}
	t.Cleanup(adminPool.Close)

	if err := adminPool.Ping(context.Background()); err != nil {
		t.Fatal("cannot reach configured test database")
	}

	schema := fmt.Sprintf(
		"store_test_%d_%d",
		os.Getpid(),
		atomic.AddUint64(&storeSchemaSequence, 1),
	)
	_, err = adminPool.Exec(
		context.Background(),
		"CREATE SCHEMA "+
			pgx.Identifier{schema}.Sanitize(),
	)
	if err != nil {
		t.Fatalf("create isolated test schema: %v", err)
	}

	testConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema

	testPool, err := pgxpool.NewWithConfig(
		context.Background(),
		testConfig,
	)
	if err != nil {
		t.Fatal("cannot create isolated test database pool")
	}

	t.Cleanup(func() {
		testPool.Close()

		_, cleanupError := adminPool.Exec(
			context.Background(),
			"DROP SCHEMA "+
				pgx.Identifier{schema}.Sanitize()+
				" CASCADE",
		)
		if cleanupError != nil {
			t.Errorf(
				"drop isolated test schema: %v",
				cleanupError,
			)
		}
	})

	migrationFiles := []string{
		"migrations/0001_initial.sql",
		"migrations/0002_verification_queue.sql",
	}

	for _, migrationFile := range migrationFiles {
		migration, readErr := os.ReadFile(migrationFile)
		if readErr != nil {
			t.Fatalf(
				"read migration %q: %v",
				migrationFile,
				readErr,
			)
		}

		_, execErr := testPool.Exec(
			context.Background(),
			string(migration),
			pgx.QueryExecModeSimpleProtocol,
		)
		if execErr != nil {
			t.Fatalf(
				"apply migration %q: %v",
				migrationFile,
				execErr,
			)
		}
	}

	return testPool
}

func mustStoreOrigin(
	t *testing.T,
	rawURL string,
) origin.Origin {
	t.Helper()

	got, err := origin.Parse(rawURL)
	if err != nil {
		t.Fatalf("origin.Parse(%q) error = %v", rawURL, err)
	}

	return got
}

func assertStoreObservation(
	t *testing.T,
	got Observation,
	want Observation,
) {
	t.Helper()

	if got.Outcome != want.Outcome {
		t.Errorf(
			"observation outcome = %v, want %v",
			got.Outcome,
			want.Outcome,
		)
	}

	if !got.ObservedAt.Equal(want.ObservedAt) {
		t.Errorf(
			"observation time = %v, want %v",
			got.ObservedAt,
			want.ObservedAt,
		)
	}

	if got.Declaration != want.Declaration {
		t.Errorf(
			"observation declaration = %#v, want %#v",
			got.Declaration,
			want.Declaration,
		)
	}
}

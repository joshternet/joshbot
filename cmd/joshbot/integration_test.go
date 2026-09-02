package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/publicdata"
	"github.com/joshternet/joshbot/internal/store"
)

var cliSchemaSequence uint64

func TestRuntimeOperationsUsePostgreSQL(
	t *testing.T,
) {
	ctx := context.Background()
	operations, pool := newCLIIntegrationEnvironment(
		t,
	)

	if err := operations.health(ctx); err != nil {
		t.Fatalf(
			"health() error = %v, want nil",
			err,
		)
	}

	if err := operations.migrate(ctx); err != nil {
		t.Fatalf(
			"migrate() error = %v, want nil",
			err,
		)
	}

	source, err := origin.Parse(
		"https://EXAMPLE.com/path",
	)
	if err != nil {
		t.Fatalf(
			"Parse() error = %v, want nil",
			err,
		)
	}

	if err := operations.schedule(
		ctx,
		source,
	); err != nil {
		t.Fatalf(
			"schedule() error = %v, want nil",
			err,
		)
	}

	var storedOrigin string
	err = pool.QueryRow(
		ctx,
		`
			SELECT origin
			FROM verification_queue
		`,
	).Scan(&storedOrigin)
	if err != nil {
		t.Fatalf(
			"query scheduled origin: %v",
			err,
		)
	}

	if storedOrigin != "https://example.com" {
		t.Errorf(
			"scheduled origin = %q, want canonical origin",
			storedOrigin,
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
		t.Fatalf(
			"count observations: %v",
			err,
		)
	}

	if observationCount != 0 {
		t.Errorf(
			"observation count = %d, want 0",
			observationCount,
		)
	}

	databaseStore := store.New(pool)
	result := declaration.Result{
		Outcome: declaration.OutcomeValid,
		Origin:  source,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityAffirmed,
		},
	}
	if err := databaseStore.RecordVerification(
		ctx,
		time.Unix(1_800_000_000, 0).UTC(),
		result,
	); err != nil {
		t.Fatalf(
			"RecordVerification() error = %v, want nil",
			err,
		)
	}

	outputRoot := filepath.Join(
		t.TempDir(),
		"snapshot",
	)
	if err := operations.export(
		ctx,
		outputRoot,
	); err != nil {
		t.Fatalf(
			"export() error = %v, want nil",
			err,
		)
	}

	verified, err := databaseStore.VerifiedOrigins(
		ctx,
	)
	if err != nil {
		t.Fatalf(
			"VerifiedOrigins() error = %v, want nil",
			err,
		)
	}

	expectedFiles, err := publicdata.Build(verified)
	if err != nil {
		t.Fatalf(
			"Build() error = %v, want nil",
			err,
		)
	}

	for _, expected := range expectedFiles {
		actual, err := os.ReadFile(
			filepath.Join(
				outputRoot,
				filepath.FromSlash(expected.Path),
			),
		)
		if err != nil {
			t.Fatalf(
				"read exported file %q: %v",
				expected.Path,
				err,
			)
		}

		if !bytes.Equal(actual, expected.Data) {
			t.Errorf(
				"exported file %q does not match expected bytes",
				expected.Path,
			)
		}
	}

	workerContext, cancelWorker :=
		context.WithCancel(ctx)
	cancelWorker()

	if err := operations.worker(
		workerContext,
	); err != nil {
		t.Errorf(
			"worker() cancellation error = %v, want nil",
			err,
		)
	}
}

func newCLIIntegrationEnvironment(
	t *testing.T,
) (runtimeOperations, *pgxpool.Pool) {
	t.Helper()

	databaseURL := os.Getenv(
		"JOSHBOT_TEST_DATABASE_URL",
	)
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

	adminPool, err := pgxpool.New(
		context.Background(),
		databaseURL,
	)
	if err != nil {
		t.Fatal(
			"cannot create test database pool",
		)
	}
	t.Cleanup(adminPool.Close)

	if err := adminPool.Ping(
		context.Background(),
	); err != nil {
		t.Fatal(
			"cannot reach configured test database",
		)
	}

	schema := fmt.Sprintf(
		"cli_test_%d_%d",
		os.Getpid(),
		atomic.AddUint64(
			&cliSchemaSequence,
			1,
		),
	)
	_, err = adminPool.Exec(
		context.Background(),
		"CREATE SCHEMA "+
			pgx.Identifier{schema}.Sanitize(),
	)
	if err != nil {
		t.Fatalf(
			"create isolated CLI schema: %v",
			err,
		)
	}

	schemaURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal("invalid test database URL")
	}
	query := schemaURL.Query()
	query.Set("search_path", schema)
	schemaURL.RawQuery = query.Encode()

	runtimeURL := *schemaURL
	databasePassword := "integration-test-password"
	if runtimeURL.User != nil {
		username := runtimeURL.User.Username()
		if password, ok := runtimeURL.User.Password(); ok {
			databasePassword = password
			runtimeURL.User = url.User(username)
		}
	}

	testPool, err := pgxpool.New(
		context.Background(),
		schemaURL.String(),
	)
	if err != nil {
		t.Fatal(
			"cannot create isolated CLI database pool",
		)
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
				"drop isolated CLI schema: %v",
				cleanupError,
			)
		}
	})

	passwordFile := filepath.Join(
		t.TempDir(),
		"database-password",
	)
	if err := os.WriteFile(
		passwordFile,
		[]byte(databasePassword+"\n"),
		0o600,
	); err != nil {
		t.Fatalf(
			"write integration password file: %v",
			err,
		)
	}

	environment := mapEnvironment{
		databaseURLEnvironment:          runtimeURL.String(),
		databasePasswordFileEnvironment: passwordFile,
		workerIDEnvironment:             "integration-worker",
	}

	operations := newRuntimeOperations(io.Discard)
	operations.getenv = environment.get

	return operations, testPool
}

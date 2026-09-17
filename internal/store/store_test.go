package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
)

var (
	storeSchemaSequence    uint64
	storeAdminPoolOnce     sync.Once
	storeAdminPool         *pgxpool.Pool
	storeAdminPoolError    error
	parallelStoreTests     sync.Map
	storeMigrationsOnce    sync.Once
	storeMigrationsSQL     string
	storeMigrationsErr     error
	storeTemplateOnce      sync.Once
	storeTemplateErr       error
	storeMigrationTemplate = "store_migration_template"
	storeSchemaCloneMutex  sync.Mutex
)

const storeTestMaxConnections = 4

func TestMain(testingMain *testing.M) {
	exitCode := testingMain.Run()
	if storeAdminPool != nil {
		storeAdminPool.Close()
	}
	os.Exit(exitCode)
}

func TestStoreRecordsValidAffirmedVerification(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	memory := New(pool)

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

	if err := memory.RecordVerification(
		ctx,
		observedAt,
		result,
	); err != nil {
		t.Fatalf(
			"RecordVerification() error = %v, want nil",
			err,
		)
	}

	got, found, err := memory.OriginState(ctx, source)
	if err != nil {
		t.Fatalf(
			"OriginState() error = %v, want nil",
			err,
		)
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

	wantObservation := Observation{
		Outcome:    declaration.OutcomeValid,
		ObservedAt: observedAt,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityAffirmed,
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
			"stored outcome = %q, want valid",
			storedOutcome,
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
			"stored identity = %q, want affirmed",
			storedIdentity,
		)
	}
}

func newStoreTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	markStoreTestParallel(t)
	return newIsolatedStoreTestPool(t, storeTestMaxConnections)
}

func newSerialStoreTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return newIsolatedStoreTestPool(t, storeTestMaxConnections)
}

func newConcurrentStoreTestPool(
	t *testing.T,
	maxConnections int32,
) *pgxpool.Pool {
	t.Helper()
	return newIsolatedStoreTestPool(t, maxConnections)
}

func newIsolatedStoreTestPool(
	t *testing.T,
	maxConnections int32,
) *pgxpool.Pool {
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

	adminPool := sharedStoreAdminPool(t, databaseURL)
	ensureStoreMigrationTemplate(t, adminPool)

	schema := fmt.Sprintf(
		"store_test_%d_%d",
		os.Getpid(),
		atomic.AddUint64(
			&storeSchemaSequence,
			1,
		),
	)
	cloneStoreMigrationSchema(t, adminPool, schema)
	t.Cleanup(func() {
		_, cleanupErr := adminPool.Exec(
			context.Background(),
			"DROP SCHEMA "+
				pgx.Identifier{schema}.Sanitize()+
				" CASCADE",
		)
		if cleanupErr != nil {
			t.Errorf(
				"drop isolated test schema: %v",
				cleanupErr,
			)
		}
	})

	testConfig := adminPool.Config()
	testConfig.ConnConfig.RuntimeParams["search_path"] =
		schema
	testConfig.MaxConns = maxConnections
	testConfig.MinConns = 0

	testPool, err := pgxpool.NewWithConfig(
		context.Background(),
		testConfig,
	)
	if err != nil {
		t.Fatal(
			"cannot create isolated test database pool",
		)
	}
	t.Cleanup(testPool.Close)

	return testPool
}

func ensureStoreMigrationTemplate(
	t *testing.T,
	adminPool *pgxpool.Pool,
) {
	t.Helper()

	storeTemplateOnce.Do(func() {
		ctx := context.Background()
		template := pgx.Identifier{storeMigrationTemplate}.Sanitize()
		if _, err := adminPool.Exec(
			ctx,
			"DROP SCHEMA IF EXISTS "+template+" CASCADE",
		); err != nil {
			storeTemplateErr = fmt.Errorf(
				"drop store migration template: %w",
				err,
			)
			return
		}
		if _, err := adminPool.Exec(
			ctx,
			"CREATE SCHEMA "+template,
		); err != nil {
			storeTemplateErr = fmt.Errorf(
				"create store migration template: %w",
				err,
			)
			return
		}

		templateConfig := adminPool.Config()
		templateConfig.ConnConfig.RuntimeParams["search_path"] =
			storeMigrationTemplate
		templateConfig.MaxConns = 2
		templateConfig.MinConns = 0
		templatePool, err := pgxpool.NewWithConfig(
			ctx,
			templateConfig,
		)
		if err != nil {
			storeTemplateErr = fmt.Errorf(
				"open store migration template pool: %w",
				err,
			)
			return
		}
		defer templatePool.Close()

		migrations, err := loadStoreTestMigrations()
		if err != nil {
			storeTemplateErr = err
			return
		}
		if _, err := templatePool.Exec(
			ctx,
			migrations,
			pgx.QueryExecModeSimpleProtocol,
		); err != nil {
			storeTemplateErr = fmt.Errorf(
				"migrate store migration template: %w",
				err,
			)
			return
		}

		if _, err := adminPool.Exec(
			ctx,
			`
CREATE OR REPLACE FUNCTION store_migration_template.clone_to(destination text)
RETURNS void
LANGUAGE plpgsql
AS $clone$
DECLARE
	source constant text := 'store_migration_template';
	table_row record;
	constraint_row record;
	definition text;
BEGIN
	EXECUTE format('CREATE SCHEMA %I', destination);

	FOR table_row IN
		SELECT c.relname AS table_name
		FROM pg_class AS c
		JOIN pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = source
			AND c.relkind = 'r'
		ORDER BY c.relname
	LOOP
		EXECUTE format(
			'CREATE TABLE %I.%I (LIKE %I.%I INCLUDING DEFAULTS INCLUDING GENERATED INCLUDING IDENTITY INCLUDING COMMENTS INCLUDING STATISTICS INCLUDING CONSTRAINTS)',
			destination,
			table_row.table_name,
			source,
			table_row.table_name
		);
	END LOOP;

	FOR constraint_row IN
		SELECT
			rel.relname AS table_name,
			constraint_def.conname AS constraint_name,
			pg_get_constraintdef(constraint_def.oid) AS definition
		FROM pg_constraint AS constraint_def
		JOIN pg_class AS rel ON rel.oid = constraint_def.conrelid
		JOIN pg_namespace AS n ON n.oid = constraint_def.connamespace
		WHERE n.nspname = source
			AND constraint_def.contype IN ('p', 'u', 'f', 'x')
		ORDER BY
			CASE constraint_def.contype
				WHEN 'p' THEN 0
				WHEN 'u' THEN 1
				WHEN 'x' THEN 2
				ELSE 3
			END,
			rel.relname,
			constraint_def.conname
	LOOP
		definition := replace(
			replace(
				constraint_row.definition,
				source || '.',
				destination || '.'
			),
			'"' || source || '".',
			'"' || destination || '".'
		);
		EXECUTE format(
			'ALTER TABLE %I.%I ADD CONSTRAINT %I %s',
			destination,
			constraint_row.table_name,
			constraint_row.constraint_name,
			definition
		);
	END LOOP;

	FOR table_row IN
		SELECT pg_get_indexdef(i.indexrelid) AS definition
		FROM pg_index AS i
		JOIN pg_class AS index_class ON index_class.oid = i.indexrelid
		JOIN pg_class AS table_class ON table_class.oid = i.indrelid
		JOIN pg_namespace AS n ON n.oid = table_class.relnamespace
		WHERE n.nspname = source
			AND NOT EXISTS (
				SELECT 1
				FROM pg_constraint AS constraint_def
				WHERE constraint_def.conindid = i.indexrelid
			)
		ORDER BY index_class.relname
	LOOP
		definition := replace(
			replace(
				table_row.definition,
				source || '.',
				destination || '.'
			),
			'"' || source || '".',
			'"' || destination || '".'
		);
		EXECUTE definition;
	END LOOP;

	PERFORM set_config('search_path', destination, true);

	FOR table_row IN
		SELECT pg_get_functiondef(p.oid) AS definition
		FROM pg_proc AS p
		JOIN pg_namespace AS n ON n.oid = p.pronamespace
		WHERE n.nspname = source
		ORDER BY p.proname
	LOOP
		definition := replace(
			replace(
				table_row.definition,
				source || '.',
				destination || '.'
			),
			'"' || source || '".',
			'"' || destination || '".'
		);
		EXECUTE definition;
	END LOOP;

	FOR table_row IN
		SELECT pg_get_triggerdef(t.oid) AS definition
		FROM pg_trigger AS t
		JOIN pg_class AS rel ON rel.oid = t.tgrelid
		JOIN pg_namespace AS n ON n.oid = rel.relnamespace
		WHERE n.nspname = source
			AND NOT t.tgisinternal
		ORDER BY t.tgname
	LOOP
		definition := replace(
			replace(
				table_row.definition,
				source || '.',
				destination || '.'
			),
			'"' || source || '".',
			'"' || destination || '".'
		);
		EXECUTE definition;
	END LOOP;

	EXECUTE format(
		'INSERT INTO %I.crawl_control SELECT * FROM %I.crawl_control',
		destination,
		source
	);
	EXECUTE format(
		'INSERT INTO %I.crawl_domain_avoid_rules SELECT * FROM %I.crawl_domain_avoid_rules',
		destination,
		source
	);
END;
$clone$;
`,
		); err != nil {
			storeTemplateErr = fmt.Errorf(
				"install store migration clone function: %w",
				err,
			)
		}
	})
	if storeTemplateErr != nil {
		t.Fatal(storeTemplateErr)
	}
}

func cloneStoreMigrationSchema(
	t *testing.T,
	adminPool *pgxpool.Pool,
	destination string,
) {
	t.Helper()

	// Serialize clones: parallel CREATE TABLE LIKE against one template
	// stampedes the catalog under -race and blows the package timeout.
	storeSchemaCloneMutex.Lock()
	defer storeSchemaCloneMutex.Unlock()

	ctx := context.Background()
	if _, err := adminPool.Exec(
		ctx,
		"SELECT store_migration_template.clone_to($1)",
		destination,
	); err != nil {
		t.Fatalf("clone store migration schema: %v", err)
	}
}

func storeTestMigrations(t *testing.T) string {
	t.Helper()
	migrations, err := loadStoreTestMigrations()
	if err != nil {
		t.Fatal(err)
	}
	return migrations
}

func loadStoreTestMigrations() (string, error) {
	storeMigrationsOnce.Do(func() {
		migrationFiles := []string{
			"migrations/0001_initial.sql",
			"migrations/0002_verification_queue.sql",
			"migrations/0003_discovery.sql",
			"migrations/0004_crawl_sources.sql",
			"migrations/0005_automatic_crawl_sources.sql",
			"migrations/0006_crawl_observability.sql",
			"migrations/0007_crawl_observability_permissions.sql",
			"migrations/0008_crawl_domain_avoid_rules.sql",
			"migrations/0009_automatic_admission.sql",
			"migrations/0010_retry_state.sql",
			"migrations/0011_operator_audit_events.sql",
			"migrations/0012_observability_reporting.sql",
		}
		var migrations strings.Builder
		for _, migrationFile := range migrationFiles {
			migration, err := os.ReadFile(migrationFile)
			if err != nil {
				storeMigrationsErr = fmt.Errorf(
					"read migration %q: %w",
					migrationFile,
					err,
				)
				return
			}
			migrations.Write(migration)
			migrations.WriteByte('\n')
		}
		storeMigrationsSQL = migrations.String()
	})
	if storeMigrationsErr != nil {
		return "", storeMigrationsErr
	}
	return storeMigrationsSQL, nil
}

func markStoreTestParallel(t *testing.T) {
	t.Helper()
	if _, loaded := parallelStoreTests.LoadOrStore(t, struct{}{}); loaded {
		return
	}
	t.Cleanup(func() {
		parallelStoreTests.Delete(t)
	})
	t.Parallel()
}

func sharedStoreAdminPool(
	t *testing.T,
	databaseURL string,
) *pgxpool.Pool {
	t.Helper()
	storeAdminPoolOnce.Do(func() {
		adminConfig, err := pgxpool.ParseConfig(databaseURL)
		if err != nil {
			storeAdminPoolError = fmt.Errorf(
				"invalid test database configuration: %w",
				err,
			)
			return
		}
		adminConfig.MaxConns = 8
		adminConfig.MinConns = 0
		storeAdminPool, err = pgxpool.NewWithConfig(
			context.Background(),
			adminConfig,
		)
		if err != nil {
			storeAdminPoolError = fmt.Errorf(
				"create shared test database pool: %w",
				err,
			)
			return
		}
		if err := storeAdminPool.Ping(context.Background()); err != nil {
			storeAdminPool.Close()
			storeAdminPool = nil
			storeAdminPoolError = fmt.Errorf(
				"reach configured test database: %w",
				err,
			)
		}
	})
	if storeAdminPoolError != nil {
		t.Fatal(storeAdminPoolError)
	}
	return storeAdminPool
}

func mustStoreOrigin(
	t *testing.T,
	rawURL string,
) origin.Origin {
	t.Helper()

	got, err := origin.Parse(rawURL)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			rawURL,
			err,
		)
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

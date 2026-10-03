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
	storeAdminPoolOnce      sync.Once
	storeAdminPool          *pgxpool.Pool
	storeAdminPoolError     error
	parallelStoreTests      sync.Map
	storeMigrationsOnce     sync.Once
	storeMigrationsSQL      string
	storeMigrationsErr      error
	storeTemplateOnce       sync.Once
	storeTemplateErr        error
	storeMigrationTemplate  = "store_migration_template"
	storeSchemaSequence     uint64
	createdSchemasMu        sync.Mutex
	createdSchemas          []string
	reusableSchemas         chan string
	schemaRebuildMu         sync.Mutex
	templateTableNames      string
	templateIndexNames      string
	templateTriggerNames    string
	templateConstraintNames string
)

// storeSchemaPoolSize is one schema per store test the quality gate
// runs at once. The gate passes -parallel 4 for this package. A test
// that changes the schema returns it restored, so the pool does not
// keep a spare copy for every test.
const storeSchemaPoolSize = 4

const storeTestMaxConnections = 4

func TestMain(testingMain *testing.M) {
	if os.Getenv("JOSHBOT_TEST_DATABASE_URL") != "" {
		if err := buildReusableStoreSchemas(); err != nil {
			fmt.Fprintf(os.Stderr, "store schema pool: %v\n", err)
			os.Exit(1)
		}
	}
	exitCode := testingMain.Run()
	dropCreatedStoreSchemas()
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
	return openReusableSchema(t, storeTestMaxConnections)
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
	return openReusableSchema(t, maxConnections)
}

func prepareIsolatedSchema(t *testing.T) (*pgxpool.Pool, string) {
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
	return adminPool, prepareSchemaName(t, adminPool)
}

func prepareSchemaName(
	t *testing.T,
	adminPool *pgxpool.Pool,
) string {
	t.Helper()

	schema := fmt.Sprintf(
		"store_test_%d_%d",
		os.Getpid(),
		atomic.AddUint64(&storeSchemaSequence, 1),
	)
	if _, err := adminPool.Exec(
		context.Background(),
		"SELECT store_migration_template.clone_to($1)",
		schema,
	); err != nil {
		t.Fatalf("clone store migration schema: %v", err)
	}

	createdSchemasMu.Lock()
	createdSchemas = append(createdSchemas, schema)
	createdSchemasMu.Unlock()
	return schema
}

func connectIsolatedSchema(
	t *testing.T,
	adminPool *pgxpool.Pool,
	schema string,
	maxConnections int32,
) *pgxpool.Pool {
	t.Helper()

	testConfig := adminPool.Config()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
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

func dropCreatedStoreSchemas() {
	if storeAdminPool == nil {
		return
	}

	createdSchemasMu.Lock()
	names := append([]string(nil), createdSchemas...)
	createdSchemasMu.Unlock()
	dropStoreSchemas(names)
}

func dropStoreSchemas(names []string) {
	if storeAdminPool == nil || len(names) == 0 {
		return
	}

	var statement strings.Builder
	statement.WriteString("DROP SCHEMA IF EXISTS ")
	for index, schema := range names {
		if index > 0 {
			statement.WriteString(", ")
		}
		statement.WriteString(pgx.Identifier{schema}.Sanitize())
	}
	statement.WriteString(" CASCADE")
	_, _ = storeAdminPool.Exec(context.Background(), statement.String())
}

func ensureStoreMigrationTemplate(
	t *testing.T,
	adminPool *pgxpool.Pool,
) {
	t.Helper()
	if err := installStoreTemplate(adminPool); err != nil {
		t.Fatal(err)
	}
}

func installStoreTemplate(adminPool *pgxpool.Pool) error {

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
CREATE OR REPLACE FUNCTION store_migration_template.copy_into(destination text)
RETURNS void
LANGUAGE plpgsql
AS $copy$
DECLARE
	source constant text := 'store_migration_template';
	table_row record;
	constraint_row record;
	definition text;
BEGIN
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
$copy$;

CREATE OR REPLACE FUNCTION store_migration_template.clone_to(destination text)
RETURNS void
LANGUAGE plpgsql
AS $clone$
BEGIN
	EXECUTE format('CREATE SCHEMA %I', destination);
	PERFORM store_migration_template.copy_into(destination);
END;
$clone$;

CREATE OR REPLACE FUNCTION store_migration_template.rebuild_clone(destination text)
RETURNS void
LANGUAGE plpgsql
AS $rebuild$
DECLARE
	object_row record;
BEGIN
	FOR object_row IN
		SELECT c.relname
		FROM pg_class AS c
		JOIN pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = destination
			AND c.relkind = 'v'
	LOOP
		EXECUTE format(
			'DROP VIEW IF EXISTS %I.%I CASCADE',
			destination,
			object_row.relname
		);
	END LOOP;

	FOR object_row IN
		SELECT c.relname
		FROM pg_class AS c
		JOIN pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = destination
			AND c.relkind = 'r'
	LOOP
		EXECUTE format(
			'DROP TABLE IF EXISTS %I.%I CASCADE',
			destination,
			object_row.relname
		);
	END LOOP;

	FOR object_row IN
		SELECT c.relname
		FROM pg_class AS c
		JOIN pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = destination
			AND c.relkind = 'S'
	LOOP
		EXECUTE format(
			'DROP SEQUENCE IF EXISTS %I.%I CASCADE',
			destination,
			object_row.relname
		);
	END LOOP;

	FOR object_row IN
		SELECT p.oid::regprocedure::text AS signature
		FROM pg_proc AS p
		JOIN pg_namespace AS n ON n.oid = p.pronamespace
		WHERE n.nspname = destination
	LOOP
		EXECUTE format('DROP FUNCTION %s CASCADE', object_row.signature);
	END LOOP;

	PERFORM store_migration_template.copy_into(destination);
END;
$rebuild$;

CREATE OR REPLACE FUNCTION store_migration_template.reset_clone(destination text)
RETURNS void
LANGUAGE plpgsql
AS $reset$
DECLARE
	tables text;
BEGIN
	SELECT string_agg(
		format('%I.%I', destination, c.relname),
		', '
		ORDER BY c.relname
	)
	INTO tables
	FROM pg_class AS c
	JOIN pg_namespace AS n ON n.oid = c.relnamespace
	WHERE n.nspname = destination
		AND c.relkind = 'r';

	IF tables IS NULL THEN
		RAISE EXCEPTION 'store test schema % has no tables', destination;
	END IF;

	EXECUTE 'TRUNCATE TABLE ' || tables || ' RESTART IDENTITY CASCADE';
	EXECUTE format(
		'INSERT INTO %I.crawl_control SELECT * FROM store_migration_template.crawl_control',
		destination
	);
	EXECUTE format(
		'INSERT INTO %I.crawl_domain_avoid_rules SELECT * FROM store_migration_template.crawl_domain_avoid_rules',
		destination
	);
END;
$reset$;
`,
		); err != nil {
			storeTemplateErr = fmt.Errorf(
				"install store migration clone function: %w",
				err,
			)
		}
	})
	return storeTemplateErr
}

func loadStoreTestMigrations() (string, error) {
	storeMigrationsOnce.Do(func() {
		migrationSet, err := loadMigrations(
			embeddedMigrations,
		)
		if err != nil {
			storeMigrationsErr = fmt.Errorf(
				"load store test migrations: %w",
				err,
			)
			return
		}

		var migrations strings.Builder

		for _, migration := range migrationSet {
			migrations.Write(migration.data)
			migrations.WriteByte('\n')
		}

		storeMigrationsSQL = migrations.String()
	})

	if storeMigrationsErr != nil {
		return "", storeMigrationsErr
	}

	return storeMigrationsSQL, nil
}

func buildReusableStoreSchemas() error {
	if err := openSharedAdmin(
		os.Getenv("JOSHBOT_TEST_DATABASE_URL"),
	); err != nil {
		return err
	}
	if err := dropLeftoverStoreSchemas(); err != nil {
		return err
	}
	if err := installStoreTemplate(storeAdminPool); err != nil {
		return err
	}

	reusableSchemas = make(chan string, storeSchemaPoolSize)
	jobs := make(chan int, storeSchemaPoolSize)
	for index := 0; index < storeSchemaPoolSize; index++ {
		jobs <- index
	}
	close(jobs)

	var (
		waitGroup sync.WaitGroup
		errOnce   sync.Once
		buildErr  error
	)
	recordErr := func(err error) {
		errOnce.Do(func() {
			buildErr = err
		})
	}
	for worker := 0; worker < 1; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for index := range jobs {
				if buildErr != nil {
					return
				}
				name := fmt.Sprintf(
					"store_pool_%d_%d",
					os.Getpid(),
					index,
				)
				if _, err := storeAdminPool.Exec(
					context.Background(),
					"SELECT store_migration_template.clone_to($1)",
					name,
				); err != nil {
					recordErr(fmt.Errorf(
						"prepare isolated test schema: %w",
						err,
					))
					return
				}
				createdSchemasMu.Lock()
				createdSchemas = append(createdSchemas, name)
				createdSchemasMu.Unlock()
				reusableSchemas <- name
			}
		}()
	}
	waitGroup.Wait()
	if buildErr != nil {
		return buildErr
	}
	return snapshotTemplateSchemaCounts()
}

func openReusableSchema(
	t *testing.T,
	maxConnections int32,
) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("JOSHBOT_TEST_DATABASE_URL") == "" {
		if os.Getenv("JOSHBOT_REQUIRE_DATABASE_TESTS") == "1" {
			t.Fatal("JOSHBOT_TEST_DATABASE_URL is required")
		}
		t.Skip("JOSHBOT_TEST_DATABASE_URL is not configured")
	}

	schema := <-reusableSchemas
	t.Cleanup(func() {
		if !schemaMatchesTemplate(schema) {
			schemaRebuildMu.Lock()
			err := rebuildStoreSchema(storeAdminPool, schema)
			schemaRebuildMu.Unlock()
			if err != nil || !schemaMatchesTemplate(schema) {
				fmt.Fprintf(
					os.Stderr,
					"restore isolated test schema %s: %v\n",
					schema,
					err,
				)
				os.Exit(1)
			}
		} else if err := resetStoreSchema(storeAdminPool, schema); err != nil {
			fmt.Fprintf(
				os.Stderr,
				"reset isolated test schema %s: %v\n",
				schema,
				err,
			)
			os.Exit(1)
		}
		reusableSchemas <- schema
	})
	return connectIsolatedSchema(
		t,
		storeAdminPool,
		schema,
		maxConnections,
	)
}

func snapshotTemplateSchemaCounts() error {
	var err error
	templateTableNames,
		templateIndexNames,
		templateTriggerNames,
		templateConstraintNames,
		err = schemaObjectNames(storeMigrationTemplate)
	return err
}

func schemaObjectNames(
	schema string,
) (tables string, indexes string, triggers string, constraints string, err error) {
	err = storeAdminPool.QueryRow(
		context.Background(),
		`
			SELECT
				(
					SELECT COALESCE(string_agg(c.relname, ',' ORDER BY c.relname), '')
					FROM pg_class AS c
					JOIN pg_namespace AS n ON n.oid = c.relnamespace
					WHERE n.nspname = $1
						AND c.relkind = 'r'
				),
				(
					SELECT COALESCE(string_agg(index_class.relname, ',' ORDER BY index_class.relname), '')
					FROM pg_index AS i
					JOIN pg_class AS index_class ON index_class.oid = i.indexrelid
					JOIN pg_class AS table_class ON table_class.oid = i.indrelid
					JOIN pg_namespace AS n ON n.oid = table_class.relnamespace
					WHERE n.nspname = $1
				),
				(
					SELECT COALESCE(string_agg(trigger_row.tgname, ',' ORDER BY trigger_row.tgname), '')
					FROM pg_trigger AS trigger_row
					JOIN pg_class AS c ON c.oid = trigger_row.tgrelid
					JOIN pg_namespace AS n ON n.oid = c.relnamespace
					WHERE n.nspname = $1
						AND NOT trigger_row.tgisinternal
				),
				(
					SELECT COALESCE(string_agg(constraint_row.conname, ',' ORDER BY constraint_row.conname), '')
					FROM pg_constraint AS constraint_row
					JOIN pg_namespace AS n
						ON n.oid = constraint_row.connamespace
					WHERE n.nspname = $1
				)
		`,
		schema,
	).Scan(&tables, &indexes, &triggers, &constraints)
	return tables, indexes, triggers, constraints, err
}

func schemaMatchesTemplate(schema string) bool {
	tables, indexes, triggers, constraints, err := schemaObjectNames(schema)
	if err != nil {
		return false
	}
	return tables == templateTableNames &&
		indexes == templateIndexNames &&
		triggers == templateTriggerNames &&
		constraints == templateConstraintNames
}

func dropLeftoverStoreSchemas() error {
	rows, err := storeAdminPool.Query(
		context.Background(),
		`
			SELECT nspname
			FROM pg_namespace
			WHERE nspname LIKE 'store_pool_%'
				OR nspname LIKE 'store_test_%'
			ORDER BY nspname
		`,
	)
	if err != nil {
		return fmt.Errorf("list leftover store test schemas: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("read leftover store test schema: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list leftover store test schemas: %w", err)
	}
	dropStoreSchemas(names)
	return nil
}

func rebuildStoreSchema(adminPool *pgxpool.Pool, schema string) error {
	_, err := adminPool.Exec(
		context.Background(),
		"SELECT store_migration_template.rebuild_clone($1)",
		schema,
	)
	if err != nil {
		return fmt.Errorf("rebuild schema %s: %w", schema, err)
	}
	return nil
}

func resetStoreSchema(adminPool *pgxpool.Pool, schema string) error {
	_, err := adminPool.Exec(
		context.Background(),
		"SELECT store_migration_template.reset_clone($1)",
		schema,
	)
	if err != nil {
		return fmt.Errorf("reset schema %s: %w", schema, err)
	}
	return nil
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
	if err := openSharedAdmin(databaseURL); err != nil {
		t.Fatal(err)
	}
	return storeAdminPool
}

func openSharedAdmin(databaseURL string) error {
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
	return storeAdminPoolError
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

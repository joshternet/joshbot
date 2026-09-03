package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var emptyStoreSchemaSequence uint64

func TestMigrateAppliesEmbeddedMigrations(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate() error = %v, want nil", err)
	}

	var tableCount int
	err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM information_schema.tables
			WHERE table_schema = current_schema()
				AND table_name IN (
					'origins',
					'verification_observations',
					'verification_queue',
					'schema_migrations'
				)
		`,
	).Scan(&tableCount)
	if err != nil {
		t.Fatalf("count migrated tables: %v", err)
	}

	if tableCount != 4 {
		t.Errorf(
			"migrated table count = %d, want 4",
			tableCount,
		)
	}

	migrations, err := loadMigrations(
		embeddedMigrations,
	)
	if err != nil {
		t.Fatalf(
			"loadMigrations() error = %v, want nil",
			err,
		)
	}

	rows, err := pool.Query(
		ctx,
		`
			SELECT
				version,
				name,
				checksum
			FROM schema_migrations
			ORDER BY version
		`,
	)
	if err != nil {
		t.Fatalf("query migration records: %v", err)
	}
	defer rows.Close()

	index := 0
	for rows.Next() {
		var (
			version  int64
			name     string
			checksum string
		)
		if err := rows.Scan(
			&version,
			&name,
			&checksum,
		); err != nil {
			t.Fatalf(
				"scan migration record: %v",
				err,
			)
		}

		if index >= len(migrations) {
			t.Fatal(
				"more migration records than embedded migrations",
			)
		}

		want := migrations[index]
		if version != want.version {
			t.Errorf(
				"version = %d, want %d",
				version,
				want.version,
			)
		}

		if name != want.name {
			t.Errorf(
				"name = %q, want %q",
				name,
				want.name,
			)
		}

		if checksum != want.checksum {
			t.Errorf(
				"checksum = %q, want %q",
				checksum,
				want.checksum,
			)
		}

		index++
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration records: %v", err)
	}

	if index != len(migrations) {
		t.Errorf(
			"migration record count = %d, want %d",
			index,
			len(migrations),
		)
	}
}

func TestMigrateRerunIsNoOp(t *testing.T) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf(
			"first Migrate() error = %v, want nil",
			err,
		)
	}

	first := migrationAppliedTimes(t, ctx, pool)

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf(
			"second Migrate() error = %v, want nil",
			err,
		)
	}

	second := migrationAppliedTimes(t, ctx, pool)

	if len(second) != len(first) {
		t.Fatalf(
			"migration count = %d, want %d",
			len(second),
			len(first),
		)
	}

	for version, firstAppliedAt := range first {
		secondAppliedAt, exists := second[version]
		if !exists {
			t.Errorf(
				"migration version %d disappeared",
				version,
			)

			continue
		}

		if !secondAppliedAt.Equal(firstAppliedAt) {
			t.Errorf(
				"migration %d applied_at = %v, want %v",
				version,
				secondAppliedAt,
				firstAppliedAt,
			)
		}
	}
}

func TestMigrateSerializesConcurrentInvocations(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	const invocationCount = 4

	results := make(chan error, invocationCount)
	var group sync.WaitGroup

	for range invocationCount {
		group.Add(1)

		go func() {
			defer group.Done()
			results <- Migrate(ctx, pool)
		}()
	}

	group.Wait()
	close(results)

	for err := range results {
		if err != nil {
			t.Errorf(
				"concurrent Migrate() error = %v, want nil",
				err,
			)
		}
	}

	var count int
	err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM schema_migrations",
	).Scan(&count)
	if err != nil {
		t.Fatalf("count migration records: %v", err)
	}

	if count != 3 {
		t.Errorf(
			"migration record count = %d, want 3",
			count,
		)
	}
}

func TestMigrateRejectsChecksumDrift(t *testing.T) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	original := fstest.MapFS{
		"migrations/0001_test.sql": {
			Data: []byte(
				"CREATE TABLE migration_test (id BIGINT);",
			),
		},
	}
	if err := migrate(
		ctx,
		pool,
		original,
	); err != nil {
		t.Fatalf(
			"initial migrate() error = %v, want nil",
			err,
		)
	}

	changed := fstest.MapFS{
		"migrations/0001_test.sql": {
			Data: []byte(
				"CREATE TABLE migration_test (id TEXT);",
			),
		},
	}
	err := migrate(ctx, pool, changed)
	if !errors.Is(err, ErrMigrationDrift) {
		t.Errorf(
			"changed migrate() error = %v, want ErrMigrationDrift",
			err,
		)
	}
}

func TestMigrateRejectsUnknownAppliedVersion(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)
	source := oneTestMigration()

	if err := migrate(
		ctx,
		pool,
		source,
	); err != nil {
		t.Fatalf(
			"initial migrate() error = %v, want nil",
			err,
		)
	}

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO schema_migrations (
				version,
				name,
				checksum,
				applied_at
			)
			VALUES (99, '0099_unknown.sql', 'unknown', $1)
		`,
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf(
			"insert unknown migration: %v",
			err,
		)
	}

	err = migrate(ctx, pool, source)
	if !errors.Is(err, ErrMigrationDrift) {
		t.Errorf(
			"migrate() error = %v, want ErrMigrationDrift",
			err,
		)
	}
}

func TestMigrateRollsBackFailedMigrationSet(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	source := fstest.MapFS{
		"migrations/0001_valid.sql": {
			Data: []byte(
				"CREATE TABLE first_migration_object (id BIGINT);",
			),
		},
		"migrations/0002_invalid.sql": {
			Data: []byte(`
				CREATE TABLE second_migration_object (
					id BIGINT
				);
				SELECT missing_migration_function();
			`),
		},
	}

	err := migrate(ctx, pool, source)
	if err == nil {
		t.Fatal("migrate() error = nil, want non-nil")
	}

	for _, table := range []string{
		"first_migration_object",
		"second_migration_object",
		"schema_migrations",
	} {
		var exists bool
		err := pool.QueryRow(
			ctx,
			`
				SELECT to_regclass($1) IS NOT NULL
			`,
			table,
		).Scan(&exists)
		if err != nil {
			t.Fatalf(
				"query table %q: %v",
				table,
				err,
			)
		}

		if exists {
			t.Errorf(
				"table %q exists after rollback",
				table,
			)
		}
	}
}

func TestMigrateRollsBackMetadataInsertFailure(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	_, err := pool.Exec(
		ctx,
		`
			CREATE TABLE schema_migrations (
				version BIGINT PRIMARY KEY,
				name TEXT NOT NULL UNIQUE,
				checksum TEXT NOT NULL,
				applied_at TIMESTAMPTZ NOT NULL,
				CONSTRAINT reject_test_version
					CHECK (version < 0)
			)
		`,
	)
	if err != nil {
		t.Fatalf(
			"create constrained metadata table: %v",
			err,
		)
	}

	err = migrate(ctx, pool, oneTestMigration())
	if err == nil {
		t.Fatal("migrate() error = nil, want non-nil")
	}

	var exists bool
	err = pool.QueryRow(
		ctx,
		`
			SELECT to_regclass(
				'migration_test'
			) IS NOT NULL
		`,
	).Scan(&exists)
	if err != nil {
		t.Fatalf(
			"query migration test table: %v",
			err,
		)
	}

	if exists {
		t.Error(
			"migration_test exists after metadata rollback",
		)
	}

	var count int
	err = pool.QueryRow(
		ctx,
		"SELECT count(*) FROM schema_migrations",
	).Scan(&count)
	if err != nil {
		t.Fatalf(
			"count migration records: %v",
			err,
		)
	}

	if count != 0 {
		t.Errorf(
			"migration record count = %d, want 0",
			count,
		)
	}
}

func TestMigrateRejectsInvalidMetadataSchema(
	t *testing.T,
) {
	tests := []struct {
		name   string
		schema string
	}{
		{
			name: "query failure",
			schema: `
				CREATE TABLE schema_migrations (
					version BIGINT PRIMARY KEY
				)
			`,
		},
		{
			name: "scan failure",
			schema: `
				CREATE TABLE schema_migrations (
					version TEXT PRIMARY KEY,
					name TEXT NOT NULL,
					checksum TEXT NOT NULL,
					applied_at TIMESTAMPTZ NOT NULL
				);
				INSERT INTO schema_migrations (
					version,
					name,
					checksum,
					applied_at
				)
				VALUES (
					'not-a-number',
					'0001_test.sql',
					'checksum',
					clock_timestamp()
				)
			`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := newEmptyStoreTestPool(t)

			_, err := pool.Exec(
				ctx,
				test.schema,
				pgx.QueryExecModeSimpleProtocol,
			)
			if err != nil {
				t.Fatalf(
					"create invalid metadata schema: %v",
					err,
				)
			}

			if err := migrate(
				ctx,
				pool,
				oneTestMigration(),
			); err == nil {
				t.Error(
					"migrate() error = nil, want non-nil",
				)
			}
		})
	}
}

func TestMigrateValidatesInputs(t *testing.T) {
	pool := newEmptyStoreTestPool(t)

	var nilContext context.Context
	if err := Migrate(
		nilContext,
		pool,
	); !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"Migrate(nil context) error = %v, want errInvalidContext",
			err,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := Migrate(
		ctx,
		pool,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Migrate(canceled context) error = %v, want context.Canceled",
			err,
		)
	}

	if err := Migrate(
		context.Background(),
		nil,
	); !errors.Is(err, errPoolUnavailable) {
		t.Errorf(
			"Migrate(nil pool) error = %v, want errPoolUnavailable",
			err,
		)
	}

	if err := migrate(
		context.Background(),
		pool,
		nil,
	); !errors.Is(
		err,
		errMigrationSourceUnavailable,
	) {
		t.Errorf(
			"migrate(nil source) error = %v, want errMigrationSourceUnavailable",
			err,
		)
	}

	pool.Close()
	if err := Migrate(
		context.Background(),
		pool,
	); err == nil {
		t.Error(
			"Migrate(closed pool) error = nil, want non-nil",
		)
	}
}

func TestMigratePreservesMigrationLoadFailure(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	err := migrate(ctx, pool, fstest.MapFS{})
	if !errors.Is(err, errInvalidMigrationSet) {
		t.Errorf(
			"migrate() error = %v, want errInvalidMigrationSet",
			err,
		)
	}
}

func TestRunMigrationsPreservesSetupFailure(
	t *testing.T,
) {
	setupError := errors.New(
		"test setup failure",
	)
	tx := setupFailingMigrationTx{
		err: setupError,
	}

	err := runMigrations(
		context.Background(),
		tx,
		nil,
	)
	if !errors.Is(err, setupError) {
		t.Errorf(
			"runMigrations() error = %v, want setup failure",
			err,
		)
	}
}

type setupFailingMigrationTx struct {
	pgx.Tx
	err error
}

func (tx setupFailingMigrationTx) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, tx.err
}

func TestLoadMigrationsRejectsInvalidSets(
	t *testing.T,
) {
	tests := []struct {
		name   string
		source fs.FS
	}{
		{
			name:   "no migrations",
			source: fstest.MapFS{},
		},
		{
			name: "invalid name",
			source: fstest.MapFS{
				"migrations/invalid.sql": {
					Data: []byte("SELECT 1;"),
				},
			},
		},
		{
			name: "invalid version",
			source: fstest.MapFS{
				"migrations/0000_invalid.sql": {
					Data: []byte("SELECT 1;"),
				},
			},
		},
		{
			name: "duplicate version",
			source: fstest.MapFS{
				"migrations/0001_first.sql": {
					Data: []byte("SELECT 1;"),
				},
				"migrations/0001_second.sql": {
					Data: []byte("SELECT 2;"),
				},
			},
		},
		{
			name: "empty migration",
			source: fstest.MapFS{
				"migrations/0001_empty.sql": {},
			},
		},
		{
			name: "unreadable migration",
			source: unreadableMigrationFS{
				FS: fstest.MapFS{
					"migrations/0001_test.sql": {
						Data: []byte("SELECT 1;"),
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			migrations, err := loadMigrations(
				test.source,
			)
			if !errors.Is(
				err,
				errInvalidMigrationSet,
			) {
				t.Errorf(
					"loadMigrations() error = %v, want errInvalidMigrationSet",
					err,
				)
			}

			if migrations != nil {
				t.Errorf(
					"loadMigrations() = %#v, want nil",
					migrations,
				)
			}
		})
	}
}

func TestLoadMigrationsOrdersByNumericVersion(
	t *testing.T,
) {
	source := fstest.MapFS{
		"migrations/0010_tenth.sql": {
			Data: []byte("SELECT 10;"),
		},
		"migrations/0002_second.sql": {
			Data: []byte("SELECT 2;"),
		},
	}

	migrations, err := loadMigrations(source)
	if err != nil {
		t.Fatalf(
			"loadMigrations() error = %v, want nil",
			err,
		)
	}

	if len(migrations) != 2 {
		t.Fatalf(
			"migration count = %d, want 2",
			len(migrations),
		)
	}

	if migrations[0].version != 2 ||
		migrations[1].version != 10 {
		t.Errorf(
			"migration order = [%d, %d], want [2, 10]",
			migrations[0].version,
			migrations[1].version,
		)
	}
}

type unreadableMigrationFS struct {
	fs.FS
}

func (source unreadableMigrationFS) Open(
	name string,
) (fs.File, error) {
	if name == "migrations/0001_test.sql" {
		return nil, fs.ErrPermission
	}

	return source.FS.Open(name)
}

func oneTestMigration() fstest.MapFS {
	return fstest.MapFS{
		"migrations/0001_test.sql": {
			Data: []byte(
				"CREATE TABLE migration_test (id BIGINT);",
			),
		},
	}
}

func migrationAppliedTimes(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) map[int64]time.Time {
	t.Helper()

	rows, err := pool.Query(
		ctx,
		`
			SELECT version, applied_at
			FROM schema_migrations
			ORDER BY version
		`,
	)
	if err != nil {
		t.Fatalf(
			"query migration application times: %v",
			err,
		)
	}
	defer rows.Close()

	applied := make(map[int64]time.Time)
	for rows.Next() {
		var (
			version   int64
			appliedAt time.Time
		)
		if err := rows.Scan(
			&version,
			&appliedAt,
		); err != nil {
			t.Fatalf(
				"scan migration application time: %v",
				err,
			)
		}

		applied[version] = appliedAt.UTC()
	}

	if err := rows.Err(); err != nil {
		t.Fatalf(
			"iterate migration application times: %v",
			err,
		)
	}

	return applied
}

func newEmptyStoreTestPool(
	t *testing.T,
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

	adminConfig, err := pgxpool.ParseConfig(
		databaseURL,
	)
	if err != nil {
		t.Fatal(
			"invalid test database configuration",
		)
	}

	adminPool, err := pgxpool.NewWithConfig(
		context.Background(),
		adminConfig,
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
		"store_empty_test_%d_%d",
		os.Getpid(),
		atomic.AddUint64(
			&emptyStoreSchemaSequence,
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
			"create isolated empty schema: %v",
			err,
		)
	}

	testConfig, err := pgxpool.ParseConfig(
		databaseURL,
	)
	if err != nil {
		t.Fatal(
			"invalid test database configuration",
		)
	}
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema

	testPool, err := pgxpool.NewWithConfig(
		context.Background(),
		testConfig,
	)
	if err != nil {
		t.Fatal(
			"cannot create isolated empty database pool",
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
				"drop isolated empty schema: %v",
				cleanupError,
			)
		}
	})

	return testPool
}

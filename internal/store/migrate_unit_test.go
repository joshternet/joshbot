package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/testutil"
)

type migrationUnitTx struct {
	pgx.Tx

	execResults []controlUnitExecResult
	execIndex   int

	queryResults []storeFakeQueryResult
	queryIndex   int
}

func (tx *migrationUnitTx) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	if tx.execIndex >= len(tx.execResults) {
		return pgconn.CommandTag{},
			errUnexpectedStoreDatabaseCall
	}

	result := tx.execResults[tx.execIndex]
	tx.execIndex++

	return result.tag, result.err
}

func (tx *migrationUnitTx) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	if tx.queryIndex >= len(tx.queryResults) {
		return nil,
			errUnexpectedStoreDatabaseCall
	}

	result := tx.queryResults[tx.queryIndex]
	tx.queryIndex++

	return result.rows, result.err
}

func TestMigrateInputValidationWithoutDatabase(
	t *testing.T,
) {
	var nilContext context.Context

	if err := Migrate(
		nilContext,
		nil,
	); !errors.Is(
		err,
		errInvalidContext,
	) {
		t.Fatalf(
			"Migrate(nil context) error = %v, want %v",
			err,
			errInvalidContext,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := migrate(
		ctx,
		nil,
		embeddedMigrations,
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf(
			"migrate(canceled context) error = %v",
			err,
		)
	}

	if err := migrate(
		context.Background(),
		nil,
		embeddedMigrations,
	); !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"migrate(nil pool) error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}

	pool := new(pgxpool.Pool)

	if err := migrate(
		context.Background(),
		pool,
		nil,
	); !errors.Is(
		err,
		errMigrationSourceUnavailable,
	) {
		t.Fatalf(
			"migrate(nil source) error = %v, want %v",
			err,
			errMigrationSourceUnavailable,
		)
	}

	if err := migrate(
		context.Background(),
		pool,
		fstest.MapFS{},
	); !errors.Is(
		err,
		errInvalidMigrationSet,
	) {
		t.Fatalf(
			"migrate(empty source) error = %v, want %v",
			err,
			errInvalidMigrationSet,
		)
	}
}

func TestRunMigrationsAppliesPendingMigrationsWithoutDatabase(
	t *testing.T,
) {
	migrations := []migration{
		{
			version:  1,
			name:     "0001_first.sql",
			checksum: "first-checksum",
			data:     []byte("SELECT 1;"),
		},
		{
			version:  2,
			name:     "0002_second.sql",
			checksum: "second-checksum",
			data:     []byte("SELECT 2;"),
		},
	}

	tx := &migrationUnitTx{
		execResults: []controlUnitExecResult{
			{},
			{},
			{},
			{},
			{},
			{},
		},
		queryResults: []storeFakeQueryResult{
			{
				rows: newStoreFakeRows(),
			},
		},
	}

	if err := runMigrations(
		context.Background(),
		tx,
		migrations,
	); err != nil {
		t.Fatalf(
			"runMigrations() error = %v",
			err,
		)
	}

	if tx.execIndex != len(tx.execResults) {
		t.Fatalf(
			"runMigrations() exec count = %d, want %d",
			tx.execIndex,
			len(tx.execResults),
		)
	}
}

func TestRunMigrationsSkipsAppliedMigrationWithoutDatabase(
	t *testing.T,
) {
	appliedAt := time.Date(
		2026,
		time.September,
		18,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	migrations := []migration{
		{
			version:  1,
			name:     "0001_test.sql",
			checksum: "checksum",
			data:     []byte("SELECT 1;"),
		},
	}

	tx := &migrationUnitTx{
		execResults: []controlUnitExecResult{
			{},
			{},
		},
		queryResults: []storeFakeQueryResult{
			{
				rows: newStoreFakeRows(
					[]any{
						int64(1),
						"0001_test.sql",
						"checksum",
						appliedAt,
					},
				),
			},
		},
	}

	if err := runMigrations(
		context.Background(),
		tx,
		migrations,
	); err != nil {
		t.Fatalf(
			"runMigrations() error = %v",
			err,
		)
	}

	if tx.execIndex != 2 {
		t.Fatalf(
			"runMigrations() exec count = %d, want 2",
			tx.execIndex,
		)
	}
}

func TestRunMigrationsRejectsDriftWithoutDatabase(
	t *testing.T,
) {
	appliedAt := time.Date(
		2026,
		time.September,
		18,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	migrationSet := []migration{
		{
			version:  1,
			name:     "0001_test.sql",
			checksum: "checksum",
			data:     []byte("SELECT 1;"),
		},
	}

	tests := []struct {
		name     string
		version  int64
		fileName string
		checksum string
	}{
		{
			name:     "unknown version",
			version:  2,
			fileName: "0002_unknown.sql",
			checksum: "checksum",
		},
		{
			name:     "changed name",
			version:  1,
			fileName: "0001_changed.sql",
			checksum: "checksum",
		},
		{
			name:     "changed checksum",
			version:  1,
			fileName: "0001_test.sql",
			checksum: "changed",
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				tx := &migrationUnitTx{
					execResults: []controlUnitExecResult{
						{},
						{},
					},
					queryResults: []storeFakeQueryResult{
						{
							rows: newStoreFakeRows(
								[]any{
									test.version,
									test.fileName,
									test.checksum,
									appliedAt,
								},
							),
						},
					},
				}

				err := runMigrations(
					context.Background(),
					tx,
					migrationSet,
				)

				if !errors.Is(
					err,
					ErrMigrationDrift,
				) {
					t.Fatalf(
						"runMigrations() error = %v, want %v",
						err,
						ErrMigrationDrift,
					)
				}
			},
		)
	}
}

func TestRunMigrationsDatabaseFailuresWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test migration database failure",
	)

	migrationSet := []migration{
		{
			version:  1,
			name:     "0001_test.sql",
			checksum: "checksum",
			data:     []byte("SELECT 1;"),
		},
	}

	t.Run(
		"advisory lock",
		func(t *testing.T) {
			tx := &migrationUnitTx{
				execResults: []controlUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			err := runMigrations(
				context.Background(),
				tx,
				migrationSet,
			)

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"runMigrations() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"metadata setup",
		func(t *testing.T) {
			tx := &migrationUnitTx{
				execResults: []controlUnitExecResult{
					{},
					{
						err: testErr,
					},
				},
			}

			err := runMigrations(
				context.Background(),
				tx,
				migrationSet,
			)

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"runMigrations() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"metadata query",
		func(t *testing.T) {
			tx := &migrationUnitTx{
				execResults: []controlUnitExecResult{
					{},
					{},
				},
				queryResults: []storeFakeQueryResult{
					{
						err: testErr,
					},
				},
			}

			err := runMigrations(
				context.Background(),
				tx,
				migrationSet,
			)

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"runMigrations() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"metadata collection",
		func(t *testing.T) {
			tx := &migrationUnitTx{
				execResults: []controlUnitExecResult{
					{},
					{},
				},
				queryResults: []storeFakeQueryResult{
					{
						rows: newStoreFakeRows(
							[]any{
								int64(1),
							},
						),
					},
				},
			}

			err := runMigrations(
				context.Background(),
				tx,
				migrationSet,
			)

			if err == nil {
				t.Fatal(
					"runMigrations() error = nil",
				)
			}
		},
	)

	t.Run(
		"migration application",
		func(t *testing.T) {
			tx := &migrationUnitTx{
				execResults: []controlUnitExecResult{
					{},
					{},
					{
						err: testErr,
					},
				},
				queryResults: []storeFakeQueryResult{
					{
						rows: newStoreFakeRows(),
					},
				},
			}

			err := runMigrations(
				context.Background(),
				tx,
				migrationSet,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					`apply migration "0001_test.sql"`,
				) {
				t.Fatalf(
					"runMigrations() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"migration metadata insert",
		func(t *testing.T) {
			tx := &migrationUnitTx{
				execResults: []controlUnitExecResult{
					{},
					{},
					{},
					{
						err: testErr,
					},
				},
				queryResults: []storeFakeQueryResult{
					{
						rows: newStoreFakeRows(),
					},
				},
			}

			err := runMigrations(
				context.Background(),
				tx,
				migrationSet,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					`record migration "0001_test.sql"`,
				) {
				t.Fatalf(
					"runMigrations() error = %v",
					err,
				)
			}
		},
	)
}

func TestEmbeddedMigrationManifestGolden(
	t *testing.T,
) {
	migrations, err := loadMigrations(
		embeddedMigrations,
	)
	if err != nil {
		t.Fatalf(
			"loadMigrations() error = %v",
			err,
		)
	}

	var snapshot bytes.Buffer

	fmt.Fprintf(
		&snapshot,
		"count=%d\n",
		len(migrations),
	)

	for _, migration := range migrations {
		fmt.Fprintf(
			&snapshot,
			"%04d %s %s\n",
			migration.version,
			migration.name,
			migration.checksum,
		)
	}

	if err := testutil.CheckGolden(
		"testdata/golden/migrations.golden",
		snapshot.Bytes(),
	); err != nil {
		t.Fatalf(
			"migration manifest golden error = %v",
			err,
		)
	}
}

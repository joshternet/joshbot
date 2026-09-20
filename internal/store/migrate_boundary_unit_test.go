package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrateConcretePoolBoundaryWithoutDatabase(
	t *testing.T,
) {
	var nilContext context.Context

	err := Migrate(
		nilContext,
		new(pgxpool.Pool),
	)

	if !errors.Is(
		err,
		errInvalidContext,
	) {
		t.Fatalf(
			"Migrate() error = %v, want %v",
			err,
			errInvalidContext,
		)
	}
}

func TestMigrateTransactionBoundaryWithoutDatabase(
	t *testing.T,
) {
	source := fstest.MapFS{
		"migrations/0001_test.sql": {
			Data: []byte("SELECT 1;"),
		},
	}

	t.Run(
		"success",
		func(t *testing.T) {
			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
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

			err := migrate(
				context.Background(),
				&controlUnitBeginner{
					tx: tx,
				},
				source,
			)
			if err != nil {
				t.Fatalf(
					"migrate() error = %v",
					err,
				)
			}

			if !tx.committed {
				t.Fatal(
					"migrate() did not commit",
				)
			}
		},
	)

	t.Run(
		"begin failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test migration begin failure",
			)

			err := migrate(
				context.Background(),
				&controlUnitBeginner{
					err: testErr,
				},
				source,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: migrate",
				) {
				t.Fatalf(
					"migrate() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"transaction failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test migration transaction failure",
			)

			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			err := migrate(
				context.Background(),
				&controlUnitBeginner{
					tx: tx,
				},
				source,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: migrate",
				) {
				t.Fatalf(
					"migrate() error = %v",
					err,
				)
			}

			if tx.committed {
				t.Fatal(
					"migrate() committed failed migration",
				)
			}

			if !tx.rolledBack {
				t.Fatal(
					"migrate() did not roll back failed migration",
				)
			}
		},
	)
}

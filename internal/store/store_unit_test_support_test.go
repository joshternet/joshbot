package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var errUnexpectedStoreDatabaseCall = errors.New(
	"unexpected store database call",
)

type storeFakeQueryResult struct {
	rows pgx.Rows
	err  error
}

type storeFakePostgres struct {
	beginTx  *storeFakeTx
	beginErr error

	execErr error

	queryResults []storeFakeQueryResult
	queryIndex   int

	rowResults []storeFakeRow
	rowIndex   int
}

func (database *storeFakePostgres) Begin(
	context.Context,
) (pgx.Tx, error) {
	if database.beginErr != nil {
		return nil, database.beginErr
	}

	if database.beginTx == nil {
		return nil, errUnexpectedStoreDatabaseCall
	}

	return database.beginTx, nil
}

func (database *storeFakePostgres) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{},
		database.execErr
}

func (database *storeFakePostgres) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	if database.queryIndex >= len(
		database.queryResults,
	) {
		return nil,
			errUnexpectedStoreDatabaseCall
	}

	result :=
		database.queryResults[database.queryIndex]

	database.queryIndex++

	return result.rows,
		result.err
}

func (database *storeFakePostgres) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	if database.rowIndex >= len(
		database.rowResults,
	) {
		return storeFakeRow{
			err: errUnexpectedStoreDatabaseCall,
		}
	}

	result :=
		database.rowResults[database.rowIndex]

	database.rowIndex++

	return result
}

type storeFakeTx struct {
	pgx.Tx

	execErr     error
	commitErr   error
	rollbackErr error

	committed  bool
	rolledBack bool
}

func (tx *storeFakeTx) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{},
		tx.execErr
}

func (tx *storeFakeTx) Commit(
	context.Context,
) error {
	tx.committed = true

	return tx.commitErr
}

func (tx *storeFakeTx) Rollback(
	context.Context,
) error {
	tx.rolledBack = true

	return tx.rollbackErr
}

type storeFakeRow struct {
	values []any
	err    error
}

func (row storeFakeRow) Scan(
	destinations ...any,
) error {
	if row.err != nil {
		return row.err
	}

	return assignStoreScanValues(
		destinations,
		row.values,
	)
}

type storeFakeRows struct {
	pgx.Rows

	values [][]any
	index  int
	err    error
	closed bool
}

func newStoreFakeRows(
	values ...[]any,
) *storeFakeRows {
	return &storeFakeRows{
		values: values,
		index:  -1,
	}
}

func (rows *storeFakeRows) Close() {
	rows.closed = true
}

func (rows *storeFakeRows) Err() error {
	return rows.err
}

func (rows *storeFakeRows) Next() bool {
	if rows.closed {
		return false
	}

	next := rows.index + 1
	if next >= len(rows.values) {
		rows.Close()

		return false
	}

	rows.index = next

	return true
}

func (rows *storeFakeRows) Scan(
	destinations ...any,
) error {
	if rows.index < 0 ||
		rows.index >= len(rows.values) {
		return errUnexpectedStoreDatabaseCall
	}

	return assignStoreScanValues(
		destinations,
		rows.values[rows.index],
	)
}

func assignStoreScanValues(
	destinations []any,
	values []any,
) error {
	if len(destinations) != len(values) {
		return fmt.Errorf(
			"scan destination count %d does not match value count %d",
			len(destinations),
			len(values),
		)
	}

	for index, destination := range destinations {
		if destination == nil {
			return fmt.Errorf(
				"scan destination %d is nil",
				index,
			)
		}

		target :=
			reflect.ValueOf(destination)

		if target.Kind() != reflect.Pointer ||
			target.IsNil() {
			return fmt.Errorf(
				"scan destination %d is not a non-nil pointer",
				index,
			)
		}

		target = target.Elem()

		value := values[index]
		if value == nil {
			target.SetZero()

			continue
		}

		source :=
			reflect.ValueOf(value)

		if source.Type().AssignableTo(
			target.Type(),
		) {
			target.Set(source)

			continue
		}

		if source.Type().ConvertibleTo(
			target.Type(),
		) {
			target.Set(
				source.Convert(
					target.Type(),
				),
			)

			continue
		}

		return fmt.Errorf(
			"scan value %d has type %s, cannot assign to %s",
			index,
			source.Type(),
			target.Type(),
		)
	}

	return nil
}

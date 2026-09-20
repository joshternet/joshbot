package reporting

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var errUnexpectedDatabaseCall = errors.New(
	"unexpected database call",
)

type fakeQueryResult struct {
	rows pgx.Rows
	err  error
}

type fakeQueryer struct {
	execErr error

	queryResults []fakeQueryResult
	queryIndex   int

	rowResults []fakeRow
	rowIndex   int
}

func (queryer *fakeQueryer) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{},
		queryer.execErr
}

func (queryer *fakeQueryer) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	if queryer.queryIndex >= len(
		queryer.queryResults,
	) {
		return nil,
			errUnexpectedDatabaseCall
	}

	result :=
		queryer.queryResults[queryer.queryIndex]

	queryer.queryIndex++

	return result.rows,
		result.err
}

func (queryer *fakeQueryer) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	if queryer.rowIndex >= len(
		queryer.rowResults,
	) {
		return fakeRow{
			err: errUnexpectedDatabaseCall,
		}
	}

	result :=
		queryer.rowResults[queryer.rowIndex]

	queryer.rowIndex++

	return result
}

type fakeRow struct {
	values []any
	err    error
}

func (row fakeRow) Scan(
	destinations ...any,
) error {
	if row.err != nil {
		return row.err
	}

	return assignScanValues(
		destinations,
		row.values,
	)
}

type fakeRows struct {
	pgx.Rows

	values [][]any
	index  int
	err    error
	closed bool
}

func newFakeRows(
	values ...[]any,
) *fakeRows {
	return &fakeRows{
		values: values,
		index:  -1,
	}
}

func (rows *fakeRows) Close() {
	rows.closed = true
}

func (rows *fakeRows) Err() error {
	return rows.err
}

func (rows *fakeRows) Next() bool {
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

func (rows *fakeRows) Scan(
	destinations ...any,
) error {
	if rows.index < 0 ||
		rows.index >= len(rows.values) {
		return errUnexpectedDatabaseCall
	}

	return assignScanValues(
		destinations,
		rows.values[rows.index],
	)
}

func assignScanValues(
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

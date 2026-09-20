package database

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Queryer interface {
	Exec(
		context.Context,
		string,
		...any,
	) (pgconn.CommandTag, error)

	Query(
		context.Context,
		string,
		...any,
	) (pgx.Rows, error)

	QueryRow(
		context.Context,
		string,
		...any,
	) pgx.Row
}

type Beginner interface {
	Begin(
		context.Context,
	) (pgx.Tx, error)
}

type Postgres interface {
	Queryer
	Beginner
}

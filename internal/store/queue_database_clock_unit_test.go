package store

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDatabaseQueueClockNowWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	testErr := errors.New(
		"test queue database clock dial failure",
	)

	config, err := pgxpool.ParseConfig(
		"postgres://test:test@127.0.0.1:5432/test?sslmode=disable",
	)
	if err != nil {
		t.Fatalf(
			"pgxpool.ParseConfig() error = %v",
			err,
		)
	}

	config.ConnConfig.DialFunc = func(
		context.Context,
		string,
		string,
	) (net.Conn, error) {
		return nil, testErr
	}

	pool, err := pgxpool.NewWithConfig(
		ctx,
		config,
	)
	if err != nil {
		t.Fatalf(
			"pgxpool.NewWithConfig() error = %v",
			err,
		)
	}
	t.Cleanup(pool.Close)

	now, err := (databaseQueueClock{}).Now(
		ctx,
		pool,
	)

	if !now.IsZero() {
		t.Fatalf(
			"databaseQueueClock.Now() = %v, want zero",
			now,
		)
	}

	if err == nil {
		t.Fatal(
			"databaseQueueClock.Now() error = nil, want dial failure",
		)
	}

	if !strings.Contains(
		err.Error(),
		testErr.Error(),
	) {
		t.Fatalf(
			"databaseQueueClock.Now() error = %v, want error containing %q",
			err,
			testErr.Error(),
		)
	}
}

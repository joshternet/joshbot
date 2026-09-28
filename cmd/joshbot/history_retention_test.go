package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func (*fakeHeartbeatStore) PurgeOperationalHistory(
	context.Context,
	time.Duration,
) error {
	return nil
}

type capturingOperationalHistoryStore struct {
	fakeHeartbeatStore

	retention time.Duration
	err       error
}

func (
	storage *capturingOperationalHistoryStore,
) PurgeOperationalHistory(
	_ context.Context,
	retention time.Duration,
) error {
	storage.retention = retention

	return storage.err
}

func TestDiscoveryPurgesOperationalHistory(
	t *testing.T,
) {
	operations, _ :=
		newTestRuntimeOperations(t)

	operations.getenv =
		withCrawlRuntimeEnvironment(
			operations.getenv,
		)

	storage :=
		&capturingOperationalHistoryStore{}

	operations.newHeartbeatStore = func(
		*pgxpool.Pool,
	) (heartbeatStore, error) {
		return storage, nil
	}

	if err := operations.discover(
		context.Background(),
		true,
	); err != nil {
		t.Fatalf(
			"discover() error = %v",
			err,
		)
	}

	if storage.retention !=
		operationalHistoryRetention {
		t.Errorf(
			"operational history retention = %v, want %v",
			storage.retention,
			operationalHistoryRetention,
		)
	}
}

func TestDiscoveryReturnsOperationalHistoryPurgeFailure(
	t *testing.T,
) {
	operations, _ :=
		newTestRuntimeOperations(t)

	operations.getenv =
		withCrawlRuntimeEnvironment(
			operations.getenv,
		)

	want := errors.New(
		"operational history purge failure",
	)

	storage :=
		&capturingOperationalHistoryStore{
			err: want,
		}

	operations.newHeartbeatStore = func(
		*pgxpool.Pool,
	) (heartbeatStore, error) {
		return storage, nil
	}

	err := operations.discover(
		context.Background(),
		true,
	)

	if !errors.Is(
		err,
		want,
	) {
		t.Fatalf(
			"discover() error = %v, want %v",
			err,
			want,
		)
	}

	if !strings.Contains(
		err.Error(),
		"purge operational history",
	) {
		t.Errorf(
			"discover() error = %v, want operational history context",
			err,
		)
	}
}

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/store"
)

func (*serviceHeartbeatIntegrationStore) PurgeOperationalHistory(
	context.Context,
	time.Duration,
) error {
	return nil
}

func (*runtimeBoundaryIntegrationHeartbeatStore) PurgeOperationalHistory(
	context.Context,
	time.Duration,
) error {
	return nil
}

type operationalHistoryIntegrationStore struct {
	retention time.Duration
	purgeErr  error
}

func (*operationalHistoryIntegrationStore) UpsertServiceHeartbeat(
	context.Context,
	store.ServiceHeartbeat,
) error {
	return nil
}

func (*operationalHistoryIntegrationStore) PurgeCrawlTelemetry(
	context.Context,
	time.Duration,
) (int64, error) {
	return 0, nil
}

func (
	storage *operationalHistoryIntegrationStore,
) PurgeOperationalHistory(
	_ context.Context,
	retention time.Duration,
) error {
	storage.retention = retention

	return storage.purgeErr
}

func TestDiscoveryRuntimeIntegrationOperationalHistoryRetention(
	t *testing.T,
) {
	t.Run(
		"purges before discovery",
		func(t *testing.T) {
			operations, _ :=
				newTestRuntimeOperations(t)

			operations.getenv =
				withCrawlRuntimeEnvironment(
					operations.getenv,
				)

			storage :=
				&operationalHistoryIntegrationStore{}

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
				t.Fatalf(
					"operational history retention = %v, want %v",
					storage.retention,
					operationalHistoryRetention,
				)
			}
		},
	)

	t.Run(
		"purge failure stops discovery",
		func(t *testing.T) {
			operations, _ :=
				newTestRuntimeOperations(t)

			operations.getenv =
				withCrawlRuntimeEnvironment(
					operations.getenv,
				)

			want := errors.New(
				"integration operational history purge failure",
			)

			storage :=
				&operationalHistoryIntegrationStore{
					purgeErr: want,
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
				t.Fatalf(
					"discover() error = %v, want operational history context",
					err,
				)
			}
		},
	)
}

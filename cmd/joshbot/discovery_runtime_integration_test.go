package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiscoveryRuntimeIntegrationServiceInstanceFallback(
	t *testing.T,
) {
	tests := []struct {
		name     string
		hostname string
		want     string
	}{
		{
			name: "default without hostname",
			want: defaultDiscoveryInstanceID,
		},
		{
			name:     "hostname",
			hostname: " ephemeral-host ",
			want:     "ephemeral-host",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations, _ := newTestRuntimeOperations(t)

			baseGetenv := withCrawlRuntimeEnvironment(
				operations.getenv,
			)

			operations.getenv = func(name string) string {
				switch name {
				case serviceInstanceIDEnvironment:
					return ""
				case hostnameEnvironment:
					return test.hostname
				default:
					return baseGetenv(name)
				}
			}

			storage := &fakeHeartbeatStore{}

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

			storage.mu.Lock()
			defer storage.mu.Unlock()

			if len(storage.writes) == 0 {
				t.Fatal(
					"discovery heartbeat was not persisted",
				)
			}

			if got := storage.writes[0].InstanceID; got != test.want {
				t.Errorf(
					"heartbeat instance = %q, want %q",
					got,
					test.want,
				)
			}
		})
	}
}

func TestDiscoveryRuntimeIntegrationOrchestrationFailures(
	t *testing.T,
) {
	t.Run("Web Bot Auth signer", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)

		environment :=
			validCrawlRuntimeEnvironment()
		environment[webBotAuthModeEnvironment] =
			webBotAuthModeRequired

		operations.getenv = environment.get

		err := operations.discover(
			context.Background(),
			true,
		)

		if !errors.Is(
			err,
			errInvalidWebBotAuthConfiguration,
		) {
			t.Fatalf(
				"discover() error = %v, want invalid Web Bot Auth configuration",
				err,
			)
		}
	})

	t.Run("heartbeat store construction", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)
		operations.getenv =
			withCrawlRuntimeEnvironment(
				operations.getenv,
			)

		want := errors.New(
			"integration heartbeat store construction failure",
		)

		operations.newHeartbeatStore = func(
			*pgxpool.Pool,
		) (heartbeatStore, error) {
			return nil, want
		}

		err := operations.discover(
			context.Background(),
			true,
		)

		if !errors.Is(err, want) {
			t.Fatalf(
				"discover() error = %v, want %v",
				err,
				want,
			)
		}

		if !strings.Contains(
			err.Error(),
			"construct discovery heartbeat store",
		) {
			t.Errorf(
				"discover() error = %v, want heartbeat store context",
				err,
			)
		}
	})

	t.Run("telemetry retention", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)
		operations.getenv =
			withCrawlRuntimeEnvironment(
				operations.getenv,
			)

		want := errors.New(
			"integration telemetry purge failure",
		)

		operations.newHeartbeatStore = func(
			*pgxpool.Pool,
		) (heartbeatStore, error) {
			return &fakeHeartbeatStore{
				purgeErr: want,
			}, nil
		}

		err := operations.discover(
			context.Background(),
			true,
		)

		if !errors.Is(err, want) {
			t.Fatalf(
				"discover() error = %v, want %v",
				err,
				want,
			)
		}

		if !strings.Contains(
			err.Error(),
			"purge crawl telemetry",
		) {
			t.Errorf(
				"discover() error = %v, want telemetry purge context",
				err,
			)
		}
	})

	t.Run("invalid service instance", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)

		baseGetenv := withCrawlRuntimeEnvironment(
			operations.getenv,
		)

		operations.getenv = func(name string) string {
			if name == serviceInstanceIDEnvironment {
				return strings.Repeat(
					"d",
					maxServiceInstanceIDLength+1,
				)
			}

			return baseGetenv(name)
		}

		operations.newHeartbeatStore = func(
			*pgxpool.Pool,
		) (heartbeatStore, error) {
			return &fakeHeartbeatStore{}, nil
		}

		err := operations.discover(
			context.Background(),
			true,
		)

		if !errors.Is(
			err,
			errInvalidRuntimeConfiguration,
		) {
			t.Fatalf(
				"discover() error = %v, want invalid runtime configuration",
				err,
			)
		}
	})

	t.Run("initial heartbeat", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)
		operations.getenv =
			withCrawlRuntimeEnvironment(
				operations.getenv,
			)

		want := errors.New(
			"integration initial heartbeat failure",
		)

		operations.newHeartbeatStore = func(
			*pgxpool.Pool,
		) (heartbeatStore, error) {
			return &fakeHeartbeatStore{
				writeErr: want,
			}, nil
		}

		err := operations.discover(
			context.Background(),
			true,
		)

		if !errors.Is(err, want) {
			t.Fatalf(
				"discover() error = %v, want %v",
				err,
				want,
			)
		}
	})

	t.Run("runner construction", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)
		operations.getenv =
			withCrawlRuntimeEnvironment(
				operations.getenv,
			)

		operations.newHeartbeatStore = nil

		want := errors.New(
			"integration discovery runner construction failure",
		)

		operations.newDiscoveryRunner = func(
			context.Context,
			*pgxpool.Pool,
			crawlRuntimeSettings,
		) (discoveryRunner, error) {
			return nil, want
		}

		err := operations.discover(
			context.Background(),
			true,
		)

		if !errors.Is(err, want) {
			t.Fatalf(
				"discover() error = %v, want %v",
				err,
				want,
			)
		}
	})
}

func TestDiscoveryRuntimeIntegrationFailedHeartbeatState(
	t *testing.T,
) {
	operations, _ := newTestRuntimeOperations(t)
	operations.getenv =
		withCrawlRuntimeEnvironment(
			operations.getenv,
		)

	storage := &fakeHeartbeatStore{}
	runErr := errors.New(
		"integration discovery execution failure",
	)
	runner := &fakeDiscoveryRunner{
		runOnceErr: runErr,
	}

	operations.newHeartbeatStore = func(
		*pgxpool.Pool,
	) (heartbeatStore, error) {
		return storage, nil
	}

	operations.newDiscoveryRunner = func(
		context.Context,
		*pgxpool.Pool,
		crawlRuntimeSettings,
	) (discoveryRunner, error) {
		return runner, nil
	}

	err := operations.discover(
		context.Background(),
		true,
	)

	if !errors.Is(err, runErr) {
		t.Fatalf(
			"discover() error = %v, want %v",
			err,
			runErr,
		)
	}

	if runner.observer == nil {
		t.Error(
			"discovery lifecycle observer was not installed",
		)
	}

	storage.mu.Lock()
	defer storage.mu.Unlock()

	if len(storage.writes) != 2 {
		t.Fatalf(
			"heartbeat writes = %#v, want initial and final writes",
			storage.writes,
		)
	}

	final := storage.writes[len(storage.writes)-1]

	if final.State != "failed" {
		t.Errorf(
			"final heartbeat state = %q, want failed",
			final.State,
		)
	}
}

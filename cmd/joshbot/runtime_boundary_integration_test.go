package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/githubpublish"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/publicdata"
	"github.com/joshternet/joshbot/internal/store"
	"github.com/joshternet/joshbot/internal/worker"
)

var errRuntimeBoundaryIntegration = errors.New(
	"integration runtime boundary failure",
)

type runtimeBoundaryIntegrationEnvironment map[string]string

func (environment runtimeBoundaryIntegrationEnvironment) get(
	name string,
) string {
	return environment[name]
}

type runtimeBoundaryIntegrationDatabase struct {
	pingErr    error
	closeCount int
}

func (database *runtimeBoundaryIntegrationDatabase) Close() {
	database.closeCount++
}

func (database *runtimeBoundaryIntegrationDatabase) Ping(
	context.Context,
) error {
	return database.pingErr
}

func (*runtimeBoundaryIntegrationDatabase) Pool() *pgxpool.Pool {
	return nil
}

type runtimeBoundaryIntegrationQueue struct {
	scheduleErr error
}

func (*runtimeBoundaryIntegrationQueue) Claim(
	context.Context,
	string,
) (store.Lease, bool, error) {
	return store.Lease{}, false, nil
}

func (*runtimeBoundaryIntegrationQueue) Renew(
	_ context.Context,
	lease store.Lease,
) (store.Lease, error) {
	return lease, nil
}

func (*runtimeBoundaryIntegrationQueue) CompleteVerification(
	context.Context,
	store.Lease,
	declaration.Result,
	time.Duration,
) error {
	return nil
}

func (queue *runtimeBoundaryIntegrationQueue) Schedule(
	context.Context,
	origin.Origin,
	time.Time,
) error {
	return queue.scheduleErr
}

type runtimeBoundaryIntegrationVerifiedSource struct {
	err error
}

func (source *runtimeBoundaryIntegrationVerifiedSource) VerifiedOrigins(
	context.Context,
) ([]store.VerifiedOrigin, error) {
	return nil, source.err
}

type runtimeBoundaryIntegrationPublisher struct {
	err error
}

func (publisher *runtimeBoundaryIntegrationPublisher) Publish(
	context.Context,
	[]publicdata.File,
) (githubpublish.Result, error) {
	return githubpublish.Result{}, publisher.err
}

type runtimeBoundaryIntegrationRunner struct {
	err      error
	observer worker.LifecycleObserver
}

func (runner *runtimeBoundaryIntegrationRunner) Run(
	context.Context,
) error {
	return runner.err
}

func (runner *runtimeBoundaryIntegrationRunner) SetLifecycleObserver(
	observer worker.LifecycleObserver,
) {
	runner.observer = observer
}

type runtimeBoundaryIntegrationHeartbeatStore struct {
	mu          sync.Mutex
	writeErrors []error
	writes      []store.ServiceHeartbeat
}

func (storage *runtimeBoundaryIntegrationHeartbeatStore) UpsertServiceHeartbeat(
	_ context.Context,
	heartbeat store.ServiceHeartbeat,
) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()

	storage.writes = append(
		storage.writes,
		heartbeat,
	)

	if len(storage.writeErrors) == 0 {
		return nil
	}

	err := storage.writeErrors[0]
	storage.writeErrors = storage.writeErrors[1:]

	return err
}

func (*runtimeBoundaryIntegrationHeartbeatStore) PurgeCrawlTelemetry(
	context.Context,
	time.Duration,
) (int64, error) {
	return 0, nil
}

type runtimeBoundaryIntegrationState struct {
	database  *runtimeBoundaryIntegrationDatabase
	queue     *runtimeBoundaryIntegrationQueue
	store     *runtimeBoundaryIntegrationVerifiedSource
	publisher *runtimeBoundaryIntegrationPublisher
	runner    *runtimeBoundaryIntegrationRunner
}

func TestRuntimeBoundaryIntegrationProductionPublisherAdapter(
	t *testing.T,
) {
	if _, err := newGitHubPublisher(
		githubpublish.Config{},
		"",
	); err == nil {
		t.Error(
			"newGitHubPublisher(empty config) error = nil, want error",
		)
	}
}

func TestRuntimeBoundaryIntegrationDatabaseAndCommands(
	t *testing.T,
) {
	t.Run("database construction failure", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.newDatabase = func(
			context.Context,
			*pgxpool.Config,
		) (databaseConnection, error) {
			return nil,
				errRuntimeBoundaryIntegration
		}

		connection, err := operations.openDatabase(
			context.Background(),
		)

		if connection != nil ||
			!errors.Is(
				err,
				errRuntimeBoundaryIntegration,
			) {
			t.Errorf(
				"openDatabase() = %#v, %v",
				connection,
				err,
			)
		}
	})

	t.Run("health ping failure", func(t *testing.T) {
		operations, state :=
			runtimeBoundaryIntegrationOperations(t)

		state.database.pingErr =
			errRuntimeBoundaryIntegration

		err := operations.health(
			context.Background(),
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Errorf(
				"health() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}

		if state.database.closeCount != 1 {
			t.Errorf(
				"database close count = %d, want 1",
				state.database.closeCount,
			)
		}
	})

	t.Run("schedule configuration failure", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		environment :=
			runtimeBoundaryIntegrationEnvironmentFor(
				t,
			)
		environment[leaseDurationEnvironment] =
			"0s"

		operations.getenv = environment.get

		source :=
			runtimeBoundaryIntegrationOrigin(t)

		err := operations.schedule(
			context.Background(),
			source,
		)

		if !errors.Is(
			err,
			errInvalidRuntimeConfiguration,
		) {
			t.Errorf(
				"schedule() error = %v, want %v",
				err,
				errInvalidRuntimeConfiguration,
			)
		}
	})

	t.Run("schedule queue construction failure", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.newQueue = func(
			*pgxpool.Pool,
			store.QueueConfig,
		) (runtimeQueue, error) {
			return nil,
				errRuntimeBoundaryIntegration
		}

		err := operations.schedule(
			context.Background(),
			runtimeBoundaryIntegrationOrigin(t),
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Errorf(
				"schedule() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}
	})

	t.Run("export query failure", func(t *testing.T) {
		operations, state :=
			runtimeBoundaryIntegrationOperations(t)

		state.store.err =
			errRuntimeBoundaryIntegration

		err := operations.export(
			context.Background(),
			"snapshot",
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Errorf(
				"export() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}
	})

	t.Run("export build failure", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.buildRegistry = func(
			[]store.VerifiedOrigin,
		) ([]publicdata.File, error) {
			return nil,
				errRuntimeBoundaryIntegration
		}

		err := operations.export(
			context.Background(),
			"snapshot",
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Errorf(
				"export() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}
	})
}

func TestRuntimeBoundaryIntegrationPublishFailures(
	t *testing.T,
) {
	t.Run("settings", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.loadPublishSettings = func(
			environmentGetter,
		) (publishSettings, error) {
			return publishSettings{},
				errRuntimeBoundaryIntegration
		}

		result, err := operations.publish(
			context.Background(),
			"snapshot",
		)

		if result != (githubpublish.Result{}) ||
			!errors.Is(
				err,
				errRuntimeBoundaryIntegration,
			) {
			t.Errorf(
				"publish() = %#v, %v",
				result,
				err,
			)
		}
	})

	t.Run("read snapshot", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.readRegistry = func(
			context.Context,
			string,
		) ([]publicdata.File, error) {
			return nil,
				errRuntimeBoundaryIntegration
		}

		result, err := operations.publish(
			context.Background(),
			"snapshot",
		)

		if result != (githubpublish.Result{}) ||
			!errors.Is(
				err,
				errRuntimeBoundaryIntegration,
			) {
			t.Errorf(
				"publish() = %#v, %v",
				result,
				err,
			)
		}
	})

	t.Run("publisher construction", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.newPublisher = func(
			githubpublish.Config,
			string,
		) (registryPublisher, error) {
			return nil,
				errRuntimeBoundaryIntegration
		}

		result, err := operations.publish(
			context.Background(),
			"snapshot",
		)

		if result != (githubpublish.Result{}) ||
			!errors.Is(
				err,
				errRuntimeBoundaryIntegration,
			) {
			t.Errorf(
				"publish() = %#v, %v",
				result,
				err,
			)
		}
	})

	t.Run("publication", func(t *testing.T) {
		operations, state :=
			runtimeBoundaryIntegrationOperations(t)

		state.publisher.err =
			errRuntimeBoundaryIntegration

		result, err := operations.publish(
			context.Background(),
			"snapshot",
		)

		if result != (githubpublish.Result{}) ||
			!errors.Is(
				err,
				errRuntimeBoundaryIntegration,
			) {
			t.Errorf(
				"publish() = %#v, %v",
				result,
				err,
			)
		}
	})
}

func TestRuntimeBoundaryIntegrationWorkerFailures(
	t *testing.T,
) {
	t.Run("worker settings", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		environment :=
			runtimeBoundaryIntegrationEnvironmentFor(
				t,
			)
		environment[jobTimeoutEnvironment] =
			"10m"

		operations.getenv = environment.get

		err := operations.worker(
			context.Background(),
		)

		if !errors.Is(
			err,
			errInvalidRuntimeConfiguration,
		) {
			t.Errorf(
				"worker() error = %v, want %v",
				err,
				errInvalidRuntimeConfiguration,
			)
		}
	})

	t.Run("request delay", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		environment :=
			runtimeBoundaryIntegrationEnvironmentFor(
				t,
			)
		environment[crawlRequestDelayEnvironment] =
			"-1s"

		operations.getenv = environment.get

		err := operations.worker(
			context.Background(),
		)

		if !errors.Is(
			err,
			errInvalidRuntimeConfiguration,
		) {
			t.Errorf(
				"worker() error = %v, want %v",
				err,
				errInvalidRuntimeConfiguration,
			)
		}
	})

	t.Run("Web Bot Auth validation", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		environment :=
			runtimeBoundaryIntegrationEnvironmentFor(
				t,
			)

		keyFile := filepath.Join(
			t.TempDir(),
			"invalid-private-key.pem",
		)

		if err := os.WriteFile(
			keyFile,
			[]byte("not a private key"),
			0o600,
		); err != nil {
			t.Fatalf(
				"write invalid private key: %v",
				err,
			)
		}

		environment[webBotAuthModeEnvironment] = webBotAuthModeRequired
		environment[webBotAuthActivePrivateKeyFileEnvironment] = keyFile

		operations.getenv = environment.get

		err := operations.worker(
			context.Background(),
		)

		if !errors.Is(
			err,
			errInvalidWebBotAuthPrivateKey,
		) {
			t.Errorf(
				"worker() error = %v, want %v",
				err,
				errInvalidWebBotAuthPrivateKey,
			)
		}
	})

	t.Run("heartbeat store construction", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.newHeartbeatStore = func(
			*pgxpool.Pool,
		) (heartbeatStore, error) {
			return nil,
				errRuntimeBoundaryIntegration
		}

		err := operations.worker(
			context.Background(),
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Errorf(
				"worker() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}
	})

	t.Run("invalid heartbeat service instance", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		baseGetenv := operations.getenv
		operations.getenv = func(name string) string {
			if name == serviceInstanceIDEnvironment {
				return strings.Repeat(
					"w",
					maxServiceInstanceIDLength+1,
				)
			}

			return baseGetenv(name)
		}

		operations.newHeartbeatStore = func(
			*pgxpool.Pool,
		) (heartbeatStore, error) {
			return &runtimeBoundaryIntegrationHeartbeatStore{},
				nil
		}

		err := operations.worker(
			context.Background(),
		)

		if !errors.Is(
			err,
			errInvalidRuntimeConfiguration,
		) {
			t.Errorf(
				"worker() error = %v, want %v",
				err,
				errInvalidRuntimeConfiguration,
			)
		}
	})

	t.Run("heartbeat start write", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		storage :=
			&runtimeBoundaryIntegrationHeartbeatStore{
				writeErrors: []error{
					errRuntimeBoundaryIntegration,
				},
			}

		operations.newHeartbeatStore = func(
			*pgxpool.Pool,
		) (heartbeatStore, error) {
			return storage, nil
		}

		err := operations.worker(
			context.Background(),
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Errorf(
				"worker() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}
	})

	t.Run("queue construction", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.newQueue = func(
			*pgxpool.Pool,
			store.QueueConfig,
		) (runtimeQueue, error) {
			return nil,
				errRuntimeBoundaryIntegration
		}

		err := operations.worker(
			context.Background(),
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Errorf(
				"worker() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}
	})

	t.Run("worker construction", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.newWorker = func(
			worker.Queue,
			worker.Verifier,
			worker.Config,
		) (workerRunner, error) {
			return nil,
				errRuntimeBoundaryIntegration
		}

		err := operations.worker(
			context.Background(),
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Errorf(
				"worker() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}
	})

	t.Run("worker runtime", func(t *testing.T) {
		operations, state :=
			runtimeBoundaryIntegrationOperations(t)

		state.runner.err =
			errRuntimeBoundaryIntegration

		err := operations.worker(
			context.Background(),
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Errorf(
				"worker() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}
	})

	t.Run("primary and final heartbeat failures", func(t *testing.T) {
		operations, state :=
			runtimeBoundaryIntegrationOperations(t)

		primaryFailure := errors.New(
			"integration worker primary failure",
		)
		finalFailure := errors.New(
			"integration worker final heartbeat failure",
		)

		state.runner.err = primaryFailure

		storage :=
			&runtimeBoundaryIntegrationHeartbeatStore{
				writeErrors: []error{
					nil,
					finalFailure,
				},
			}

		operations.newHeartbeatStore = func(
			*pgxpool.Pool,
		) (heartbeatStore, error) {
			return storage, nil
		}

		err := operations.worker(
			context.Background(),
		)

		if !errors.Is(err, primaryFailure) ||
			!errors.Is(err, finalFailure) {
			t.Errorf(
				"worker() error = %v, want joined primary and heartbeat failures",
				err,
			)
		}

		if state.runner.observer == nil {
			t.Error(
				"worker lifecycle observer was not attached",
			)
		}
	})

	t.Run("parent cancellation is clean stop", func(t *testing.T) {
		operations, state :=
			runtimeBoundaryIntegrationOperations(t)

		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		cancel()

		state.runner.err = context.Canceled

		if err := operations.worker(ctx); err != nil {
			t.Errorf(
				"worker(canceled) error = %v, want nil",
				err,
			)
		}
	})

	t.Run("successful heartbeat lifecycle", func(t *testing.T) {
		operations, state :=
			runtimeBoundaryIntegrationOperations(t)

		storage :=
			&runtimeBoundaryIntegrationHeartbeatStore{}

		operations.newHeartbeatStore = func(
			*pgxpool.Pool,
		) (heartbeatStore, error) {
			return storage, nil
		}

		if err := operations.worker(
			context.Background(),
		); err != nil {
			t.Fatalf(
				"worker() error = %v",
				err,
			)
		}

		if state.runner.observer == nil {
			t.Error(
				"worker lifecycle observer was not attached",
			)
		}

		storage.mu.Lock()
		defer storage.mu.Unlock()

		if len(storage.writes) != 2 ||
			storage.writes[0].State != "starting" ||
			storage.writes[1].State != "stopping" {
			t.Errorf(
				"heartbeat lifecycle = %#v",
				storage.writes,
			)
		}
	})
}

func runtimeBoundaryIntegrationOperations(
	t *testing.T,
) (runtimeOperations, *runtimeBoundaryIntegrationState) {
	t.Helper()

	state := &runtimeBoundaryIntegrationState{
		database:  &runtimeBoundaryIntegrationDatabase{},
		queue:     &runtimeBoundaryIntegrationQueue{},
		store:     &runtimeBoundaryIntegrationVerifiedSource{},
		publisher: &runtimeBoundaryIntegrationPublisher{},
		runner:    &runtimeBoundaryIntegrationRunner{},
	}

	operations := runtimeOperations{
		getenv: runtimeBoundaryIntegrationEnvironmentFor(
			t,
		).get,

		random: bytes.NewReader(
			make(
				[]byte,
				generatedWorkerIDBytes,
			),
		),

		logger: slog.New(
			slog.NewTextHandler(
				io.Discard,
				nil,
			),
		),

		newDatabase: func(
			context.Context,
			*pgxpool.Config,
		) (databaseConnection, error) {
			return state.database, nil
		},

		newQueue: func(
			*pgxpool.Pool,
			store.QueueConfig,
		) (runtimeQueue, error) {
			return state.queue, nil
		},

		newStore: func(
			*pgxpool.Pool,
		) verifiedOriginSource {
			return state.store
		},

		buildRegistry: func(
			[]store.VerifiedOrigin,
		) ([]publicdata.File, error) {
			return []publicdata.File{
				{
					Path: "registry.json",
					Data: []byte("{}\n"),
				},
			}, nil
		},

		writeRegistry: func(
			context.Context,
			string,
			[]publicdata.File,
		) error {
			return nil
		},

		loadPublishSettings: func(
			environmentGetter,
		) (publishSettings, error) {
			return publishSettings{
				target: githubpublish.Config{
					Owner:      "joshternet",
					Repository: "registry",
					Branch:     "main",
				},
				token: "integration-token",
			}, nil
		},

		readRegistry: func(
			context.Context,
			string,
		) ([]publicdata.File, error) {
			return []publicdata.File{
				{
					Path: "registry.json",
					Data: []byte("{}\n"),
				},
			}, nil
		},

		newPublisher: func(
			githubpublish.Config,
			string,
		) (registryPublisher, error) {
			return state.publisher, nil
		},

		newWorker: func(
			worker.Queue,
			worker.Verifier,
			worker.Config,
		) (workerRunner, error) {
			return state.runner, nil
		},
	}

	return operations, state
}

func runtimeBoundaryIntegrationEnvironmentFor(
	t *testing.T,
) runtimeBoundaryIntegrationEnvironment {
	t.Helper()

	passwordFile := filepath.Join(
		t.TempDir(),
		"database-password",
	)

	if err := os.WriteFile(
		passwordFile,
		[]byte("integration-password\n"),
		0o600,
	); err != nil {
		t.Fatalf(
			"write database password: %v",
			err,
		)
	}

	return runtimeBoundaryIntegrationEnvironment{
		databaseURLEnvironment: "postgres://joshbot_app@localhost:5432/joshbot?sslmode=disable",

		databasePasswordFileEnvironment: passwordFile,

		workerIDEnvironment:       "integration-worker",
		webBotAuthModeEnvironment: webBotAuthModeUnsigned,
	}
}

func runtimeBoundaryIntegrationOrigin(
	t *testing.T,
) origin.Origin {
	t.Helper()

	source, err := origin.Parse(
		"https://example.com",
	)
	if err != nil {
		t.Fatalf(
			"origin.Parse() error = %v",
			err,
		)
	}

	return source
}

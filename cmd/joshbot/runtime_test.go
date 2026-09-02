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
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/publicdata"
	"github.com/joshternet/joshbot/internal/store"
	"github.com/joshternet/joshbot/internal/worker"
)

func TestRuntimeConfigurationDefaults(t *testing.T) {
	settings, err := loadWorkerSettings(
		mapEnvironment{
			workerIDEnvironment: "worker-test",
		}.get,
		bytes.NewReader(
			make([]byte, generatedWorkerIDBytes),
		),
	)
	if err != nil {
		t.Fatalf(
			"loadWorkerSettings() error = %v, want nil",
			err,
		)
	}

	if settings.queue.LeaseDuration !=
		defaultLeaseDuration {
		t.Errorf(
			"lease duration = %v, want %v",
			settings.queue.LeaseDuration,
			defaultLeaseDuration,
		)
	}

	if settings.queue.MinOriginInterval !=
		defaultMinOriginInterval {
		t.Errorf(
			"minimum origin interval = %v, want %v",
			settings.queue.MinOriginInterval,
			defaultMinOriginInterval,
		)
	}

	if settings.worker != (worker.Config{
		WorkerID:        "worker-test",
		PollInterval:    defaultPollInterval,
		JobTimeout:      defaultJobTimeout,
		CompletionGrace: defaultCompletionGrace,
		RecheckInterval: defaultRecheckInterval,
	}) {
		t.Errorf(
			"worker config = %#v, want defaults",
			settings.worker,
		)
	}
}

func TestRuntimeConfigurationOverrides(t *testing.T) {
	environment := mapEnvironment{
		leaseDurationEnvironment:     "10m",
		minOriginIntervalEnvironment: "2m",
		pollIntervalEnvironment:      "5s",
		jobTimeoutEnvironment:        "3m",
		completionGraceEnvironment:   "1m",
		recheckIntervalEnvironment:   "12h",
		workerIDEnvironment:          "configured-worker",
	}

	settings, err := loadWorkerSettings(
		environment.get,
		bytes.NewReader(
			make([]byte, generatedWorkerIDBytes),
		),
	)
	if err != nil {
		t.Fatalf(
			"loadWorkerSettings() error = %v, want nil",
			err,
		)
	}

	if settings.queue != (store.QueueConfig{
		LeaseDuration:     10 * time.Minute,
		MinOriginInterval: 2 * time.Minute,
	}) {
		t.Errorf(
			"queue config = %#v, want overrides",
			settings.queue,
		)
	}

	if settings.worker != (worker.Config{
		WorkerID:        "configured-worker",
		PollInterval:    5 * time.Second,
		JobTimeout:      3 * time.Minute,
		CompletionGrace: time.Minute,
		RecheckInterval: 12 * time.Hour,
	}) {
		t.Errorf(
			"worker config = %#v, want overrides",
			settings.worker,
		)
	}
}

func TestRuntimeConfigurationRejectsInvalidValues(
	t *testing.T,
) {
	tests := []struct {
		name        string
		environment mapEnvironment
	}{
		{
			name: "invalid lease duration",
			environment: mapEnvironment{
				leaseDurationEnvironment: "invalid",
			},
		},
		{
			name: "invalid minimum interval",
			environment: mapEnvironment{
				minOriginIntervalEnvironment: "0s",
			},
		},
		{
			name: "invalid poll interval",
			environment: mapEnvironment{
				pollIntervalEnvironment: "-1s",
			},
		},
		{
			name: "invalid job timeout",
			environment: mapEnvironment{
				jobTimeoutEnvironment: "invalid",
			},
		},
		{
			name: "invalid completion grace",
			environment: mapEnvironment{
				completionGraceEnvironment: "0s",
			},
		},
		{
			name: "invalid recheck interval",
			environment: mapEnvironment{
				recheckIntervalEnvironment: "-1h",
			},
		},
		{
			name: "no completion window",
			environment: mapEnvironment{
				leaseDurationEnvironment:   "5m",
				jobTimeoutEnvironment:      "4m",
				completionGraceEnvironment: "1m",
			},
		},
		{
			name: "completion grace exceeds lease",
			environment: mapEnvironment{
				leaseDurationEnvironment:   "5m",
				jobTimeoutEnvironment:      "1m",
				completionGraceEnvironment: "6m",
			},
		},
		{
			name: "worker ID too long",
			environment: mapEnvironment{
				workerIDEnvironment: strings.Repeat(
					"x",
					maxRuntimeWorkerIDLength+1,
				),
			},
		},
		{
			name: "worker ID invalid UTF-8",
			environment: mapEnvironment{
				workerIDEnvironment: string(
					[]byte{0xff},
				),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings, err := loadWorkerSettings(
				test.environment.get,
				bytes.NewReader(
					make(
						[]byte,
						generatedWorkerIDBytes,
					),
				),
			)
			if !errors.Is(
				err,
				errInvalidRuntimeConfiguration,
			) {
				t.Errorf(
					"loadWorkerSettings() error = %v, "+
						"want invalid configuration",
					err,
				)
			}

			if settings != (workerSettings{}) {
				t.Errorf(
					"settings = %#v, want zero",
					settings,
				)
			}
		})
	}
}

func TestRuntimeConfigurationGeneratesWorkerID(
	t *testing.T,
) {
	randomBytes := make(
		[]byte,
		generatedWorkerIDBytes,
	)
	for index := range randomBytes {
		randomBytes[index] = byte(index)
	}

	settings, err := loadWorkerSettings(
		mapEnvironment{}.get,
		bytes.NewReader(randomBytes),
	)
	if err != nil {
		t.Fatalf(
			"loadWorkerSettings() error = %v, want nil",
			err,
		)
	}

	const want = "worker-" +
		"000102030405060708090a0b0c0d0e0f"

	if settings.worker.WorkerID != want {
		t.Errorf(
			"worker ID = %q, want %q",
			settings.worker.WorkerID,
			want,
		)
	}
}

func TestRuntimeWorkerIDRejectsRandomFailures(
	t *testing.T,
) {
	randomFailure := errors.New(
		"test random failure",
	)

	tests := []struct {
		name   string
		random io.Reader
	}{
		{
			name:   "nil reader",
			random: nil,
		},
		{
			name: "read failure",
			random: failingRuntimeReader{
				err: randomFailure,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workerID, err := runtimeWorkerID(
				"",
				test.random,
			)
			if !errors.Is(
				err,
				errWorkerIDGeneration,
			) {
				t.Errorf(
					"runtimeWorkerID() error = %v, "+
						"want generation failure",
					err,
				)
			}

			if workerID != "" {
				t.Errorf(
					"worker ID = %q, want empty",
					workerID,
				)
			}
		})
	}
}

func TestRuntimeOpenDatabase(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		operations, state := newTestRuntimeOperations(t)

		connection, err := operations.openDatabase(
			context.Background(),
		)
		if err != nil {
			t.Fatalf(
				"openDatabase() error = %v, want nil",
				err,
			)
		}

		if connection != state.database {
			t.Error(
				"openDatabase() returned unexpected connection",
			)
		}
	})

	t.Run("configuration failure", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)
		operations.getenv = mapEnvironment{}.get

		connection, err := operations.openDatabase(
			context.Background(),
		)
		if err == nil {
			t.Fatal(
				"openDatabase() error = nil, want non-nil",
			)
		}

		if connection != nil {
			t.Error(
				"openDatabase() connection is non-nil",
			)
		}
	})

	t.Run("pool failure", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)
		poolFailure := errors.New(
			"test pool failure",
		)
		operations.newDatabase = func(
			context.Context,
			*pgxpool.Config,
		) (databaseConnection, error) {
			return nil, poolFailure
		}

		connection, err := operations.openDatabase(
			context.Background(),
		)
		if !errors.Is(err, poolFailure) {
			t.Errorf(
				"openDatabase() error = %v, want pool failure",
				err,
			)
		}

		if connection != nil {
			t.Error(
				"openDatabase() connection is non-nil",
			)
		}
	})
}

func TestRuntimeWithDatabase(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		operations, state := newTestRuntimeOperations(t)
		called := false

		err := operations.withDatabase(
			context.Background(),
			func(databaseConnection) error {
				called = true
				return nil
			},
		)
		if err != nil {
			t.Fatalf(
				"withDatabase() error = %v, want nil",
				err,
			)
		}

		if !called {
			t.Error("database action was not called")
		}

		if state.database.closeCount != 1 {
			t.Errorf(
				"close count = %d, want 1",
				state.database.closeCount,
			)
		}
	})

	t.Run("action failure", func(t *testing.T) {
		operations, state := newTestRuntimeOperations(t)
		actionFailure := errors.New(
			"test action failure",
		)

		err := operations.withDatabase(
			context.Background(),
			func(databaseConnection) error {
				return actionFailure
			},
		)
		if !errors.Is(err, actionFailure) {
			t.Errorf(
				"withDatabase() error = %v, want action failure",
				err,
			)
		}

		if state.database.closeCount != 1 {
			t.Errorf(
				"close count = %d, want 1",
				state.database.closeCount,
			)
		}
	})

	t.Run("open failure", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)
		operations.getenv = mapEnvironment{}.get

		err := operations.withDatabase(
			context.Background(),
			func(databaseConnection) error {
				t.Fatal(
					"database action called after open failure",
				)
				return nil
			},
		)
		if err == nil {
			t.Error(
				"withDatabase() error = nil, want non-nil",
			)
		}
	})
}

func TestRuntimeHealth(t *testing.T) {
	operations, state := newTestRuntimeOperations(t)

	if err := operations.health(
		context.Background(),
	); err != nil {
		t.Fatalf(
			"health() error = %v, want nil",
			err,
		)
	}

	state.database.pingErr = errors.New(
		"test ping failure",
	)
	if err := operations.health(
		context.Background(),
	); err == nil {
		t.Error("health() error = nil, want non-nil")
	}
}

func TestRuntimeMigrate(t *testing.T) {
	operations, state := newTestRuntimeOperations(t)

	if err := operations.migrate(
		context.Background(),
	); err != nil {
		t.Fatalf(
			"migrate() error = %v, want nil",
			err,
		)
	}

	if state.migrateCount != 1 {
		t.Errorf(
			"migration count = %d, want 1",
			state.migrateCount,
		)
	}

	state.migrateErr = errors.New(
		"test migration failure",
	)
	if err := operations.migrate(
		context.Background(),
	); err == nil {
		t.Error("migrate() error = nil, want non-nil")
	}
}

func TestRuntimeSchedule(t *testing.T) {
	source, err := origin.Parse(
		"https://example.com",
	)
	if err != nil {
		t.Fatal("parse test origin")
	}

	t.Run("success", func(t *testing.T) {
		operations, state := newTestRuntimeOperations(t)

		if err := operations.schedule(
			context.Background(),
			source,
		); err != nil {
			t.Fatalf(
				"schedule() error = %v, want nil",
				err,
			)
		}

		if state.queue.scheduled != source {
			t.Errorf(
				"scheduled origin = %v, want %v",
				state.queue.scheduled,
				source,
			)
		}

		if !state.queue.availableAt.Equal(
			time.Unix(0, 0).UTC(),
		) {
			t.Errorf(
				"available at = %v, want Unix epoch",
				state.queue.availableAt,
			)
		}
	})

	t.Run("configuration failure", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)
		environment := runtimeTestEnvironment(t)
		environment[leaseDurationEnvironment] = "invalid"
		operations.getenv = environment.get

		if err := operations.schedule(
			context.Background(),
			source,
		); err == nil {
			t.Error(
				"schedule() error = nil, want non-nil",
			)
		}
	})

	t.Run("queue construction failure", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)
		queueFailure := errors.New(
			"test queue construction failure",
		)
		operations.newQueue = func(
			*pgxpool.Pool,
			store.QueueConfig,
		) (runtimeQueue, error) {
			return nil, queueFailure
		}

		if err := operations.schedule(
			context.Background(),
			source,
		); !errors.Is(err, queueFailure) {
			t.Errorf(
				"schedule() error = %v, want queue failure",
				err,
			)
		}
	})

	t.Run("schedule failure", func(t *testing.T) {
		operations, state := newTestRuntimeOperations(t)
		state.queue.scheduleErr = errors.New(
			"test schedule failure",
		)

		if err := operations.schedule(
			context.Background(),
			source,
		); err == nil {
			t.Error(
				"schedule() error = nil, want non-nil",
			)
		}
	})
}

func TestRuntimeExport(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		operations, state := newTestRuntimeOperations(t)

		if err := operations.export(
			context.Background(),
			"snapshot",
		); err != nil {
			t.Fatalf(
				"export() error = %v, want nil",
				err,
			)
		}

		if state.outputRoot != "snapshot" {
			t.Errorf(
				"output root = %q, want snapshot",
				state.outputRoot,
			)
		}

		if len(state.writtenFiles) != 1 ||
			state.writtenFiles[0].Path !=
				"registry.json" {
			t.Errorf(
				"written files = %#v, want registry",
				state.writtenFiles,
			)
		}
	})

	t.Run("query failure", func(t *testing.T) {
		operations, state := newTestRuntimeOperations(t)
		state.store.err = errors.New(
			"test query failure",
		)

		if err := operations.export(
			context.Background(),
			"snapshot",
		); err == nil {
			t.Error("export() error = nil, want non-nil")
		}
	})

	t.Run("build failure", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)
		buildFailure := errors.New(
			"test build failure",
		)
		operations.buildRegistry = func(
			[]store.VerifiedOrigin,
		) ([]publicdata.File, error) {
			return nil, buildFailure
		}

		if err := operations.export(
			context.Background(),
			"snapshot",
		); !errors.Is(err, buildFailure) {
			t.Errorf(
				"export() error = %v, want build failure",
				err,
			)
		}
	})

	t.Run("write failure", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)
		writeFailure := errors.New(
			"test write failure",
		)
		operations.writeRegistry = func(
			context.Context,
			string,
			[]publicdata.File,
		) error {
			return writeFailure
		}

		if err := operations.export(
			context.Background(),
			"snapshot",
		); !errors.Is(err, writeFailure) {
			t.Errorf(
				"export() error = %v, want write failure",
				err,
			)
		}
	})
}

func TestRuntimeWorker(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		operations, state := newTestRuntimeOperations(t)

		if err := operations.worker(
			context.Background(),
		); err != nil {
			t.Fatalf(
				"worker() error = %v, want nil",
				err,
			)
		}

		if state.newWorkerCount != 1 {
			t.Errorf(
				"worker construction count = %d, want 1",
				state.newWorkerCount,
			)
		}

		if state.verifier == nil {
			t.Error("worker verifier is nil")
		}

		if state.workerConfig.WorkerID !=
			"worker-test" {
			t.Errorf(
				"worker ID = %q, want worker-test",
				state.workerConfig.WorkerID,
			)
		}
	})

	t.Run("configuration failure", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)
		environment := runtimeTestEnvironment(t)
		environment[jobTimeoutEnvironment] = "10m"
		operations.getenv = environment.get

		if err := operations.worker(
			context.Background(),
		); err == nil {
			t.Error("worker() error = nil, want non-nil")
		}
	})

	t.Run("queue construction failure", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)
		queueFailure := errors.New(
			"test queue failure",
		)
		operations.newQueue = func(
			*pgxpool.Pool,
			store.QueueConfig,
		) (runtimeQueue, error) {
			return nil, queueFailure
		}

		if err := operations.worker(
			context.Background(),
		); !errors.Is(err, queueFailure) {
			t.Errorf(
				"worker() error = %v, want queue failure",
				err,
			)
		}
	})

	t.Run("worker construction failure", func(t *testing.T) {
		operations, _ := newTestRuntimeOperations(t)
		constructionFailure := errors.New(
			"test worker construction failure",
		)
		operations.newWorker = func(
			worker.Queue,
			worker.Verifier,
			worker.Config,
		) (workerRunner, error) {
			return nil, constructionFailure
		}

		if err := operations.worker(
			context.Background(),
		); !errors.Is(
			err,
			constructionFailure,
		) {
			t.Errorf(
				"worker() error = %v, want construction failure",
				err,
			)
		}
	})

	t.Run("runtime failure", func(t *testing.T) {
		operations, state := newTestRuntimeOperations(t)
		state.runner.err = errors.New(
			"test worker runtime failure",
		)

		if err := operations.worker(
			context.Background(),
		); err == nil {
			t.Error("worker() error = nil, want non-nil")
		}
	})

	t.Run("parent cancellation", func(t *testing.T) {
		operations, state := newTestRuntimeOperations(t)
		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		cancel()
		state.runner.err = context.Canceled

		if err := operations.worker(ctx); err != nil {
			t.Errorf(
				"worker() error = %v, want nil",
				err,
			)
		}
	})
}

func TestRuntimeProductionAdapters(t *testing.T) {
	config, err := pgxpool.ParseConfig(
		"postgres://localhost:1/joshbot" +
			"?sslmode=disable&connect_timeout=1",
	)
	if err != nil {
		t.Fatalf(
			"ParseConfig() error = %v, want nil",
			err,
		)
	}

	database, err := newPostgresDatabase(
		context.Background(),
		config,
	)
	if err != nil {
		t.Fatalf(
			"newPostgresDatabase() error = %v, want nil",
			err,
		)
	}

	if database.Pool() == nil {
		t.Error("database pool is nil")
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := database.Ping(ctx); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Errorf(
			"Ping() error = %v, want context.Canceled",
			err,
		)
	}
	database.Close()

	if err := migrateDatabase(
		context.Background(),
		nil,
	); err == nil {
		t.Error(
			"migrateDatabase() error = nil, want non-nil",
		)
	}

	if _, err := newStoreQueue(
		nil,
		store.QueueConfig{
			LeaseDuration:     time.Minute,
			MinOriginInterval: time.Second,
		},
	); err == nil {
		t.Error(
			"newStoreQueue() error = nil, want non-nil",
		)
	}

	if newVerifiedOriginStore(nil) == nil {
		t.Error("newVerifiedOriginStore() is nil")
	}

	files, err := buildRegistry(nil)
	if err != nil {
		t.Fatalf(
			"buildRegistry() error = %v, want nil",
			err,
		)
	}

	if len(files) != 1 {
		t.Errorf(
			"buildRegistry() file count = %d, want 1",
			len(files),
		)
	}

	if err := writeRegistry(
		ctx,
		"snapshot",
		nil,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"writeRegistry() error = %v, want context.Canceled",
			err,
		)
	}

	if _, err := newWorkerRuntime(
		nil,
		nil,
		worker.Config{},
	); err == nil {
		t.Error(
			"newWorkerRuntime() error = nil, want non-nil",
		)
	}

	operations := newRuntimeOperations(io.Discard)
	if operations.logger == nil ||
		operations.newDatabase == nil ||
		operations.migrateDatabase == nil ||
		operations.newQueue == nil ||
		operations.newStore == nil ||
		operations.buildRegistry == nil ||
		operations.writeRegistry == nil ||
		operations.newWorker == nil {
		t.Error(
			"newRuntimeOperations() omitted a dependency",
		)
	}
}

type mapEnvironment map[string]string

func (environment mapEnvironment) get(
	name string,
) string {
	return environment[name]
}

type failingRuntimeReader struct {
	err error
}

func (reader failingRuntimeReader) Read(
	[]byte,
) (int, error) {
	return 0, reader.err
}

type runtimeTestState struct {
	database       *fakeRuntimeDatabase
	queue          *fakeRuntimeQueue
	store          *fakeVerifiedOriginSource
	runner         *fakeWorkerRunner
	migrateCount   int
	migrateErr     error
	newWorkerCount int
	verifier       worker.Verifier
	workerConfig   worker.Config
	outputRoot     string
	writtenFiles   []publicdata.File
}

func newTestRuntimeOperations(
	t *testing.T,
) (runtimeOperations, *runtimeTestState) {
	t.Helper()

	state := &runtimeTestState{
		database: &fakeRuntimeDatabase{},
		queue:    &fakeRuntimeQueue{},
		store: &fakeVerifiedOriginSource{
			verified: nil,
		},
		runner: &fakeWorkerRunner{},
	}

	operations := runtimeOperations{
		getenv: runtimeTestEnvironment(t).get,
		random: bytes.NewReader(
			make([]byte, generatedWorkerIDBytes),
		),
		logger: slog.New(
			slog.NewTextHandler(io.Discard, nil),
		),
		newDatabase: func(
			context.Context,
			*pgxpool.Config,
		) (databaseConnection, error) {
			return state.database, nil
		},
		migrateDatabase: func(
			context.Context,
			*pgxpool.Pool,
		) error {
			state.migrateCount++

			return state.migrateErr
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
			_ context.Context,
			root string,
			files []publicdata.File,
		) error {
			state.outputRoot = root
			state.writtenFiles = files

			return nil
		},
		newWorker: func(
			_ worker.Queue,
			verifier worker.Verifier,
			config worker.Config,
		) (workerRunner, error) {
			state.newWorkerCount++
			state.verifier = verifier
			state.workerConfig = config

			return state.runner, nil
		},
	}

	return operations, state
}

func runtimeTestEnvironment(
	t *testing.T,
) mapEnvironment {
	t.Helper()

	passwordFile := filepath.Join(
		t.TempDir(),
		"database-password",
	)
	if err := os.WriteFile(
		passwordFile,
		[]byte("test-password\n"),
		0o600,
	); err != nil {
		t.Fatalf(
			"write test password file: %v",
			err,
		)
	}

	return mapEnvironment{
		databaseURLEnvironment: "postgres://" +
			"joshbot_app@localhost:5432/joshbot" +
			"?sslmode=disable",
		databasePasswordFileEnvironment: passwordFile,
		workerIDEnvironment:             "worker-test",
	}
}

type fakeRuntimeDatabase struct {
	pingErr    error
	closeCount int
}

func (database *fakeRuntimeDatabase) Close() {
	database.closeCount++
}

func (database *fakeRuntimeDatabase) Ping(
	context.Context,
) error {
	return database.pingErr
}

func (database *fakeRuntimeDatabase) Pool() *pgxpool.Pool {
	return nil
}

type fakeRuntimeQueue struct {
	scheduled   origin.Origin
	availableAt time.Time
	scheduleErr error
}

func (queue *fakeRuntimeQueue) Schedule(
	_ context.Context,
	source origin.Origin,
	availableAt time.Time,
) error {
	queue.scheduled = source
	queue.availableAt = availableAt

	return queue.scheduleErr
}

func (queue *fakeRuntimeQueue) Claim(
	context.Context,
	string,
) (store.Lease, bool, error) {
	return store.Lease{}, false, nil
}

func (queue *fakeRuntimeQueue) CompleteVerification(
	context.Context,
	store.Lease,
	declaration.Result,
	time.Duration,
) error {
	return nil
}

type fakeVerifiedOriginSource struct {
	verified []store.VerifiedOrigin
	err      error
}

func (source *fakeVerifiedOriginSource) VerifiedOrigins(
	context.Context,
) ([]store.VerifiedOrigin, error) {
	return source.verified, source.err
}

type fakeWorkerRunner struct {
	err error
}

func (runner *fakeWorkerRunner) Run(
	context.Context,
) error {
	return runner.err
}

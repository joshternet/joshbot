package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/database"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/githubpublish"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/publicdata"
	"github.com/joshternet/joshbot/internal/robots"
	"github.com/joshternet/joshbot/internal/store"
	"github.com/joshternet/joshbot/internal/worker"
)

type databaseConnection interface {
	Close()
	Ping(context.Context) error
	Pool() *pgxpool.Pool
}

type runtimeQueue interface {
	worker.Queue

	Schedule(
		context.Context,
		origin.Origin,
		time.Time,
	) error
}

type verifiedOriginSource interface {
	VerifiedOrigins(
		context.Context,
	) ([]store.VerifiedOrigin, error)
}

type workerRunner interface {
	Run(context.Context) error
}

type registryPublisher interface {
	Publish(
		context.Context,
		[]publicdata.File,
	) (githubpublish.Result, error)
}

type runtimeOperations struct {
	getenv environmentGetter
	random io.Reader
	logger *slog.Logger

	newDatabase func(
		context.Context,
		*pgxpool.Config,
	) (databaseConnection, error)

	migrateDatabase func(
		context.Context,
		*pgxpool.Pool,
	) error

	newQueue func(
		*pgxpool.Pool,
		store.QueueConfig,
	) (runtimeQueue, error)

	newStore func(
		*pgxpool.Pool,
	) verifiedOriginSource

	newDiscoveryStore func(
		*pgxpool.Pool,
	) (crawlSeedStore, error)

	buildRegistry func(
		[]store.VerifiedOrigin,
	) ([]publicdata.File, error)

	writeRegistry func(
		context.Context,
		string,
		[]publicdata.File,
	) error

	loadPublishSettings func(
		environmentGetter,
	) (publishSettings, error)

	readRegistry func(
		context.Context,
		string,
	) ([]publicdata.File, error)

	newPublisher func(
		githubpublish.Config,
		string,
	) (registryPublisher, error)

	newWorker func(
		worker.Queue,
		worker.Verifier,
		worker.Config,
	) (workerRunner, error)
}

type postgresDatabase struct {
	pool *pgxpool.Pool
}

func newRuntimeOperations(
	stderr io.Writer,
) runtimeOperations {
	return runtimeOperations{
		getenv:              os.Getenv,
		random:              rand.Reader,
		logger:              slog.New(slog.NewTextHandler(stderr, nil)),
		newDatabase:         newPostgresDatabase,
		migrateDatabase:     migrateDatabase,
		newQueue:            newStoreQueue,
		newStore:            newVerifiedOriginStore,
		newDiscoveryStore:   newRuntimeCrawlSeedStore,
		buildRegistry:       buildRegistry,
		writeRegistry:       writeRegistry,
		loadPublishSettings: loadPublishSettings,
		readRegistry:        readRegistry,
		newPublisher:        newGitHubPublisher,
		newWorker:           newWorkerRuntime,
	}
}

func newPostgresDatabase(
	ctx context.Context,
	config *pgxpool.Config,
) (databaseConnection, error) {
	pool, err := pgxpool.NewWithConfig(ctx, config)

	return &postgresDatabase{
		pool: pool,
	}, err
}

func (database *postgresDatabase) Close() {
	database.pool.Close()
}

func (database *postgresDatabase) Ping(
	ctx context.Context,
) error {
	return database.pool.Ping(ctx)
}

func (database *postgresDatabase) Pool() *pgxpool.Pool {
	return database.pool
}

func migrateDatabase(
	ctx context.Context,
	pool *pgxpool.Pool,
) error {
	return store.Migrate(ctx, pool)
}

func newStoreQueue(
	pool *pgxpool.Pool,
	config store.QueueConfig,
) (runtimeQueue, error) {
	return store.NewQueue(pool, config)
}

func newVerifiedOriginStore(
	pool *pgxpool.Pool,
) verifiedOriginSource {
	return store.New(pool)
}

func buildRegistry(
	verified []store.VerifiedOrigin,
) ([]publicdata.File, error) {
	return publicdata.Build(verified)
}

func writeRegistry(
	ctx context.Context,
	root string,
	files []publicdata.File,
) error {
	return publicdata.WriteDirectory(
		ctx,
		root,
		files,
	)
}

func readRegistry(
	ctx context.Context,
	root string,
) ([]publicdata.File, error) {
	return publicdata.ReadDirectory(
		ctx,
		root,
	)
}

func newGitHubPublisher(
	config githubpublish.Config,
	token string,
) (registryPublisher, error) {
	return githubpublish.New(
		config,
		token,
	)
}

func newWorkerRuntime(
	queue worker.Queue,
	verifier worker.Verifier,
	config worker.Config,
) (workerRunner, error) {
	return worker.New(queue, verifier, config)
}

func (operations runtimeOperations) openDatabase(
	ctx context.Context,
) (databaseConnection, error) {
	config, err := database.LoadConfig(
		operations.getenv(databaseURLEnvironment),
		operations.getenv(
			databasePasswordFileEnvironment,
		),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"load database configuration: %w",
			err,
		)
	}

	connection, err := operations.newDatabase(
		ctx,
		config,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"open PostgreSQL pool: %w",
			err,
		)
	}

	return connection, nil
}

func (operations runtimeOperations) withDatabase(
	ctx context.Context,
	action func(databaseConnection) error,
) error {
	connection, err := operations.openDatabase(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()

	return action(connection)
}

func (operations runtimeOperations) health(
	ctx context.Context,
) error {
	return operations.withDatabase(
		ctx,
		func(connection databaseConnection) error {
			if err := connection.Ping(ctx); err != nil {
				return fmt.Errorf(
					"ping PostgreSQL: %w",
					err,
				)
			}

			return nil
		},
	)
}

func (operations runtimeOperations) migrate(
	ctx context.Context,
) error {
	return operations.withDatabase(
		ctx,
		func(connection databaseConnection) error {
			return operations.migrateDatabase(
				ctx,
				connection.Pool(),
			)
		},
	)
}

func (operations runtimeOperations) schedule(
	ctx context.Context,
	source origin.Origin,
) error {
	queueConfig, err := loadQueueConfig(
		operations.getenv,
	)
	if err != nil {
		return err
	}

	return operations.withDatabase(
		ctx,
		func(connection databaseConnection) error {
			queue, err := operations.newQueue(
				connection.Pool(),
				queueConfig,
			)
			if err != nil {
				return fmt.Errorf(
					"construct queue: %w",
					err,
				)
			}

			return queue.Schedule(
				ctx,
				source,
				time.Unix(0, 0).UTC(),
			)
		},
	)
}

func (operations runtimeOperations) export(
	ctx context.Context,
	root string,
) error {
	return operations.withDatabase(
		ctx,
		func(connection databaseConnection) error {
			verified, err := operations.newStore(
				connection.Pool(),
			).VerifiedOrigins(ctx)
			if err != nil {
				return fmt.Errorf(
					"query verified origins: %w",
					err,
				)
			}

			files, err := operations.buildRegistry(
				verified,
			)
			if err != nil {
				return fmt.Errorf(
					"build public registry: %w",
					err,
				)
			}

			return operations.writeRegistry(
				ctx,
				root,
				files,
			)
		},
	)
}

func (operations runtimeOperations) publish(
	ctx context.Context,
	root string,
) (githubpublish.Result, error) {
	settings, err := operations.loadPublishSettings(
		operations.getenv,
	)
	if err != nil {
		return githubpublish.Result{},
			fmt.Errorf(
				"load publication configuration: %w",
				err,
			)
	}

	files, err := operations.readRegistry(
		ctx,
		root,
	)
	if err != nil {
		return githubpublish.Result{},
			fmt.Errorf(
				"read public registry snapshot: %w",
				err,
			)
	}

	publisher, err := operations.newPublisher(
		settings.target,
		settings.token,
	)
	if err != nil {
		return githubpublish.Result{},
			fmt.Errorf(
				"construct GitHub publisher: %w",
				err,
			)
	}

	result, err := publisher.Publish(
		ctx,
		files,
	)
	if err != nil {
		return githubpublish.Result{},
			fmt.Errorf(
				"publish public registry: %w",
				err,
			)
	}

	return result, nil
}

func (operations runtimeOperations) worker(
	ctx context.Context,
) error {
	settings, err := loadWorkerSettings(
		operations.getenv,
		operations.random,
	)
	if err != nil {
		return err
	}

	return operations.withDatabase(
		ctx,
		func(connection databaseConnection) error {
			queue, err := operations.newQueue(
				connection.Pool(),
				settings.queue,
			)
			if err != nil {
				return fmt.Errorf(
					"construct queue: %w",
					err,
				)
			}

			checker := robots.NewChecker(
				net.DefaultResolver,
				&net.Dialer{},
			)
			verifier := declaration.NewVerifier(
				checker,
			)

			runtimeWorker, err :=
				operations.newWorker(
					queue,
					verifier,
					settings.worker,
				)
			if err != nil {
				return fmt.Errorf(
					"construct worker: %w",
					err,
				)
			}

			operations.logger.Info(
				"worker started",
				"worker_id",
				settings.worker.WorkerID,
			)

			err = runtimeWorker.Run(ctx)
			if err == nil {
				operations.logger.Info(
					"worker stopped",
					"worker_id",
					settings.worker.WorkerID,
				)

				return nil
			}

			if contextError := ctx.Err(); contextError != nil &&
				errors.Is(err, contextError) {
				operations.logger.Info(
					"worker stopped",
					"worker_id",
					settings.worker.WorkerID,
				)

				return nil
			}

			return fmt.Errorf(
				"run worker: %w",
				err,
			)
		},
	)
}

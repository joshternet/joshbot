package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/robots"
	"github.com/joshternet/joshbot/internal/store"
)

var (
	errDiscoveryRunnerUnavailable = errors.New(
		"discovery runner is unavailable",
	)
	errDiscoveryLoggerUnavailable = errors.New(
		"discovery logger is unavailable",
	)
)

type discoveryRunner interface {
	Run(context.Context) error
	RunOnce(context.Context) (discovery.Report, error)
}

func newDiscoveryRuntime(
	pool *pgxpool.Pool,
	config discovery.Config,
) (discoveryRunner, error) {
	discoveryStore, err := store.NewDiscoveryStore(pool)
	if err != nil {
		return nil, fmt.Errorf(
			"construct discovery store: %w",
			err,
		)
	}

	checker := robots.NewChecker(
		net.DefaultResolver,
		&net.Dialer{},
	)
	crawler := discovery.NewCrawler(checker)

	runner, err := discovery.NewRunner(
		discoveryStore,
		crawler,
		config,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"construct discovery runner: %w",
			err,
		)
	}

	return runner, nil
}

func (operations runtimeOperations) discover(
	ctx context.Context,
	once bool,
) error {
	config, err := loadDiscoveryConfig(
		operations.getenv,
	)
	if err != nil {
		return err
	}

	return operations.withDatabase(
		ctx,
		func(connection databaseConnection) error {
			runner, err := newDiscoveryRuntime(
				connection.Pool(),
				config,
			)
			if err != nil {
				return err
			}

			return executeDiscovery(
				ctx,
				once,
				runner,
				operations.logger,
			)
		},
	)
}

func executeDiscovery(
	ctx context.Context,
	once bool,
	runner discoveryRunner,
	logger *slog.Logger,
) error {
	if runner == nil {
		return errDiscoveryRunnerUnavailable
	}

	if logger == nil {
		return errDiscoveryLoggerUnavailable
	}

	if once {
		_, err := runner.RunOnce(ctx)
		if err != nil {
			return fmt.Errorf(
				"run one discovery attempt: %w",
				err,
			)
		}

		return nil
	}

	logger.Info("discovery started")

	err := runner.Run(ctx)
	if err == nil {
		logger.Info("discovery stopped")

		return nil
	}

	if contextErr := ctx.Err(); contextErr != nil &&
		errors.Is(err, contextErr) {
		logger.Info("discovery stopped")

		return nil
	}

	return fmt.Errorf(
		"run discovery: %w",
		err,
	)
}

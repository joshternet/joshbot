package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
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
	RunOnce(context.Context) (discovery.CrawlReport, error)
}

type discoveryCandidateStore interface {
	RecordDiscovery(
		context.Context,
		origin.Origin,
		[]discovery.Candidate,
	) (discovery.RecordResult, error)
}

type runtimeCandidateSink struct {
	store discoveryCandidateStore
}

func (sink runtimeCandidateSink) RecordCandidates(
	ctx context.Context,
	source origin.Origin,
	candidates []discovery.Candidate,
) error {
	_, err := sink.store.RecordDiscovery(
		ctx,
		source,
		candidates,
	)

	return err
}

func newDiscoveryRuntime(
	pool *pgxpool.Pool,
	settings crawlRuntimeSettings,
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

	crawler, err := discovery.NewMultiPageCrawler(
		checker,
		runtimeCandidateSink{
			store: discoveryStore,
		},
		settings.crawl,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"construct multi-page crawler: %w",
			err,
		)
	}

	runner, err := discovery.NewCrawlRunner(
		discoveryStore,
		crawler,
		settings.runner,
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
	settings, err := loadCrawlRuntimeSettings(
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
				settings,
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

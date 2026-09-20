package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/netguard"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
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
	store    discoveryCandidateStore
	resolver netguard.Resolver
}

type runDiscoveryCandidateStore interface {
	RecordDiscoveryForRun(
		context.Context,
		discovery.CrawlRunID,
		origin.Origin,
		[]discovery.Candidate,
	) (discovery.RecordResult, error)
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

// RecordCandidatesForRun preserves evidence and its durable crawl-run
// attribution before any network admission decision.
func (sink runtimeCandidateSink) RecordCandidatesForRun(
	ctx context.Context,
	runID discovery.CrawlRunID,
	source origin.Origin,
	candidates []discovery.Candidate,
) error {
	runStore, ok := sink.store.(runDiscoveryCandidateStore)
	if !ok {
		return sink.RecordCandidates(ctx, source, candidates)
	}

	_, err := runStore.RecordDiscoveryForRun(
		ctx,
		runID,
		source,
		candidates,
	)

	return err
}

type automaticCandidateStore interface {
	PendingAutomaticCandidates(
		context.Context,
		discovery.CrawlRunID,
	) ([]discovery.Candidate, error)
	AdmitAutomaticCandidates(
		context.Context,
		discovery.CrawlRunID,
		[]discovery.Candidate,
	) error
}

type retryingAutomaticCandidateStore interface {
	CompleteAutomaticCandidates(
		context.Context,
		discovery.CrawlRunID,
		[]store.AutomaticCandidateResult,
	) error
}

// FinalizeCandidates resolves every pending link through netguard immediately
// before asking PostgreSQL to admit candidates under this crawl run's budgets.
func (sink runtimeCandidateSink) FinalizeCandidates(
	ctx context.Context,
	runID discovery.CrawlRunID,
) error {
	admissionStore, ok := sink.store.(automaticCandidateStore)
	if !ok {
		return nil
	}

	pending, err := admissionStore.PendingAutomaticCandidates(
		ctx,
		runID,
	)
	if err != nil {
		return err
	}

	decisions := make(
		[]store.AutomaticCandidateResult,
		0,
		len(pending),
	)
	eligible := make(
		[]discovery.Candidate,
		0,
		len(pending),
	)

	for _, candidate := range pending {
		category := retry.CategoryNone

		for range retry.MaxAttemptsPerCycle {
			_, resolveErr := netguard.Resolve(
				ctx,
				candidate.Origin,
				sink.resolver,
			)
			if resolveErr == nil {
				category = retry.CategoryNone

				break
			}

			category = netguard.FailureCategory(
				resolveErr,
			)
			if !category.Transient() {
				break
			}
		}

		decisions = append(
			decisions,
			store.AutomaticCandidateResult{
				Candidate:       candidate,
				FailureCategory: category,
			},
		)

		if category == retry.CategoryNone {
			eligible = append(
				eligible,
				candidate,
			)
		}
	}

	if retryingStore, ok :=
		sink.store.(retryingAutomaticCandidateStore); ok {
		return retryingStore.CompleteAutomaticCandidates(
			ctx,
			runID,
			decisions,
		)
	}

	return admissionStore.AdmitAutomaticCandidates(
		ctx,
		runID,
		eligible,
	)
}

type discoveryRuntimeStore interface {
	discoveryCandidateStore
	discovery.CrawlSourceStore
	discovery.CrawlTelemetry

	ReconcileAutomaticCrawlPolicy(
		context.Context,
	) (int, error)
}

type discoveryRuntimeStoreFactory func(
	*pgxpool.Pool,
	store.AutomaticCrawlConfig,
) (discoveryRuntimeStore, error)

func newRuntimeDiscoveryStore(
	pool *pgxpool.Pool,
	config store.AutomaticCrawlConfig,
) (discoveryRuntimeStore, error) {
	return store.NewDiscoveryStoreWithAutomaticCrawling(
		pool,
		config,
	)
}

func newDiscoveryRuntime(
	ctx context.Context,
	pool *pgxpool.Pool,
	settings crawlRuntimeSettings,
) (discoveryRunner, error) {
	return newDiscoveryRuntimeWithStoreFactory(
		ctx,
		pool,
		settings,
		newRuntimeDiscoveryStore,
	)
}

func newDiscoveryRuntimeWithStoreFactory(
	ctx context.Context,
	pool *pgxpool.Pool,
	settings crawlRuntimeSettings,
	newStore discoveryRuntimeStoreFactory,
) (discoveryRunner, error) {
	discoveryStore, err := newStore(
		pool,
		settings.automatic,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"construct discovery store: %w",
			err,
		)
	}

	if _, err := discoveryStore.ReconcileAutomaticCrawlPolicy(
		ctx,
	); err != nil {
		return nil, fmt.Errorf(
			"reconcile automatic crawl policy: %w",
			err,
		)
	}

	checker := robots.NewCheckerWithRequestDelayAndSigner(
		net.DefaultResolver,
		&net.Dialer{},
		settings.crawl.RequestDelay,
		settings.signer,
	)

	crawler, err := discovery.NewMultiPageCrawlerWithTelemetry(
		checker,
		runtimeCandidateSink{
			store:    discoveryStore,
			resolver: net.DefaultResolver,
		},
		discoveryStore,
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

	signer, err := loadWebBotAuthSigner(
		operations.getenv,
	)
	if err != nil {
		return err
	}
	settings.signer = signer

	return operations.withDatabase(
		ctx,
		func(connection databaseConnection) (operationErr error) {
			var heartbeat *serviceHeartbeat

			if operations.newHeartbeatStore != nil &&
				ctx.Err() == nil {
				heartbeatStorage, err :=
					operations.newHeartbeatStore(
						connection.Pool(),
					)
				if err != nil {
					return fmt.Errorf(
						"construct discovery heartbeat store: %w",
						err,
					)
				}

				if _, err := heartbeatStorage.PurgeCrawlTelemetry(
					ctx,
					settings.telemetryRetention,
				); err != nil {
					return fmt.Errorf(
						"purge crawl telemetry: %w",
						err,
					)
				}

				instanceID := os.Getenv(
					"HOSTNAME",
				)
				if instanceID == "" {
					instanceID = "discovery"
				}

				heartbeat, err =
					startServiceHeartbeatWithReporter(
						ctx,
						heartbeatStorage,
						"discovery",
						instanceID,
						serviceHeartbeatInterval,
						heartbeatErrorReporter(
							operations.logger,
						),
					)
				if err != nil {
					return err
				}

				defer func() {
					state := "stopping"

					if operationErr != nil &&
						ctx.Err() == nil {
						state = "failed"
					}

					operationErr = errors.Join(
						operationErr,
						heartbeat.stop(
							ctx,
							state,
						),
					)
				}()
			}

			runner, err :=
				operations.newDiscoveryRunner(
					ctx,
					connection.Pool(),
					settings,
				)
			if err != nil {
				return err
			}

			if observed, ok := runner.(interface {
				SetLifecycleObserver(
					discovery.LifecycleObserver,
				)
			}); ok && heartbeat != nil {
				observed.SetLifecycleObserver(
					heartbeat,
				)
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

func newRuntimeHeartbeatStore(
	pool *pgxpool.Pool,
) (heartbeatStore, error) {
	return store.NewDiscoveryStore(
		pool,
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
		_, err := runner.RunOnce(
			ctx,
		)
		if err != nil {
			return fmt.Errorf(
				"run one discovery attempt: %w",
				err,
			)
		}

		return nil
	}

	logger.Info(
		"discovery started",
	)

	err := runner.Run(
		ctx,
	)
	if err == nil {
		logger.Info(
			"discovery stopped",
		)

		return nil
	}

	if contextErr := ctx.Err(); contextErr != nil &&
		errors.Is(
			err,
			contextErr,
		) {
		logger.Info(
			"discovery stopped",
		)

		return nil
	}

	return fmt.Errorf(
		"run discovery: %w",
		err,
	)
}

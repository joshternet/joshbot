package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/store"
)

func TestRuntimeStoreConstructorsCoverSuccessPaths(
	t *testing.T,
) {
	pool := new(pgxpool.Pool)

	seedStore, err :=
		newRuntimeCrawlSeedStore(pool)
	if err != nil || seedStore == nil {
		t.Fatalf(
			"newRuntimeCrawlSeedStore() = %#v, %v; want store, nil",
			seedStore,
			err,
		)
	}

	sourceStore, err :=
		newRuntimeCrawlSourceStore(pool)
	if err != nil || sourceStore == nil {
		t.Fatalf(
			"newRuntimeCrawlSourceStore() = %#v, %v; want store, nil",
			sourceStore,
			err,
		)
	}

	heartbeatStorage, err :=
		newRuntimeHeartbeatStore(pool)
	if err != nil || heartbeatStorage == nil {
		t.Fatalf(
			"newRuntimeHeartbeatStore() = %#v, %v; want store, nil",
			heartbeatStorage,
			err,
		)
	}
}

func TestNewDiscoveryRuntimeCoversProductionStoreFactory(
	t *testing.T,
) {
	runner, err := newDiscoveryRuntime(
		context.Background(),
		new(pgxpool.Pool),
		testCrawlRuntimeSettings(t),
	)
	if err != nil {
		t.Fatalf(
			"newDiscoveryRuntime() error = %v, want nil",
			err,
		)
	}

	if runner == nil {
		t.Fatal(
			"newDiscoveryRuntime() runner = nil, want runner",
		)
	}
}

func TestNewDiscoveryRuntimeWithStoreFactory(
	t *testing.T,
) {
	storeFailure := errors.New(
		"store construction failure",
	)
	reconcileFailure := errors.New(
		"reconcile failure",
	)

	t.Run(
		"store construction failure",
		func(t *testing.T) {
			runner, err :=
				newDiscoveryRuntimeWithStoreFactory(
					context.Background(),
					nil,
					testCrawlRuntimeSettings(t),
					func(
						*pgxpool.Pool,
						store.AutomaticCrawlConfig,
					) (discoveryRuntimeStore, error) {
						return nil, storeFailure
					},
				)

			if runner != nil ||
				!errors.Is(
					err,
					storeFailure,
				) {
				t.Fatalf(
					"newDiscoveryRuntimeWithStoreFactory() = %#v, %v; want nil, store failure",
					runner,
					err,
				)
			}
		},
	)

	t.Run(
		"reconcile failure",
		func(t *testing.T) {
			runner, err :=
				newDiscoveryRuntimeWithStoreFactory(
					context.Background(),
					nil,
					testCrawlRuntimeSettings(t),
					func(
						*pgxpool.Pool,
						store.AutomaticCrawlConfig,
					) (discoveryRuntimeStore, error) {
						return &coverageDiscoveryRuntimeStore{
							reconcileErr: reconcileFailure,
						}, nil
					},
				)

			if runner != nil ||
				!errors.Is(
					err,
					reconcileFailure,
				) {
				t.Fatalf(
					"newDiscoveryRuntimeWithStoreFactory() = %#v, %v; want nil, reconcile failure",
					runner,
					err,
				)
			}
		},
	)

	t.Run(
		"crawler construction failure",
		func(t *testing.T) {
			settings :=
				testCrawlRuntimeSettings(t)
			settings.crawl.MaxPages = 0

			runner, err :=
				newDiscoveryRuntimeWithStoreFactory(
					context.Background(),
					nil,
					settings,
					func(
						*pgxpool.Pool,
						store.AutomaticCrawlConfig,
					) (discoveryRuntimeStore, error) {
						return &coverageDiscoveryRuntimeStore{},
							nil
					},
				)

			if runner != nil ||
				err == nil ||
				!strings.Contains(
					err.Error(),
					"construct multi-page crawler",
				) {
				t.Fatalf(
					"newDiscoveryRuntimeWithStoreFactory() = %#v, %v; want crawler construction failure",
					runner,
					err,
				)
			}
		},
	)

	t.Run(
		"runner construction failure",
		func(t *testing.T) {
			settings :=
				testCrawlRuntimeSettings(t)
			settings.runner.DiscoveryInterval = 0

			runner, err :=
				newDiscoveryRuntimeWithStoreFactory(
					context.Background(),
					nil,
					settings,
					func(
						*pgxpool.Pool,
						store.AutomaticCrawlConfig,
					) (discoveryRuntimeStore, error) {
						return &coverageDiscoveryRuntimeStore{},
							nil
					},
				)

			if runner != nil ||
				err == nil ||
				!strings.Contains(
					err.Error(),
					"construct discovery runner",
				) {
				t.Fatalf(
					"newDiscoveryRuntimeWithStoreFactory() = %#v, %v; want runner construction failure",
					runner,
					err,
				)
			}
		},
	)

	t.Run(
		"success",
		func(t *testing.T) {
			runner, err :=
				newDiscoveryRuntimeWithStoreFactory(
					context.Background(),
					nil,
					testCrawlRuntimeSettings(t),
					func(
						*pgxpool.Pool,
						store.AutomaticCrawlConfig,
					) (discoveryRuntimeStore, error) {
						return &coverageDiscoveryRuntimeStore{},
							nil
					},
				)

			if err != nil {
				t.Fatalf(
					"newDiscoveryRuntimeWithStoreFactory() error = %v, want nil",
					err,
				)
			}

			if runner == nil {
				t.Fatal(
					"newDiscoveryRuntimeWithStoreFactory() runner = nil, want runner",
				)
			}
		},
	)
}

type coverageDiscoveryRuntimeStore struct {
	reconcileErr error
}

func (
	runtimeStore *coverageDiscoveryRuntimeStore,
) ReconcileAutomaticCrawlPolicy(
	context.Context,
) (int, error) {
	return 0, runtimeStore.reconcileErr
}

func (
	*coverageDiscoveryRuntimeStore,
) RecordDiscovery(
	context.Context,
	origin.Origin,
	[]discovery.Candidate,
) (discovery.RecordResult, error) {
	return discovery.RecordResult{}, nil
}

func (
	*coverageDiscoveryRuntimeStore,
) ClaimDiscoverySourceLease(
	context.Context,
	time.Duration,
	time.Duration,
) (discovery.CrawlSourceLease, bool, error) {
	return discovery.CrawlSourceLease{}, false, nil
}

func (
	*coverageDiscoveryRuntimeStore,
) RenewDiscoverySourceLease(
	_ context.Context,
	lease discovery.CrawlSourceLease,
	_ time.Duration,
) (discovery.CrawlSourceLease, error) {
	return lease, nil
}

func (
	*coverageDiscoveryRuntimeStore,
) CompleteDiscoverySourceLeaseRetry(
	context.Context,
	discovery.CrawlSourceLease,
	retry.Category,
	time.Duration,
) error {
	return nil
}

func (
	*coverageDiscoveryRuntimeStore,
) BeginCrawl(
	context.Context,
	origin.Origin,
	discovery.CrawlConfig,
) (discovery.CrawlRunID, error) {
	return 0, nil
}

func (
	*coverageDiscoveryRuntimeStore,
) RecordPageAttempt(
	context.Context,
	discovery.CrawlRunID,
	discovery.PageAttempt,
) error {
	return nil
}

func (
	*coverageDiscoveryRuntimeStore,
) FinishCrawl(
	context.Context,
	discovery.CrawlRunID,
	discovery.CrawlResult,
	discovery.CrawlRunOutcome,
	string,
) error {
	return nil
}

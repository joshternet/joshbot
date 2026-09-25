package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSeedRuntimeIntegrationFailureBoundaries(
	t *testing.T,
) {
	source := runtimeBoundaryIntegrationOrigin(t)

	t.Run("production store construction", func(t *testing.T) {
		seedStore, err := newRuntimeCrawlSeedStore(nil)
		if err == nil {
			t.Fatal(
				"newRuntimeCrawlSeedStore(nil) error = nil, want failure",
			)
		}
		if seedStore != nil {
			t.Errorf(
				"newRuntimeCrawlSeedStore(nil) store = %#v, want nil",
				seedStore,
			)
		}
	})

	t.Run("discovery store construction", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.newDiscoveryStore = func(
			*pgxpool.Pool,
		) (crawlSeedStore, error) {
			return nil, errRuntimeBoundaryIntegration
		}

		err := operations.addCrawlSeed(
			context.Background(),
			source,
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Fatalf(
				"addCrawlSeed() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}

		if !strings.Contains(
			err.Error(),
			"construct discovery store",
		) {
			t.Errorf(
				"addCrawlSeed() error = %v, want construction context",
				err,
			)
		}
	})

	t.Run("add failure", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.newDiscoveryStore = func(
			*pgxpool.Pool,
		) (crawlSeedStore, error) {
			return &fakeRuntimeCrawlSeedStore{
				addErr: errRuntimeBoundaryIntegration,
			}, nil
		}

		err := operations.addCrawlSeed(
			context.Background(),
			source,
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Fatalf(
				"addCrawlSeed() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}

		if !strings.Contains(
			err.Error(),
			"add crawl seed",
		) {
			t.Errorf(
				"addCrawlSeed() error = %v, want add context",
				err,
			)
		}
	})

	t.Run("remove failure", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.newDiscoveryStore = func(
			*pgxpool.Pool,
		) (crawlSeedStore, error) {
			return &fakeRuntimeCrawlSeedStore{
				removeErr: errRuntimeBoundaryIntegration,
			}, nil
		}

		err := operations.removeCrawlSeed(
			context.Background(),
			source,
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Fatalf(
				"removeCrawlSeed() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}

		if !strings.Contains(
			err.Error(),
			"remove crawl seed",
		) {
			t.Errorf(
				"removeCrawlSeed() error = %v, want remove context",
				err,
			)
		}
	})

	t.Run("list failure", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.newDiscoveryStore = func(
			*pgxpool.Pool,
		) (crawlSeedStore, error) {
			return &fakeRuntimeCrawlSeedStore{
				listErr: errRuntimeBoundaryIntegration,
			}, nil
		}

		seeds, err := operations.crawlSeeds(
			context.Background(),
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Fatalf(
				"crawlSeeds() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}

		if seeds != nil {
			t.Errorf(
				"crawlSeeds() seeds = %#v, want nil",
				seeds,
			)
		}

		if !strings.Contains(
			err.Error(),
			"list crawl seeds",
		) {
			t.Errorf(
				"crawlSeeds() error = %v, want list context",
				err,
			)
		}
	})
}

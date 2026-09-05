package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/store"
)

type crawlSeedStore interface {
	AddCrawlSeed(
		context.Context,
		origin.Origin,
	) error

	RemoveCrawlSeed(
		context.Context,
		origin.Origin,
	) error

	CrawlSeeds(
		context.Context,
	) ([]origin.Origin, error)
}

func newRuntimeCrawlSeedStore(
	pool *pgxpool.Pool,
) (crawlSeedStore, error) {
	seedStore, err := store.NewDiscoveryStore(pool)
	if err != nil {
		return nil, err
	}

	return seedStore, nil
}

func (operations runtimeOperations) addCrawlSeed(
	ctx context.Context,
	source origin.Origin,
) error {
	return operations.withCrawlSeedStore(
		ctx,
		func(seedStore crawlSeedStore) error {
			if err := seedStore.AddCrawlSeed(
				ctx,
				source,
			); err != nil {
				return fmt.Errorf(
					"add crawl seed: %w",
					err,
				)
			}

			return nil
		},
	)
}

func (operations runtimeOperations) removeCrawlSeed(
	ctx context.Context,
	source origin.Origin,
) error {
	return operations.withCrawlSeedStore(
		ctx,
		func(seedStore crawlSeedStore) error {
			if err := seedStore.RemoveCrawlSeed(
				ctx,
				source,
			); err != nil {
				return fmt.Errorf(
					"remove crawl seed: %w",
					err,
				)
			}

			return nil
		},
	)
}

func (operations runtimeOperations) crawlSeeds(
	ctx context.Context,
) ([]origin.Origin, error) {
	var seeds []origin.Origin

	err := operations.withCrawlSeedStore(
		ctx,
		func(seedStore crawlSeedStore) error {
			stored, err := seedStore.CrawlSeeds(ctx)
			if err != nil {
				return fmt.Errorf(
					"list crawl seeds: %w",
					err,
				)
			}

			seeds = stored

			return nil
		},
	)
	if err != nil {
		return nil, err
	}

	return seeds, nil
}

func (operations runtimeOperations) withCrawlSeedStore(
	ctx context.Context,
	action func(crawlSeedStore) error,
) error {
	return operations.withDatabase(
		ctx,
		func(connection databaseConnection) error {
			seedStore, err :=
				operations.newDiscoveryStore(
					connection.Pool(),
				)
			if err != nil {
				return fmt.Errorf(
					"construct discovery store: %w",
					err,
				)
			}

			return action(seedStore)
		},
	)
}

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/store"
)

type crawlSourceStore interface {
	SetCrawlBlocked(context.Context, origin.Origin, bool) error
	CrawlSources(context.Context) ([]store.CrawlSource, error)
}

func newRuntimeCrawlSourceStore(pool *pgxpool.Pool) (crawlSourceStore, error) {
	sourceStore, err := store.NewDiscoveryStore(pool)
	if err != nil {
		return nil, err
	}
	return sourceStore, nil
}

func (operations runtimeOperations) setCrawlBlocked(
	ctx context.Context,
	source origin.Origin,
	blocked bool,
) error {
	return operations.withCrawlSourceStore(ctx, func(sourceStore crawlSourceStore) error {
		if err := sourceStore.SetCrawlBlocked(ctx, source, blocked); err != nil {
			return fmt.Errorf("set crawl source policy: %w", err)
		}
		return nil
	})
}

func (operations runtimeOperations) crawlSources(
	ctx context.Context,
) ([]store.CrawlSource, error) {
	var sources []store.CrawlSource
	err := operations.withCrawlSourceStore(ctx, func(sourceStore crawlSourceStore) error {
		stored, err := sourceStore.CrawlSources(ctx)
		if err != nil {
			return fmt.Errorf("list crawl sources: %w", err)
		}
		sources = stored
		return nil
	})
	return sources, err
}

func (operations runtimeOperations) withCrawlSourceStore(
	ctx context.Context,
	action func(crawlSourceStore) error,
) error {
	return operations.withDatabase(ctx, func(connection databaseConnection) error {
		sourceStore, err := operations.newCrawlSourceStore(connection.Pool())
		if err != nil {
			return fmt.Errorf("construct crawl source store: %w", err)
		}
		return action(sourceStore)
	})
}

func formatCrawlSource(source store.CrawlSource) string {
	first := "-"
	last := "-"
	if source.FirstDiscoveredAt != nil {
		first = source.FirstDiscoveredAt.UTC().Format(time.RFC3339)
	}
	if source.LastDiscoveredAt != nil {
		last = source.LastDiscoveredAt.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf(
		"%s\tcurated=%t\tdiscovered=%t\tverified=%t\tblocked=%t\tfirst_discovered=%s\tlast_discovered=%s",
		source.Origin.String(), source.Seeded, source.AutomaticallyDiscovered,
		source.Verified, source.Blocked, first, last,
	)
}

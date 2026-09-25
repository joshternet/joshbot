package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/store"
)

func TestSourceRuntimeIntegrationFailureBoundaries(
	t *testing.T,
) {
	source := runtimeBoundaryIntegrationOrigin(t)

	t.Run("production store construction", func(t *testing.T) {
		sourceStore, err := newRuntimeCrawlSourceStore(nil)
		if err == nil {
			t.Fatal(
				"newRuntimeCrawlSourceStore(nil) error = nil, want failure",
			)
		}
		if sourceStore != nil {
			t.Errorf(
				"newRuntimeCrawlSourceStore(nil) store = %#v, want nil",
				sourceStore,
			)
		}
	})

	t.Run("source store construction", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.newCrawlSourceStore = func(
			*pgxpool.Pool,
		) (crawlSourceStore, error) {
			return nil, errRuntimeBoundaryIntegration
		}

		err := operations.setCrawlBlocked(
			context.Background(),
			source,
			true,
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Fatalf(
				"setCrawlBlocked() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}

		if !strings.Contains(
			err.Error(),
			"construct crawl source store",
		) {
			t.Errorf(
				"setCrawlBlocked() error = %v, want construction context",
				err,
			)
		}
	})

	t.Run("set policy failure", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.newCrawlSourceStore = func(
			*pgxpool.Pool,
		) (crawlSourceStore, error) {
			return &fakeRuntimeCrawlSourceStore{
				err: errRuntimeBoundaryIntegration,
			}, nil
		}

		err := operations.setCrawlBlocked(
			context.Background(),
			source,
			true,
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Fatalf(
				"setCrawlBlocked() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}

		if !strings.Contains(
			err.Error(),
			"set crawl source policy",
		) {
			t.Errorf(
				"setCrawlBlocked() error = %v, want policy context",
				err,
			)
		}
	})

	t.Run("list failure", func(t *testing.T) {
		operations, _ :=
			runtimeBoundaryIntegrationOperations(t)

		operations.newCrawlSourceStore = func(
			*pgxpool.Pool,
		) (crawlSourceStore, error) {
			return &fakeRuntimeCrawlSourceStore{
				err: errRuntimeBoundaryIntegration,
			}, nil
		}

		sources, err := operations.crawlSources(
			context.Background(),
		)

		if !errors.Is(
			err,
			errRuntimeBoundaryIntegration,
		) {
			t.Fatalf(
				"crawlSources() error = %v, want %v",
				err,
				errRuntimeBoundaryIntegration,
			)
		}

		if sources != nil {
			t.Errorf(
				"crawlSources() sources = %#v, want nil",
				sources,
			)
		}

		if !strings.Contains(
			err.Error(),
			"list crawl sources",
		) {
			t.Errorf(
				"crawlSources() error = %v, want list context",
				err,
			)
		}
	})
}

func TestSourceRuntimeIntegrationFormatsDiscoveryTimes(
	t *testing.T,
) {
	first := time.Date(
		2026,
		time.September,
		24,
		19,
		34,
		56,
		0,
		time.UTC,
	)
	last := time.Date(
		2026,
		time.September,
		24,
		20,
		45,
		12,
		0,
		time.UTC,
	)

	source := store.CrawlSource{
		Origin:            runtimeBoundaryIntegrationOrigin(t),
		FirstDiscoveredAt: &first,
		LastDiscoveredAt:  &last,
	}

	got := formatCrawlSource(source)

	if !strings.Contains(
		got,
		"first_discovered=2026-09-24T19:34:56Z",
	) {
		t.Errorf(
			"formatCrawlSource() = %q, want first discovery timestamp",
			got,
		)
	}

	if !strings.Contains(
		got,
		"last_discovered=2026-09-24T20:45:12Z",
	) {
		t.Errorf(
			"formatCrawlSource() = %q, want last discovery timestamp",
			got,
		)
	}
}

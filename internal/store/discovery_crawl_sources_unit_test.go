package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCrawlSourcesWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"validation",
		func(t *testing.T) {
			var store *DiscoveryStore

			sources, err := store.CrawlSources(ctx)

			if sources != nil {
				t.Fatalf(
					"CrawlSources() = %#v, want nil",
					sources,
				)
			}

			if !errors.Is(
				err,
				errDiscoveryStoreUnavailable,
			) {
				t.Fatalf(
					"CrawlSources() error = %v, want %v",
					err,
					errDiscoveryStoreUnavailable,
				)
			}
		},
	)

	t.Run(
		"success",
		func(t *testing.T) {
			firstDiscovered := time.Date(
				2026,
				time.September,
				18,
				10,
				0,
				0,
				0,
				time.UTC,
			)
			lastDiscovered := firstDiscovered.Add(
				2 * time.Hour,
			)

			database := &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{
									values: []any{
										"https://example.com",
										true,
										false,
										false,
										true,
										&firstDiscovered,
										&lastDiscovered,
									},
								},
								{
									values: []any{
										"https://example.org",
										false,
										true,
										true,
										false,
										nil,
										nil,
									},
								},
							},
						},
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			sources, err := store.CrawlSources(ctx)
			if err != nil {
				t.Fatalf(
					"CrawlSources() error = %v",
					err,
				)
			}

			want := []CrawlSource{
				{
					Origin: mustStoreOrigin(
						t,
						"https://example.com",
					),
					Seeded:                  true,
					AutomaticallyDiscovered: false,
					Blocked:                 false,
					Verified:                true,
					FirstDiscoveredAt:       &firstDiscovered,
					LastDiscoveredAt:        &lastDiscovered,
				},
				{
					Origin: mustStoreOrigin(
						t,
						"https://example.org",
					),
					Seeded:                  false,
					AutomaticallyDiscovered: true,
					Blocked:                 true,
					Verified:                false,
					FirstDiscoveredAt:       nil,
					LastDiscoveredAt:        nil,
				},
			}

			if !reflect.DeepEqual(
				sources,
				want,
			) {
				t.Fatalf(
					"CrawlSources() = %#v, want %#v",
					sources,
					want,
				)
			}
		},
	)

	t.Run(
		"query failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test crawl sources query failure",
			)

			database := &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						err: testErr,
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			sources, err := store.CrawlSources(ctx)

			if sources != nil {
				t.Fatalf(
					"CrawlSources() = %#v, want nil",
					sources,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: list crawl sources",
				) {
				t.Fatalf(
					"CrawlSources() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"scan failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test crawl source scan failure",
			)

			database := &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{
									err: testErr,
								},
							},
						},
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			sources, err := store.CrawlSources(ctx)

			if sources != nil {
				t.Fatalf(
					"CrawlSources() = %#v, want nil",
					sources,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: collect crawl sources",
				) {
				t.Fatalf(
					"CrawlSources() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"invalid stored origin",
		func(t *testing.T) {
			database := &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{
									values: []any{
										"not-an-origin",
										false,
										false,
										false,
										false,
										nil,
										nil,
									},
								},
							},
						},
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			sources, err := store.CrawlSources(ctx)

			if sources != nil {
				t.Fatalf(
					"CrawlSources() = %#v, want nil",
					sources,
				)
			}

			if err == nil ||
				!strings.Contains(
					err.Error(),
					"store: collect crawl sources",
				) ||
				!strings.Contains(
					err.Error(),
					"invalid origin",
				) {
				t.Fatalf(
					"CrawlSources() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"rows failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test crawl sources rows failure",
			)

			database := &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{
							err: testErr,
						},
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			sources, err := store.CrawlSources(ctx)

			if sources != nil {
				t.Fatalf(
					"CrawlSources() = %#v, want nil",
					sources,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: collect crawl sources",
				) {
				t.Fatalf(
					"CrawlSources() error = %v",
					err,
				)
			}
		},
	)
}

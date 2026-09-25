package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/store"
)

func TestDiscoveryConstructorRuntimeIntegrationFailures(
	t *testing.T,
) {
	t.Run("store construction", func(t *testing.T) {
		storeErr := errors.New(
			"integration discovery store construction failure",
		)

		runner, err :=
			newDiscoveryRuntimeWithStoreFactory(
				context.Background(),
				nil,
				testCrawlRuntimeSettings(t),
				func(
					*pgxpool.Pool,
					store.AutomaticCrawlConfig,
				) (discoveryRuntimeStore, error) {
					return nil, storeErr
				},
			)

		if runner != nil {
			t.Errorf(
				"runner = %#v, want nil",
				runner,
			)
		}

		if !errors.Is(err, storeErr) {
			t.Fatalf(
				"error = %v, want %v",
				err,
				storeErr,
			)
		}

		if !strings.Contains(
			err.Error(),
			"construct discovery store",
		) {
			t.Errorf(
				"error = %v, want store construction context",
				err,
			)
		}
	})

	t.Run("policy reconciliation", func(t *testing.T) {
		reconcileErr := errors.New(
			"integration automatic crawl reconciliation failure",
		)

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
						reconcileErr: reconcileErr,
					}, nil
				},
			)

		if runner != nil {
			t.Errorf(
				"runner = %#v, want nil",
				runner,
			)
		}

		if !errors.Is(err, reconcileErr) {
			t.Fatalf(
				"error = %v, want %v",
				err,
				reconcileErr,
			)
		}

		if !strings.Contains(
			err.Error(),
			"reconcile automatic crawl policy",
		) {
			t.Errorf(
				"error = %v, want reconciliation context",
				err,
			)
		}
	})

	t.Run("crawler construction", func(t *testing.T) {
		settings := testCrawlRuntimeSettings(t)
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

		if runner != nil {
			t.Errorf(
				"runner = %#v, want nil",
				runner,
			)
		}

		if err == nil {
			t.Fatal(
				"error = nil, want crawler construction failure",
			)
		}

		if !strings.Contains(
			err.Error(),
			"construct multi-page crawler",
		) {
			t.Errorf(
				"error = %v, want crawler construction context",
				err,
			)
		}
	})

	t.Run("runner construction", func(t *testing.T) {
		settings := testCrawlRuntimeSettings(t)
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

		if runner != nil {
			t.Errorf(
				"runner = %#v, want nil",
				runner,
			)
		}

		if err == nil {
			t.Fatal(
				"error = nil, want runner construction failure",
			)
		}

		if !strings.Contains(
			err.Error(),
			"construct discovery runner",
		) {
			t.Errorf(
				"error = %v, want runner construction context",
				err,
			)
		}
	})
}

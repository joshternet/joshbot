package main

import (
	"context"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/discovery"
)

func TestNewDiscoveryRuntimeReturnsConstructionFailure(
	t *testing.T,
) {
	_, pool := newCLIIntegrationEnvironment(t)

	tests := []struct {
		name     string
		mutate   func(*crawlRuntimeSettings)
		wantText string
	}{
		{
			name: "invalid crawler configuration",
			mutate: func(
				settings *crawlRuntimeSettings,
			) {
				settings.crawl = discovery.CrawlConfig{}
			},
			wantText: "construct multi-page crawler",
		},
		{
			name: "invalid runner configuration",
			mutate: func(
				settings *crawlRuntimeSettings,
			) {
				settings.runner =
					discovery.CrawlRunnerConfig{}
			},
			wantText: "construct discovery runner",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := testCrawlRuntimeSettings(t)
			test.mutate(&settings)

			runner, err := newDiscoveryRuntime(
				context.Background(),
				pool,
				settings,
			)
			if err == nil {
				t.Fatal(
					"newDiscoveryRuntime() error = nil, want non-nil",
				)
			}

			if runner != nil {
				t.Errorf(
					"newDiscoveryRuntime() runner = %#v, want nil",
					runner,
				)
			}

			if !strings.Contains(
				err.Error(),
				test.wantText,
			) {
				t.Errorf(
					"newDiscoveryRuntime() error = %v, want %q",
					err,
					test.wantText,
				)
			}
		})
	}
}

func TestNewDiscoveryRuntimeReturnsPolicyReconciliationFailure(t *testing.T) {
	ctx := context.Background()
	_, pool := newCLIIntegrationEnvironment(t)
	settings := testCrawlRuntimeSettings(t)
	settings.automatic.Enabled = true
	runner, err := newDiscoveryRuntime(ctx, pool, settings)
	if err == nil || !strings.Contains(err.Error(), "reconcile automatic crawl policy") {
		t.Fatalf("newDiscoveryRuntime() error = %v, want reconciliation failure", err)
	}
	if runner != nil {
		t.Fatalf("newDiscoveryRuntime() runner = %#v, want nil", runner)
	}
}

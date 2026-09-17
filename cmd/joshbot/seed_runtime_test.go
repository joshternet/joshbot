package main

import "testing"

func TestNewRuntimeCrawlSeedStoreUsesPool(
	t *testing.T,
) {
	_, pool := newCLIIntegrationEnvironment(t)

	seedStore, err := newRuntimeCrawlSeedStore(pool)
	if err != nil {
		t.Fatalf(
			"newRuntimeCrawlSeedStore() error = %v, want nil",
			err,
		)
	}

	if seedStore == nil {
		t.Fatal(
			"newRuntimeCrawlSeedStore() store = nil, want non-nil",
		)
	}
}

func TestNewRuntimeCrawlSourceStoreUsesPool(t *testing.T) {
	_, pool := newCLIIntegrationEnvironment(t)

	sourceStore, err := newRuntimeCrawlSourceStore(pool)
	if err != nil {
		t.Fatalf("newRuntimeCrawlSourceStore() error = %v, want nil", err)
	}
	if sourceStore == nil {
		t.Fatal("newRuntimeCrawlSourceStore() store = nil, want non-nil")
	}
}

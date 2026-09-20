package store

import (
	"errors"
	"testing"
)

func TestAutomaticDiscoveryStoreRejectsUnavailablePool(
	t *testing.T,
) {
	discoveryStore, err :=
		NewDiscoveryStoreWithAutomaticCrawling(
			nil,
			AutomaticCrawlConfig{},
		)

	if discoveryStore != nil {
		t.Fatalf(
			"NewDiscoveryStoreWithAutomaticCrawling(nil) = %#v, want nil",
			discoveryStore,
		)
	}

	if !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"NewDiscoveryStoreWithAutomaticCrawling(nil) error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}
}

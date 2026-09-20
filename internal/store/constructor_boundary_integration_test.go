package store

import "testing"

func TestStoreConstructorBoundaryIntegrationRejectsNilPools(t *testing.T) {
	controlStore, err := newControlStore(nil)
	if err == nil {
		t.Fatal("newControlStore(nil) error = nil, want error")
	}
	if controlStore != nil {
		t.Errorf("newControlStore(nil) store = %#v, want nil", controlStore)
	}

	discoveryStore, err := NewDiscoveryStoreWithAutomaticCrawling(
		nil,
		AutomaticCrawlConfig{},
	)
	if err == nil {
		t.Fatal("NewDiscoveryStoreWithAutomaticCrawling(nil) error = nil, want error")
	}
	if discoveryStore != nil {
		t.Errorf(
			"NewDiscoveryStoreWithAutomaticCrawling(nil) store = %#v, want nil",
			discoveryStore,
		)
	}
}

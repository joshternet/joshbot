package store

import (
	"errors"
	"testing"
)

func TestInternalControlStoreRejectsUnavailablePool(
	t *testing.T,
) {
	controlStore, err := newControlStore(nil)

	if controlStore != nil {
		t.Fatalf(
			"newControlStore(nil) = %#v, want nil",
			controlStore,
		)
	}

	if !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"newControlStore(nil) error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}
}

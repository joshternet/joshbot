package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
)

func TestStoreRecordVerificationReturnsValidationFailure(
	t *testing.T,
) {
	store := newStore(nil)

	err := store.RecordVerification(
		context.Background(),
		time.Now().UTC(),
		declaration.Result{},
	)

	if !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"RecordVerification() error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}
}

func TestStoreOriginStateReturnsValidationFailure(
	t *testing.T,
) {
	store := newStore(nil)

	_, found, err := store.OriginState(
		context.Background(),
		mustStoreOrigin(
			t,
			"https://example.com",
		),
	)

	if found {
		t.Fatal(
			"OriginState() found = true, want false",
		)
	}

	if !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"OriginState() error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}
}

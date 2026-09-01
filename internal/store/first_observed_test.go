package store

import (
	"context"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
)

func TestStoreKeepsEarliestFirstObservedAt(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	store := New(pool)
	source := mustStoreOrigin(t, "https://example.com")

	offset := time.FixedZone(
		"test offset",
		-7*60*60,
	)
	t1 := time.Date(
		2026,
		time.August,
		30,
		5,
		0,
		0,
		0,
		offset,
	)
	t2 := time.Date(
		2026,
		time.August,
		30,
		13,
		0,
		0,
		0,
		time.UTC,
	)
	t3 := time.Date(
		2026,
		time.August,
		30,
		14,
		0,
		0,
		0,
		time.UTC,
	)
	result := declaration.Result{
		Outcome: declaration.OutcomeValid,
		Origin:  source,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityAffirmed,
		},
	}

	for _, observedAt := range []time.Time{
		t2,
		t1,
		t3,
	} {
		if err := store.RecordVerification(
			ctx,
			observedAt,
			result,
		); err != nil {
			t.Fatalf(
				"RecordVerification(%v) error = %v, want nil",
				observedAt,
				err,
			)
		}
	}

	got, found, err := store.OriginState(ctx, source)
	if err != nil {
		t.Fatalf("OriginState() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("OriginState() found = false, want true")
	}

	if !got.FirstObservedAt.Equal(t1) {
		t.Errorf(
			"first observed at = %v, want %v",
			got.FirstObservedAt,
			t1,
		)
	}

	if got.FirstObservedAt.Location() != time.UTC {
		t.Errorf(
			"first observed location = %v, want UTC",
			got.FirstObservedAt.Location(),
		)
	}

	if !got.Latest.ObservedAt.Equal(t3) {
		t.Errorf(
			"latest observed at = %v, want %v",
			got.Latest.ObservedAt,
			t3,
		)
	}

	var observationCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_observations
			WHERE origin = $1
		`,
		source.String(),
	).Scan(&observationCount)
	if err != nil {
		t.Fatalf(
			"count stored observations: %v",
			err,
		)
	}

	if observationCount != 3 {
		t.Errorf(
			"observation count = %d, want 3",
			observationCount,
		)
	}
}

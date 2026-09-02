package store

import (
	"context"
	"testing"
	"time"
)

func TestQueueSchedulesOneOrigin(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	queue, err := NewQueue(
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Hour,
		},
	)
	if err != nil {
		t.Fatalf("NewQueue() error = %v, want nil", err)
	}

	source := mustStoreOrigin(t, "https://example.com")
	availableAt := time.Date(
		2026,
		time.August,
		31,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	if err := queue.Schedule(
		ctx,
		source,
		availableAt,
	); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	var (
		storedOrigin      string
		storedAvailableAt time.Time
		leaseGeneration   int64
		leaseOwner        *string
		leaseExpiresAt    *time.Time
		lastClaimedAt     *time.Time
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				origin,
				available_at,
				lease_generation,
				lease_owner,
				lease_expires_at,
				last_claimed_at
			FROM verification_queue
		`,
	).Scan(
		&storedOrigin,
		&storedAvailableAt,
		&leaseGeneration,
		&leaseOwner,
		&leaseExpiresAt,
		&lastClaimedAt,
	)
	if err != nil {
		t.Fatalf("query scheduled work: %v", err)
	}

	if storedOrigin != source.String() {
		t.Errorf(
			"stored origin = %q, want %q",
			storedOrigin,
			source,
		)
	}

	if !storedAvailableAt.Equal(availableAt) {
		t.Errorf(
			"stored available_at = %v, want %v",
			storedAvailableAt,
			availableAt,
		)
	}

	if leaseGeneration != 0 {
		t.Errorf(
			"lease generation = %d, want 0",
			leaseGeneration,
		)
	}

	if leaseOwner != nil {
		t.Errorf(
			"lease owner = %q, want nil",
			*leaseOwner,
		)
	}

	if leaseExpiresAt != nil {
		t.Errorf(
			"lease expiration = %v, want nil",
			*leaseExpiresAt,
		)
	}

	if lastClaimedAt != nil {
		t.Errorf(
			"last claimed at = %v, want nil",
			*lastClaimedAt,
		)
	}

	var originCount int
	err = pool.QueryRow(
		ctx,
		"SELECT count(*) FROM origins",
	).Scan(&originCount)
	if err != nil {
		t.Fatalf("count origins: %v", err)
	}

	if originCount != 0 {
		t.Errorf(
			"origin count = %d, want 0",
			originCount,
		)
	}

	var observationCount int
	err = pool.QueryRow(
		ctx,
		"SELECT count(*) FROM verification_observations",
	).Scan(&observationCount)
	if err != nil {
		t.Fatalf("count observations: %v", err)
	}

	if observationCount != 0 {
		t.Errorf(
			"observation count = %d, want 0",
			observationCount,
		)
	}
}

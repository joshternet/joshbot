package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestQueueSkipsManyBusyNeighborhoodSiblings(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     5 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		now,
	)

	busyHolder := mustStoreOrigin(
		t,
		"https://host-00.example.com",
	)
	if err := queue.Schedule(
		ctx,
		busyHolder,
		now,
	); err != nil {
		t.Fatalf("Schedule(%s) error = %v", busyHolder, err)
	}

	holderLease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil || !found {
		t.Fatalf(
			"holder Claim() = %#v, %t, %v",
			holderLease,
			found,
			err,
		)
	}

	const siblingCount = 40
	for index := 1; index <= siblingCount; index++ {
		source := mustStoreOrigin(
			t,
			fmt.Sprintf(
				"https://host-%02d.example.com",
				index,
			),
		)
		if err := queue.Schedule(
			ctx,
			source,
			now,
		); err != nil {
			t.Fatalf("Schedule(%s) error = %v", source, err)
		}
	}

	free := mustStoreOrigin(t, "https://solo.other.test")
	if err := queue.Schedule(ctx, free, now); err != nil {
		t.Fatalf("Schedule(%s) error = %v", free, err)
	}

	claimed, found, err := queue.Claim(ctx, "worker-b")
	if err != nil || !found {
		t.Fatalf(
			"free Claim() = %#v, %t, %v",
			claimed,
			found,
			err,
		)
	}

	if claimed.Origin != free {
		t.Fatalf(
			"free Claim() origin = %s, want %s",
			claimed.Origin,
			free,
		)
	}
}

func TestDiscoveryDoesNotMutateStateWhenDomainBusy(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	leaseDuration := 5 * time.Minute

	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     leaseDuration,
			MinOriginInterval: time.Minute,
		},
		now,
	)
	holder := mustStoreOrigin(
		t,
		"https://host-a.example.com",
	)
	if err := queue.Schedule(ctx, holder, now); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	verificationLease, found, err := queue.Claim(
		ctx,
		"worker-a",
	)
	if err != nil || !found {
		t.Fatalf(
			"Claim() = %#v, %t, %v",
			verificationLease,
			found,
			err,
		)
	}

	source := seedDiscoveryTestSource(
		t,
		pool,
		"https://host-b.example.com",
		now,
	)

	discoveryStore := newDiscoveryTestStore(t, pool, now)
	lease, found, err := discoveryStore.ClaimDiscoverySourceLease(
		ctx,
		testDiscoveryLeaseOwner,
		168*time.Hour,
		leaseDuration,
	)
	if err != nil {
		t.Fatalf(
			"ClaimDiscoverySourceLease() error = %v",
			err,
		)
	}
	if found {
		t.Fatalf(
			"ClaimDiscoverySourceLease() found %#v",
			lease,
		)
	}

	var (
		generation int64
		owner      *string
		expiresAt  *time.Time
		claimedAt  *time.Time
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				lease_generation,
				lease_owner,
				lease_expires_at,
				last_claimed_at
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		source.String(),
	).Scan(
		&generation,
		&owner,
		&expiresAt,
		&claimedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		t.Fatalf("query discovery state: %v", err)
	}

	if generation != 0 {
		t.Fatalf(
			"lease_generation = %d, want 0 after busy domain skip",
			generation,
		)
	}
	if owner != nil || expiresAt != nil || claimedAt != nil {
		t.Fatalf(
			"lease fields mutated on busy domain: owner=%v expires=%v claimed=%v",
			owner,
			expiresAt,
			claimedAt,
		)
	}
}

func TestDiscoverySkipsManyBusyNeighborhoodSiblings(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	leaseDuration := 5 * time.Minute

	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     leaseDuration,
			MinOriginInterval: time.Minute,
		},
		now,
	)
	holder := mustStoreOrigin(
		t,
		"https://host-00.example.com",
	)
	if err := queue.Schedule(ctx, holder, now); err != nil {
		t.Fatalf("Schedule(%s) error = %v", holder, err)
	}

	holderLease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil || !found {
		t.Fatalf(
			"holder Claim() = %#v, %t, %v",
			holderLease,
			found,
			err,
		)
	}

	const siblingCount = 8
	for index := 1; index <= siblingCount; index++ {
		_ = seedDiscoveryTestSource(
			t,
			pool,
			fmt.Sprintf(
				"https://host-%02d.example.com",
				index,
			),
			now,
		)
	}
	_ = seedDiscoveryTestSource(
		t,
		pool,
		"https://solo.other.test",
		now,
	)

	discoveryStore := newDiscoveryTestStore(t, pool, now)
	lease, found, err := discoveryStore.ClaimDiscoverySourceLease(
		ctx,
		testDiscoveryLeaseOwner,
		168*time.Hour,
		leaseDuration,
	)
	if err != nil || !found {
		t.Fatalf(
			"ClaimDiscoverySourceLease() = %#v, %t, %v",
			lease,
			found,
			err,
		)
	}
	if lease.Origin.String() != "https://solo.other.test" {
		t.Fatalf(
			"ClaimDiscoverySourceLease() origin = %s, want https://solo.other.test",
			lease.Origin,
		)
	}
}

func TestQueueRenewLostWhenDomainRowMissing(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     5 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		now,
	)

	source := mustStoreOrigin(t, "https://example.com")
	if err := queue.Schedule(ctx, source, now); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	lease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil || !found {
		t.Fatalf("Claim() = %#v, %t, %v", lease, found, err)
	}

	if _, err := pool.Exec(
		ctx,
		`DELETE FROM domain_inflight_leases`,
	); err != nil {
		t.Fatalf("DELETE domain_inflight_leases: %v", err)
	}

	_, err = queue.Renew(ctx, lease)
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf(
			"Renew() error = %v, want %v",
			err,
			ErrLeaseLost,
		)
	}
}

func TestDiscoveryRenewLostWhenDomainRowMissing(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	leaseDuration := 5 * time.Minute

	_ = seedDiscoveryTestSource(
		t,
		pool,
		"https://example.com",
		now,
	)

	discoveryStore := newDiscoveryTestStore(t, pool, now)
	lease, found, err := discoveryStore.ClaimDiscoverySourceLease(
		ctx,
		testDiscoveryLeaseOwner,
		168*time.Hour,
		leaseDuration,
	)
	if err != nil || !found {
		t.Fatalf(
			"ClaimDiscoverySourceLease() = %#v, %t, %v",
			lease,
			found,
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		`DELETE FROM domain_inflight_leases`,
	); err != nil {
		t.Fatalf("DELETE domain_inflight_leases: %v", err)
	}

	_, err = discoveryStore.RenewDiscoverySourceLease(
		ctx,
		lease,
		leaseDuration,
	)
	if !errors.Is(err, ErrDiscoverySourceLeaseLost) {
		t.Fatalf(
			"RenewDiscoverySourceLease() error = %v, want %v",
			err,
			ErrDiscoverySourceLeaseLost,
		)
	}
}

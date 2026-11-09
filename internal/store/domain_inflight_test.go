package store

import (
	"context"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

func TestQueueClaimsDistinctNeighborhoodsInParallel(
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

	first := mustStoreOrigin(t, "https://a.example.com")
	second := mustStoreOrigin(t, "https://b.other.test")
	for _, source := range []origin.Origin{first, second} {
		if err := queue.Schedule(ctx, source, now); err != nil {
			t.Fatalf("Schedule(%s) error = %v", source, err)
		}
	}

	firstLease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil || !found {
		t.Fatalf(
			"first Claim() = %#v, %t, %v",
			firstLease,
			found,
			err,
		)
	}

	secondLease, found, err := queue.Claim(ctx, "worker-b")
	if err != nil || !found {
		t.Fatalf(
			"second Claim() = %#v, %t, %v",
			secondLease,
			found,
			err,
		)
	}

	if firstLease.Origin == secondLease.Origin {
		t.Fatalf(
			"claims shared origin %s",
			firstLease.Origin,
		)
	}
}

func TestQueueBlocksSiblingNeighborhoodClaim(
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

	hostA := mustStoreOrigin(t, "https://host-a.example.com")
	hostB := mustStoreOrigin(t, "https://host-b.example.com")
	for _, source := range []origin.Origin{hostA, hostB} {
		if err := queue.Schedule(ctx, source, now); err != nil {
			t.Fatalf("Schedule(%s) error = %v", source, err)
		}
	}

	firstLease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil || !found {
		t.Fatalf(
			"first Claim() = %#v, %t, %v",
			firstLease,
			found,
			err,
		)
	}

	secondLease, found, err := queue.Claim(ctx, "worker-b")
	if err != nil {
		t.Fatalf("second Claim() error = %v", err)
	}
	if found {
		t.Fatalf(
			"second Claim() found sibling %#v",
			secondLease,
		)
	}
}

func TestQueueSkipsBusyNeighborhoodAndClaimsOther(
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

	hostA := mustStoreOrigin(t, "https://host-a.example.com")
	hostB := mustStoreOrigin(t, "https://host-b.example.com")
	other := mustStoreOrigin(t, "https://solo.other.test")
	for _, source := range []origin.Origin{
		hostA,
		hostB,
		other,
	} {
		if err := queue.Schedule(ctx, source, now); err != nil {
			t.Fatalf("Schedule(%s) error = %v", source, err)
		}
	}

	firstLease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil || !found {
		t.Fatalf(
			"first Claim() = %#v, %t, %v",
			firstLease,
			found,
			err,
		)
	}

	secondLease, found, err := queue.Claim(ctx, "worker-b")
	if err != nil || !found {
		t.Fatalf(
			"second Claim() = %#v, %t, %v",
			secondLease,
			found,
			err,
		)
	}

	if secondLease.Origin != other {
		t.Fatalf(
			"second Claim() origin = %s, want %s",
			secondLease.Origin,
			other,
		)
	}
}

func TestQueueCompleteFreesNeighborhoodImmediately(
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

	hostA := mustStoreOrigin(t, "https://host-a.example.com")
	hostB := mustStoreOrigin(t, "https://host-b.example.com")
	for _, source := range []origin.Origin{hostA, hostB} {
		if err := queue.Schedule(ctx, source, now); err != nil {
			t.Fatalf("Schedule(%s) error = %v", source, err)
		}
	}

	firstLease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil || !found {
		t.Fatalf(
			"first Claim() = %#v, %t, %v",
			firstLease,
			found,
			err,
		)
	}

	if err := queue.CompleteVerification(
		ctx,
		firstLease,
		validCompletionResult(firstLease.Origin),
		24*time.Hour,
	); err != nil {
		t.Fatalf("CompleteVerification() error = %v", err)
	}

	secondLease, found, err := queue.Claim(ctx, "worker-b")
	if err != nil || !found {
		t.Fatalf(
			"second Claim() = %#v, %t, %v",
			secondLease,
			found,
			err,
		)
	}

	if secondLease.Origin != hostB &&
		secondLease.Origin != hostA {
		t.Fatalf(
			"second Claim() origin = %s, want sibling",
			secondLease.Origin,
		)
	}
}

func TestQueueDomainLeaseExpiresForSiblingClaim(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	start := queueTestTime()
	leaseDuration := 5 * time.Minute
	afterExpiry := start.Add(leaseDuration)

	hostA := mustStoreOrigin(t, "https://host-a.example.com")
	hostB := mustStoreOrigin(t, "https://host-b.example.com")

	firstQueue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     leaseDuration,
			MinOriginInterval: time.Minute,
		},
		start,
	)
	for _, source := range []origin.Origin{hostA, hostB} {
		if err := firstQueue.Schedule(
			ctx,
			source,
			start,
		); err != nil {
			t.Fatalf("Schedule(%s) error = %v", source, err)
		}
	}

	firstLease, found, err := firstQueue.Claim(ctx, "worker-a")
	if err != nil || !found {
		t.Fatalf(
			"first Claim() = %#v, %t, %v",
			firstLease,
			found,
			err,
		)
	}

	secondQueue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     leaseDuration,
			MinOriginInterval: time.Minute,
		},
		afterExpiry,
	)
	secondLease, found, err := secondQueue.Claim(
		ctx,
		"worker-b",
	)
	if err != nil || !found {
		t.Fatalf(
			"expired Claim() = %#v, %t, %v",
			secondLease,
			found,
			err,
		)
	}
}

func TestQueueClaimsIndependentIPOriginsInParallel(
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

	first := mustStoreOrigin(t, "https://192.0.2.1")
	second := mustStoreOrigin(t, "https://198.51.100.1")
	for _, source := range []origin.Origin{first, second} {
		if err := queue.Schedule(ctx, source, now); err != nil {
			t.Fatalf("Schedule(%s) error = %v", source, err)
		}
	}

	firstLease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil || !found {
		t.Fatalf(
			"first Claim() = %#v, %t, %v",
			firstLease,
			found,
			err,
		)
	}

	secondLease, found, err := queue.Claim(ctx, "worker-b")
	if err != nil || !found {
		t.Fatalf(
			"second Claim() = %#v, %t, %v",
			secondLease,
			found,
			err,
		)
	}
}

func TestVerificationBlocksDiscoverySiblingNeighborhood(
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
	hostA := mustStoreOrigin(t, "https://host-a.example.com")
	if err := queue.Schedule(ctx, hostA, now); err != nil {
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

	_ = seedDiscoveryTestSource(
		t,
		pool,
		"https://host-b.example.com",
		now,
	)

	discoveryStore := newDiscoveryTestStore(t, pool, now)
	discoveryLease, found, err :=
		discoveryStore.ClaimDiscoverySourceLease(
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
			"discovery claimed sibling %#v",
			discoveryLease,
		)
	}

	if err := queue.CompleteVerification(
		ctx,
		verificationLease,
		validCompletionResult(verificationLease.Origin),
		24*time.Hour,
	); err != nil {
		t.Fatalf("CompleteVerification() error = %v", err)
	}

	discoveryLease, found, err =
		discoveryStore.ClaimDiscoverySourceLease(
			ctx,
			testDiscoveryLeaseOwner,
			168*time.Hour,
			leaseDuration,
		)
	if err != nil || !found {
		t.Fatalf(
			"post-complete discovery Claim() = %#v, %t, %v",
			discoveryLease,
			found,
			err,
		)
	}
}

func TestQueueRenewExtendsDomainInflightLease(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	start := queueTestTime()
	leaseDuration := 5 * time.Minute
	renewAt := start.Add(2 * time.Minute)

	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     leaseDuration,
			MinOriginInterval: time.Minute,
		},
		start,
	)
	source := mustStoreOrigin(t, "https://renew.example.com")
	if err := queue.Schedule(ctx, source, start); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	lease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil || !found {
		t.Fatalf("Claim() = %#v, %t, %v", lease, found, err)
	}

	renewQueue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     leaseDuration,
			MinOriginInterval: time.Minute,
		},
		renewAt,
	)
	renewed, err := renewQueue.Renew(ctx, lease)
	if err != nil {
		t.Fatalf("Renew() error = %v", err)
	}

	var domainExpiresAt time.Time
	if err := pool.QueryRow(
		ctx,
		`
			SELECT lease_expires_at
			FROM domain_inflight_leases
			WHERE domain_key = $1
		`,
		domainInflightKey(source),
	).Scan(&domainExpiresAt); err != nil {
		t.Fatalf("query domain lease: %v", err)
	}

	if !domainExpiresAt.Equal(renewed.ExpiresAt) {
		t.Fatalf(
			"domain expires = %s, want %s",
			domainExpiresAt,
			renewed.ExpiresAt,
		)
	}
}

func TestQueueAbandonFreesNeighborhood(
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

	hostA := mustStoreOrigin(t, "https://host-a.example.com")
	hostB := mustStoreOrigin(t, "https://host-b.example.com")
	for _, source := range []origin.Origin{hostA, hostB} {
		if err := queue.Schedule(ctx, source, now); err != nil {
			t.Fatalf("Schedule(%s) error = %v", source, err)
		}
	}

	firstLease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil || !found {
		t.Fatalf(
			"first Claim() = %#v, %t, %v",
			firstLease,
			found,
			err,
		)
	}

	if err := queue.AbandonVerification(
		ctx,
		firstLease,
	); err != nil {
		t.Fatalf("AbandonVerification() error = %v", err)
	}

	secondLease, found, err := queue.Claim(ctx, "worker-b")
	if err != nil || !found {
		t.Fatalf(
			"sibling Claim() = %#v, %t, %v",
			secondLease,
			found,
			err,
		)
	}

	if secondLease.Origin == firstLease.Origin {
		t.Fatalf(
			"sibling Claim() returned abandoned origin %s",
			secondLease.Origin,
		)
	}
}

func TestDiscoveryCompleteFreesDomainForVerification(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	leaseDuration := 5 * time.Minute

	_ = seedDiscoveryTestSource(
		t,
		pool,
		"https://host-a.example.com",
		now,
	)

	discoveryStore := newDiscoveryTestStore(t, pool, now)
	discoveryLease, found, err :=
		discoveryStore.ClaimDiscoverySourceLease(
			ctx,
			testDiscoveryLeaseOwner,
			168*time.Hour,
			leaseDuration,
		)
	if err != nil || !found {
		t.Fatalf(
			"ClaimDiscoverySourceLease() = %#v, %t, %v",
			discoveryLease,
			found,
			err,
		)
	}

	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     leaseDuration,
			MinOriginInterval: time.Minute,
		},
		now,
	)
	hostB := mustStoreOrigin(t, "https://host-b.example.com")
	if err := queue.Schedule(ctx, hostB, now); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	blocked, found, err := queue.Claim(ctx, "worker-a")
	if err != nil {
		t.Fatalf("blocked Claim() error = %v", err)
	}
	if found {
		t.Fatalf("blocked Claim() found %#v", blocked)
	}

	if err := discoveryStore.CompleteDiscoverySourceLeaseRetry(
		ctx,
		discoveryLease,
		retry.CategoryNone,
		0,
	); err != nil {
		t.Fatalf(
			"CompleteDiscoverySourceLeaseRetry() error = %v",
			err,
		)
	}

	claimed, found, err := queue.Claim(ctx, "worker-a")
	if err != nil || !found {
		t.Fatalf(
			"post-complete Claim() = %#v, %t, %v",
			claimed,
			found,
			err,
		)
	}
}

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestQueueReclaimsOnlyWhenLeaseAndPolitenessPermit(
	t *testing.T,
) {
	tests := []struct {
		name              string
		leaseDuration     time.Duration
		minOriginInterval time.Duration
		firstCheckpoint   time.Duration
		finalCheckpoint   time.Duration
	}{
		{
			name:              "exact expiration boundary",
			leaseDuration:     10 * time.Minute,
			minOriginInterval: 10 * time.Minute,
			firstCheckpoint:   10*time.Minute - time.Microsecond,
			finalCheckpoint:   10 * time.Minute,
		},
		{
			name:              "politeness delays crash recovery",
			leaseDuration:     time.Minute,
			minOriginInterval: 10 * time.Minute,
			firstCheckpoint:   time.Minute,
			finalCheckpoint:   10 * time.Minute,
		},
		{
			name:              "active lease outlasts politeness",
			leaseDuration:     10 * time.Minute,
			minOriginInterval: time.Minute,
			firstCheckpoint:   time.Minute,
			finalCheckpoint:   10 * time.Minute,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := newStoreTestPool(t)
			now := queueTestTime()
			queue, clock := newControlledQueue(
				t,
				pool,
				QueueConfig{
					LeaseDuration: test.leaseDuration,
					MinOriginInterval: test.
						minOriginInterval,
				},
				now,
			)
			source := mustStoreOrigin(
				t,
				"https://example.com",
			)

			if err := queue.Schedule(
				ctx,
				source,
				now,
			); err != nil {
				t.Fatalf(
					"Schedule() error = %v, want nil",
					err,
				)
			}

			firstLease, found, err := queue.Claim(
				ctx,
				"worker-a",
			)
			if err != nil {
				t.Fatalf(
					"first Claim() error = %v, want nil",
					err,
				)
			}

			if !found {
				t.Fatal(
					"first Claim() found = false, want true",
				)
			}

			if firstLease.Generation != 1 {
				t.Fatalf(
					"first generation = %d, want 1",
					firstLease.Generation,
				)
			}

			clock.Set(now.Add(test.firstCheckpoint))

			blockedLease, found, err := queue.Claim(
				ctx,
				"worker-b",
			)
			if err != nil {
				t.Fatalf(
					"blocked Claim() error = %v, want nil",
					err,
				)
			}

			if found {
				t.Error(
					"blocked Claim() found = true, want false",
				)
			}

			if blockedLease != (Lease{}) {
				t.Errorf(
					"blocked Claim() lease = %#v, want zero value",
					blockedLease,
				)
			}

			reclaimedAt := now.Add(test.finalCheckpoint)
			clock.Set(reclaimedAt)

			reclaimed, found, err := queue.Claim(
				ctx,
				"worker-b",
			)
			if err != nil {
				t.Fatalf(
					"reclaim Claim() error = %v, want nil",
					err,
				)
			}

			if !found {
				t.Fatal(
					"reclaim Claim() found = false, want true",
				)
			}

			if reclaimed.Origin != source {
				t.Errorf(
					"reclaimed origin = %q, want %q",
					reclaimed.Origin,
					source,
				)
			}

			if reclaimed.WorkerID != "worker-b" {
				t.Errorf(
					"reclaimed worker = %q, want %q",
					reclaimed.WorkerID,
					"worker-b",
				)
			}

			if reclaimed.Generation != 2 {
				t.Errorf(
					"reclaimed generation = %d, want 2",
					reclaimed.Generation,
				)
			}

			if !reclaimed.ClaimedAt.Equal(reclaimedAt) {
				t.Errorf(
					"reclaimed at = %v, want %v",
					reclaimed.ClaimedAt,
					reclaimedAt,
				)
			}
		})
	}
}

func TestQueueRenewsValidLease(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	config := QueueConfig{
		LeaseDuration:     10 * time.Minute,
		MinOriginInterval: time.Minute,
	}
	queue, clock := newControlledQueue(
		t,
		pool,
		config,
		now,
	)
	source := mustStoreOrigin(t, "https://example.com")

	if err := queue.Schedule(ctx, source, now); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	lease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("Claim() found = false, want true")
	}

	before := readQueueState(t, pool, source.String())

	renewedAt := now.Add(8 * time.Minute)
	clock.Set(renewedAt)

	renewed, err := queue.Renew(ctx, lease)
	if err != nil {
		t.Fatalf("Renew() error = %v, want nil", err)
	}

	wantExpiresAt := renewedAt.Add(config.LeaseDuration)

	if renewed.Origin != lease.Origin {
		t.Errorf(
			"renewed origin = %q, want %q",
			renewed.Origin,
			lease.Origin,
		)
	}

	if renewed.WorkerID != lease.WorkerID {
		t.Errorf(
			"renewed worker = %q, want %q",
			renewed.WorkerID,
			lease.WorkerID,
		)
	}

	if renewed.Generation != lease.Generation {
		t.Errorf(
			"renewed generation = %d, want %d",
			renewed.Generation,
			lease.Generation,
		)
	}

	if !renewed.ClaimedAt.Equal(lease.ClaimedAt) {
		t.Errorf(
			"renewed claim time = %v, want %v",
			renewed.ClaimedAt,
			lease.ClaimedAt,
		)
	}

	if !renewed.ExpiresAt.Equal(wantExpiresAt) {
		t.Errorf(
			"renewed expiration = %v, want %v",
			renewed.ExpiresAt,
			wantExpiresAt,
		)
	}

	if renewed.ClaimedAt.Location() != time.UTC {
		t.Errorf(
			"renewed claim location = %v, want UTC",
			renewed.ClaimedAt.Location(),
		)
	}

	if renewed.ExpiresAt.Location() != time.UTC {
		t.Errorf(
			"renewed expiration location = %v, want UTC",
			renewed.ExpiresAt.Location(),
		)
	}

	after := readQueueState(t, pool, source.String())
	wantState := before
	wantState.leaseExpiresAt = wantExpiresAt.UTC()

	if after != wantState {
		t.Errorf(
			"stored state after Renew() = %#v, want %#v",
			after,
			wantState,
		)
	}

	clock.Set(lease.ExpiresAt)

	blockedLease, found, err := queue.Claim(
		ctx,
		"worker-b",
	)
	if err != nil {
		t.Fatalf(
			"Claim() at original expiration error = %v, want nil",
			err,
		)
	}

	if found {
		t.Error(
			"Claim() at original expiration found = true, want false",
		)
	}

	if blockedLease != (Lease{}) {
		t.Errorf(
			"Claim() at original expiration lease = %#v, want zero value",
			blockedLease,
		)
	}
}

func TestQueueRenewDoesNotShortenLease(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue, clock := newControlledQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		now,
	)
	source := mustStoreOrigin(t, "https://example.com")

	if err := queue.Schedule(ctx, source, now); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	lease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("Claim() found = false, want true")
	}

	before := readQueueState(t, pool, source.String())
	clock.Set(now.Add(-5 * time.Minute))

	renewed, err := queue.Renew(ctx, lease)
	if err != nil {
		t.Fatalf("Renew() error = %v, want nil", err)
	}

	if !renewed.ExpiresAt.Equal(lease.ExpiresAt) {
		t.Errorf(
			"renewed expiration = %v, want unchanged %v",
			renewed.ExpiresAt,
			lease.ExpiresAt,
		)
	}

	after := readQueueState(t, pool, source.String())
	if after != before {
		t.Errorf(
			"stored state after early Renew() = %#v, want %#v",
			after,
			before,
		)
	}
}

func TestQueueRenewRejectsLostLease(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(
			*testing.T,
			*controlledQueueClock,
			*Lease,
		)
	}{
		{
			name: "expired",
			prepare: func(
				_ *testing.T,
				clock *controlledQueueClock,
				lease *Lease,
			) {
				clock.Set(lease.ExpiresAt)
			},
		},
		{
			name: "wrong origin",
			prepare: func(
				t *testing.T,
				_ *controlledQueueClock,
				lease *Lease,
			) {
				lease.Origin = mustStoreOrigin(
					t,
					"https://example.net",
				)
			},
		},
		{
			name: "wrong worker",
			prepare: func(
				_ *testing.T,
				_ *controlledQueueClock,
				lease *Lease,
			) {
				lease.WorkerID = "worker-b"
			},
		},
		{
			name: "stale generation",
			prepare: func(
				_ *testing.T,
				_ *controlledQueueClock,
				lease *Lease,
			) {
				lease.Generation++
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := newStoreTestPool(t)
			now := queueTestTime()
			queue, clock := newControlledQueue(
				t,
				pool,
				QueueConfig{
					LeaseDuration:     10 * time.Minute,
					MinOriginInterval: time.Minute,
				},
				now,
			)
			source := mustStoreOrigin(
				t,
				"https://example.com",
			)

			if err := queue.Schedule(
				ctx,
				source,
				now,
			); err != nil {
				t.Fatalf(
					"Schedule() error = %v, want nil",
					err,
				)
			}

			lease, found, err := queue.Claim(
				ctx,
				"worker-a",
			)
			if err != nil {
				t.Fatalf(
					"Claim() error = %v, want nil",
					err,
				)
			}

			if !found {
				t.Fatal(
					"Claim() found = false, want true",
				)
			}

			before := readQueueState(
				t,
				pool,
				source.String(),
			)

			test.prepare(t, clock, &lease)

			renewed, err := queue.Renew(ctx, lease)
			if !errors.Is(err, ErrLeaseLost) {
				t.Errorf(
					"Renew() error = %v, want ErrLeaseLost",
					err,
				)
			}

			if renewed != (Lease{}) {
				t.Errorf(
					"Renew() lease = %#v, want zero value",
					renewed,
				)
			}

			after := readQueueState(
				t,
				pool,
				source.String(),
			)
			if after != before {
				t.Errorf(
					"stored state after rejected Renew() = %#v, want %#v",
					after,
					before,
				)
			}
		})
	}
}

func TestQueueStaleWorkerCannotRenewReclaimedLease(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue, clock := newControlledQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     time.Minute,
			MinOriginInterval: time.Minute,
		},
		now,
	)
	source := mustStoreOrigin(t, "https://example.com")

	if err := queue.Schedule(ctx, source, now); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	oldLease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil {
		t.Fatalf("first Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("first Claim() found = false, want true")
	}

	clock.Set(oldLease.ExpiresAt)

	currentLease, found, err := queue.Claim(
		ctx,
		"worker-b",
	)
	if err != nil {
		t.Fatalf("second Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("second Claim() found = false, want true")
	}

	if currentLease.Generation != 2 {
		t.Fatalf(
			"current generation = %d, want 2",
			currentLease.Generation,
		)
	}

	before := readQueueState(t, pool, source.String())

	renewed, err := queue.Renew(ctx, oldLease)
	if !errors.Is(err, ErrLeaseLost) {
		t.Errorf(
			"stale Renew() error = %v, want ErrLeaseLost",
			err,
		)
	}

	if renewed != (Lease{}) {
		t.Errorf(
			"stale Renew() lease = %#v, want zero value",
			renewed,
		)
	}

	after := readQueueState(t, pool, source.String())
	if after != before {
		t.Errorf(
			"stored state after stale Renew() = %#v, want %#v",
			after,
			before,
		)
	}
}

func TestQueueRenewPreservesClockFailure(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue, clock := newControlledQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		now,
	)
	source := mustStoreOrigin(t, "https://example.com")

	if err := queue.Schedule(ctx, source, now); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	lease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("Claim() found = false, want true")
	}

	before := readQueueState(t, pool, source.String())
	clockError := errors.New("test queue clock failure")
	clock.Fail(clockError)

	renewed, err := queue.Renew(ctx, lease)
	if !errors.Is(err, clockError) {
		t.Errorf(
			"Renew() error = %v, want clock failure",
			err,
		)
	}

	if renewed != (Lease{}) {
		t.Errorf(
			"Renew() lease = %#v, want zero value",
			renewed,
		)
	}

	after := readQueueState(t, pool, source.String())
	if after != before {
		t.Errorf(
			"stored state after clock failure = %#v, want %#v",
			after,
			before,
		)
	}
}

func TestQueueRenewReturnsDatabaseFailure(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue, _ := newControlledQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		now,
	)
	source := mustStoreOrigin(t, "https://example.com")

	if err := queue.Schedule(ctx, source, now); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	lease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("Claim() found = false, want true")
	}

	pool.Close()

	renewed, err := queue.Renew(ctx, lease)
	if err == nil {
		t.Error("Renew() error = nil, want non-nil")
	}

	if errors.Is(err, ErrLeaseLost) {
		t.Errorf(
			"Renew() error = %v, do not want ErrLeaseLost",
			err,
		)
	}

	if renewed != (Lease{}) {
		t.Errorf(
			"Renew() lease = %#v, want zero value",
			renewed,
		)
	}
}

type controlledQueueClock struct {
	mu  sync.RWMutex
	now time.Time
	err error
}

func (clock *controlledQueueClock) Now(
	context.Context,
	*pgxpool.Pool,
) (time.Time, error) {
	clock.mu.RLock()
	defer clock.mu.RUnlock()

	return clock.now.UTC(), clock.err
}

func (clock *controlledQueueClock) Set(now time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()

	clock.now = now.UTC()
	clock.err = nil
}

func (clock *controlledQueueClock) Fail(err error) {
	clock.mu.Lock()
	defer clock.mu.Unlock()

	clock.err = err
}

func newControlledQueue(
	t *testing.T,
	pool *pgxpool.Pool,
	config QueueConfig,
	now time.Time,
) (*Queue, *controlledQueueClock) {
	t.Helper()

	clock := &controlledQueueClock{
		now: now.UTC(),
	}
	queue, err := newQueue(pool, config, clock)
	if err != nil {
		t.Fatalf("newQueue() error = %v, want nil", err)
	}

	return queue, clock
}

type queueState struct {
	availableAt        time.Time
	generation         int64
	leaseOwner         string
	hasLeaseOwner      bool
	leaseExpiresAt     time.Time
	hasLeaseExpiration bool
	lastClaimedAt      time.Time
	hasLastClaim       bool
}

func readQueueState(
	t *testing.T,
	pool *pgxpool.Pool,
	originString string,
) queueState {
	t.Helper()

	var (
		state          queueState
		leaseOwner     *string
		leaseExpiresAt *time.Time
		lastClaimedAt  *time.Time
	)

	err := pool.QueryRow(
		context.Background(),
		`
			SELECT
				available_at,
				lease_generation,
				lease_owner,
				lease_expires_at,
				last_claimed_at
			FROM verification_queue
			WHERE origin = $1
		`,
		originString,
	).Scan(
		&state.availableAt,
		&state.generation,
		&leaseOwner,
		&leaseExpiresAt,
		&lastClaimedAt,
	)
	if err != nil {
		t.Fatalf("query queue state: %v", err)
	}

	state.availableAt = state.availableAt.UTC()

	if leaseOwner != nil {
		state.leaseOwner = *leaseOwner
		state.hasLeaseOwner = true
	}

	if leaseExpiresAt != nil {
		state.leaseExpiresAt = leaseExpiresAt.UTC()
		state.hasLeaseExpiration = true
	}

	if lastClaimedAt != nil {
		state.lastClaimedAt = lastClaimedAt.UTC()
		state.hasLastClaim = true
	}

	return state
}

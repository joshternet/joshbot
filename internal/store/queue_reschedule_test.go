package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueueReschedulesAtPoliteAvailability(
	t *testing.T,
) {
	now := queueTestTime()
	localZone := time.FixedZone("test-zone", -7*60*60)

	tests := []struct {
		name        string
		requestedAt time.Time
		wantAt      time.Time
	}{
		{
			name:        "politeness overrides early request",
			requestedAt: now.Add(time.Minute),
			wantAt:      now.Add(10 * time.Minute),
		},
		{
			name:        "later non-UTC request is preserved",
			requestedAt: now.Add(time.Hour).In(localZone),
			wantAt:      now.Add(time.Hour),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := newStoreTestPool(t)
			queue, clock := newControlledQueue(
				t,
				pool,
				QueueConfig{
					LeaseDuration:     30 * time.Minute,
					MinOriginInterval: 10 * time.Minute,
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

			if err := queue.Reschedule(
				ctx,
				lease,
				test.requestedAt,
			); err != nil {
				t.Fatalf(
					"Reschedule() error = %v, want nil",
					err,
				)
			}

			state := readQueueState(
				t,
				pool,
				source.String(),
			)

			if !state.availableAt.Equal(test.wantAt) {
				t.Errorf(
					"available_at = %v, want %v",
					state.availableAt,
					test.wantAt,
				)
			}

			if state.availableAt.Location() != time.UTC {
				t.Errorf(
					"available_at location = %v, want UTC",
					state.availableAt.Location(),
				)
			}

			if state.generation != lease.Generation {
				t.Errorf(
					"generation = %d, want %d",
					state.generation,
					lease.Generation,
				)
			}

			if state.hasLeaseOwner {
				t.Errorf(
					"lease owner = %q, want NULL",
					state.leaseOwner,
				)
			}

			if state.hasLeaseExpiration {
				t.Errorf(
					"lease expiration = %v, want NULL",
					state.leaseExpiresAt,
				)
			}

			if !state.hasLastClaim {
				t.Fatal(
					"last_claimed_at is NULL, want claim time",
				)
			}

			if !state.lastClaimedAt.Equal(
				lease.ClaimedAt,
			) {
				t.Errorf(
					"last_claimed_at = %v, want %v",
					state.lastClaimedAt,
					lease.ClaimedAt,
				)
			}

			clock.Set(
				test.wantAt.Add(-time.Microsecond),
			)

			blocked, found, err := queue.Claim(
				ctx,
				"worker-b",
			)
			if err != nil {
				t.Fatalf(
					"early Claim() error = %v, want nil",
					err,
				)
			}

			if found {
				t.Error(
					"early Claim() found = true, want false",
				)
			}

			if blocked != (Lease{}) {
				t.Errorf(
					"early Claim() lease = %#v, want zero value",
					blocked,
				)
			}

			clock.Set(test.wantAt)

			reclaimed, found, err := queue.Claim(
				ctx,
				"worker-b",
			)
			if err != nil {
				t.Fatalf(
					"due Claim() error = %v, want nil",
					err,
				)
			}

			if !found {
				t.Fatal(
					"due Claim() found = false, want true",
				)
			}

			if reclaimed.Generation != 2 {
				t.Errorf(
					"reclaimed generation = %d, want 2",
					reclaimed.Generation,
				)
			}
		})
	}
}

func TestQueueRescheduleIsSingleUse(t *testing.T) {
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

	if err := queue.Reschedule(
		ctx,
		lease,
		now.Add(time.Hour),
	); err != nil {
		t.Fatalf(
			"first Reschedule() error = %v, want nil",
			err,
		)
	}

	before := readQueueState(t, pool, source.String())

	err = queue.Reschedule(
		ctx,
		lease,
		now.Add(2*time.Hour),
	)
	if !errors.Is(err, ErrLeaseLost) {
		t.Errorf(
			"second Reschedule() error = %v, want ErrLeaseLost",
			err,
		)
	}

	after := readQueueState(t, pool, source.String())
	if after != before {
		t.Errorf(
			"state after repeated Reschedule() = %#v, want %#v",
			after,
			before,
		)
	}
}

func TestQueueRescheduleRejectsLostLease(t *testing.T) {
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

			err = queue.Reschedule(
				ctx,
				lease,
				now.Add(time.Hour),
			)
			if !errors.Is(err, ErrLeaseLost) {
				t.Errorf(
					"Reschedule() error = %v, want ErrLeaseLost",
					err,
				)
			}

			after := readQueueState(
				t,
				pool,
				source.String(),
			)
			if after != before {
				t.Errorf(
					"state after rejected Reschedule() = %#v, want %#v",
					after,
					before,
				)
			}
		})
	}
}

func TestQueueStaleWorkerCannotRescheduleReclaimedLease(
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

	err = queue.Reschedule(
		ctx,
		oldLease,
		now.Add(time.Hour),
	)
	if !errors.Is(err, ErrLeaseLost) {
		t.Errorf(
			"stale Reschedule() error = %v, want ErrLeaseLost",
			err,
		)
	}

	after := readQueueState(t, pool, source.String())
	if after != before {
		t.Errorf(
			"state after stale Reschedule() = %#v, want %#v",
			after,
			before,
		)
	}
}

func TestQueueReschedulePreservesClockFailure(
	t *testing.T,
) {
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

	err = queue.Reschedule(
		ctx,
		lease,
		now.Add(time.Hour),
	)
	if !errors.Is(err, clockError) {
		t.Errorf(
			"Reschedule() error = %v, want clock failure",
			err,
		)
	}

	after := readQueueState(t, pool, source.String())
	if after != before {
		t.Errorf(
			"state after clock failure = %#v, want %#v",
			after,
			before,
		)
	}
}

func TestQueueRescheduleReturnsDatabaseFailure(
	t *testing.T,
) {
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

	err = queue.Reschedule(
		ctx,
		lease,
		now.Add(time.Hour),
	)
	if err == nil {
		t.Error(
			"Reschedule() error = nil, want non-nil",
		)
	}

	if errors.Is(err, ErrLeaseLost) {
		t.Errorf(
			"Reschedule() error = %v, do not want ErrLeaseLost",
			err,
		)
	}
}

func TestQueueScheduleCoalescesActiveLease(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue, _ := newControlledQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Hour,
		},
		now,
	)
	source := mustStoreOrigin(t, "https://example.com")

	if err := queue.Schedule(ctx, source, now); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	_, found, err := queue.Claim(ctx, "worker-a")
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("Claim() found = false, want true")
	}

	before := readQueueState(t, pool, source.String())

	if err := queue.Schedule(
		ctx,
		source,
		now.Add(-time.Hour),
	); err != nil {
		t.Fatalf(
			"duplicate Schedule() error = %v, want nil",
			err,
		)
	}

	after := readQueueState(t, pool, source.String())
	if after != before {
		t.Errorf(
			"active state after Schedule() = %#v, want %#v",
			after,
			before,
		)
	}

	var rowCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_queue
			WHERE origin = $1
		`,
		source.String(),
	).Scan(&rowCount)
	if err != nil {
		t.Fatalf("count queue rows: %v", err)
	}

	if rowCount != 1 {
		t.Errorf(
			"queue row count = %d, want 1",
			rowCount,
		)
	}

	blocked, found, err := queue.Claim(
		ctx,
		"worker-b",
	)
	if err != nil {
		t.Fatalf(
			"duplicate Claim() error = %v, want nil",
			err,
		)
	}

	if found {
		t.Error(
			"duplicate Claim() found = true, want false",
		)
	}

	if blocked != (Lease{}) {
		t.Errorf(
			"duplicate Claim() lease = %#v, want zero value",
			blocked,
		)
	}
}

func TestQueueGenerationChangesOnlyOnClaim(t *testing.T) {
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

	firstLease, found, err := queue.Claim(
		ctx,
		"worker-a",
	)
	if err != nil {
		t.Fatalf("first Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("first Claim() found = false, want true")
	}

	if firstLease.Generation != 1 {
		t.Fatalf(
			"first generation = %d, want 1",
			firstLease.Generation,
		)
	}

	firstDueAt := now.Add(time.Minute)
	if err := queue.Reschedule(
		ctx,
		firstLease,
		firstDueAt,
	); err != nil {
		t.Fatalf(
			"first Reschedule() error = %v, want nil",
			err,
		)
	}

	clock.Set(firstDueAt)

	secondLease, found, err := queue.Claim(
		ctx,
		"worker-b",
	)
	if err != nil {
		t.Fatalf("second Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("second Claim() found = false, want true")
	}

	if secondLease.Generation != 2 {
		t.Fatalf(
			"second generation = %d, want 2",
			secondLease.Generation,
		)
	}

	clock.Set(firstDueAt.Add(30 * time.Second))

	renewed, err := queue.Renew(ctx, secondLease)
	if err != nil {
		t.Fatalf("Renew() error = %v, want nil", err)
	}

	if renewed.Generation != 2 {
		t.Errorf(
			"renewed generation = %d, want 2",
			renewed.Generation,
		)
	}

	secondDueAt := now.Add(2 * time.Minute)
	if err := queue.Reschedule(
		ctx,
		renewed,
		secondDueAt,
	); err != nil {
		t.Fatalf(
			"second Reschedule() error = %v, want nil",
			err,
		)
	}

	clock.Set(secondDueAt)

	thirdLease, found, err := queue.Claim(
		ctx,
		"worker-c",
	)
	if err != nil {
		t.Fatalf("third Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("third Claim() found = false, want true")
	}

	if thirdLease.Generation != 3 {
		t.Errorf(
			"third generation = %d, want 3",
			thirdLease.Generation,
		)
	}

	var (
		originCount      int
		observationCount int
	)
	err = pool.QueryRow(
		ctx,
		"SELECT count(*) FROM origins",
	).Scan(&originCount)
	if err != nil {
		t.Fatalf("count origins: %v", err)
	}

	err = pool.QueryRow(
		ctx,
		"SELECT count(*) FROM verification_observations",
	).Scan(&observationCount)
	if err != nil {
		t.Fatalf("count observations: %v", err)
	}

	if originCount != 0 {
		t.Errorf(
			"origin count = %d, want 0",
			originCount,
		)
	}

	if observationCount != 0 {
		t.Errorf(
			"observation count = %d, want 0",
			observationCount,
		)
	}
}

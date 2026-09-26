package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueueAbandonVerificationReleasesAuthoritativeLease(
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

	abandonedAt := now.Add(
		30 * time.Second,
	)
	clock.Set(abandonedAt)

	if err := queue.AbandonVerification(
		ctx,
		lease,
	); err != nil {
		t.Fatalf(
			"AbandonVerification() error = %v, want nil",
			err,
		)
	}

	after := readQueueState(
		t,
		pool,
		source.String(),
	)

	wantState := before
	wantState.leaseOwner = ""
	wantState.hasLeaseOwner = false
	wantState.leaseExpiresAt = time.Time{}
	wantState.hasLeaseExpiration = false

	if after != wantState {
		t.Errorf(
			"stored state after AbandonVerification() = %#v, want %#v",
			after,
			wantState,
		)
	}

	blockedLease, found, err := queue.Claim(
		ctx,
		"worker-b",
	)
	if err != nil {
		t.Fatalf(
			"Claim() before politeness interval error = %v, want nil",
			err,
		)
	}

	if found {
		t.Error(
			"Claim() before politeness interval found = true, want false",
		)
	}

	if blockedLease != (Lease{}) {
		t.Errorf(
			"Claim() before politeness interval lease = %#v, want zero value",
			blockedLease,
		)
	}

	clock.Set(
		lease.ClaimedAt.Add(
			time.Minute,
		),
	)

	reclaimed, found, err := queue.Claim(
		ctx,
		"worker-b",
	)
	if err != nil {
		t.Fatalf(
			"Claim() after politeness interval error = %v, want nil",
			err,
		)
	}

	if !found {
		t.Fatal(
			"Claim() after politeness interval found = false, want true",
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
			"reclaimed worker = %q, want worker-b",
			reclaimed.WorkerID,
		)
	}

	if reclaimed.Generation != lease.Generation+1 {
		t.Errorf(
			"reclaimed generation = %d, want %d",
			reclaimed.Generation,
			lease.Generation+1,
		)
	}
}

func TestQueueAbandonVerificationRejectsLostLease(
	t *testing.T,
) {
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
				clock.Set(
					lease.ExpiresAt,
				)
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
		t.Run(
			test.name,
			func(t *testing.T) {
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

				test.prepare(
					t,
					clock,
					&lease,
				)

				err = queue.AbandonVerification(
					ctx,
					lease,
				)
				if !errors.Is(
					err,
					ErrLeaseLost,
				) {
					t.Errorf(
						"AbandonVerification() error = %v, want ErrLeaseLost",
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
						"stored state after rejected AbandonVerification() = %#v, want %#v",
						after,
						before,
					)
				}
			},
		)
	}
}

func TestQueueAbandonVerificationRejectsInvalidLease(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	queue, _ := newControlledQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)

	err := queue.AbandonVerification(
		ctx,
		Lease{},
	)
	if !errors.Is(
		err,
		errInvalidLease,
	) {
		t.Errorf(
			"AbandonVerification() error = %v, want errInvalidLease",
			err,
		)
	}
}

func TestQueueAbandonVerificationPreservesClockFailure(
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

	clockError := errors.New(
		"test queue clock failure",
	)
	clock.Fail(clockError)

	err = queue.AbandonVerification(
		ctx,
		lease,
	)
	if !errors.Is(
		err,
		clockError,
	) {
		t.Errorf(
			"AbandonVerification() error = %v, want clock failure",
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
			"stored state after clock failure = %#v, want %#v",
			after,
			before,
		)
	}
}

func TestQueueAbandonVerificationReturnsDatabaseFailure(
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

	pool.Close()

	err = queue.AbandonVerification(
		ctx,
		lease,
	)
	if err == nil {
		t.Fatal(
			"AbandonVerification() error = nil, want non-nil",
		)
	}

	if errors.Is(
		err,
		ErrLeaseLost,
	) {
		t.Errorf(
			"AbandonVerification() error = %v, do not want ErrLeaseLost",
			err,
		)
	}
}

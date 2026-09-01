package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
)

func TestNewQueueValidatesDependenciesAndConfiguration(
	t *testing.T,
) {
	pool := newStoreTestPool(t)
	validConfig := QueueConfig{
		LeaseDuration:     time.Minute,
		MinOriginInterval: time.Minute,
	}

	queue, err := NewQueue(nil, validConfig)
	if !errors.Is(err, errPoolUnavailable) {
		t.Errorf(
			"NewQueue(nil) error = %v, want errPoolUnavailable",
			err,
		)
	}

	if queue != nil {
		t.Errorf("NewQueue(nil) queue = %#v, want nil", queue)
	}

	invalidConfigs := []QueueConfig{
		{},
		{
			LeaseDuration:     -time.Minute,
			MinOriginInterval: time.Minute,
		},
		{
			LeaseDuration:     time.Minute,
			MinOriginInterval: -time.Minute,
		},
	}

	for _, config := range invalidConfigs {
		queue, err = NewQueue(pool, config)
		if !errors.Is(err, errInvalidQueueConfig) {
			t.Errorf(
				"NewQueue(%#v) error = %v, want errInvalidQueueConfig",
				config,
				err,
			)
		}

		if queue != nil {
			t.Errorf(
				"NewQueue(%#v) queue = %#v, want nil",
				config,
				queue,
			)
		}
	}

	queue, err = newQueue(pool, validConfig, nil)
	if !errors.Is(err, errQueueClockUnavailable) {
		t.Errorf(
			"newQueue(nil clock) error = %v, want errQueueClockUnavailable",
			err,
		)
	}

	if queue != nil {
		t.Errorf(
			"newQueue(nil clock) queue = %#v, want nil",
			queue,
		)
	}
}

func TestQueueRejectsUnavailableReceiverState(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	config := QueueConfig{
		LeaseDuration:     time.Minute,
		MinOriginInterval: time.Minute,
	}
	source := mustStoreOrigin(t, "https://example.com")
	now := queueTestTime()

	var nilQueue *Queue

	if err := nilQueue.Schedule(
		ctx,
		source,
		now,
	); !errors.Is(err, errQueueUnavailable) {
		t.Errorf(
			"nil Schedule() error = %v, want errQueueUnavailable",
			err,
		)
	}

	lease, found, err := nilQueue.Claim(ctx, "worker-a")
	if !errors.Is(err, errQueueUnavailable) {
		t.Errorf(
			"nil Claim() error = %v, want errQueueUnavailable",
			err,
		)
	}

	if found || lease != (Lease{}) {
		t.Errorf(
			"nil Claim() = (%#v, %v), want zero and false",
			lease,
			found,
		)
	}

	renewed, err := nilQueue.Renew(ctx, Lease{})
	if !errors.Is(err, errQueueUnavailable) {
		t.Errorf(
			"nil Renew() error = %v, want errQueueUnavailable",
			err,
		)
	}

	if renewed != (Lease{}) {
		t.Errorf(
			"nil Renew() lease = %#v, want zero value",
			renewed,
		)
	}

	err = nilQueue.Reschedule(ctx, Lease{}, now)
	if !errors.Is(err, errQueueUnavailable) {
		t.Errorf(
			"nil Reschedule() error = %v, want errQueueUnavailable",
			err,
		)
	}

	noPool := &Queue{
		config: config,
		clock: fixedQueueClock{
			now: now,
		},
	}
	if err := noPool.Schedule(
		ctx,
		source,
		now,
	); !errors.Is(err, errPoolUnavailable) {
		t.Errorf(
			"no-pool Schedule() error = %v, want errPoolUnavailable",
			err,
		)
	}

	badConfig := &Queue{
		pool:   pool,
		config: QueueConfig{},
		clock: fixedQueueClock{
			now: now,
		},
	}
	if err := badConfig.Schedule(
		ctx,
		source,
		now,
	); !errors.Is(err, errInvalidQueueConfig) {
		t.Errorf(
			"bad-config Schedule() error = %v, want errInvalidQueueConfig",
			err,
		)
	}

	noClock := &Queue{
		pool:   pool,
		config: config,
	}
	if err := noClock.Schedule(
		ctx,
		source,
		now,
	); !errors.Is(err, errQueueClockUnavailable) {
		t.Errorf(
			"no-clock Schedule() error = %v, want errQueueClockUnavailable",
			err,
		)
	}
}

func TestQueueRejectsNilAndCanceledContexts(t *testing.T) {
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     time.Minute,
			MinOriginInterval: time.Minute,
		},
		now,
	)
	source := mustStoreOrigin(t, "https://example.com")

	var nilContext context.Context

	if err := queue.Schedule(
		nilContext,
		source,
		now,
	); !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"Schedule(nil context) error = %v, want errInvalidContext",
			err,
		)
	}

	lease, found, err := queue.Claim(
		nilContext,
		"worker-a",
	)
	if !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"Claim(nil context) error = %v, want errInvalidContext",
			err,
		)
	}

	if found || lease != (Lease{}) {
		t.Errorf(
			"Claim(nil context) = (%#v, %v), want zero and false",
			lease,
			found,
		)
	}

	renewed, err := queue.Renew(nilContext, Lease{})
	if !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"Renew(nil context) error = %v, want errInvalidContext",
			err,
		)
	}

	if renewed != (Lease{}) {
		t.Errorf(
			"Renew(nil context) lease = %#v, want zero value",
			renewed,
		)
	}

	err = queue.Reschedule(nilContext, Lease{}, now)
	if !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"Reschedule(nil context) error = %v, want errInvalidContext",
			err,
		)
	}

	canceledContext, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := queue.Schedule(
		canceledContext,
		source,
		now,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Schedule(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	lease, found, err = queue.Claim(
		canceledContext,
		"worker-a",
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Claim(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	if found || lease != (Lease{}) {
		t.Errorf(
			"Claim(canceled) = (%#v, %v), want zero and false",
			lease,
			found,
		)
	}

	renewed, err = queue.Renew(
		canceledContext,
		Lease{},
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Renew(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	if renewed != (Lease{}) {
		t.Errorf(
			"Renew(canceled) lease = %#v, want zero value",
			renewed,
		)
	}

	err = queue.Reschedule(
		canceledContext,
		Lease{},
		now,
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Reschedule(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	deadlineContext, deadlineCancel :=
		context.WithDeadline(
			context.Background(),
			time.Now().Add(-time.Second),
		)
	defer deadlineCancel()

	if err := queue.Schedule(
		deadlineContext,
		source,
		now,
	); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf(
			"Schedule(expired deadline) error = %v, want context.DeadlineExceeded",
			err,
		)
	}
}

func TestQueueRejectsInvalidScheduleInputs(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     time.Minute,
			MinOriginInterval: time.Minute,
		},
		now,
	)
	source := mustStoreOrigin(t, "https://example.com")

	if err := queue.Schedule(
		ctx,
		originZeroValue(),
		now,
	); !errors.Is(err, errInvalidOrigin) {
		t.Errorf(
			"Schedule(zero origin) error = %v, want errInvalidOrigin",
			err,
		)
	}

	if err := queue.Schedule(
		ctx,
		source,
		time.Time{},
	); !errors.Is(err, errInvalidAvailableAt) {
		t.Errorf(
			"Schedule(zero time) error = %v, want errInvalidAvailableAt",
			err,
		)
	}
}

func TestQueueValidatesWorkerIDs(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)

	tests := []struct {
		name     string
		workerID string
	}{
		{
			name:     "empty",
			workerID: "",
		},
		{
			name: "oversized",
			workerID: strings.Repeat(
				"x",
				maxQueueWorkerIDLength+1,
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lease, found, err := queue.Claim(
				ctx,
				test.workerID,
			)
			if !errors.Is(err, errInvalidWorkerID) {
				t.Errorf(
					"Claim() error = %v, want errInvalidWorkerID",
					err,
				)
			}

			if found || lease != (Lease{}) {
				t.Errorf(
					"Claim() = (%#v, %v), want zero and false",
					lease,
					found,
				)
			}
		})
	}

	validWorkerID := strings.Repeat(
		"é",
		maxQueueWorkerIDLength,
	)
	lease, found, err := queue.Claim(ctx, validWorkerID)
	if err != nil {
		t.Errorf(
			"Claim(maximum worker ID) error = %v, want nil",
			err,
		)
	}

	if found || lease != (Lease{}) {
		t.Errorf(
			"Claim(maximum worker ID) = (%#v, %v), want zero and false",
			lease,
			found,
		)
	}
}

func TestQueueRejectsInvalidLeases(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     time.Minute,
			MinOriginInterval: time.Minute,
		},
		now,
	)
	source := mustStoreOrigin(t, "https://example.com")

	validLease := Lease{
		Origin:     source,
		WorkerID:   "worker-a",
		Generation: 1,
		ClaimedAt:  now,
		ExpiresAt:  now.Add(time.Minute),
	}

	tests := []struct {
		name  string
		lease Lease
	}{
		{
			name:  "zero value",
			lease: Lease{},
		},
		{
			name: "zero origin",
			lease: func() Lease {
				lease := validLease
				lease.Origin = originZeroValue()
				return lease
			}(),
		},
		{
			name: "empty worker",
			lease: func() Lease {
				lease := validLease
				lease.WorkerID = ""
				return lease
			}(),
		},
		{
			name: "oversized worker",
			lease: func() Lease {
				lease := validLease
				lease.WorkerID = strings.Repeat(
					"x",
					maxQueueWorkerIDLength+1,
				)
				return lease
			}(),
		},
		{
			name: "zero generation",
			lease: func() Lease {
				lease := validLease
				lease.Generation = 0
				return lease
			}(),
		},
		{
			name: "zero claim time",
			lease: func() Lease {
				lease := validLease
				lease.ClaimedAt = time.Time{}
				return lease
			}(),
		},
		{
			name: "zero expiration",
			lease: func() Lease {
				lease := validLease
				lease.ExpiresAt = time.Time{}
				return lease
			}(),
		},
		{
			name: "expiration at claim time",
			lease: func() Lease {
				lease := validLease
				lease.ExpiresAt = lease.ClaimedAt
				return lease
			}(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			renewed, err := queue.Renew(
				ctx,
				test.lease,
			)
			if !errors.Is(err, errInvalidLease) {
				t.Errorf(
					"Renew() error = %v, want errInvalidLease",
					err,
				)
			}

			if renewed != (Lease{}) {
				t.Errorf(
					"Renew() lease = %#v, want zero value",
					renewed,
				)
			}

			err = queue.Reschedule(
				ctx,
				test.lease,
				now.Add(time.Hour),
			)
			if !errors.Is(err, errInvalidLease) {
				t.Errorf(
					"Reschedule() error = %v, want errInvalidLease",
					err,
				)
			}
		})
	}

	err := queue.Reschedule(
		ctx,
		validLease,
		time.Time{},
	)
	if !errors.Is(err, errInvalidAvailableAt) {
		t.Errorf(
			"Reschedule(zero time) error = %v, want errInvalidAvailableAt",
			err,
		)
	}
}

func TestQueueSchedulePreservesClockAndDatabaseFailures(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	source := mustStoreOrigin(t, "https://example.com")
	config := QueueConfig{
		LeaseDuration:     time.Minute,
		MinOriginInterval: time.Minute,
	}

	queue, clock := newControlledQueue(
		t,
		pool,
		config,
		now,
	)
	clockError := errors.New("test queue clock failure")
	clock.Fail(clockError)

	if err := queue.Schedule(
		ctx,
		source,
		now,
	); !errors.Is(err, clockError) {
		t.Errorf(
			"Schedule() clock error = %v, want clock failure",
			err,
		)
	}

	clock.Set(now)
	pool.Close()

	if err := queue.Schedule(
		ctx,
		source,
		now,
	); err == nil {
		t.Error(
			"Schedule() database error = nil, want non-nil",
		)
	}
}

func TestQueueProductionClockFailureFailsClosed(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	queue, err := NewQueue(
		pool,
		QueueConfig{
			LeaseDuration:     time.Minute,
			MinOriginInterval: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("NewQueue() error = %v, want nil", err)
	}

	pool.Close()

	err = queue.Schedule(
		ctx,
		mustStoreOrigin(t, "https://example.com"),
		queueTestTime(),
	)
	if err == nil {
		t.Error(
			"Schedule() error = nil, want database clock failure",
		)
	}
}

func originZeroValue() origin.Origin {
	return origin.Origin{}
}

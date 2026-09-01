package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestQueueClaimsDueOrigin(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	config := QueueConfig{
		LeaseDuration:     10 * time.Minute,
		MinOriginInterval: time.Hour,
	}
	queue := newFixedQueue(t, pool, config, now)
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

	if lease.Origin != source {
		t.Errorf(
			"lease origin = %q, want %q",
			lease.Origin,
			source,
		)
	}

	if lease.WorkerID != "worker-a" {
		t.Errorf(
			"lease worker = %q, want %q",
			lease.WorkerID,
			"worker-a",
		)
	}

	if lease.Generation != 1 {
		t.Errorf(
			"lease generation = %d, want 1",
			lease.Generation,
		)
	}

	if !lease.ClaimedAt.Equal(now) {
		t.Errorf(
			"lease claimed at = %v, want %v",
			lease.ClaimedAt,
			now,
		)
	}

	wantExpiresAt := now.Add(config.LeaseDuration)
	if !lease.ExpiresAt.Equal(wantExpiresAt) {
		t.Errorf(
			"lease expires at = %v, want %v",
			lease.ExpiresAt,
			wantExpiresAt,
		)
	}

	if lease.ClaimedAt.Location() != time.UTC {
		t.Errorf(
			"claimed-at location = %v, want UTC",
			lease.ClaimedAt.Location(),
		)
	}

	if lease.ExpiresAt.Location() != time.UTC {
		t.Errorf(
			"expires-at location = %v, want UTC",
			lease.ExpiresAt.Location(),
		)
	}

	var (
		storedGeneration int64
		storedOwner      string
		storedExpiresAt  time.Time
		storedClaimedAt  time.Time
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				lease_generation,
				lease_owner,
				lease_expires_at,
				last_claimed_at
			FROM verification_queue
			WHERE origin = $1
		`,
		source.String(),
	).Scan(
		&storedGeneration,
		&storedOwner,
		&storedExpiresAt,
		&storedClaimedAt,
	)
	if err != nil {
		t.Fatalf("query claimed work: %v", err)
	}

	if storedGeneration != 1 {
		t.Errorf(
			"stored generation = %d, want 1",
			storedGeneration,
		)
	}

	if storedOwner != "worker-a" {
		t.Errorf(
			"stored owner = %q, want %q",
			storedOwner,
			"worker-a",
		)
	}

	if !storedExpiresAt.Equal(wantExpiresAt) {
		t.Errorf(
			"stored expiration = %v, want %v",
			storedExpiresAt,
			wantExpiresAt,
		)
	}

	if !storedClaimedAt.Equal(now) {
		t.Errorf(
			"stored claim time = %v, want %v",
			storedClaimedAt,
			now,
		)
	}
}

func TestQueueDoesNotClaimFutureWork(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Hour,
		},
		now,
	)
	source := mustStoreOrigin(t, "https://example.com")

	if err := queue.Schedule(
		ctx,
		source,
		now.Add(time.Minute),
	); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	lease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}

	if found {
		t.Errorf("Claim() found = true, want false")
	}

	if lease != (Lease{}) {
		t.Errorf("Claim() lease = %#v, want zero value", lease)
	}

	var generation int64
	err = pool.QueryRow(
		ctx,
		`
			SELECT lease_generation
			FROM verification_queue
			WHERE origin = $1
		`,
		source.String(),
	).Scan(&generation)
	if err != nil {
		t.Fatalf("query generation: %v", err)
	}

	if generation != 0 {
		t.Errorf(
			"lease generation = %d, want 0",
			generation,
		)
	}
}

func TestQueueClaimsEligibleWorkInDeterministicOrder(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Hour,
		},
		now,
	)

	t1 := now.Add(-2 * time.Hour)
	t2 := now.Add(-time.Hour)

	scheduled := []struct {
		rawURL      string
		availableAt time.Time
	}{
		{
			rawURL:      "https://example.net",
			availableAt: t2,
		},
		{
			rawURL:      "https://example.org",
			availableAt: t1,
		},
		{
			rawURL:      "https://example.com",
			availableAt: t1,
		},
	}

	for _, work := range scheduled {
		source := mustStoreOrigin(t, work.rawURL)
		if err := queue.Schedule(
			ctx,
			source,
			work.availableAt,
		); err != nil {
			t.Fatalf(
				"Schedule(%q) error = %v, want nil",
				work.rawURL,
				err,
			)
		}
	}

	wantOrigins := []string{
		"https://example.com",
		"https://example.org",
		"https://example.net",
	}

	for index, wantOrigin := range wantOrigins {
		lease, found, err := queue.Claim(
			ctx,
			"worker-a",
		)
		if err != nil {
			t.Fatalf(
				"Claim(%d) error = %v, want nil",
				index,
				err,
			)
		}

		if !found {
			t.Fatalf(
				"Claim(%d) found = false, want true",
				index,
			)
		}

		if lease.Origin.String() != wantOrigin {
			t.Errorf(
				"Claim(%d) origin = %q, want %q",
				index,
				lease.Origin,
				wantOrigin,
			)
		}
	}

	lease, found, err := queue.Claim(ctx, "worker-b")
	if err != nil {
		t.Fatalf("final Claim() error = %v, want nil", err)
	}

	if found {
		t.Errorf("final Claim() found = true, want false")
	}

	if lease != (Lease{}) {
		t.Errorf(
			"final Claim() lease = %#v, want zero value",
			lease,
		)
	}
}

func TestQueueUsesPostgreSQLClock(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	config := QueueConfig{
		LeaseDuration:     10 * time.Minute,
		MinOriginInterval: time.Hour,
	}
	queue, err := NewQueue(pool, config)
	if err != nil {
		t.Fatalf("NewQueue() error = %v, want nil", err)
	}

	source := mustStoreOrigin(t, "https://example.com")
	dueAt := time.Date(
		2000,
		time.January,
		1,
		0,
		0,
		0,
		0,
		time.UTC,
	)

	if err := queue.Schedule(ctx, source, dueAt); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	lease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("Claim() found = false, want true")
	}

	if lease.ClaimedAt.IsZero() {
		t.Error("lease claimed at is zero")
	}

	if lease.ExpiresAt.Sub(lease.ClaimedAt) !=
		config.LeaseDuration {
		t.Errorf(
			"lease duration = %v, want %v",
			lease.ExpiresAt.Sub(lease.ClaimedAt),
			config.LeaseDuration,
		)
	}

	if lease.ClaimedAt.Location() != time.UTC {
		t.Errorf(
			"claimed-at location = %v, want UTC",
			lease.ClaimedAt.Location(),
		)
	}

	if lease.ExpiresAt.Location() != time.UTC {
		t.Errorf(
			"expires-at location = %v, want UTC",
			lease.ExpiresAt.Location(),
		)
	}
}

func TestQueueClaimPreservesClockFailure(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	clockError := errors.New("test clock failure")

	queue, err := newQueue(
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Hour,
		},
		fixedQueueClock{
			err: clockError,
		},
	)
	if err != nil {
		t.Fatalf("newQueue() error = %v, want nil", err)
	}

	lease, found, err := queue.Claim(ctx, "worker-a")
	if !errors.Is(err, clockError) {
		t.Errorf(
			"Claim() error = %v, want clock failure",
			err,
		)
	}

	if found {
		t.Error("Claim() found = true, want false")
	}

	if lease != (Lease{}) {
		t.Errorf("Claim() lease = %#v, want zero value", lease)
	}
}

func TestQueueClaimReturnsDatabaseFailure(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Hour,
		},
		queueTestTime(),
	)

	pool.Close()

	lease, found, err := queue.Claim(ctx, "worker-a")
	if err == nil {
		t.Error("Claim() error = nil, want non-nil")
	}

	if found {
		t.Error("Claim() found = true, want false")
	}

	if lease != (Lease{}) {
		t.Errorf("Claim() lease = %#v, want zero value", lease)
	}
}

func TestQueueClaimRollsBackInvalidStoredOrigin(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	now := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Hour,
		},
		now,
	)

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO verification_queue (
				origin,
				available_at
			)
			VALUES ($1, $2)
		`,
		"not-an-origin",
		now,
	)
	if err != nil {
		t.Fatalf("insert invalid stored origin: %v", err)
	}

	lease, found, err := queue.Claim(ctx, "worker-a")
	if err == nil {
		t.Error("Claim() error = nil, want non-nil")
	}

	if found {
		t.Error("Claim() found = true, want false")
	}

	if lease != (Lease{}) {
		t.Errorf("Claim() lease = %#v, want zero value", lease)
	}

	var generation int64
	err = pool.QueryRow(
		ctx,
		`
			SELECT lease_generation
			FROM verification_queue
			WHERE origin = $1
		`,
		"not-an-origin",
	).Scan(&generation)
	if err != nil {
		t.Fatalf("query invalid row generation: %v", err)
	}

	if generation != 0 {
		t.Errorf(
			"lease generation = %d, want 0",
			generation,
		)
	}
}

type fixedQueueClock struct {
	now time.Time
	err error
}

func (clock fixedQueueClock) Now(
	context.Context,
	*pgxpool.Pool,
) (time.Time, error) {
	return clock.now.UTC(), clock.err
}

func newFixedQueue(
	t *testing.T,
	pool *pgxpool.Pool,
	config QueueConfig,
	now time.Time,
) *Queue {
	t.Helper()

	queue, err := newQueue(
		pool,
		config,
		fixedQueueClock{
			now: now,
		},
	)
	if err != nil {
		t.Fatalf("newQueue() error = %v, want nil", err)
	}

	return queue
}

func queueTestTime() time.Time {
	return time.Date(
		2026,
		time.August,
		31,
		12,
		0,
		0,
		0,
		time.UTC,
	)
}

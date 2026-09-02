package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
)

type completionFixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	queue   *Queue
	source  origin.Origin
	lease   Lease
	claimed time.Time
}

type completionQueueState struct {
	availableAt    time.Time
	generation     int64
	leaseOwner     *string
	leaseExpiresAt *time.Time
	lastClaimedAt  time.Time
}

func TestQueueCompletesVerificationAtomically(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Hour,
		},
		queueTestTime(),
	)
	completedAt := fixture.claimed.Add(5 * time.Minute)
	fixture.queue.clock = fixedQueueClock{
		now: completedAt,
	}

	result := validCompletionResult(fixture.source)
	recheckAfter := 24 * time.Hour

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		result,
		recheckAfter,
	); err != nil {
		t.Fatalf(
			"CompleteVerification() error = %v, want nil",
			err,
		)
	}

	if got := completionObservationCount(
		t,
		fixture,
	); got != 1 {
		t.Errorf(
			"observation count = %d, want 1",
			got,
		)
	}

	state, found, err := New(fixture.pool).OriginState(
		fixture.ctx,
		fixture.source,
	)
	if err != nil {
		t.Fatalf(
			"OriginState() error = %v, want nil",
			err,
		)
	}

	if !found {
		t.Fatal(
			"OriginState() found = false, want true",
		)
	}

	if !state.FirstObservedAt.Equal(completedAt) {
		t.Errorf(
			"first observed at = %v, want %v",
			state.FirstObservedAt,
			completedAt,
		)
	}

	wantObservation := Observation{
		Outcome:    declaration.OutcomeValid,
		ObservedAt: completedAt,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityAffirmed,
		},
	}
	assertStoreObservation(
		t,
		state.Latest,
		wantObservation,
	)

	if state.Effective.State != StateVerified {
		t.Errorf(
			"effective state = %v, want StateVerified",
			state.Effective.State,
		)
	}

	assertStoreObservation(
		t,
		state.Effective.Observation,
		wantObservation,
	)

	queueState := readCompletionQueueState(
		t,
		fixture,
	)
	if !queueState.availableAt.Equal(
		completedAt.Add(recheckAfter),
	) {
		t.Errorf(
			"available at = %v, want %v",
			queueState.availableAt,
			completedAt.Add(recheckAfter),
		)
	}

	if queueState.generation != fixture.lease.Generation {
		t.Errorf(
			"lease generation = %d, want %d",
			queueState.generation,
			fixture.lease.Generation,
		)
	}

	if queueState.leaseOwner != nil {
		t.Errorf(
			"lease owner = %q, want nil",
			*queueState.leaseOwner,
		)
	}

	if queueState.leaseExpiresAt != nil {
		t.Errorf(
			"lease expiration = %v, want nil",
			*queueState.leaseExpiresAt,
		)
	}

	if !queueState.lastClaimedAt.Equal(
		fixture.lease.ClaimedAt,
	) {
		t.Errorf(
			"last claimed at = %v, want %v",
			queueState.lastClaimedAt,
			fixture.lease.ClaimedAt,
		)
	}
}

func TestQueueCompletionEnforcesMinimumOriginInterval(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     3 * time.Hour,
			MinOriginInterval: 2 * time.Hour,
		},
		queueTestTime(),
	)
	completedAt := fixture.claimed.Add(5 * time.Minute)
	fixture.queue.clock = fixedQueueClock{
		now: completedAt,
	}

	if err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		validCompletionResult(fixture.source),
		30*time.Minute,
	); err != nil {
		t.Fatalf(
			"CompleteVerification() error = %v, want nil",
			err,
		)
	}

	got := readCompletionQueueState(
		t,
		fixture,
	)
	wantAvailableAt := fixture.claimed.Add(
		2 * time.Hour,
	)

	if !got.availableAt.Equal(wantAvailableAt) {
		t.Errorf(
			"available at = %v, want %v",
			got.availableAt,
			wantAvailableAt,
		)
	}
}

func TestQueueCompletionRejectsExpiredLease(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)
	before := readCompletionQueueState(t, fixture)

	fixture.queue.clock = fixedQueueClock{
		now: fixture.lease.ExpiresAt,
	}

	err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		validCompletionResult(fixture.source),
		time.Hour,
	)
	if !errors.Is(err, ErrLeaseLost) {
		t.Errorf(
			"CompleteVerification() error = %v, want ErrLeaseLost",
			err,
		)
	}

	assertCompletionNotRecorded(t, fixture)
	assertCompletionQueueState(
		t,
		readCompletionQueueState(t, fixture),
		before,
	)
}

func TestQueueCompletionRejectsWrongWorker(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)
	before := readCompletionQueueState(t, fixture)
	completedAt := fixture.claimed.Add(time.Minute)
	fixture.queue.clock = fixedQueueClock{
		now: completedAt,
	}

	fabricated := fixture.lease
	fabricated.WorkerID = "worker-b"

	err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fabricated,
		validCompletionResult(fixture.source),
		time.Hour,
	)
	if !errors.Is(err, ErrLeaseLost) {
		t.Errorf(
			"CompleteVerification() error = %v, want ErrLeaseLost",
			err,
		)
	}

	assertCompletionNotRecorded(t, fixture)
	assertCompletionQueueState(
		t,
		readCompletionQueueState(t, fixture),
		before,
	)
}

func TestQueueCompletionRejectsWrongGeneration(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)
	before := readCompletionQueueState(t, fixture)
	fixture.queue.clock = fixedQueueClock{
		now: fixture.claimed.Add(time.Minute),
	}

	fabricated := fixture.lease
	fabricated.Generation++

	err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fabricated,
		validCompletionResult(fixture.source),
		time.Hour,
	)
	if !errors.Is(err, ErrLeaseLost) {
		t.Errorf(
			"CompleteVerification() error = %v, want ErrLeaseLost",
			err,
		)
	}

	assertCompletionNotRecorded(t, fixture)
	assertCompletionQueueState(
		t,
		readCompletionQueueState(t, fixture),
		before,
	)
}

func TestQueueCompletionRejectsReclaimedLease(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)

	fixture.queue.clock = fixedQueueClock{
		now: fixture.lease.ExpiresAt,
	}
	replacement, found, err := fixture.queue.Claim(
		fixture.ctx,
		"worker-b",
	)
	if err != nil {
		t.Fatalf(
			"replacement Claim() error = %v, want nil",
			err,
		)
	}

	if !found {
		t.Fatal(
			"replacement Claim() found = false, want true",
		)
	}

	before := readCompletionQueueState(t, fixture)

	err = fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		validCompletionResult(fixture.source),
		time.Hour,
	)
	if !errors.Is(err, ErrLeaseLost) {
		t.Errorf(
			"stale CompleteVerification() error = %v, want ErrLeaseLost",
			err,
		)
	}

	assertCompletionNotRecorded(t, fixture)

	after := readCompletionQueueState(t, fixture)
	assertCompletionQueueState(t, after, before)

	if after.leaseOwner == nil ||
		*after.leaseOwner != replacement.WorkerID {
		t.Errorf(
			"current lease owner = %v, want %q",
			after.leaseOwner,
			replacement.WorkerID,
		)
	}

	if after.generation != replacement.Generation {
		t.Errorf(
			"current generation = %d, want %d",
			after.generation,
			replacement.Generation,
		)
	}
}

func TestQueueCompletionRollsBackEveryMutation(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)
	before := readCompletionQueueState(t, fixture)

	_, err := fixture.pool.Exec(
		fixture.ctx,
		`
			ALTER TABLE verification_observations
			ADD CONSTRAINT test_reject_valid_completion
			CHECK (outcome <> 'valid')
		`,
	)
	if err != nil {
		t.Fatalf(
			"install test constraint: %v",
			err,
		)
	}

	fixture.queue.clock = fixedQueueClock{
		now: fixture.claimed.Add(time.Minute),
	}

	err = fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		validCompletionResult(fixture.source),
		time.Hour,
	)
	if err == nil {
		t.Fatal(
			"CompleteVerification() error = nil, want non-nil",
		)
	}

	assertCompletionNotRecorded(t, fixture)
	assertCompletionQueueState(
		t,
		readCompletionQueueState(t, fixture),
		before,
	)

	var originCount int
	err = fixture.pool.QueryRow(
		fixture.ctx,
		`
			SELECT count(*)
			FROM origins
			WHERE origin = $1
		`,
		fixture.source.String(),
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
}

func TestQueueCompletionRejectsInvalidInput(
	t *testing.T,
) {
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)
	validResult := validCompletionResult(source)

	var nilQueue *Queue
	err := nilQueue.CompleteVerification(
		context.Background(),
		Lease{},
		validResult,
		time.Hour,
	)
	if !errors.Is(err, errQueueUnavailable) {
		t.Errorf(
			"nil queue error = %v, want errQueueUnavailable",
			err,
		)
	}

	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)
	before := readCompletionQueueState(t, fixture)

	var nilContext context.Context
	err = fixture.queue.CompleteVerification(
		nilContext,
		fixture.lease,
		validResult,
		time.Hour,
	)
	if !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"nil context error = %v, want errInvalidContext",
			err,
		)
	}

	canceledContext, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	err = fixture.queue.CompleteVerification(
		canceledContext,
		fixture.lease,
		validResult,
		time.Hour,
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"canceled context error = %v, want context.Canceled",
			err,
		)
	}

	err = fixture.queue.CompleteVerification(
		fixture.ctx,
		Lease{},
		validResult,
		time.Hour,
	)
	if !errors.Is(err, errInvalidLease) {
		t.Errorf(
			"invalid lease error = %v, want errInvalidLease",
			err,
		)
	}

	err = fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		declaration.Result{
			Outcome: declaration.OutcomeValid,
		},
		time.Hour,
	)
	if !errors.Is(err, errInvalidOrigin) {
		t.Errorf(
			"empty result origin error = %v, want errInvalidOrigin",
			err,
		)
	}

	otherOrigin := mustStoreOrigin(
		t,
		"https://example.net",
	)
	err = fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		validCompletionResult(otherOrigin),
		time.Hour,
	)
	if !errors.Is(err, errCompletionOriginMismatch) {
		t.Errorf(
			"mismatched origin error = %v, want errCompletionOriginMismatch",
			err,
		)
	}

	err = fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		declaration.Result{
			Outcome: declaration.Outcome(255),
			Origin:  fixture.source,
		},
		time.Hour,
	)
	if !errors.Is(err, errInvalidResult) {
		t.Errorf(
			"invalid result error = %v, want errInvalidResult",
			err,
		)
	}

	for _, recheckAfter := range []time.Duration{
		0,
		-time.Second,
	} {
		err = fixture.queue.CompleteVerification(
			fixture.ctx,
			fixture.lease,
			validResult,
			recheckAfter,
		)
		if !errors.Is(err, errInvalidRecheckAfter) {
			t.Errorf(
				"recheck duration %v error = %v, want errInvalidRecheckAfter",
				recheckAfter,
				err,
			)
		}
	}

	assertCompletionNotRecorded(t, fixture)
	assertCompletionQueueState(
		t,
		readCompletionQueueState(t, fixture),
		before,
	)
}

func TestQueueCompletionPreservesClockFailure(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)
	before := readCompletionQueueState(t, fixture)
	clockError := errors.New("test completion clock failure")
	fixture.queue.clock = fixedQueueClock{
		err: clockError,
	}

	err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		validCompletionResult(fixture.source),
		time.Hour,
	)
	if !errors.Is(err, clockError) {
		t.Errorf(
			"CompleteVerification() error = %v, want clock failure",
			err,
		)
	}

	assertCompletionNotRecorded(t, fixture)
	assertCompletionQueueState(
		t,
		readCompletionQueueState(t, fixture),
		before,
	)
}

func TestQueueCompletionReturnsDatabaseFailure(
	t *testing.T,
) {
	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)
	fixture.queue.clock = fixedQueueClock{
		now: fixture.claimed.Add(time.Minute),
	}
	fixture.pool.Close()

	err := fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		validCompletionResult(fixture.source),
		time.Hour,
	)
	if err == nil {
		t.Fatal(
			"CompleteVerification() error = nil, want non-nil",
		)
	}
}

func TestQueueCompletionPreservesAuthoritativeStateForTemporaryOutcomes(
	t *testing.T,
) {
	outcomes := []declaration.Outcome{
		declaration.OutcomeUnavailable,
		declaration.OutcomeRobotsDenied,
	}

	for _, outcome := range outcomes {
		t.Run(
			completionOutcomeName(outcome),
			func(t *testing.T) {
				claimedAt := queueTestTime()
				fixture := newCompletionFixture(
					t,
					QueueConfig{
						LeaseDuration:     10 * time.Minute,
						MinOriginInterval: time.Minute,
					},
					claimedAt,
				)
				previousAt := claimedAt.Add(
					-24 * time.Hour,
				)
				previous := validCompletionResult(
					fixture.source,
				)

				if err := New(
					fixture.pool,
				).RecordVerification(
					fixture.ctx,
					previousAt,
					previous,
				); err != nil {
					t.Fatalf(
						"RecordVerification() error = %v, want nil",
						err,
					)
				}

				completedAt := claimedAt.Add(
					time.Minute,
				)
				fixture.queue.clock = fixedQueueClock{
					now: completedAt,
				}

				if err := fixture.queue.CompleteVerification(
					fixture.ctx,
					fixture.lease,
					declaration.Result{
						Outcome: outcome,
						Origin:  fixture.source,
					},
					time.Hour,
				); err != nil {
					t.Fatalf(
						"CompleteVerification() error = %v, want nil",
						err,
					)
				}

				state, found, err := New(
					fixture.pool,
				).OriginState(
					fixture.ctx,
					fixture.source,
				)
				if err != nil {
					t.Fatalf(
						"OriginState() error = %v, want nil",
						err,
					)
				}

				if !found {
					t.Fatal(
						"OriginState() found = false, want true",
					)
				}

				assertStoreObservation(
					t,
					state.Latest,
					Observation{
						Outcome:    outcome,
						ObservedAt: completedAt,
					},
				)

				if state.Effective.State != StateVerified {
					t.Errorf(
						"effective state = %v, want StateVerified",
						state.Effective.State,
					)
				}

				assertStoreObservation(
					t,
					state.Effective.Observation,
					Observation{
						Outcome:    declaration.OutcomeValid,
						ObservedAt: previousAt,
						Declaration: declaration.Declaration{
							Version:  1,
							Identity: declaration.IdentityAffirmed,
						},
					},
				)

				if got := completionObservationCount(
					t,
					fixture,
				); got != 2 {
					t.Errorf(
						"observation count = %d, want 2",
						got,
					)
				}

				queueState := readCompletionQueueState(
					t,
					fixture,
				)
				if queueState.leaseOwner != nil {
					t.Errorf(
						"lease owner = %q, want nil",
						*queueState.leaseOwner,
					)
				}

				if queueState.leaseExpiresAt != nil {
					t.Errorf(
						"lease expiration = %v, want nil",
						*queueState.leaseExpiresAt,
					)
				}
			},
		)
	}
}

func TestQueueCompletionUsesPostgreSQLClockInsideTransaction(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	config := QueueConfig{
		LeaseDuration:     10 * time.Minute,
		MinOriginInterval: time.Nanosecond,
	}
	queue, err := NewQueue(pool, config)
	if err != nil {
		t.Fatalf(
			"NewQueue() error = %v, want nil",
			err,
		)
	}

	source := mustStoreOrigin(
		t,
		"https://example.com",
	)
	if err := queue.Schedule(
		ctx,
		source,
		time.Unix(0, 0).UTC(),
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
		t.Fatal("Claim() found = false, want true")
	}

	var before time.Time
	err = pool.QueryRow(
		ctx,
		"SELECT clock_timestamp()",
	).Scan(&before)
	if err != nil {
		t.Fatalf(
			"read database time before completion: %v",
			err,
		)
	}

	recheckAfter := time.Hour
	if err := queue.CompleteVerification(
		ctx,
		lease,
		validCompletionResult(source),
		recheckAfter,
	); err != nil {
		t.Fatalf(
			"CompleteVerification() error = %v, want nil",
			err,
		)
	}

	var after time.Time
	err = pool.QueryRow(
		ctx,
		"SELECT clock_timestamp()",
	).Scan(&after)
	if err != nil {
		t.Fatalf(
			"read database time after completion: %v",
			err,
		)
	}

	var (
		observedAt  time.Time
		availableAt time.Time
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				observation.observed_at,
				queued.available_at
			FROM verification_observations AS observation
			JOIN verification_queue AS queued
				ON queued.origin = observation.origin
			WHERE observation.origin = $1
		`,
		source.String(),
	).Scan(
		&observedAt,
		&availableAt,
	)
	if err != nil {
		t.Fatalf(
			"query completed timestamps: %v",
			err,
		)
	}

	if observedAt.Before(before) ||
		observedAt.After(after) {
		t.Errorf(
			"observed at = %v, want between %v and %v",
			observedAt,
			before,
			after,
		)
	}

	if !availableAt.Equal(
		observedAt.Add(recheckAfter),
	) {
		t.Errorf(
			"available at = %v, want %v",
			availableAt,
			observedAt.Add(recheckAfter),
		)
	}
}

func newCompletionFixture(
	t *testing.T,
	config QueueConfig,
	claimedAt time.Time,
) completionFixture {
	t.Helper()

	ctx := context.Background()
	pool := newStoreTestPool(t)
	queue := newFixedQueue(
		t,
		pool,
		config,
		claimedAt,
	)
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	if err := queue.Schedule(
		ctx,
		source,
		claimedAt,
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
		t.Fatal("Claim() found = false, want true")
	}

	return completionFixture{
		ctx:     ctx,
		pool:    pool,
		queue:   queue,
		source:  source,
		lease:   lease,
		claimed: claimedAt,
	}
}

func validCompletionResult(
	source origin.Origin,
) declaration.Result {
	return declaration.Result{
		Outcome: declaration.OutcomeValid,
		Origin:  source,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityAffirmed,
		},
	}
}

func readCompletionQueueState(
	t *testing.T,
	fixture completionFixture,
) completionQueueState {
	t.Helper()

	var state completionQueueState
	err := fixture.pool.QueryRow(
		fixture.ctx,
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
		fixture.source.String(),
	).Scan(
		&state.availableAt,
		&state.generation,
		&state.leaseOwner,
		&state.leaseExpiresAt,
		&state.lastClaimedAt,
	)
	if err != nil {
		t.Fatalf(
			"query queue state: %v",
			err,
		)
	}

	return state
}

func assertCompletionQueueState(
	t *testing.T,
	got completionQueueState,
	want completionQueueState,
) {
	t.Helper()

	if !got.availableAt.Equal(want.availableAt) {
		t.Errorf(
			"available at = %v, want %v",
			got.availableAt,
			want.availableAt,
		)
	}

	if got.generation != want.generation {
		t.Errorf(
			"generation = %d, want %d",
			got.generation,
			want.generation,
		)
	}

	if !equalCompletionStrings(
		got.leaseOwner,
		want.leaseOwner,
	) {
		t.Errorf(
			"lease owner = %v, want %v",
			got.leaseOwner,
			want.leaseOwner,
		)
	}

	if !equalCompletionTimes(
		got.leaseExpiresAt,
		want.leaseExpiresAt,
	) {
		t.Errorf(
			"lease expiration = %v, want %v",
			got.leaseExpiresAt,
			want.leaseExpiresAt,
		)
	}

	if !got.lastClaimedAt.Equal(want.lastClaimedAt) {
		t.Errorf(
			"last claimed at = %v, want %v",
			got.lastClaimedAt,
			want.lastClaimedAt,
		)
	}
}

func equalCompletionStrings(
	left *string,
	right *string,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return *left == *right
}

func equalCompletionTimes(
	left *time.Time,
	right *time.Time,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return left.Equal(*right)
}

func completionObservationCount(
	t *testing.T,
	fixture completionFixture,
) int {
	t.Helper()

	var count int
	err := fixture.pool.QueryRow(
		fixture.ctx,
		`
			SELECT count(*)
			FROM verification_observations
			WHERE origin = $1
		`,
		fixture.source.String(),
	).Scan(&count)
	if err != nil {
		t.Fatalf(
			"count verification observations: %v",
			err,
		)
	}

	return count
}

func assertCompletionNotRecorded(
	t *testing.T,
	fixture completionFixture,
) {
	t.Helper()

	if got := completionObservationCount(
		t,
		fixture,
	); got != 0 {
		t.Errorf(
			"observation count = %d, want 0",
			got,
		)
	}
}

func completionOutcomeName(
	outcome declaration.Outcome,
) string {
	switch outcome {
	case declaration.OutcomeUnavailable:
		return "unavailable"
	case declaration.OutcomeRobotsDenied:
		return "robots denied"
	default:
		return "unexpected"
	}
}

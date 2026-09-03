package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
)

func TestManualScheduleCreatesRecurringWork(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	queue := newPhase9Queue(t, pool)

	source := mustStoreOrigin(t, "https://example.com")
	availableAt := queueTestTime()

	if err := queue.Schedule(
		ctx,
		source,
		availableAt,
	); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	assertQueueMode(
		t,
		ctx,
		pool,
		source.String(),
		"recurring",
	)
}

func TestManualSchedulePromotesProbeWithoutChangingLease(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)
	claimedAt := queueTestTime()
	queue := newFixedQueue(
		t,
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		claimedAt,
	)

	source := mustStoreOrigin(t, "https://example.com")
	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO verification_queue (
				origin,
				available_at,
				mode
			)
			VALUES ($1, $2, 'probe')
		`,
		source.String(),
		claimedAt,
	)
	if err != nil {
		t.Fatalf("insert probe: %v", err)
	}

	lease, found, err := queue.Claim(ctx, "worker-a")
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}

	if !found {
		t.Fatal("Claim() found = false, want true")
	}

	before := readPhase9LeaseState(
		t,
		ctx,
		pool,
		source.String(),
	)

	if err := queue.Schedule(
		ctx,
		source,
		claimedAt.Add(-time.Hour),
	); err != nil {
		t.Fatalf("Schedule() error = %v, want nil", err)
	}

	after := readPhase9LeaseState(
		t,
		ctx,
		pool,
		source.String(),
	)

	if after.mode != "recurring" {
		t.Errorf(
			"queue mode = %q, want recurring",
			after.mode,
		)
	}

	if !after.availableAt.Equal(before.availableAt) {
		t.Errorf(
			"available_at = %v, want %v",
			after.availableAt,
			before.availableAt,
		)
	}

	if after.generation != before.generation {
		t.Errorf(
			"generation = %d, want %d",
			after.generation,
			before.generation,
		)
	}

	if after.owner != before.owner {
		t.Errorf(
			"owner = %q, want %q",
			after.owner,
			before.owner,
		)
	}

	if !after.expiresAt.Equal(before.expiresAt) {
		t.Errorf(
			"expires_at = %v, want %v",
			after.expiresAt,
			before.expiresAt,
		)
	}

	if !after.claimedAt.Equal(before.claimedAt) {
		t.Errorf(
			"claimed_at = %v, want %v",
			after.claimedAt,
			before.claimedAt,
		)
	}

	if lease.Generation != after.generation {
		t.Errorf(
			"lease generation = %d, want %d",
			after.generation,
			lease.Generation,
		)
	}
}

func TestValidProbePromotesToRecurring(t *testing.T) {
	identities := []declaration.Identity{
		declaration.IdentityUndeclared,
		declaration.IdentityAffirmed,
		declaration.IdentityDeclined,
	}

	for _, identity := range identities {
		t.Run(
			phase9IdentityName(identity),
			func(t *testing.T) {
				fixture := newProbeCompletionFixture(t)
				completedAt := fixture.lease.ClaimedAt.Add(
					time.Minute,
				)
				fixture.queue.clock = fixedQueueClock{
					now: completedAt,
				}

				result := declaration.Result{
					Outcome: declaration.OutcomeValid,
					Origin:  fixture.source,
					Declaration: declaration.Declaration{
						Version:  1,
						Identity: identity,
					},
				}

				if err := fixture.queue.CompleteVerification(
					fixture.ctx,
					fixture.lease,
					result,
					24*time.Hour,
				); err != nil {
					t.Fatalf(
						"CompleteVerification() error = %v, want nil",
						err,
					)
				}

				assertQueueMode(
					t,
					fixture.ctx,
					fixture.pool,
					fixture.source.String(),
					"recurring",
				)

				state := readPhase9LeaseState(
					t,
					fixture.ctx,
					fixture.pool,
					fixture.source.String(),
				)

				if state.owner != "" {
					t.Errorf(
						"lease owner = %q, want empty",
						state.owner,
					)
				}

				if !state.expiresAt.IsZero() {
					t.Errorf(
						"lease expiration = %v, want zero",
						state.expiresAt,
					)
				}

				wantAvailableAt := completedAt.Add(
					24 * time.Hour,
				)
				if !state.availableAt.Equal(
					wantAvailableAt,
				) {
					t.Errorf(
						"available_at = %v, want %v",
						state.availableAt,
						wantAvailableAt,
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
			},
		)
	}
}

func TestEveryNonValidProbeEnds(t *testing.T) {
	outcomes := []declaration.Outcome{
		declaration.OutcomeAbsent,
		declaration.OutcomeInvalid,
		declaration.OutcomeUnsupportedVersion,
		declaration.OutcomeUnavailable,
		declaration.OutcomeRobotsDenied,
		declaration.OutcomeCrossOriginRedirect,
	}

	for _, outcome := range outcomes {
		t.Run(
			phase9OutcomeName(outcome),
			func(t *testing.T) {
				fixture := newProbeCompletionFixture(t)
				fixture.queue.clock = fixedQueueClock{
					now: fixture.lease.ClaimedAt.Add(
						time.Minute,
					),
				}

				result := declaration.Result{
					Outcome: outcome,
					Origin:  fixture.source,
				}

				if err := fixture.queue.CompleteVerification(
					fixture.ctx,
					fixture.lease,
					result,
					24*time.Hour,
				); err != nil {
					t.Fatalf(
						"CompleteVerification() error = %v, want nil",
						err,
					)
				}

				var queueCount int
				err := fixture.pool.QueryRow(
					fixture.ctx,
					`
						SELECT count(*)
						FROM verification_queue
						WHERE origin = $1
					`,
					fixture.source.String(),
				).Scan(&queueCount)
				if err != nil {
					t.Fatalf(
						"count queue rows: %v",
						err,
					)
				}

				if queueCount != 0 {
					t.Errorf(
						"queue row count = %d, want 0",
						queueCount,
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
			},
		)
	}
}

func TestFailedProbeCompletionIsAtomic(t *testing.T) {
	fixture := newProbeCompletionFixture(t)
	fixture.queue.clock = fixedQueueClock{
		now: fixture.lease.ClaimedAt.Add(time.Minute),
	}

	_, err := fixture.pool.Exec(
		fixture.ctx,
		`
			ALTER TABLE verification_observations
			ADD CONSTRAINT reject_probe_completion
			CHECK (outcome <> 'absent')
		`,
	)
	if err != nil {
		t.Fatalf(
			"add rejecting constraint: %v",
			err,
		)
	}

	err = fixture.queue.CompleteVerification(
		fixture.ctx,
		fixture.lease,
		declaration.Result{
			Outcome: declaration.OutcomeAbsent,
			Origin:  fixture.source,
		},
		24*time.Hour,
	)
	if err == nil {
		t.Fatal(
			"CompleteVerification() error = nil, want non-nil",
		)
	}

	state := readPhase9LeaseState(
		t,
		fixture.ctx,
		fixture.pool,
		fixture.source.String(),
	)

	if state.mode != "probe" {
		t.Errorf(
			"queue mode = %q, want probe",
			state.mode,
		)
	}

	if state.owner != fixture.lease.WorkerID {
		t.Errorf(
			"lease owner = %q, want %q",
			state.owner,
			fixture.lease.WorkerID,
		)
	}

	if state.generation != fixture.lease.Generation {
		t.Errorf(
			"lease generation = %d, want %d",
			state.generation,
			fixture.lease.Generation,
		)
	}

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

func TestRecurringCompletionStillRetainsEveryOutcome(
	t *testing.T,
) {
	outcomes := []declaration.Outcome{
		declaration.OutcomeValid,
		declaration.OutcomeAbsent,
		declaration.OutcomeInvalid,
		declaration.OutcomeUnsupportedVersion,
		declaration.OutcomeUnavailable,
		declaration.OutcomeRobotsDenied,
		declaration.OutcomeCrossOriginRedirect,
	}

	for _, outcome := range outcomes {
		t.Run(
			phase9OutcomeName(outcome),
			func(t *testing.T) {
				fixture := newCompletionFixture(
					t,
					QueueConfig{
						LeaseDuration:     10 * time.Minute,
						MinOriginInterval: time.Minute,
					},
					queueTestTime(),
				)
				fixture.queue.clock = fixedQueueClock{
					now: fixture.lease.ClaimedAt.Add(
						time.Minute,
					),
				}

				result := declaration.Result{
					Outcome: outcome,
					Origin:  fixture.source,
				}
				if outcome == declaration.OutcomeValid {
					result.Declaration =
						declaration.Declaration{
							Version:  1,
							Identity: declaration.IdentityUndeclared,
						}
				}

				if err := fixture.queue.CompleteVerification(
					fixture.ctx,
					fixture.lease,
					result,
					time.Hour,
				); err != nil {
					t.Fatalf(
						"CompleteVerification() error = %v, want nil",
						err,
					)
				}

				assertQueueMode(
					t,
					fixture.ctx,
					fixture.pool,
					fixture.source.String(),
					"recurring",
				)
			},
		)
	}
}

type phase9LeaseState struct {
	mode        string
	availableAt time.Time
	generation  int64
	owner       string
	expiresAt   time.Time
	claimedAt   time.Time
}

func newPhase9Queue(
	t *testing.T,
	pool *pgxpool.Pool,
) *Queue {
	t.Helper()

	queue, err := NewQueue(
		pool,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewQueue() error = %v, want nil",
			err,
		)
	}

	return queue
}

func newProbeCompletionFixture(
	t *testing.T,
) completionFixture {
	t.Helper()

	fixture := newCompletionFixture(
		t,
		QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Minute,
		},
		queueTestTime(),
	)

	_, err := fixture.pool.Exec(
		fixture.ctx,
		`
			UPDATE verification_queue
			SET mode = 'probe'
			WHERE origin = $1
		`,
		fixture.source.String(),
	)
	if err != nil {
		t.Fatalf(
			"set queue mode to probe: %v",
			err,
		)
	}

	return fixture
}

func assertQueueMode(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	storedOrigin string,
	want string,
) {
	t.Helper()

	var mode string
	err := pool.QueryRow(
		ctx,
		`
			SELECT mode
			FROM verification_queue
			WHERE origin = $1
		`,
		storedOrigin,
	).Scan(&mode)
	if err != nil {
		t.Fatalf(
			"query queue mode: %v",
			err,
		)
	}

	if mode != want {
		t.Errorf(
			"queue mode = %q, want %q",
			mode,
			want,
		)
	}
}

func readPhase9LeaseState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	storedOrigin string,
) phase9LeaseState {
	t.Helper()

	var (
		state     phase9LeaseState
		owner     *string
		expiresAt *time.Time
		claimedAt *time.Time
	)

	err := pool.QueryRow(
		ctx,
		`
			SELECT
				mode,
				available_at,
				lease_generation,
				lease_owner,
				lease_expires_at,
				last_claimed_at
			FROM verification_queue
			WHERE origin = $1
		`,
		storedOrigin,
	).Scan(
		&state.mode,
		&state.availableAt,
		&state.generation,
		&owner,
		&expiresAt,
		&claimedAt,
	)
	if err != nil {
		t.Fatalf(
			"query queue lease state: %v",
			err,
		)
	}

	if owner != nil {
		state.owner = *owner
	}

	if expiresAt != nil {
		state.expiresAt = expiresAt.UTC()
	}

	if claimedAt != nil {
		state.claimedAt = claimedAt.UTC()
	}

	state.availableAt = state.availableAt.UTC()

	return state
}

func phase9IdentityName(
	identity declaration.Identity,
) string {
	switch identity {
	case declaration.IdentityUndeclared:
		return "undeclared"
	case declaration.IdentityAffirmed:
		return "affirmed"
	case declaration.IdentityDeclined:
		return "declined"
	default:
		return "unknown"
	}
}

func phase9OutcomeName(
	outcome declaration.Outcome,
) string {
	switch outcome {
	case declaration.OutcomeValid:
		return "valid"
	case declaration.OutcomeAbsent:
		return "absent"
	case declaration.OutcomeInvalid:
		return "invalid"
	case declaration.OutcomeUnsupportedVersion:
		return "unsupported_version"
	case declaration.OutcomeUnavailable:
		return "unavailable"
	case declaration.OutcomeRobotsDenied:
		return "robots_denied"
	case declaration.OutcomeCrossOriginRedirect:
		return "cross_origin_redirect"
	default:
		return "unknown"
	}
}

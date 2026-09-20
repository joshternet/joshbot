package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/retry"
)

type discoveryLeaseIntegrationState struct {
	generation          int64
	lastAttemptedAt     *time.Time
	lastClaimedAt       *time.Time
	leaseExpiresAt      *time.Time
	consecutiveFailures int
	lastFailureCategory *string
	nextAttemptAt       *time.Time
}

func TestDiscoverySourceLeaseCrashRecovery(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	start := queueTestTime()
	interval := 168 * time.Hour
	leaseDuration := 5 * time.Minute

	firstClaimAt := start
	beforeExpiry := firstClaimAt.
		Add(leaseDuration).
		Add(-time.Microsecond)
	reclaimedAt := firstClaimAt.Add(
		leaseDuration,
	)
	staleCompletionAt := reclaimedAt.Add(
		time.Minute,
	)
	completedAt := reclaimedAt.Add(
		2 * time.Minute,
	)
	beforeInterval := completedAt.
		Add(interval).
		Add(-time.Microsecond)
	dueAt := completedAt.Add(interval)

	source := seedDiscoveryTestSource(
		t,
		pool,
		"https://example.com",
		start,
	)

	discoveryStore := newDiscoveryTestStore(
		t,
		pool,
		firstClaimAt,
		beforeExpiry,
		reclaimedAt,
		staleCompletionAt,
		completedAt,
		beforeInterval,
		dueAt,
	)

	firstLease, found, err :=
		discoveryStore.ClaimDiscoverySourceLease(
			ctx,
			interval,
			leaseDuration,
		)
	if err != nil {
		t.Fatalf(
			"first ClaimDiscoverySourceLease() error = %v",
			err,
		)
	}
	if !found {
		t.Fatal(
			"first ClaimDiscoverySourceLease() found = false, want true",
		)
	}
	if firstLease.Origin != source {
		t.Fatalf(
			"first lease origin = %q, want %q",
			firstLease.Origin,
			source,
		)
	}
	if firstLease.Generation != 1 {
		t.Fatalf(
			"first lease generation = %d, want 1",
			firstLease.Generation,
		)
	}
	if !firstLease.ClaimedAt.Equal(
		firstClaimAt,
	) {
		t.Fatalf(
			"first lease claimed at = %v, want %v",
			firstLease.ClaimedAt,
			firstClaimAt,
		)
	}

	wantFirstExpiry := firstClaimAt.Add(
		leaseDuration,
	)
	if !firstLease.ExpiresAt.Equal(
		wantFirstExpiry,
	) {
		t.Fatalf(
			"first lease expiration = %v, want %v",
			firstLease.ExpiresAt,
			wantFirstExpiry,
		)
	}

	firstState := readDiscoveryLeaseIntegrationState(
		t,
		ctx,
		pool,
		source.String(),
	)

	if firstState.generation != 1 {
		t.Fatalf(
			"stored first generation = %d, want 1",
			firstState.generation,
		)
	}
	if firstState.lastAttemptedAt != nil {
		t.Fatalf(
			"last_attempted_at after claim = %v, want NULL",
			firstState.lastAttemptedAt,
		)
	}
	assertDiscoveryLeaseIntegrationTime(
		t,
		"last_claimed_at after first claim",
		firstState.lastClaimedAt,
		firstClaimAt,
	)
	assertDiscoveryLeaseIntegrationTime(
		t,
		"lease_expires_at after first claim",
		firstState.leaseExpiresAt,
		wantFirstExpiry,
	)

	blockedLease, found, err :=
		discoveryStore.ClaimDiscoverySourceLease(
			ctx,
			interval,
			leaseDuration,
		)
	if err != nil {
		t.Fatalf(
			"active-lease ClaimDiscoverySourceLease() error = %v",
			err,
		)
	}
	if found {
		t.Fatalf(
			"active-lease claim = %#v, true, want zero, false",
			blockedLease,
		)
	}
	if blockedLease != (firstLeaseZero()) {
		t.Fatalf(
			"active-lease claim = %#v, want zero",
			blockedLease,
		)
	}

	stateBeforeReclaim :=
		readDiscoveryLeaseIntegrationState(
			t,
			ctx,
			pool,
			source.String(),
		)
	if !discoveryLeaseIntegrationStatesEqual(
		stateBeforeReclaim,
		firstState,
	) {
		t.Fatalf(
			"state changed while active lease blocked reclaim:\n got: %#v\nwant: %#v",
			stateBeforeReclaim,
			firstState,
		)
	}

	reclaimedLease, found, err :=
		discoveryStore.ClaimDiscoverySourceLease(
			ctx,
			interval,
			leaseDuration,
		)
	if err != nil {
		t.Fatalf(
			"expired-lease ClaimDiscoverySourceLease() error = %v",
			err,
		)
	}
	if !found {
		t.Fatal(
			"expired-lease ClaimDiscoverySourceLease() found = false, want true",
		)
	}
	if reclaimedLease.Origin != source {
		t.Fatalf(
			"reclaimed origin = %q, want %q",
			reclaimedLease.Origin,
			source,
		)
	}
	if reclaimedLease.Generation != 2 {
		t.Fatalf(
			"reclaimed generation = %d, want 2",
			reclaimedLease.Generation,
		)
	}
	if !reclaimedLease.ClaimedAt.Equal(
		reclaimedAt,
	) {
		t.Fatalf(
			"reclaimed at = %v, want %v",
			reclaimedLease.ClaimedAt,
			reclaimedAt,
		)
	}

	wantReclaimedExpiry := reclaimedAt.Add(
		leaseDuration,
	)
	if !reclaimedLease.ExpiresAt.Equal(
		wantReclaimedExpiry,
	) {
		t.Fatalf(
			"reclaimed expiration = %v, want %v",
			reclaimedLease.ExpiresAt,
			wantReclaimedExpiry,
		)
	}

	reclaimedState :=
		readDiscoveryLeaseIntegrationState(
			t,
			ctx,
			pool,
			source.String(),
		)

	if reclaimedState.generation != 2 {
		t.Fatalf(
			"stored reclaimed generation = %d, want 2",
			reclaimedState.generation,
		)
	}
	if reclaimedState.lastAttemptedAt != nil {
		t.Fatalf(
			"last_attempted_at after reclaim = %v, want NULL",
			reclaimedState.lastAttemptedAt,
		)
	}
	assertDiscoveryLeaseIntegrationTime(
		t,
		"last_claimed_at after reclaim",
		reclaimedState.lastClaimedAt,
		reclaimedAt,
	)
	assertDiscoveryLeaseIntegrationTime(
		t,
		"lease_expires_at after reclaim",
		reclaimedState.leaseExpiresAt,
		wantReclaimedExpiry,
	)

	err = discoveryStore.
		CompleteDiscoverySourceLeaseRetry(
			ctx,
			firstLease,
			retry.CategoryNone,
			0,
		)
	if !errors.Is(
		err,
		ErrDiscoverySourceLeaseLost,
	) {
		t.Fatalf(
			"stale completion error = %v, want %v",
			err,
			ErrDiscoverySourceLeaseLost,
		)
	}

	afterStaleCompletion :=
		readDiscoveryLeaseIntegrationState(
			t,
			ctx,
			pool,
			source.String(),
		)

	if !discoveryLeaseIntegrationStatesEqual(
		afterStaleCompletion,
		reclaimedState,
	) {
		t.Fatalf(
			"stale completion changed state:\n got: %#v\nwant: %#v",
			afterStaleCompletion,
			reclaimedState,
		)
	}

	err = discoveryStore.
		CompleteDiscoverySourceLeaseRetry(
			ctx,
			reclaimedLease,
			retry.CategoryNone,
			0,
		)
	if err != nil {
		t.Fatalf(
			"current completion error = %v",
			err,
		)
	}

	completedState :=
		readDiscoveryLeaseIntegrationState(
			t,
			ctx,
			pool,
			source.String(),
		)

	if completedState.generation != 2 {
		t.Fatalf(
			"generation after completion = %d, want 2",
			completedState.generation,
		)
	}
	assertDiscoveryLeaseIntegrationTime(
		t,
		"last_attempted_at after completion",
		completedState.lastAttemptedAt,
		completedAt,
	)
	assertDiscoveryLeaseIntegrationTime(
		t,
		"last_claimed_at after completion",
		completedState.lastClaimedAt,
		reclaimedAt,
	)
	if completedState.leaseExpiresAt != nil {
		t.Fatalf(
			"lease_expires_at after completion = %v, want NULL",
			completedState.leaseExpiresAt,
		)
	}
	if completedState.consecutiveFailures != 0 {
		t.Fatalf(
			"consecutive_failures after completion = %d, want 0",
			completedState.consecutiveFailures,
		)
	}
	if completedState.lastFailureCategory != nil {
		t.Fatalf(
			"last_failure_category after completion = %v, want NULL",
			completedState.lastFailureCategory,
		)
	}
	if completedState.nextAttemptAt != nil {
		t.Fatalf(
			"next_attempt_at after completion = %v, want NULL",
			completedState.nextAttemptAt,
		)
	}

	earlyLease, found, err :=
		discoveryStore.ClaimDiscoverySourceLease(
			ctx,
			interval,
			leaseDuration,
		)
	if err != nil {
		t.Fatalf(
			"pre-interval ClaimDiscoverySourceLease() error = %v",
			err,
		)
	}
	if found {
		t.Fatalf(
			"pre-interval claim = %#v, true, want zero, false",
			earlyLease,
		)
	}

	dueLease, found, err :=
		discoveryStore.ClaimDiscoverySourceLease(
			ctx,
			interval,
			leaseDuration,
		)
	if err != nil {
		t.Fatalf(
			"due ClaimDiscoverySourceLease() error = %v",
			err,
		)
	}
	if !found {
		t.Fatal(
			"due ClaimDiscoverySourceLease() found = false, want true",
		)
	}
	if dueLease.Origin != source {
		t.Fatalf(
			"due lease origin = %q, want %q",
			dueLease.Origin,
			source,
		)
	}
	if dueLease.Generation != 3 {
		t.Fatalf(
			"due lease generation = %d, want 3",
			dueLease.Generation,
		)
	}
	if !dueLease.ClaimedAt.Equal(dueAt) {
		t.Fatalf(
			"due lease claimed at = %v, want %v",
			dueLease.ClaimedAt,
			dueAt,
		)
	}

	finalState :=
		readDiscoveryLeaseIntegrationState(
			t,
			ctx,
			pool,
			source.String(),
		)

	assertDiscoveryLeaseIntegrationTime(
		t,
		"last_attempted_at after next claim",
		finalState.lastAttemptedAt,
		completedAt,
	)
}

func TestDiscoverySourceLeaseRenewAndStaleRenew(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	start := queueTestTime()
	interval := 168 * time.Hour
	leaseDuration := 5 * time.Minute
	renewDuration := 10 * time.Minute

	firstClaimAt := start
	renewAt := firstClaimAt.Add(time.Minute)
	beforeExpiry := renewAt.
		Add(renewDuration).
		Add(-time.Microsecond)
	reclaimedAt := renewAt.Add(renewDuration)
	staleRenewAt := reclaimedAt.Add(time.Minute)
	currentRenewAt := staleRenewAt.Add(time.Minute)

	source := seedDiscoveryTestSource(
		t,
		pool,
		"https://renew.example.com",
		start,
	)

	discoveryStore := newDiscoveryTestStore(
		t,
		pool,
		firstClaimAt,
		renewAt,
		beforeExpiry,
		reclaimedAt,
		staleRenewAt,
		currentRenewAt,
	)

	firstLease, found, err :=
		discoveryStore.ClaimDiscoverySourceLease(
			ctx,
			interval,
			leaseDuration,
		)
	if err != nil {
		t.Fatalf(
			"ClaimDiscoverySourceLease() error = %v",
			err,
		)
	}
	if !found {
		t.Fatal(
			"ClaimDiscoverySourceLease() found = false, want true",
		)
	}
	if firstLease.Generation != 1 {
		t.Fatalf(
			"first lease generation = %d, want 1",
			firstLease.Generation,
		)
	}

	renewedLease, err :=
		discoveryStore.RenewDiscoverySourceLease(
			ctx,
			firstLease,
			renewDuration,
		)
	if err != nil {
		t.Fatalf(
			"RenewDiscoverySourceLease() error = %v",
			err,
		)
	}
	if renewedLease.Origin != source {
		t.Fatalf(
			"renewed origin = %q, want %q",
			renewedLease.Origin,
			source,
		)
	}
	if renewedLease.Generation != 1 {
		t.Fatalf(
			"renewed generation = %d, want 1",
			renewedLease.Generation,
		)
	}
	if !renewedLease.ClaimedAt.Equal(
		firstClaimAt,
	) {
		t.Fatalf(
			"renewed claimed at = %v, want %v",
			renewedLease.ClaimedAt,
			firstClaimAt,
		)
	}

	wantRenewedExpiry := renewAt.Add(
		renewDuration,
	)
	if !renewedLease.ExpiresAt.Equal(
		wantRenewedExpiry,
	) {
		t.Fatalf(
			"renewed expiration = %v, want %v",
			renewedLease.ExpiresAt,
			wantRenewedExpiry,
		)
	}

	renewedState :=
		readDiscoveryLeaseIntegrationState(
			t,
			ctx,
			pool,
			source.String(),
		)
	if renewedState.lastAttemptedAt != nil {
		t.Fatalf(
			"last_attempted_at after renew = %v, want NULL",
			renewedState.lastAttemptedAt,
		)
	}
	assertDiscoveryLeaseIntegrationTime(
		t,
		"last_claimed_at after renew",
		renewedState.lastClaimedAt,
		firstClaimAt,
	)
	assertDiscoveryLeaseIntegrationTime(
		t,
		"lease_expires_at after renew",
		renewedState.leaseExpiresAt,
		wantRenewedExpiry,
	)

	blockedLease, found, err :=
		discoveryStore.ClaimDiscoverySourceLease(
			ctx,
			interval,
			leaseDuration,
		)
	if err != nil {
		t.Fatalf(
			"active-lease ClaimDiscoverySourceLease() error = %v",
			err,
		)
	}
	if found {
		t.Fatalf(
			"active-lease claim = %#v, true, want zero, false",
			blockedLease,
		)
	}

	reclaimedLease, found, err :=
		discoveryStore.ClaimDiscoverySourceLease(
			ctx,
			interval,
			leaseDuration,
		)
	if err != nil {
		t.Fatalf(
			"expired-lease ClaimDiscoverySourceLease() error = %v",
			err,
		)
	}
	if !found {
		t.Fatal(
			"expired-lease ClaimDiscoverySourceLease() found = false, want true",
		)
	}
	if reclaimedLease.Generation != 2 {
		t.Fatalf(
			"reclaimed generation = %d, want 2",
			reclaimedLease.Generation,
		)
	}

	reclaimedState :=
		readDiscoveryLeaseIntegrationState(
			t,
			ctx,
			pool,
			source.String(),
		)

	_, err = discoveryStore.
		RenewDiscoverySourceLease(
			ctx,
			firstLease,
			renewDuration,
		)
	if !errors.Is(
		err,
		ErrDiscoverySourceLeaseLost,
	) {
		t.Fatalf(
			"stale renew error = %v, want %v",
			err,
			ErrDiscoverySourceLeaseLost,
		)
	}

	afterStaleRenew :=
		readDiscoveryLeaseIntegrationState(
			t,
			ctx,
			pool,
			source.String(),
		)
	if !discoveryLeaseIntegrationStatesEqual(
		afterStaleRenew,
		reclaimedState,
	) {
		t.Fatalf(
			"stale renew changed state:\n got: %#v\nwant: %#v",
			afterStaleRenew,
			reclaimedState,
		)
	}

	currentRenew, err :=
		discoveryStore.RenewDiscoverySourceLease(
			ctx,
			reclaimedLease,
			renewDuration,
		)
	if err != nil {
		t.Fatalf(
			"current RenewDiscoverySourceLease() error = %v",
			err,
		)
	}
	if currentRenew.Generation != 2 {
		t.Fatalf(
			"current renew generation = %d, want 2",
			currentRenew.Generation,
		)
	}
}

func firstLeaseZero() discovery.CrawlSourceLease {
	return discovery.CrawlSourceLease{}
}

func readDiscoveryLeaseIntegrationState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	source string,
) discoveryLeaseIntegrationState {
	t.Helper()

	var state discoveryLeaseIntegrationState

	err := pool.QueryRow(
		ctx,
		`
			SELECT
				lease_generation,
				last_attempted_at,
				last_claimed_at,
				lease_expires_at,
				consecutive_failures,
				last_failure_category,
				next_attempt_at
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		source,
	).Scan(
		&state.generation,
		&state.lastAttemptedAt,
		&state.lastClaimedAt,
		&state.leaseExpiresAt,
		&state.consecutiveFailures,
		&state.lastFailureCategory,
		&state.nextAttemptAt,
	)
	if err != nil {
		t.Fatalf(
			"read discovery lease state: %v",
			err,
		)
	}

	return state
}

func assertDiscoveryLeaseIntegrationTime(
	t *testing.T,
	label string,
	got *time.Time,
	want time.Time,
) {
	t.Helper()

	if got == nil {
		t.Fatalf(
			"%s = NULL, want %v",
			label,
			want,
		)
	}

	if !got.Equal(want) {
		t.Fatalf(
			"%s = %v, want %v",
			label,
			*got,
			want,
		)
	}
}

func discoveryLeaseIntegrationStatesEqual(
	left discoveryLeaseIntegrationState,
	right discoveryLeaseIntegrationState,
) bool {
	return left.generation == right.generation &&
		discoveryLeaseIntegrationTimesEqual(
			left.lastAttemptedAt,
			right.lastAttemptedAt,
		) &&
		discoveryLeaseIntegrationTimesEqual(
			left.lastClaimedAt,
			right.lastClaimedAt,
		) &&
		discoveryLeaseIntegrationTimesEqual(
			left.leaseExpiresAt,
			right.leaseExpiresAt,
		) &&
		left.consecutiveFailures ==
			right.consecutiveFailures &&
		discoveryLeaseIntegrationStringsEqual(
			left.lastFailureCategory,
			right.lastFailureCategory,
		) &&
		discoveryLeaseIntegrationTimesEqual(
			left.nextAttemptAt,
			right.nextAttemptAt,
		)
}

func discoveryLeaseIntegrationTimesEqual(
	left *time.Time,
	right *time.Time,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return left.Equal(*right)
}

func discoveryLeaseIntegrationStringsEqual(
	left *string,
	right *string,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return *left == *right
}

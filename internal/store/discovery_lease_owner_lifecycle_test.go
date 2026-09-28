package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/retry"
)

type discoveryLeaseOwnerState struct {
	generation    int64
	owner         *string
	lastClaimedAt *time.Time
	expiresAt     *time.Time
}

func TestDiscoverySourceLeaseOwnerLifecycle(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	start := queueTestTime()
	interval := 168 * time.Hour
	leaseDuration := 5 * time.Minute
	renewDuration := 5 * time.Minute

	firstClaimAt := start
	renewAt := firstClaimAt.Add(
		2 * time.Minute,
	)
	reclaimedAt := renewAt.Add(
		renewDuration,
	)
	staleCompletionAt := reclaimedAt.Add(
		time.Minute,
	)
	completedAt := reclaimedAt.Add(
		2 * time.Minute,
	)

	source := seedDiscoveryTestSource(
		t,
		pool,
		"https://lease-owner.example.com",
		start,
	)

	discoveryStore := newDiscoveryTestStore(
		t,
		pool,
		firstClaimAt,
		renewAt,
		reclaimedAt,
		staleCompletionAt,
		completedAt,
	)

	firstLease, found, err :=
		discoveryStore.ClaimDiscoverySourceLease(
			ctx,
			"discovery-worker-a",
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

	firstState := readDiscoveryLeaseOwnerState(
		t,
		ctx,
		pool,
		source.String(),
	)

	if firstState.generation != 1 {
		t.Fatalf(
			"first generation = %d, want 1",
			firstState.generation,
		)
	}

	assertDiscoveryLeaseOwner(
		t,
		"first claim",
		firstState.owner,
		"discovery-worker-a",
	)

	assertDiscoveryLeaseOwnerTime(
		t,
		"first last_claimed_at",
		firstState.lastClaimedAt,
		firstClaimAt,
	)

	assertDiscoveryLeaseOwnerTime(
		t,
		"first lease_expires_at",
		firstState.expiresAt,
		firstClaimAt.Add(leaseDuration),
	)

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

	if renewedLease.Generation != firstLease.Generation {
		t.Fatalf(
			"renewed generation = %d, want %d",
			renewedLease.Generation,
			firstLease.Generation,
		)
	}

	renewedState := readDiscoveryLeaseOwnerState(
		t,
		ctx,
		pool,
		source.String(),
	)

	assertDiscoveryLeaseOwner(
		t,
		"renewal",
		renewedState.owner,
		"discovery-worker-a",
	)

	assertDiscoveryLeaseOwnerTime(
		t,
		"renewed last_claimed_at",
		renewedState.lastClaimedAt,
		firstClaimAt,
	)

	assertDiscoveryLeaseOwnerTime(
		t,
		"renewed lease_expires_at",
		renewedState.expiresAt,
		reclaimedAt,
	)

	reclaimedLease, found, err :=
		discoveryStore.ClaimDiscoverySourceLease(
			ctx,
			"discovery-worker-b",
			interval,
			leaseDuration,
		)
	if err != nil {
		t.Fatalf(
			"reclaimed ClaimDiscoverySourceLease() error = %v",
			err,
		)
	}

	if !found {
		t.Fatal(
			"reclaimed ClaimDiscoverySourceLease() found = false, want true",
		)
	}

	if reclaimedLease.Generation != 2 {
		t.Fatalf(
			"reclaimed generation = %d, want 2",
			reclaimedLease.Generation,
		)
	}

	reclaimedState := readDiscoveryLeaseOwnerState(
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

	assertDiscoveryLeaseOwner(
		t,
		"reclaim",
		reclaimedState.owner,
		"discovery-worker-b",
	)

	assertDiscoveryLeaseOwnerTime(
		t,
		"reclaimed last_claimed_at",
		reclaimedState.lastClaimedAt,
		reclaimedAt,
	)

	assertDiscoveryLeaseOwnerTime(
		t,
		"reclaimed lease_expires_at",
		reclaimedState.expiresAt,
		reclaimedAt.Add(leaseDuration),
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
		readDiscoveryLeaseOwnerState(
			t,
			ctx,
			pool,
			source.String(),
		)

	if afterStaleCompletion.generation != 2 {
		t.Fatalf(
			"generation after stale completion = %d, want 2",
			afterStaleCompletion.generation,
		)
	}

	assertDiscoveryLeaseOwner(
		t,
		"stale completion",
		afterStaleCompletion.owner,
		"discovery-worker-b",
	)

	assertDiscoveryLeaseOwnerTime(
		t,
		"lease_expires_at after stale completion",
		afterStaleCompletion.expiresAt,
		reclaimedAt.Add(leaseDuration),
	)

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

	completedState := readDiscoveryLeaseOwnerState(
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

	if completedState.owner != nil {
		t.Fatalf(
			"lease owner after completion = %q, want NULL",
			*completedState.owner,
		)
	}

	if completedState.expiresAt != nil {
		t.Fatalf(
			"lease_expires_at after completion = %v, want NULL",
			completedState.expiresAt,
		)
	}
}

func readDiscoveryLeaseOwnerState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	source string,
) discoveryLeaseOwnerState {
	t.Helper()

	var state discoveryLeaseOwnerState

	err := pool.QueryRow(
		ctx,
		`
			SELECT
				lease_generation,
				lease_owner,
				last_claimed_at,
				lease_expires_at
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		source,
	).Scan(
		&state.generation,
		&state.owner,
		&state.lastClaimedAt,
		&state.expiresAt,
	)
	if err != nil {
		t.Fatalf(
			"read discovery lease owner state: %v",
			err,
		)
	}

	return state
}

func assertDiscoveryLeaseOwner(
	t *testing.T,
	label string,
	got *string,
	want string,
) {
	t.Helper()

	if got == nil {
		t.Fatalf(
			"%s lease owner = NULL, want %q",
			label,
			want,
		)
	}

	if *got != want {
		t.Fatalf(
			"%s lease owner = %q, want %q",
			label,
			*got,
			want,
		)
	}
}

func assertDiscoveryLeaseOwnerTime(
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

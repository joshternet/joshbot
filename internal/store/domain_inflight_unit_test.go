package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/retry"
)

func TestDomainInflightKeyWithoutNeighborhood(
	t *testing.T,
) {
	source := mustStoreOrigin(t, "https://192.0.2.1")
	got := domainInflightKey(source)
	if got != source.String() {
		t.Fatalf(
			"domainInflightKey() = %q, want %q",
			got,
			source.String(),
		)
	}
}

func TestAcquireDomainInflightWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := queueTestTime()
	source := mustStoreOrigin(t, "https://example.com")
	expiresAt := now.Add(5 * time.Minute)

	t.Run("busy", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{true}},
			},
		}

		acquired, err := acquireDomainInflight(
			ctx,
			tx,
			"example.com",
			source,
			"worker-a",
			1,
			now,
			expiresAt,
		)
		if err != nil {
			t.Fatalf("acquireDomainInflight() error = %v", err)
		}
		if acquired {
			t.Fatal("acquireDomainInflight() acquired = true, want false")
		}
	})

	t.Run("database failure", func(t *testing.T) {
		testErr := errors.New("test acquire failure")
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{err: testErr},
			},
		}

		acquired, err := acquireDomainInflight(
			ctx,
			tx,
			"example.com",
			source,
			"worker-a",
			1,
			now,
			expiresAt,
		)
		if acquired {
			t.Fatal("acquireDomainInflight() acquired = true, want false")
		}
		if !errors.Is(err, testErr) ||
			!strings.Contains(
				err.Error(),
				"store: read durable domain lease",
			) {
			t.Fatalf("acquireDomainInflight() error = %v", err)
		}
	})
}

func TestExtendDomainInflightWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := queueTestTime()
	source := mustStoreOrigin(t, "https://example.com")

	t.Run("database failure", func(t *testing.T) {
		testErr := errors.New("test extend failure")
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{
				{err: testErr},
			},
		}

		err := extendDomainInflight(
			ctx,
			tx,
			"example.com",
			source,
			1,
			now.Add(time.Minute),
		)
		if !errors.Is(err, testErr) ||
			!strings.Contains(
				err.Error(),
				"store: extend domain inflight lease",
			) {
			t.Fatalf("extendDomainInflight() error = %v", err)
		}
	})

	t.Run("lost", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{
				{
					tag: pgconn.NewCommandTag("UPDATE 0"),
				},
			},
		}

		err := extendDomainInflight(
			ctx,
			tx,
			"example.com",
			source,
			1,
			now.Add(time.Minute),
		)
		if !errors.Is(err, errDomainInflightLost) {
			t.Fatalf(
				"extendDomainInflight() error = %v, want %v",
				err,
				errDomainInflightLost,
			)
		}
	})
}

func TestReleaseDomainInflightWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	source := mustStoreOrigin(t, "https://example.com")
	testErr := errors.New("test release failure")
	tx := &discoveryPolicyUnitTx{
		execResults: []discoveryUnitExecResult{
			{err: testErr},
		},
	}

	err := releaseDomainInflight(
		ctx,
		tx,
		"example.com",
		source,
		1,
	)
	if !errors.Is(err, testErr) ||
		!strings.Contains(
			err.Error(),
			"store: release domain inflight lease",
		) {
		t.Fatalf("releaseDomainInflight() error = %v", err)
	}
}

func TestQueueClaimDomainInflightBranchesWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := queueTestTime()
	workerID := "worker-a"
	expiresAt := now.Add(10 * time.Minute)

	t.Run("skips busy neighborhood then claims other", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				// host-a durable lease active
				{values: []any{true}},
				// solo reserve + lock + upsert + bind
				{values: []any{false}},
				{values: []any{false}},
				{
					values: []any{
						"https://solo.other.test",
						int64(0),
					},
				},
				{values: []any{"other.test"}},
				{
					values: []any{
						workerID,
						int64(1),
						now,
						expiresAt,
					},
				},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://host-a.example.com",
									int64(0),
									now,
									2,
								},
							},
							{
								values: []any{
									"https://host-b.example.com",
									int64(0),
									now,
									2,
								},
							},
							{
								values: []any{
									"https://solo.other.test",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
			},
		}

		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)

		lease, found, err := queue.Claim(ctx, workerID)
		if err != nil {
			t.Fatalf("Claim() error = %v", err)
		}
		if !found {
			t.Fatal("Claim() found = false, want true")
		}
		if lease.Origin.String() != "https://solo.other.test" {
			t.Fatalf(
				"Claim() origin = %s, want https://solo.other.test",
				lease.Origin,
			)
		}
	})

	t.Run("acquire failure", func(t *testing.T) {
		testErr := errors.New("test domain acquire failure")
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{err: testErr},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
			},
		}

		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)

		_, found, err := queue.Claim(ctx, workerID)
		if found {
			t.Fatal("Claim() found = true, want false")
		}
		if !errors.Is(err, testErr) ||
			!strings.Contains(
				err.Error(),
				"store: claim queue work",
			) {
			t.Fatalf("Claim() error = %v", err)
		}
	})

	t.Run("update failure after acquire", func(t *testing.T) {
		testErr := errors.New("test queue update failure")
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{values: []any{false}},
				{values: []any{false}},
				{
					values: []any{
						"https://example.com",
						int64(0),
					},
				},
				{values: []any{"example.com"}},
				{err: testErr},
			},
			queryResults: []discoveryUnitQueryResult{
				{
					rows: &discoveryUnitRows{
						rows: []discoveryUnitRow{
							{
								values: []any{
									"https://example.com",
									int64(0),
									now,
									2,
								},
							},
						},
					},
				},
			},
		}

		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)

		_, found, err := queue.Claim(ctx, workerID)
		if found {
			t.Fatal("Claim() found = true, want false")
		}
		if !errors.Is(err, testErr) ||
			!strings.Contains(
				err.Error(),
				"store: claim queue work",
			) {
			t.Fatalf("Claim() error = %v", err)
		}
	})
}

func TestQueueRenewDomainInflightBranchesWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := queueTestTime()
	lease := queueUnitLease(
		t,
		now.Add(-time.Minute),
		now.Add(4*time.Minute),
	)

	t.Run("domain lost", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{
					values: []any{
						lease.WorkerID,
						lease.Generation,
						lease.ClaimedAt,
						now.Add(5 * time.Minute),
					},
				},
			},
			execResults: []discoveryUnitExecResult{
				{
					tag: pgconn.NewCommandTag("UPDATE 0"),
				},
			},
		}

		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)

		_, err := queue.Renew(ctx, lease)
		if !errors.Is(err, ErrLeaseLost) {
			t.Fatalf(
				"Renew() error = %v, want %v",
				err,
				ErrLeaseLost,
			)
		}
	})

	t.Run("domain extend failure", func(t *testing.T) {
		testErr := errors.New("test renew domain failure")
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{
					values: []any{
						lease.WorkerID,
						lease.Generation,
						lease.ClaimedAt,
						now.Add(5 * time.Minute),
					},
				},
			},
			execResults: []discoveryUnitExecResult{
				{err: testErr},
			},
		}

		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)

		_, err := queue.Renew(ctx, lease)
		if !errors.Is(err, testErr) ||
			!strings.Contains(
				err.Error(),
				"store: renew queue lease",
			) {
			t.Fatalf("Renew() error = %v", err)
		}
	})
}

func TestClaimDiscoveryDomainInflightBranchesWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := queueTestTime()
	interval := 15 * time.Minute
	leaseDuration := 5 * time.Minute
	expiresAt := now.Add(leaseDuration)

	t.Run("skips busy neighborhood then claims other", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			// advisory lock, then two crawl_runs cleanups after claim.
			execResults: []discoveryUnitExecResult{
				{},
				{},
				{},
			},
			rowResults: []discoveryUnitRow{
				{values: []any{false, false}},
				// identify host-a
				{
					values: []any{
						"https://host-a.example.com",
						false,
						int64(0),
					},
				},
				// durable neighborhood busy
				{values: []any{true}},
				// identify host-b (skipped via domain map after busy)
				{
					values: []any{
						"https://host-b.example.com",
						false,
						int64(0),
					},
				},
				// identify solo
				{
					values: []any{
						"https://solo.other.test",
						false,
						int64(0),
					},
				},
				{values: []any{false}},
				{values: []any{false}},
				// lock solo
				{
					values: []any{
						"https://solo.other.test",
						false,
						int64(0),
					},
				},
				{values: []any{"other.test"}},
				{
					values: []any{
						"https://solo.other.test",
						int64(1),
						now,
						expiresAt,
					},
				},
			},
		}

		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)

		lease, found, err := store.ClaimDiscoverySourceLease(
			ctx,
			testDiscoveryLeaseOwner,
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
		if lease.Origin.String() != "https://solo.other.test" {
			t.Fatalf(
				"ClaimDiscoverySourceLease() origin = %s, want https://solo.other.test",
				lease.Origin,
			)
		}
	})

	t.Run("acquire failure", func(t *testing.T) {
		testErr := errors.New("test discovery domain acquire failure")
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{{}},
			rowResults: []discoveryUnitRow{
				{values: []any{false, false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(0),
					},
				},
				{err: testErr},
			},
		}

		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)

		_, found, err := store.ClaimDiscoverySourceLease(
			ctx,
			testDiscoveryLeaseOwner,
			interval,
			leaseDuration,
		)
		if found {
			t.Fatal(
				"ClaimDiscoverySourceLease() found = true, want false",
			)
		}
		if !errors.Is(err, testErr) ||
			!strings.Contains(
				err.Error(),
				"store: claim discovery source lease",
			) {
			t.Fatalf(
				"ClaimDiscoverySourceLease() error = %v",
				err,
			)
		}
	})

	t.Run("upsert race releases domain", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{
				{},
			},
			rowResults: []discoveryUnitRow{
				{values: []any{false, false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(0),
					},
				},
				{values: []any{false}},
				{values: []any{false}},
				// lock revalidation
				{
					values: []any{
						"https://example.com",
						false,
						int64(0),
					},
				},
				{values: []any{"example.com"}},
				{err: pgx.ErrNoRows},
				// next identify empty
				{err: pgx.ErrNoRows},
			},
		}

		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)

		lease, found, err := store.ClaimDiscoverySourceLease(
			ctx,
			testDiscoveryLeaseOwner,
			interval,
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
				"ClaimDiscoverySourceLease() found %#v",
				lease,
			)
		}
	})

	t.Run("upsert race rolls back via savepoint", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{
				{},
			},
			rowResults: []discoveryUnitRow{
				{values: []any{false, false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(0),
					},
				},
				{values: []any{false}},
				{values: []any{false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(0),
					},
				},
				{values: []any{"example.com"}},
				{err: pgx.ErrNoRows},
				{err: pgx.ErrNoRows},
			},
		}

		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)

		lease, found, err := store.ClaimDiscoverySourceLease(
			ctx,
			testDiscoveryLeaseOwner,
			interval,
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
				"ClaimDiscoverySourceLease() found %#v",
				lease,
			)
		}
	})

	t.Run("upsert failure", func(t *testing.T) {
		testErr := errors.New("test discovery upsert failure")
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{{}},
			rowResults: []discoveryUnitRow{
				{values: []any{false, false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(0),
					},
				},
				{values: []any{false}},
				{values: []any{false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(0),
					},
				},
				{values: []any{"example.com"}},
				{err: testErr},
			},
		}

		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)

		_, found, err := store.ClaimDiscoverySourceLease(
			ctx,
			testDiscoveryLeaseOwner,
			interval,
			leaseDuration,
		)
		if found {
			t.Fatal(
				"ClaimDiscoverySourceLease() found = true, want false",
			)
		}
		if !errors.Is(err, testErr) ||
			!strings.Contains(
				err.Error(),
				"store: claim discovery source lease",
			) {
			t.Fatalf(
				"ClaimDiscoverySourceLease() error = %v",
				err,
			)
		}
	})
}

func TestRenewDiscoveryDomainInflightBranchesWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := queueTestTime()
	lease := discovery.CrawlSourceLease{
		Origin: mustStoreOrigin(
			t,
			"https://example.com",
		),
		Generation: 7,
		ClaimedAt:  now.Add(-time.Minute),
		ExpiresAt:  now.Add(4 * time.Minute),
	}
	leaseDuration := 5 * time.Minute

	t.Run("domain lost", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{
				{},
				{
					tag: pgconn.NewCommandTag("UPDATE 0"),
				},
			},
			rowResults: []discoveryUnitRow{
				{
					values: []any{
						lease.Generation,
						lease.ClaimedAt,
						now.Add(leaseDuration),
					},
				},
			},
		}

		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)

		_, err := store.RenewDiscoverySourceLease(
			ctx,
			lease,
			leaseDuration,
		)
		if !errors.Is(err, ErrDiscoverySourceLeaseLost) {
			t.Fatalf(
				"RenewDiscoverySourceLease() error = %v, want %v",
				err,
				ErrDiscoverySourceLeaseLost,
			)
		}
	})

	t.Run("domain extend failure", func(t *testing.T) {
		testErr := errors.New("test discovery renew domain failure")
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{
				{},
				{err: testErr},
			},
			rowResults: []discoveryUnitRow{
				{
					values: []any{
						lease.Generation,
						lease.ClaimedAt,
						now.Add(leaseDuration),
					},
				},
			},
		}

		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)

		_, err := store.RenewDiscoverySourceLease(
			ctx,
			lease,
			leaseDuration,
		)
		if !errors.Is(err, testErr) ||
			!strings.Contains(
				err.Error(),
				"store: renew discovery source lease",
			) {
			t.Fatalf(
				"RenewDiscoverySourceLease() error = %v",
				err,
			)
		}
	})
}

func TestCompleteDiscoveryReleaseDomainFailureWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := queueTestTime()
	lease := discovery.CrawlSourceLease{
		Origin: mustStoreOrigin(
			t,
			"https://example.com",
		),
		Generation: 3,
		ClaimedAt:  now.Add(-time.Minute),
		ExpiresAt:  now.Add(4 * time.Minute),
	}
	testErr := errors.New("test complete domain release failure")

	tx := &discoveryPolicyUnitTx{
		rowResults: []discoveryUnitRow{
			{values: []any{1}},
		},
		execResults: []discoveryUnitExecResult{
			{
				tag: pgconn.NewCommandTag("UPDATE 1"),
			},
			{err: testErr},
		},
	}

	store := discoveryCompletionStore(
		t,
		discoveryPolicyDatabase(tx),
		discoveryCompletionClock{now: now},
	)

	err := store.CompleteDiscoverySourceLeaseRetry(
		ctx,
		lease,
		retry.CategoryNone,
		0,
	)
	if !errors.Is(err, testErr) ||
		!strings.Contains(
			err.Error(),
			"store: complete discovery source lease",
		) {
		t.Fatalf(
			"CompleteDiscoverySourceLeaseRetry() error = %v",
			err,
		)
	}
}

func TestNeighborhoodAdvisoryHelpersWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	source := mustStoreOrigin(t, "https://helper.example.com")
	now := queueTestTime()

	t.Run("current schema failure", func(t *testing.T) {
		testErr := errors.New("schema boom")
		tx := &discoveryPolicyUnitTx{
			scriptAdvisoryHelpers: true,
			rowResults: []discoveryUnitRow{
				{err: testErr},
			},
		}
		_, err := tryNeighborhoodAdvisoryLock(ctx, tx, "example.com")
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("try advisory failure", func(t *testing.T) {
		testErr := errors.New("try boom")
		tx := &discoveryPolicyUnitTx{
			scriptAdvisoryHelpers: true,
			rowResults: []discoveryUnitRow{
				{values: []any{"public"}},
				{err: testErr},
			},
		}
		_, err := tryNeighborhoodAdvisoryLock(ctx, tx, "example.com")
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("blocking lock failure", func(t *testing.T) {
		testErr := errors.New("lock boom")
		tx := &discoveryPolicyUnitTx{
			discoveryScheduleLockErr: testErr,
		}
		err := lockNeighborhoodAdvisory(ctx, tx, "example.com")
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("write lock_timeout set failure", func(t *testing.T) {
		testErr := errors.New("set timeout boom")
		tx := &discoveryPolicyUnitTx{
			lockTimeoutErrs: []error{testErr},
		}
		_, err := writeDomainInflightLease(
			ctx,
			tx,
			"example.com",
			source,
			"worker",
			1,
			now,
			now.Add(time.Minute),
		)
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("write lock_timeout reset failure after miss", func(t *testing.T) {
		testErr := errors.New("reset timeout boom")
		tx := &discoveryPolicyUnitTx{
			lockTimeoutErrs: []error{nil, testErr},
			rowResults: []discoveryUnitRow{
				{err: pgx.ErrNoRows},
			},
		}
		_, err := writeDomainInflightLease(
			ctx,
			tx,
			"example.com",
			source,
			"worker",
			1,
			now,
			now.Add(time.Minute),
		)
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("write lock_timeout reset failure after success", func(t *testing.T) {
		testErr := errors.New("reset after write boom")
		tx := &discoveryPolicyUnitTx{
			lockTimeoutErrs: []error{nil, testErr},
			rowResults: []discoveryUnitRow{
				{values: []any{"example.com"}},
			},
		}
		_, err := writeDomainInflightLease(
			ctx,
			tx,
			"example.com",
			source,
			"worker",
			1,
			now,
			now.Add(time.Minute),
		)
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("lock schema failure", func(t *testing.T) {
		testErr := errors.New("lock schema boom")
		tx := &discoveryPolicyUnitTx{
			scriptAdvisoryHelpers: true,
			rowResults: []discoveryUnitRow{
				{err: testErr},
			},
		}
		err := lockNeighborhoodAdvisory(ctx, tx, "example.com")
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("reserve try failure", func(t *testing.T) {
		testErr := errors.New("reserve try boom")
		tx := &discoveryPolicyUnitTx{
			scriptAdvisoryHelpers: true,
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{values: []any{"public"}},
				{err: testErr},
			},
		}
		_, err := reserveNeighborhoodForAcquire(ctx, tx, "example.com", now)
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("reserve recheck failure", func(t *testing.T) {
		testErr := errors.New("reserve recheck boom")
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{err: testErr},
			},
		}
		_, err := reserveNeighborhoodForAcquire(ctx, tx, "example.com", now)
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("write upsert hard failure", func(t *testing.T) {
		testErr := errors.New("upsert boom")
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{err: testErr},
			},
		}
		_, err := writeDomainInflightLease(
			ctx, tx, "example.com", source, "worker", 1, now, now.Add(time.Minute),
		)
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("write miss with successful reset", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{err: pgx.ErrNoRows},
			},
		}
		written, err := writeDomainInflightLease(
			ctx, tx, "example.com", source, "worker", 1, now, now.Add(time.Minute),
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if written {
			t.Fatal("written = true")
		}
	})

	t.Run("acquire success writes durable lease", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{values: []any{false}},
				{values: []any{"example.com"}},
			},
		}
		acquired, err := acquireDomainInflight(
			ctx, tx, "example.com", source, "worker", 1, now, now.Add(time.Minute),
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if !acquired {
			t.Fatal("acquired = false")
		}
	})

	t.Run("reserve recheck busy after advisory", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			rowResults: []discoveryUnitRow{
				{values: []any{false}},
				{values: []any{true}},
			},
		}
		reserved, err := reserveNeighborhoodForAcquire(
			ctx,
			tx,
			"example.com",
			now,
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if reserved {
			t.Fatal("reserved = true, want false")
		}
	})
}

func TestQueueMutatorsNeighborhoodLockFailureWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := queueTestTime()
	lease := queueUnitLease(
		t,
		now.Add(-time.Minute),
		now.Add(4*time.Minute),
	)
	testErr := errors.New("neighborhood lock boom")

	t.Run("renew", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			discoveryScheduleLockErr: testErr,
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)
		_, err := queue.Renew(ctx, lease)
		if !errors.Is(err, testErr) {
			t.Fatalf("Renew() error = %v", err)
		}
	})

	t.Run("abandon", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			discoveryScheduleLockErr: testErr,
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)
		err := queue.AbandonVerification(ctx, lease)
		if !errors.Is(err, testErr) {
			t.Fatalf("AbandonVerification() error = %v", err)
		}
	})

	t.Run("reschedule", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			discoveryScheduleLockErr: testErr,
		}
		queue := queueSimpleDatabaseStore(
			discoveryPolicyDatabase(tx),
			queueUnitClock{now: now},
		)
		err := queue.Reschedule(
			ctx,
			lease,
			now.Add(time.Hour),
		)
		if !errors.Is(err, testErr) {
			t.Fatalf("Reschedule() error = %v", err)
		}
	})
}

func TestCompleteVerificationNeighborhoodLockFailureWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := queueTestTime()
	lease := queueUnitLease(
		t,
		now.Add(-time.Minute),
		now.Add(4*time.Minute),
	)
	testErr := errors.New("complete neighborhood lock boom")
	tx := &discoveryPolicyUnitTx{
		failAdvisoryAfter:   2,
		advisoryLockFailErr: testErr,
	}
	queue := queueSimpleDatabaseStore(
		discoveryPolicyDatabase(tx),
		queueUnitClock{now: now},
	)
	err := queue.CompleteVerification(
		ctx,
		lease,
		validCompletionResult(lease.Origin),
		time.Hour,
	)
	if !errors.Is(err, testErr) {
		t.Fatalf("CompleteVerification() error = %v", err)
	}
}

func TestDiscoveryClaimLockOrderBranchesWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	now := queueTestTime()
	interval := 15 * time.Minute
	leaseDuration := 5 * time.Minute
	testErr := errors.New("discovery branch boom")

	t.Run("savepoint failure", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			scriptSavepoints: true,
			execResults: []discoveryUnitExecResult{
				{}, // global discovery advisory
				{err: testErr},
			},
			rowResults: []discoveryUnitRow{
				{values: []any{false, false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(0),
					},
				},
			},
		}
		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)
		_, _, err := store.ClaimDiscoverySourceLease(
			ctx,
			testDiscoveryLeaseOwner,
			interval,
			leaseDuration,
		)
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("lock miss continues", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{{}},
			rowResults: []discoveryUnitRow{
				{values: []any{false, false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(0),
					},
				},
				{values: []any{false}},
				{values: []any{false}},
				{err: pgx.ErrNoRows},
				{err: pgx.ErrNoRows},
			},
		}
		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)
		lease, found, err := store.ClaimDiscoverySourceLease(
			ctx,
			testDiscoveryLeaseOwner,
			interval,
			leaseDuration,
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if found {
			t.Fatalf("found %#v", lease)
		}
	})

	t.Run("write miss continues", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{{}},
			rowResults: []discoveryUnitRow{
				{values: []any{false, false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(0),
					},
				},
				{values: []any{false}},
				{values: []any{false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(0),
					},
				},
				{err: pgx.ErrNoRows},
				{err: pgx.ErrNoRows},
			},
		}
		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)
		lease, found, err := store.ClaimDiscoverySourceLease(
			ctx,
			testDiscoveryLeaseOwner,
			interval,
			leaseDuration,
		)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if found {
			t.Fatalf("found %#v", lease)
		}
	})
}

func TestDiscoveryRenewReleaseNeighborhoodLockFailureWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := queueTestTime()
	testErr := errors.New("discovery renew lock boom")
	lease := discovery.CrawlSourceLease{
		Origin:     mustStoreOrigin(t, "https://example.com"),
		Generation: 1,
		ClaimedAt:  now.Add(-time.Minute),
		ExpiresAt:  now.Add(4 * time.Minute),
	}

	t.Run("renew", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{
				{}, // discovery claim global advisory
			},
			discoveryScheduleLockErr: testErr,
		}
		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)
		_, err := store.RenewDiscoverySourceLease(
			ctx,
			lease,
			5*time.Minute,
		)
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("release", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			discoveryScheduleLockErr: testErr,
		}
		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)
		err := store.ReleaseDiscoverySourceLease(ctx, lease)
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestDiscoveryCompleteNeighborhoodLockFailureWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := queueTestTime()
	testErr := errors.New("discovery complete lock boom")
	lease := discovery.CrawlSourceLease{
		Origin:     mustStoreOrigin(t, "https://example.com"),
		Generation: 1,
		ClaimedAt:  now.Add(-time.Minute),
		ExpiresAt:  now.Add(4 * time.Minute),
	}
	tx := &discoveryPolicyUnitTx{
		discoveryScheduleLockErr: testErr,
	}
	store := claimDiscoveryUnitStore(
		t,
		discoveryPolicyDatabase(tx),
		claimDiscoveryClock{now: now},
	)
	err := store.CompleteDiscoverySourceLeaseRetry(
		ctx,
		lease,
		retry.CategoryNone,
		0,
	)
	if !errors.Is(err, testErr) {
		t.Fatalf("error = %v", err)
	}
}

func TestDiscoveryClaimAttemptHardFailuresWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	now := queueTestTime()
	interval := 15 * time.Minute
	leaseDuration := 5 * time.Minute
	testErr := errors.New("discovery hard boom")

	t.Run("lock hard error", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{{}},
			rowResults: []discoveryUnitRow{
				{values: []any{false, false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(0),
					},
				},
				{values: []any{false}},
				{values: []any{false}},
				{err: testErr},
			},
		}
		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)
		_, _, err := store.ClaimDiscoverySourceLease(
			ctx,
			testDiscoveryLeaseOwner,
			interval,
			leaseDuration,
		)
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("write hard error", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{{}},
			rowResults: []discoveryUnitRow{
				{values: []any{false, false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(0),
					},
				},
				{values: []any{false}},
				{values: []any{false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(0),
					},
				},
				{err: testErr},
			},
		}
		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)
		_, _, err := store.ClaimDiscoverySourceLease(
			ctx,
			testDiscoveryLeaseOwner,
			interval,
			leaseDuration,
		)
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("release savepoint failure", func(t *testing.T) {
		expiresAt := now.Add(leaseDuration)
		tx := &discoveryPolicyUnitTx{
			scriptSavepoints: true,
			execResults: []discoveryUnitExecResult{
				{},     // global advisory
				{},     // SAVEPOINT
				{}, {}, // crawl run cleanups
				{err: testErr}, // RELEASE
			},
			rowResults: []discoveryUnitRow{
				{values: []any{false, false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(6),
					},
				},
				{values: []any{false}},
				{values: []any{false}},
				{
					values: []any{
						"https://example.com",
						false,
						int64(6),
					},
				},
				{values: []any{"example.com"}},
				{
					values: []any{
						"https://example.com",
						int64(7),
						now,
						expiresAt,
					},
				},
			},
		}
		store := claimDiscoveryUnitStore(
			t,
			discoveryPolicyDatabase(tx),
			claimDiscoveryClock{now: now},
		)
		_, _, err := store.ClaimDiscoverySourceLease(
			ctx,
			testDiscoveryLeaseOwner,
			interval,
			leaseDuration,
		)
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v", err)
		}
	})
}

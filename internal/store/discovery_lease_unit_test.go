package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/database"
)

type claimDiscoveryClock struct {
	now time.Time
	err error
}

func (clock claimDiscoveryClock) NowTransaction(
	context.Context,
	pgx.Tx,
) (time.Time, error) {
	return clock.now, clock.err
}

func claimDiscoveryUnitStore(
	t *testing.T,
	storeDatabase database.Postgres,
	clock transactionQueueClock,
) *DiscoveryStore {
	t.Helper()

	store, err := newDiscoveryStoreWithConfig(
		storeDatabase,
		clock,
		AutomaticCrawlConfig{},
	)
	if err != nil {
		t.Fatalf(
			"newDiscoveryStoreWithConfig() error = %v",
			err,
		)
	}

	return store
}

type discoveryLeaseCaptureTx struct {
	*discoveryPolicyUnitTx

	claimQuery   string
	claimArgs    []any
	cleanupQuery string
	cleanupArgs  []any
}

func (tx *discoveryLeaseCaptureTx) Exec(
	ctx context.Context,
	query string,
	args ...any,
) (pgconn.CommandTag, error) {
	if strings.Contains(
		query,
		"UPDATE crawl_runs",
	) {
		tx.cleanupQuery = query
		tx.cleanupArgs = append(
			[]any(nil),
			args...,
		)
	}

	return tx.discoveryPolicyUnitTx.Exec(
		ctx,
		query,
		args...,
	)
}

func (tx *discoveryLeaseCaptureTx) QueryRow(
	ctx context.Context,
	query string,
	args ...any,
) pgx.Row {
	if strings.Contains(
		query,
		"WITH verified_candidate AS",
	) {
		tx.claimQuery = query
		tx.claimArgs = append(
			[]any(nil),
			args...,
		)
	}

	return tx.discoveryPolicyUnitTx.QueryRow(
		ctx,
		query,
		args...,
	)
}

func TestClaimDiscoverySourceLeaseWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.September,
		19,
		18,
		0,
		0,
		0,
		time.UTC,
	)
	interval := 15 * time.Minute
	leaseDuration := 5 * time.Minute

	t.Run(
		"validation",
		func(t *testing.T) {
			var store *DiscoveryStore

			lease, found, err :=
				store.ClaimDiscoverySourceLease(
					ctx,
					interval,
					leaseDuration,
				)

			if lease.Origin.String() != "" ||
				lease.Generation != 0 ||
				!lease.ClaimedAt.IsZero() ||
				!lease.ExpiresAt.IsZero() {
				t.Fatalf(
					"ClaimDiscoverySourceLease() lease = %#v, want zero",
					lease,
				)
			}

			if found {
				t.Fatal(
					"ClaimDiscoverySourceLease() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				errDiscoveryStoreUnavailable,
			) {
				t.Fatalf(
					"ClaimDiscoverySourceLease() error = %v, want %v",
					err,
					errDiscoveryStoreUnavailable,
				)
			}
		},
	)

	t.Run(
		"invalid interval",
		func(t *testing.T) {
			store := claimDiscoveryUnitStore(
				t,
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
				},
				claimDiscoveryClock{
					now: now,
				},
			)

			_, found, err :=
				store.ClaimDiscoverySourceLease(
					ctx,
					0,
					leaseDuration,
				)

			if found {
				t.Fatal(
					"ClaimDiscoverySourceLease() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				errInvalidDiscoveryInterval,
			) {
				t.Fatalf(
					"ClaimDiscoverySourceLease() error = %v, want %v",
					err,
					errInvalidDiscoveryInterval,
				)
			}
		},
	)

	t.Run(
		"invalid lease duration",
		func(t *testing.T) {
			store := claimDiscoveryUnitStore(
				t,
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
				},
				claimDiscoveryClock{
					now: now,
				},
			)

			_, found, err :=
				store.ClaimDiscoverySourceLease(
					ctx,
					interval,
					0,
				)

			if found {
				t.Fatal(
					"ClaimDiscoverySourceLease() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				errInvalidDiscoveryLeaseDuration,
			) {
				t.Fatalf(
					"ClaimDiscoverySourceLease() error = %v, want %v",
					err,
					errInvalidDiscoveryLeaseDuration,
				)
			}
		},
	)

	t.Run(
		"begin failure",
		func(t *testing.T) {
			store := claimDiscoveryUnitStore(
				t,
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
					beginErr:              errDiscoveryPolicyBegin,
				},
				claimDiscoveryClock{
					now: now,
				},
			)

			_, found, err :=
				store.ClaimDiscoverySourceLease(
					ctx,
					interval,
					leaseDuration,
				)

			if found {
				t.Fatal(
					"ClaimDiscoverySourceLease() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				errDiscoveryPolicyBegin,
			) ||
				!strings.Contains(
					err.Error(),
					"store: claim discovery source lease",
				) {
				t.Fatalf(
					"ClaimDiscoverySourceLease() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test discovery lease clock failure",
			)

			tx := &discoveryPolicyUnitTx{}

			store := claimDiscoveryUnitStore(
				t,
				discoveryPolicyDatabase(tx),
				claimDiscoveryClock{
					err: testErr,
				},
			)

			_, found, err :=
				store.ClaimDiscoverySourceLease(
					ctx,
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
					"store: read discovery clock",
				) {
				t.Fatalf(
					"ClaimDiscoverySourceLease() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"advisory lock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test discovery lease lock failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store := claimDiscoveryUnitStore(
				t,
				discoveryPolicyDatabase(tx),
				claimDiscoveryClock{
					now: now,
				},
			)

			_, found, err :=
				store.ClaimDiscoverySourceLease(
					ctx,
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
					"store: lock discovery claim",
				) {
				t.Fatalf(
					"ClaimDiscoverySourceLease() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"crawl control read failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test discovery lease control failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{},
				},
				rowResults: []discoveryUnitRow{
					{
						err: testErr,
					},
				},
			}

			store := claimDiscoveryUnitStore(
				t,
				discoveryPolicyDatabase(tx),
				claimDiscoveryClock{
					now: now,
				},
			)

			_, found, err :=
				store.ClaimDiscoverySourceLease(
					ctx,
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
					"store: read crawl control",
				) {
				t.Fatalf(
					"ClaimDiscoverySourceLease() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"paused",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{},
				},
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							true,
						},
					},
				},
			}

			store := claimDiscoveryUnitStore(
				t,
				discoveryPolicyDatabase(tx),
				claimDiscoveryClock{
					now: now,
				},
			)

			lease, found, err :=
				store.ClaimDiscoverySourceLease(
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

			if found {
				t.Fatal(
					"ClaimDiscoverySourceLease() found = true, want false",
				)
			}

			if lease.Origin.String() != "" {
				t.Fatalf(
					"ClaimDiscoverySourceLease() origin = %q, want empty",
					lease.Origin.String(),
				)
			}
		},
	)

	t.Run(
		"no candidate",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{},
				},
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							false,
						},
					},
					{
						err: pgx.ErrNoRows,
					},
				},
			}

			store := claimDiscoveryUnitStore(
				t,
				discoveryPolicyDatabase(tx),
				claimDiscoveryClock{
					now: now,
				},
			)

			lease, found, err :=
				store.ClaimDiscoverySourceLease(
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

			if found {
				t.Fatal(
					"ClaimDiscoverySourceLease() found = true, want false",
				)
			}

			if lease.Origin.String() != "" {
				t.Fatalf(
					"ClaimDiscoverySourceLease() origin = %q, want empty",
					lease.Origin.String(),
				)
			}
		},
	)

	t.Run(
		"candidate read failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test discovery lease candidate failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{},
				},
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							false,
						},
					},
					{
						err: testErr,
					},
				},
			}

			store := claimDiscoveryUnitStore(
				t,
				discoveryPolicyDatabase(tx),
				claimDiscoveryClock{
					now: now,
				},
			)

			_, found, err :=
				store.ClaimDiscoverySourceLease(
					ctx,
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
		},
	)

	t.Run(
		"invalid stored origin",
		func(t *testing.T) {
			expiresAt := now.Add(
				leaseDuration,
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{},
				},
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							false,
						},
					},
					{
						values: []any{
							"ftp://example.com",
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
				claimDiscoveryClock{
					now: now,
				},
			)

			_, found, err :=
				store.ClaimDiscoverySourceLease(
					ctx,
					interval,
					leaseDuration,
				)

			if found {
				t.Fatal(
					"ClaimDiscoverySourceLease() found = true, want false",
				)
			}

			if err == nil ||
				!strings.Contains(
					err.Error(),
					"store: invalid discovery source",
				) ||
				!strings.Contains(
					err.Error(),
					"store: claim discovery source lease",
				) {
				t.Fatalf(
					"ClaimDiscoverySourceLease() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"crawl run cleanup failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test abandoned crawl cleanup failure",
			)
			expiresAt := now.Add(leaseDuration)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{},
					{
						err: testErr,
					},
				},
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							false,
						},
					},
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
				claimDiscoveryClock{
					now: now,
				},
			)

			lease, found, err :=
				store.ClaimDiscoverySourceLease(
					ctx,
					interval,
					leaseDuration,
				)

			if found {
				t.Fatal(
					"ClaimDiscoverySourceLease() found = true, want false",
				)
			}

			if lease != (firstLeaseZero()) {
				t.Fatalf(
					"ClaimDiscoverySourceLease() lease = %#v, want zero",
					lease,
				)
			}

			if !errors.Is(err, testErr) ||
				!strings.Contains(
					err.Error(),
					"store: cancel abandoned crawl runs",
				) ||
				!strings.Contains(
					err.Error(),
					"store: claim discovery source lease",
				) {
				t.Fatalf(
					"ClaimDiscoverySourceLease() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"success preserves attempt scheduling",
		func(t *testing.T) {
			location := time.FixedZone(
				"test-offset",
				2*60*60,
			)
			claimedAt := now.In(location)
			expiresAt := now.Add(
				leaseDuration,
			).In(location)

			baseTx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{},
					{},
				},
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							false,
						},
					},
					{
						values: []any{
							"https://example.com",
							int64(7),
							claimedAt,
							expiresAt,
						},
					},
				},
			}
			tx := &discoveryLeaseCaptureTx{
				discoveryPolicyUnitTx: baseTx,
			}

			store := claimDiscoveryUnitStore(
				t,
				discoveryPolicyDatabase(tx),
				claimDiscoveryClock{
					now: now,
				},
			)

			lease, found, err :=
				store.ClaimDiscoverySourceLease(
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

			if lease.Origin.String() !=
				"https://example.com" {
				t.Fatalf(
					"ClaimDiscoverySourceLease() origin = %q, want %q",
					lease.Origin.String(),
					"https://example.com",
				)
			}

			if lease.Generation != 7 {
				t.Fatalf(
					"ClaimDiscoverySourceLease() generation = %d, want 7",
					lease.Generation,
				)
			}

			if !lease.ClaimedAt.Equal(now) ||
				lease.ClaimedAt.Location() != time.UTC {
				t.Fatalf(
					"ClaimDiscoverySourceLease() claimed at = %v, want UTC %v",
					lease.ClaimedAt,
					now,
				)
			}

			wantExpiresAt := now.Add(
				leaseDuration,
			)
			if !lease.ExpiresAt.Equal(
				wantExpiresAt,
			) ||
				lease.ExpiresAt.Location() != time.UTC {
				t.Fatalf(
					"ClaimDiscoverySourceLease() expires at = %v, want UTC %v",
					lease.ExpiresAt,
					wantExpiresAt,
				)
			}

			normalizedQuery := strings.Join(
				strings.Fields(tx.claimQuery),
				" ",
			)

			if !strings.Contains(
				normalizedQuery,
				"source_state.lease_expires_at IS NULL OR source_state.lease_expires_at <= $1",
			) {
				t.Fatalf(
					"claim query does not exclude active leases: %s",
					normalizedQuery,
				)
			}

			if !strings.Contains(
				normalizedQuery,
				"lease_generation = discovery_source_state. lease_generation + 1",
			) {
				t.Fatalf(
					"claim query does not advance lease generation: %s",
					normalizedQuery,
				)
			}

			if strings.Contains(
				normalizedQuery,
				"SET last_attempted_at =",
			) {
				t.Fatalf(
					"claim query consumes last_attempted_at: %s",
					normalizedQuery,
				)
			}

			normalizedCleanupQuery := strings.Join(
				strings.Fields(tx.cleanupQuery),
				" ",
			)

			if !strings.Contains(
				normalizedCleanupQuery,
				"UPDATE crawl_runs SET finished_at = $2, outcome = 'canceled', stop_reason = 'lease_reclaimed'",
			) {
				t.Fatalf(
					"claim does not cancel abandoned crawl runs: %s",
					normalizedCleanupQuery,
				)
			}

			if !strings.Contains(
				normalizedCleanupQuery,
				"WHERE source_origin = $1 AND outcome = 'running' AND finished_at IS NULL",
			) {
				t.Fatalf(
					"crawl-run cleanup is not bounded to the claimed origin: %s",
					normalizedCleanupQuery,
				)
			}

			if len(tx.cleanupArgs) != 2 {
				t.Fatalf(
					"cleanup argument count = %d, want 2",
					len(tx.cleanupArgs),
				)
			}

			if got, ok := tx.cleanupArgs[0].(string); !ok ||
				got != "https://example.com" {
				t.Fatalf(
					"cleanup origin argument = %#v, want %q",
					tx.cleanupArgs[0],
					"https://example.com",
				)
			}

			if got, ok := tx.cleanupArgs[1].(time.Time); !ok ||
				!got.Equal(now) {
				t.Fatalf(
					"cleanup time argument = %#v, want %v",
					tx.cleanupArgs[1],
					now,
				)
			}

			if len(tx.claimArgs) != 5 {
				t.Fatalf(
					"claim argument count = %d, want 5",
					len(tx.claimArgs),
				)
			}

			if got, ok := tx.claimArgs[0].(time.Time); !ok ||
				!got.Equal(now) {
				t.Fatalf(
					"claim now argument = %#v, want %v",
					tx.claimArgs[0],
					now,
				)
			}

			if got, ok := tx.claimArgs[1].(float64); !ok ||
				got != interval.Seconds() {
				t.Fatalf(
					"claim interval argument = %#v, want %v",
					tx.claimArgs[1],
					interval.Seconds(),
				)
			}

			if got, ok := tx.claimArgs[4].(time.Time); !ok ||
				!got.Equal(wantExpiresAt) {
				t.Fatalf(
					"claim expiration argument = %#v, want %v",
					tx.claimArgs[4],
					wantExpiresAt,
				)
			}
		},
	)
}

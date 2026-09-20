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

func TestClaimDiscoverySourceWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.September,
		18,
		19,
		30,
		0,
		0,
		time.UTC,
	)
	interval := 15 * time.Minute

	t.Run(
		"validation",
		func(t *testing.T) {
			var store *DiscoveryStore

			claimed, found, err :=
				store.ClaimDiscoverySource(
					ctx,
					interval,
				)

			if claimed.String() != "" {
				t.Fatalf(
					"ClaimDiscoverySource() origin = %q, want empty",
					claimed.String(),
				)
			}

			if found {
				t.Fatal(
					"ClaimDiscoverySource() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				errDiscoveryStoreUnavailable,
			) {
				t.Fatalf(
					"ClaimDiscoverySource() error = %v, want %v",
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

			claimed, found, err :=
				store.ClaimDiscoverySource(
					ctx,
					0,
				)

			if claimed.String() != "" {
				t.Fatalf(
					"ClaimDiscoverySource() origin = %q, want empty",
					claimed.String(),
				)
			}

			if found {
				t.Fatal(
					"ClaimDiscoverySource() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				errInvalidDiscoveryInterval,
			) {
				t.Fatalf(
					"ClaimDiscoverySource() error = %v, want %v",
					err,
					errInvalidDiscoveryInterval,
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

			claimed, found, err :=
				store.ClaimDiscoverySource(
					ctx,
					interval,
				)

			if claimed.String() != "" {
				t.Fatalf(
					"ClaimDiscoverySource() origin = %q, want empty",
					claimed.String(),
				)
			}

			if found {
				t.Fatal(
					"ClaimDiscoverySource() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				errDiscoveryPolicyBegin,
			) ||
				!strings.Contains(
					err.Error(),
					"store: claim discovery source",
				) {
				t.Fatalf(
					"ClaimDiscoverySource() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test claim discovery clock failure",
			)

			tx := &discoveryPolicyUnitTx{}

			store := claimDiscoveryUnitStore(
				t,
				discoveryPolicyDatabase(tx),
				claimDiscoveryClock{
					err: testErr,
				},
			)

			claimed, found, err :=
				store.ClaimDiscoverySource(
					ctx,
					interval,
				)

			if claimed.String() != "" {
				t.Fatalf(
					"ClaimDiscoverySource() origin = %q, want empty",
					claimed.String(),
				)
			}

			if found {
				t.Fatal(
					"ClaimDiscoverySource() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read discovery clock",
				) {
				t.Fatalf(
					"ClaimDiscoverySource() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"advisory lock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test claim discovery lock failure",
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

			claimed, found, err :=
				store.ClaimDiscoverySource(
					ctx,
					interval,
				)

			if claimed.String() != "" {
				t.Fatalf(
					"ClaimDiscoverySource() origin = %q, want empty",
					claimed.String(),
				)
			}

			if found {
				t.Fatal(
					"ClaimDiscoverySource() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: lock discovery claim",
				) {
				t.Fatalf(
					"ClaimDiscoverySource() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"crawl control read failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test crawl control read failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"SELECT 1",
						),
					},
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

			claimed, found, err :=
				store.ClaimDiscoverySource(
					ctx,
					interval,
				)

			if claimed.String() != "" {
				t.Fatalf(
					"ClaimDiscoverySource() origin = %q, want empty",
					claimed.String(),
				)
			}

			if found {
				t.Fatal(
					"ClaimDiscoverySource() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read crawl control",
				) {
				t.Fatalf(
					"ClaimDiscoverySource() error = %v",
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
					{
						tag: pgconn.NewCommandTag(
							"SELECT 1",
						),
					},
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

			claimed, found, err :=
				store.ClaimDiscoverySource(
					ctx,
					interval,
				)
			if err != nil {
				t.Fatalf(
					"ClaimDiscoverySource() error = %v",
					err,
				)
			}

			if claimed.String() != "" {
				t.Fatalf(
					"ClaimDiscoverySource() origin = %q, want empty",
					claimed.String(),
				)
			}

			if found {
				t.Fatal(
					"ClaimDiscoverySource() found = true, want false",
				)
			}
		},
	)

	t.Run(
		"no candidate",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"SELECT 1",
						),
					},
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

			claimed, found, err :=
				store.ClaimDiscoverySource(
					ctx,
					interval,
				)
			if err != nil {
				t.Fatalf(
					"ClaimDiscoverySource() error = %v",
					err,
				)
			}

			if claimed.String() != "" {
				t.Fatalf(
					"ClaimDiscoverySource() origin = %q, want empty",
					claimed.String(),
				)
			}

			if found {
				t.Fatal(
					"ClaimDiscoverySource() found = true, want false",
				)
			}
		},
	)

	t.Run(
		"candidate read failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test candidate read failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"SELECT 1",
						),
					},
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

			claimed, found, err :=
				store.ClaimDiscoverySource(
					ctx,
					interval,
				)

			if claimed.String() != "" {
				t.Fatalf(
					"ClaimDiscoverySource() origin = %q, want empty",
					claimed.String(),
				)
			}

			if found {
				t.Fatal(
					"ClaimDiscoverySource() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: claim discovery source",
				) {
				t.Fatalf(
					"ClaimDiscoverySource() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"invalid stored origin",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"SELECT 1",
						),
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
							"ftp://example.com",
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

			claimed, found, err :=
				store.ClaimDiscoverySource(
					ctx,
					interval,
				)

			if claimed.String() != "" {
				t.Fatalf(
					"ClaimDiscoverySource() origin = %q, want empty",
					claimed.String(),
				)
			}

			if found {
				t.Fatal(
					"ClaimDiscoverySource() found = true, want false",
				)
			}

			if err == nil ||
				!strings.Contains(
					err.Error(),
					"store: invalid discovery source",
				) ||
				!strings.Contains(
					err.Error(),
					"store: claim discovery source",
				) {
				t.Fatalf(
					"ClaimDiscoverySource() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"success",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"SELECT 1",
						),
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

			claimed, found, err :=
				store.ClaimDiscoverySource(
					ctx,
					interval,
				)
			if err != nil {
				t.Fatalf(
					"ClaimDiscoverySource() error = %v",
					err,
				)
			}

			if !found {
				t.Fatal(
					"ClaimDiscoverySource() found = false, want true",
				)
			}

			if claimed.String() !=
				"https://example.com" {
				t.Fatalf(
					"ClaimDiscoverySource() origin = %q, want %q",
					claimed.String(),
					"https://example.com",
				)
			}
		},
	)
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

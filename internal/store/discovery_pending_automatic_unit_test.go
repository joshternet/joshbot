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
)

type pendingAutomaticClock struct {
	now time.Time
	err error
}

func (clock pendingAutomaticClock) NowTransaction(
	context.Context,
	pgx.Tx,
) (time.Time, error) {
	return clock.now, clock.err
}

func TestPendingAutomaticCandidatesWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.September,
		18,
		20,
		30,
		0,
		0,
		time.UTC,
	)

	t.Run(
		"validation",
		func(t *testing.T) {
			var store *DiscoveryStore

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}

			if !errors.Is(
				err,
				errDiscoveryStoreUnavailable,
			) {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v, want %v",
					err,
					errDiscoveryStoreUnavailable,
				)
			}
		},
	)

	t.Run(
		"automatic crawling disabled",
		func(t *testing.T) {
			store := pendingAutomaticStore(
				t,
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
				},
				pendingAutomaticClock{
					now: now,
				},
				false,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)
			if err != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v",
					err,
				)
			}

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}
		},
	)

	t.Run(
		"invalid run",
		func(t *testing.T) {
			store := pendingAutomaticStore(
				t,
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
				},
				pendingAutomaticClock{
					now: now,
				},
				true,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					0,
				)

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}

			if !errors.Is(
				err,
				errInvalidCrawlRun,
			) {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v, want %v",
					err,
					errInvalidCrawlRun,
				)
			}
		},
	)

	t.Run(
		"begin failure",
		func(t *testing.T) {
			database :=
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
					beginErr:              errDiscoveryPolicyBegin,
				}

			store := pendingAutomaticStore(
				t,
				database,
				pendingAutomaticClock{
					now: now,
				},
				true,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}

			if !errors.Is(
				err,
				errDiscoveryPolicyBegin,
			) ||
				!strings.Contains(
					err.Error(),
					"store: prepare automatic candidates",
				) {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"advisory lock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test automatic batch lock failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store := pendingAutomaticStore(
				t,
				discoveryPolicyDatabase(tx),
				pendingAutomaticClock{
					now: now,
				},
				true,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: lock automatic batch allocation",
				) ||
				!strings.Contains(
					err.Error(),
					"store: prepare automatic candidates",
				) {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test automatic admission clock failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"SELECT 1",
						),
					},
				},
			}

			store := pendingAutomaticStore(
				t,
				discoveryPolicyDatabase(tx),
				pendingAutomaticClock{
					err: testErr,
				},
				true,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read automatic admission clock",
				) {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"unknown run",
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
						err: pgx.ErrNoRows,
					},
				},
			}

			store := pendingAutomaticStore(
				t,
				discoveryPolicyDatabase(tx),
				pendingAutomaticClock{
					now: now,
				},
				true,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}

			if !errors.Is(
				err,
				errUnknownCrawlRun,
			) {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v, want %v",
					err,
					errUnknownCrawlRun,
				)
			}
		},
	)

	t.Run(
		"run read failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test automatic admission run failure",
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

			store := pendingAutomaticStore(
				t,
				discoveryPolicyDatabase(tx),
				pendingAutomaticClock{
					now: now,
				},
				true,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"read automatic admission run",
				) {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"exclusion read failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test automatic exclusion read failure",
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
							0,
							0,
						},
					},
				},
				queryResults: []discoveryUnitQueryResult{
					{
						err: testErr,
					},
				},
			}

			store := pendingAutomaticStore(
				t,
				discoveryPolicyDatabase(tx),
				pendingAutomaticClock{
					now: now,
				},
				true,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read crawl domain avoid rules",
				) {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"batch allocation failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test automatic batch allocation failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"SELECT 1",
						),
					},
					{
						err: testErr,
					},
				},
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							2,
							0,
						},
					},
				},
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{},
					},
				},
			}

			store := pendingAutomaticStore(
				t,
				discoveryPolicyDatabase(tx),
				pendingAutomaticClock{
					now: now,
				},
				true,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"allocate automatic admission batch",
				) {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"pending probe count failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test pending probe count failure",
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
							0,
							0,
						},
					},
					{
						err: testErr,
					},
				},
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{},
					},
				},
			}

			store := pendingAutomaticStore(
				t,
				discoveryPolicyDatabase(tx),
				pendingAutomaticClock{
					now: now,
				},
				true,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"count pending probes",
				) {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"deferred probe admission failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test deferred probe admission failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"SELECT 1",
						),
					},
					{
						err: testErr,
					},
				},
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							0,
							0,
						},
					},
					{
						values: []any{
							0,
						},
					},
				},
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{},
					},
				},
			}

			store := pendingAutomaticStore(
				t,
				discoveryPolicyDatabase(tx),
				pendingAutomaticClock{
					now: now,
				},
				true,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"admit deferred discovery probes",
				) {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"pending candidate query failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test pending candidate query failure",
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
							0,
							0,
						},
					},
					{
						values: []any{
							1000,
						},
					},
				},
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{},
					},
					{
						err: testErr,
					},
				},
			}

			store := pendingAutomaticStore(
				t,
				discoveryPolicyDatabase(tx),
				pendingAutomaticClock{
					now: now,
				},
				true,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"list pending automatic candidates",
				) {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"pending candidate scan failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test pending candidate scan failure",
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
							0,
							0,
						},
					},
					{
						values: []any{
							1000,
						},
					},
				},
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{},
					},
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{
									err: testErr,
								},
							},
						},
					},
				},
			}

			store := pendingAutomaticStore(
				t,
				discoveryPolicyDatabase(tx),
				pendingAutomaticClock{
					now: now,
				},
				true,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"collect pending automatic candidates",
				) {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"invalid pending origin",
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
							0,
							0,
						},
					},
					{
						values: []any{
							1000,
						},
					},
				},
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{},
					},
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{
									values: []any{
										"ftp://example.com",
									},
								},
							},
						},
					},
				},
			}

			store := pendingAutomaticStore(
				t,
				discoveryPolicyDatabase(tx),
				pendingAutomaticClock{
					now: now,
				},
				true,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)

			if candidates != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() = %#v, want nil",
					candidates,
				)
			}

			if err == nil ||
				!strings.Contains(
					err.Error(),
					"collect pending automatic candidates",
				) {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v",
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
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 2",
						),
					},
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							2,
							0,
						},
					},
					{
						values: []any{
							0,
						},
					},
				},
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{},
					},
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{
									values: []any{
										"https://example.com",
									},
								},
								{
									values: []any{
										"https://example.org",
									},
								},
							},
						},
					},
				},
			}

			store := pendingAutomaticStore(
				t,
				discoveryPolicyDatabase(tx),
				pendingAutomaticClock{
					now: now,
				},
				true,
			)

			candidates, err :=
				store.PendingAutomaticCandidates(
					ctx,
					1,
				)
			if err != nil {
				t.Fatalf(
					"PendingAutomaticCandidates() error = %v",
					err,
				)
			}

			if len(candidates) != 2 {
				t.Fatalf(
					"PendingAutomaticCandidates() count = %d, want 2",
					len(candidates),
				)
			}

			if candidates[0].Origin.String() !=
				"https://example.com" ||
				candidates[0].Kind !=
					discovery.KindLink {
				t.Fatalf(
					"PendingAutomaticCandidates()[0] = %#v",
					candidates[0],
				)
			}

			if candidates[1].Origin.String() !=
				"https://example.org" ||
				candidates[1].Kind !=
					discovery.KindLink {
				t.Fatalf(
					"PendingAutomaticCandidates()[1] = %#v",
					candidates[1],
				)
			}
		},
	)
}

func pendingAutomaticStore(
	t *testing.T,
	storeDatabase *discoveryPolicyUnitDatabase,
	clock transactionQueueClock,
	enabled bool,
) *DiscoveryStore {
	t.Helper()

	store, err := newDiscoveryStoreWithConfig(
		storeDatabase,
		clock,
		AutomaticCrawlConfig{
			Enabled: enabled,
		},
	)
	if err != nil {
		t.Fatalf(
			"newDiscoveryStoreWithConfig() error = %v",
			err,
		)
	}

	return store
}

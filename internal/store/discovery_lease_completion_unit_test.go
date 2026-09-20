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
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/retry"
)

type discoveryCompletionClock struct {
	now time.Time
	err error
}

func (clock discoveryCompletionClock) NowTransaction(
	context.Context,
	pgx.Tx,
) (time.Time, error) {
	return clock.now, clock.err
}

func discoveryCompletionStore(
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

type discoveryLeaseCompletionCaptureTx struct {
	*discoveryPolicyUnitTx

	lockQuery  string
	lockArgs   []any
	updateSQL  string
	updateArgs []any
}

func (tx *discoveryLeaseCompletionCaptureTx) QueryRow(
	ctx context.Context,
	query string,
	args ...any,
) pgx.Row {
	if strings.Contains(
		query,
		"SELECT consecutive_failures",
	) &&
		strings.Contains(
			query,
			"lease_generation",
		) {
		tx.lockQuery = query
		tx.lockArgs = append(
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

func (tx *discoveryLeaseCompletionCaptureTx) Exec(
	ctx context.Context,
	query string,
	args ...any,
) (pgconn.CommandTag, error) {
	if strings.Contains(
		query,
		"UPDATE discovery_source_state",
	) &&
		strings.Contains(
			query,
			"last_attempted_at",
		) {
		tx.updateSQL = query
		tx.updateArgs = append(
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

func TestCompleteDiscoverySourceLeaseWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.September,
		19,
		23,
		0,
		0,
		0,
		time.UTC,
	)

	lease := discovery.CrawlSourceLease{
		Origin: mustStoreOrigin(
			t,
			"https://example.com",
		),
		Generation: 7,
		ClaimedAt:  now.Add(-5 * time.Minute),
		ExpiresAt:  now.Add(5 * time.Minute),
	}

	t.Run(
		"validation",
		func(t *testing.T) {
			var store *DiscoveryStore

			err := store.CompleteDiscoverySourceLeaseRetry(
				ctx,
				lease,
				retry.CategoryNone,
				0,
			)

			if !errors.Is(
				err,
				errDiscoveryStoreUnavailable,
			) {
				t.Fatalf(
					"CompleteDiscoverySourceLeaseRetry() error = %v, want %v",
					err,
					errDiscoveryStoreUnavailable,
				)
			}
		},
	)

	t.Run(
		"invalid lease",
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

			err := store.CompleteDiscoverySourceLeaseRetry(
				ctx,
				discovery.CrawlSourceLease{},
				retry.CategoryNone,
				0,
			)

			if !errors.Is(
				err,
				errInvalidDiscoverySourceLease,
			) {
				t.Fatalf(
					"CompleteDiscoverySourceLeaseRetry() error = %v, want %v",
					err,
					errInvalidDiscoverySourceLease,
				)
			}
		},
	)

	for _, test := range []struct {
		name       string
		category   retry.Category
		retryAfter time.Duration
	}{
		{
			name:       "invalid category",
			category:   retry.Category("invalid"),
			retryAfter: 0,
		},
		{
			name:       "negative retry after",
			category:   retry.CategoryDNS,
			retryAfter: -time.Second,
		},
		{
			name:       "retry after too large",
			category:   retry.CategoryDNS,
			retryAfter: retry.MaxDelay + time.Second,
		},
	} {
		t.Run(
			test.name,
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

				err := store.CompleteDiscoverySourceLeaseRetry(
					ctx,
					lease,
					test.category,
					test.retryAfter,
				)

				if !errors.Is(
					err,
					errInvalidDiscoveryCandidate,
				) {
					t.Fatalf(
						"CompleteDiscoverySourceLeaseRetry() error = %v, want %v",
						err,
						errInvalidDiscoveryCandidate,
					)
				}
			},
		)
	}

	t.Run(
		"begin failure",
		func(t *testing.T) {
			store := discoveryCompletionStore(
				t,
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
					beginErr:              errDiscoveryPolicyBegin,
				},
				discoveryCompletionClock{
					now: now,
				},
			)

			err := store.CompleteDiscoverySourceLeaseRetry(
				ctx,
				lease,
				retry.CategoryNone,
				0,
			)

			if !errors.Is(
				err,
				errDiscoveryPolicyBegin,
			) ||
				!strings.Contains(
					err.Error(),
					"store: complete discovery source lease",
				) {
				t.Fatalf(
					"CompleteDiscoverySourceLeaseRetry() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test discovery lease completion clock failure",
			)

			tx := &discoveryPolicyUnitTx{}

			store := discoveryCompletionStore(
				t,
				discoveryPolicyDatabase(tx),
				discoveryCompletionClock{
					err: testErr,
				},
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
					"store: read discovery clock",
				) {
				t.Fatalf(
					"CompleteDiscoverySourceLeaseRetry() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"lease lost before lock",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						err: pgx.ErrNoRows,
					},
				},
			}

			store := discoveryCompletionStore(
				t,
				discoveryPolicyDatabase(tx),
				discoveryCompletionClock{
					now: now,
				},
			)

			err := store.CompleteDiscoverySourceLeaseRetry(
				ctx,
				lease,
				retry.CategoryNone,
				0,
			)

			if !errors.Is(
				err,
				ErrDiscoverySourceLeaseLost,
			) {
				t.Fatalf(
					"CompleteDiscoverySourceLeaseRetry() error = %v, want %v",
					err,
					ErrDiscoverySourceLeaseLost,
				)
			}
		},
	)

	t.Run(
		"lock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test discovery lease completion lock failure",
			)

			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						err: testErr,
					},
				},
			}

			store := discoveryCompletionStore(
				t,
				discoveryPolicyDatabase(tx),
				discoveryCompletionClock{
					now: now,
				},
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
					"store: lock discovery source lease",
				) {
				t.Fatalf(
					"CompleteDiscoverySourceLeaseRetry() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"update failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test discovery lease completion update failure",
			)

			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							1,
						},
					},
				},
				execResults: []discoveryUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store := discoveryCompletionStore(
				t,
				discoveryPolicyDatabase(tx),
				discoveryCompletionClock{
					now: now,
				},
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
					"store: update discovery source completion",
				) {
				t.Fatalf(
					"CompleteDiscoverySourceLeaseRetry() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"lease lost during update",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							1,
						},
					},
				},
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 0",
						),
					},
				},
			}

			store := discoveryCompletionStore(
				t,
				discoveryPolicyDatabase(tx),
				discoveryCompletionClock{
					now: now,
				},
			)

			err := store.CompleteDiscoverySourceLeaseRetry(
				ctx,
				lease,
				retry.CategoryNone,
				0,
			)

			if !errors.Is(
				err,
				ErrDiscoverySourceLeaseLost,
			) {
				t.Fatalf(
					"CompleteDiscoverySourceLeaseRetry() error = %v, want %v",
					err,
					ErrDiscoverySourceLeaseLost,
				)
			}
		},
	)

	t.Run(
		"non transient success records completion time",
		func(t *testing.T) {
			baseTx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							4,
						},
					},
				},
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
						),
					},
				},
			}
			tx := &discoveryLeaseCompletionCaptureTx{
				discoveryPolicyUnitTx: baseTx,
			}

			store := discoveryCompletionStore(
				t,
				discoveryPolicyDatabase(tx),
				discoveryCompletionClock{
					now: now,
				},
			)

			err := store.CompleteDiscoverySourceLeaseRetry(
				ctx,
				lease,
				retry.CategoryMalformedOrigin,
				0,
			)
			if err != nil {
				t.Fatalf(
					"CompleteDiscoverySourceLeaseRetry() error = %v",
					err,
				)
			}

			lockSQL := strings.Join(
				strings.Fields(tx.lockQuery),
				" ",
			)

			if !strings.Contains(
				lockSQL,
				"AND lease_generation = $2 AND lease_expires_at > $3",
			) {
				t.Fatalf(
					"completion lock does not require current unexpired generation: %s",
					lockSQL,
				)
			}

			updateSQL := strings.Join(
				strings.Fields(tx.updateSQL),
				" ",
			)

			if !strings.Contains(
				updateSQL,
				"last_attempted_at = $3",
			) {
				t.Fatalf(
					"completion does not record last_attempted_at: %s",
					updateSQL,
				)
			}

			if !strings.Contains(
				updateSQL,
				"lease_expires_at = NULL",
			) {
				t.Fatalf(
					"completion does not release lease: %s",
					updateSQL,
				)
			}

			if !strings.Contains(
				updateSQL,
				"AND lease_generation = $2 AND lease_expires_at > $3",
			) {
				t.Fatalf(
					"completion update does not require current unexpired generation: %s",
					updateSQL,
				)
			}

			if len(tx.updateArgs) != 7 {
				t.Fatalf(
					"completion argument count = %d, want 7",
					len(tx.updateArgs),
				)
			}

			if got, ok := tx.updateArgs[2].(time.Time); !ok ||
				!got.Equal(now) {
				t.Fatalf(
					"completion time argument = %#v, want %v",
					tx.updateArgs[2],
					now,
				)
			}

			if got, ok := tx.updateArgs[3].(int); !ok ||
				got != 0 {
				t.Fatalf(
					"completion failure count = %#v, want 0",
					tx.updateArgs[3],
				)
			}

			if got, ok := tx.updateArgs[4].(bool); !ok ||
				got {
				t.Fatalf(
					"completion transient argument = %#v, want false",
					tx.updateArgs[4],
				)
			}
		},
	)

	t.Run(
		"transient success records retry state",
		func(t *testing.T) {
			baseTx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							2,
						},
					},
				},
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
						),
					},
				},
			}
			tx := &discoveryLeaseCompletionCaptureTx{
				discoveryPolicyUnitTx: baseTx,
			}

			store := discoveryCompletionStore(
				t,
				discoveryPolicyDatabase(tx),
				discoveryCompletionClock{
					now: now,
				},
			)

			retryAfter := 3 * time.Hour

			err := store.CompleteDiscoverySourceLeaseRetry(
				ctx,
				lease,
				retry.CategoryDNS,
				retryAfter,
			)
			if err != nil {
				t.Fatalf(
					"CompleteDiscoverySourceLeaseRetry() error = %v",
					err,
				)
			}

			if len(tx.updateArgs) != 7 {
				t.Fatalf(
					"completion argument count = %d, want 7",
					len(tx.updateArgs),
				)
			}

			if got, ok := tx.updateArgs[3].(int); !ok ||
				got != 3 {
				t.Fatalf(
					"completion failure count = %#v, want 3",
					tx.updateArgs[3],
				)
			}

			if got, ok := tx.updateArgs[4].(bool); !ok ||
				!got {
				t.Fatalf(
					"completion transient argument = %#v, want true",
					tx.updateArgs[4],
				)
			}

			if got, ok := tx.updateArgs[5].(string); !ok ||
				got != string(retry.CategoryDNS) {
				t.Fatalf(
					"completion category argument = %#v, want %q",
					tx.updateArgs[5],
					retry.CategoryDNS,
				)
			}

			wantNextAttempt := now.Add(
				retryAfter,
			)

			if got, ok := tx.updateArgs[6].(time.Time); !ok ||
				!got.Equal(wantNextAttempt) {
				t.Fatalf(
					"completion next attempt argument = %#v, want %v",
					tx.updateArgs[6],
					wantNextAttempt,
				)
			}
		},
	)
}

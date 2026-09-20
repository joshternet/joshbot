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

func TestCompleteDiscoverySourceRetryDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)
	now := time.Date(
		2026,
		time.September,
		18,
		18,
		30,
		0,
		0,
		time.UTC,
	)

	t.Run(
		"begin failure",
		func(t *testing.T) {
			database := &discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginErr:              errDiscoveryPolicyBegin,
			}

			store := discoveryCompletionStore(
				t,
				database,
				discoveryCompletionClock{
					now: now,
				},
			)

			err := store.CompleteDiscoverySourceRetry(
				ctx,
				source,
				retry.CategoryNone,
				0,
			)

			if !errors.Is(
				err,
				errDiscoveryPolicyBegin,
			) {
				t.Fatalf(
					"CompleteDiscoverySourceRetry() error = %v, want %v",
					err,
					errDiscoveryPolicyBegin,
				)
			}
		},
	)

	t.Run(
		"clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test discovery completion clock failure",
			)

			tx := &discoveryPolicyUnitTx{}

			store := discoveryCompletionStore(
				t,
				discoveryPolicyDatabase(tx),
				discoveryCompletionClock{
					err: testErr,
				},
			)

			err := store.CompleteDiscoverySourceRetry(
				ctx,
				source,
				retry.CategoryNone,
				0,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read discovery clock",
				) {
				t.Fatalf(
					"CompleteDiscoverySourceRetry() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"non transient success",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
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

			err := store.CompleteDiscoverySourceRetry(
				ctx,
				source,
				retry.CategoryNone,
				0,
			)
			if err != nil {
				t.Fatalf(
					"CompleteDiscoverySourceRetry() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"non transient update failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test discovery reset failure",
			)

			tx := &discoveryPolicyUnitTx{
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

			err := store.CompleteDiscoverySourceRetry(
				ctx,
				source,
				retry.CategoryNone,
				0,
			)

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"CompleteDiscoverySourceRetry() error = %v, want %v",
					err,
					testErr,
				)
			}
		},
	)

	t.Run(
		"transient streak read failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test discovery retry streak failure",
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

			err := store.CompleteDiscoverySourceRetry(
				ctx,
				source,
				retry.CategoryDNS,
				0,
			)

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"CompleteDiscoverySourceRetry() error = %v, want %v",
					err,
					testErr,
				)
			}
		},
	)

	t.Run(
		"transient success",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
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

			store := discoveryCompletionStore(
				t,
				discoveryPolicyDatabase(tx),
				discoveryCompletionClock{
					now: now,
				},
			)

			err := store.CompleteDiscoverySourceRetry(
				ctx,
				source,
				retry.CategoryDNS,
				10*time.Minute,
			)
			if err != nil {
				t.Fatalf(
					"CompleteDiscoverySourceRetry() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"transient update failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test discovery retry update failure",
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

			err := store.CompleteDiscoverySourceRetry(
				ctx,
				source,
				retry.CategoryDNS,
				0,
			)

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"CompleteDiscoverySourceRetry() error = %v, want %v",
					err,
					testErr,
				)
			}
		},
	)
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

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type retentionCaptureTx struct {
	*discoveryPolicyUnitTx

	execQueries   []string
	execArguments [][]any
}

func (tx *retentionCaptureTx) Exec(
	ctx context.Context,
	query string,
	arguments ...any,
) (pgconn.CommandTag, error) {
	tx.execQueries = append(
		tx.execQueries,
		query,
	)
	tx.execArguments = append(
		tx.execArguments,
		append([]any(nil), arguments...),
	)

	return tx.discoveryPolicyUnitTx.Exec(
		ctx,
		query,
		arguments...,
	)
}

func retentionUnitStore(
	t *testing.T,
	database *discoveryPolicyUnitDatabase,
) *DiscoveryStore {
	t.Helper()

	storage, err := newDiscoveryStore(
		database,
		databaseQueueClock{},
	)
	if err != nil {
		t.Fatalf(
			"newDiscoveryStore() error = %v",
			err,
		)
	}

	return storage
}

func TestPurgeOperationalHistoryWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	retention := 30 * 24 * time.Hour

	t.Run(
		"validation",
		func(t *testing.T) {
			var storage *DiscoveryStore

			err := storage.PurgeOperationalHistory(
				ctx,
				retention,
			)

			if !errors.Is(
				err,
				errDiscoveryStoreUnavailable,
			) {
				t.Fatalf(
					"PurgeOperationalHistory() error = %v, want %v",
					err,
					errDiscoveryStoreUnavailable,
				)
			}
		},
	)

	t.Run(
		"invalid retention",
		func(t *testing.T) {
			storage := retentionUnitStore(
				t,
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
				},
			)

			err := storage.PurgeOperationalHistory(
				ctx,
				0,
			)

			if !errors.Is(
				err,
				errInvalidOperationalHistoryRetention,
			) {
				t.Fatalf(
					"PurgeOperationalHistory() error = %v, want %v",
					err,
					errInvalidOperationalHistoryRetention,
				)
			}
		},
	)

	t.Run(
		"begin failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test retention begin failure",
			)

			storage := retentionUnitStore(
				t,
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
					beginErr:              testErr,
				},
			)

			err := storage.PurgeOperationalHistory(
				ctx,
				retention,
			)

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"PurgeOperationalHistory() error = %v, want %v",
					err,
					testErr,
				)
			}

			if !strings.Contains(
				err.Error(),
				"store: purge operational history",
			) {
				t.Fatalf(
					"PurgeOperationalHistory() error = %v, want purge context",
					err,
				)
			}
		},
	)

	t.Run(
		"observation delete failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test observation delete failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			storage := retentionUnitStore(
				t,
				discoveryPolicyDatabase(tx),
			)

			err := storage.PurgeOperationalHistory(
				ctx,
				retention,
			)

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"PurgeOperationalHistory() error = %v, want %v",
					err,
					testErr,
				)
			}

			for _, expected := range []string{
				"delete expired verification observations",
				"store: purge operational history",
			} {
				if !strings.Contains(
					err.Error(),
					expected,
				) {
					t.Fatalf(
						"PurgeOperationalHistory() error = %v, want %q",
						err,
						expected,
					)
				}
			}
		},
	)

	t.Run(
		"queue event delete failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test queue event delete failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{},
					{
						err: testErr,
					},
				},
			}

			storage := retentionUnitStore(
				t,
				discoveryPolicyDatabase(tx),
			)

			err := storage.PurgeOperationalHistory(
				ctx,
				retention,
			)

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"PurgeOperationalHistory() error = %v, want %v",
					err,
					testErr,
				)
			}

			for _, expected := range []string{
				"delete expired verification queue events",
				"store: purge operational history",
			} {
				if !strings.Contains(
					err.Error(),
					expected,
				) {
					t.Fatalf(
						"PurgeOperationalHistory() error = %v, want %q",
						err,
						expected,
					)
				}
			}
		},
	)

	t.Run(
		"heartbeat delete failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test heartbeat delete failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{},
					{},
					{
						err: testErr,
					},
				},
			}

			storage := retentionUnitStore(
				t,
				discoveryPolicyDatabase(tx),
			)

			err := storage.PurgeOperationalHistory(
				ctx,
				retention,
			)

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"PurgeOperationalHistory() error = %v, want %v",
					err,
					testErr,
				)
			}

			for _, expected := range []string{
				"delete stale service heartbeats",
				"store: purge operational history",
			} {
				if !strings.Contains(
					err.Error(),
					expected,
				) {
					t.Fatalf(
						"PurgeOperationalHistory() error = %v, want %q",
						err,
						expected,
					)
				}
			}
		},
	)

	t.Run(
		"commit failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test retention commit failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{},
					{},
					{},
				},
				commitErr: testErr,
			}

			storage := retentionUnitStore(
				t,
				discoveryPolicyDatabase(tx),
			)

			err := storage.PurgeOperationalHistory(
				ctx,
				retention,
			)

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"PurgeOperationalHistory() error = %v, want %v",
					err,
					testErr,
				)
			}

			if !strings.Contains(
				err.Error(),
				"store: purge operational history",
			) {
				t.Fatalf(
					"PurgeOperationalHistory() error = %v, want purge context",
					err,
				)
			}
		},
	)

	t.Run(
		"success",
		func(t *testing.T) {
			baseTx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{},
					{},
					{},
				},
			}
			tx := &retentionCaptureTx{
				discoveryPolicyUnitTx: baseTx,
			}

			storage := retentionUnitStore(
				t,
				discoveryPolicyDatabase(tx),
			)

			if err := storage.PurgeOperationalHistory(
				ctx,
				retention,
			); err != nil {
				t.Fatalf(
					"PurgeOperationalHistory() error = %v",
					err,
				)
			}

			if len(tx.execQueries) != 3 {
				t.Fatalf(
					"retention Exec count = %d, want 3",
					len(tx.execQueries),
				)
			}

			expectedTargets := []string{
				"DELETE FROM verification_observations",
				"DELETE FROM verification_queue_events",
				"DELETE FROM crawl_service_heartbeats",
			}

			for index, expected := range expectedTargets {
				normalizedQuery := strings.Join(
					strings.Fields(
						tx.execQueries[index],
					),
					" ",
				)

				if !strings.Contains(
					normalizedQuery,
					expected,
				) {
					t.Errorf(
						"retention query %d = %q, want %q",
						index,
						normalizedQuery,
						expected,
					)
				}

				if len(tx.execArguments[index]) != 1 {
					t.Fatalf(
						"retention query %d argument count = %d, want 1",
						index,
						len(tx.execArguments[index]),
					)
				}

				seconds, ok :=
					tx.execArguments[index][0].(float64)
				if !ok {
					t.Fatalf(
						"retention query %d argument = %#v, want float64",
						index,
						tx.execArguments[index][0],
					)
				}

				if seconds != retention.Seconds() {
					t.Errorf(
						"retention query %d seconds = %v, want %v",
						index,
						seconds,
						retention.Seconds(),
					)
				}
			}

			normalizedObservations := strings.Join(
				strings.Fields(
					tx.execQueries[0],
				),
				" ",
			)

			for _, expected := range []string{
				"protected_observations",
				"ORDER BY observation.observed_at DESC, observation.id DESC",
				"AND NOT EXISTS",
			} {
				if !strings.Contains(
					normalizedObservations,
					expected,
				) {
					t.Errorf(
						"observation retention query does not contain %q",
						expected,
					)
				}
			}
		},
	)
}

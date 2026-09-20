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
	"github.com/joshternet/joshbot/internal/origin"
)

type recordDiscoveryBeginResult struct {
	tx  pgx.Tx
	err error
}

type recordDiscoveryUnitDatabase struct {
	beginResults []recordDiscoveryBeginResult
	beginIndex   int

	queryResults []discoveryUnitQueryResult
	queryIndex   int
}

var _ database.Postgres = (*recordDiscoveryUnitDatabase)(nil)

func (database *recordDiscoveryUnitDatabase) Begin(
	context.Context,
) (pgx.Tx, error) {
	if database.beginIndex >= len(database.beginResults) {
		return nil, errUnexpectedDiscoveryUnitDatabaseCall
	}

	result := database.beginResults[database.beginIndex]
	database.beginIndex++

	return result.tx, result.err
}

func (*recordDiscoveryUnitDatabase) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{},
		errUnexpectedDiscoveryUnitDatabaseCall
}

func (database *recordDiscoveryUnitDatabase) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	if database.queryIndex >= len(database.queryResults) {
		return nil, errUnexpectedDiscoveryUnitDatabaseCall
	}

	result := database.queryResults[database.queryIndex]
	database.queryIndex++

	return result.rows, result.err
}

func (*recordDiscoveryUnitDatabase) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	return discoveryUnitRow{
		err: errUnexpectedDiscoveryUnitDatabaseCall,
	}
}

type recordDiscoveryUnitClock struct {
	now time.Time
	err error
}

func (clock recordDiscoveryUnitClock) NowTransaction(
	context.Context,
	pgx.Tx,
) (time.Time, error) {
	return clock.now, clock.err
}

func TestRecordDiscoveryDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.September,
		18,
		20,
		0,
		0,
		0,
		time.UTC,
	)

	source := mustStoreOrigin(
		t,
		"https://source.example",
	)
	candidate := discovery.Candidate{
		Origin: mustStoreOrigin(
			t,
			"https://candidate.example",
		),
		Kind: discovery.KindLink,
	}

	t.Run(
		"validation",
		func(t *testing.T) {
			var store *DiscoveryStore

			result, err := store.recordDiscovery(
				ctx,
				0,
				source,
				[]discovery.Candidate{
					candidate,
				},
			)

			if result != (discovery.RecordResult{}) {
				t.Fatalf(
					"recordDiscovery() result = %#v, want zero",
					result,
				)
			}

			if !errors.Is(
				err,
				errDiscoveryStoreUnavailable,
			) {
				t.Fatalf(
					"recordDiscovery() error = %v, want %v",
					err,
					errDiscoveryStoreUnavailable,
				)
			}
		},
	)

	t.Run(
		"invalid source",
		func(t *testing.T) {
			store := recordDiscoveryStore(
				t,
				&recordDiscoveryUnitDatabase{},
				recordDiscoveryUnitClock{
					now: now,
				},
				true,
			)

			result, err := store.recordDiscovery(
				ctx,
				0,
				origin.Origin{},
				[]discovery.Candidate{
					candidate,
				},
			)

			if result != (discovery.RecordResult{}) {
				t.Fatalf(
					"recordDiscovery() result = %#v, want zero",
					result,
				)
			}

			if !errors.Is(
				err,
				errInvalidOrigin,
			) {
				t.Fatalf(
					"recordDiscovery() error = %v, want %v",
					err,
					errInvalidOrigin,
				)
			}
		},
	)

	t.Run(
		"empty candidates",
		func(t *testing.T) {
			store := recordDiscoveryStore(
				t,
				&recordDiscoveryUnitDatabase{},
				recordDiscoveryUnitClock{
					now: now,
				},
				true,
			)

			result, err := store.recordDiscovery(
				ctx,
				0,
				source,
				nil,
			)
			if err != nil {
				t.Fatalf(
					"recordDiscovery() error = %v",
					err,
				)
			}

			if result != (discovery.RecordResult{}) {
				t.Fatalf(
					"recordDiscovery() result = %#v, want zero",
					result,
				)
			}
		},
	)

	t.Run(
		"begin failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test record discovery begin failure",
			)

			database := &recordDiscoveryUnitDatabase{
				beginResults: []recordDiscoveryBeginResult{
					{
						err: testErr,
					},
				},
			}

			store := recordDiscoveryStore(
				t,
				database,
				recordDiscoveryUnitClock{
					now: now,
				},
				true,
			)

			result, err := store.recordDiscovery(
				ctx,
				0,
				source,
				[]discovery.Candidate{
					candidate,
				},
			)

			if result != (discovery.RecordResult{}) {
				t.Fatalf(
					"recordDiscovery() result = %#v, want zero",
					result,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: record discovery",
				) {
				t.Fatalf(
					"recordDiscovery() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test record discovery clock failure",
			)

			tx := &discoveryPolicyUnitTx{}

			database := &recordDiscoveryUnitDatabase{
				beginResults: []recordDiscoveryBeginResult{
					{
						tx: tx,
					},
				},
			}

			store := recordDiscoveryStore(
				t,
				database,
				recordDiscoveryUnitClock{
					err: testErr,
				},
				true,
			)

			result, err := store.recordDiscovery(
				ctx,
				0,
				source,
				[]discovery.Candidate{
					candidate,
				},
			)

			if result != (discovery.RecordResult{}) {
				t.Fatalf(
					"recordDiscovery() result = %#v, want zero",
					result,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read discovery clock",
				) ||
				!strings.Contains(
					err.Error(),
					"store: record discovery",
				) {
				t.Fatalf(
					"recordDiscovery() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"record query failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test record discovery query failure",
			)

			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						err: testErr,
					},
				},
			}

			database := &recordDiscoveryUnitDatabase{
				beginResults: []recordDiscoveryBeginResult{
					{
						tx: tx,
					},
				},
			}

			store := recordDiscoveryStore(
				t,
				database,
				recordDiscoveryUnitClock{
					now: now,
				},
				true,
			)

			result, err := store.recordDiscovery(
				ctx,
				0,
				source,
				[]discovery.Candidate{
					candidate,
				},
			)

			if result != (discovery.RecordResult{}) {
				t.Fatalf(
					"recordDiscovery() result = %#v, want zero",
					result,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: record discovery",
				) {
				t.Fatalf(
					"recordDiscovery() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"unknown source",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							0,
							1,
							0,
						},
					},
				},
			}

			database := &recordDiscoveryUnitDatabase{
				beginResults: []recordDiscoveryBeginResult{
					{
						tx: tx,
					},
				},
			}

			store := recordDiscoveryStore(
				t,
				database,
				recordDiscoveryUnitClock{
					now: now,
				},
				true,
			)

			result, err := store.recordDiscovery(
				ctx,
				0,
				source,
				[]discovery.Candidate{
					candidate,
				},
			)

			if result != (discovery.RecordResult{}) {
				t.Fatalf(
					"recordDiscovery() result = %#v, want zero",
					result,
				)
			}

			if !errors.Is(
				err,
				errDiscoverySourceUnknown,
			) {
				t.Fatalf(
					"recordDiscovery() error = %v, want %v",
					err,
					errDiscoverySourceUnknown,
				)
			}

			if err != errDiscoverySourceUnknown {
				t.Fatalf(
					"recordDiscovery() error = %v, want direct %v",
					err,
					errDiscoverySourceUnknown,
				)
			}
		},
	)

	t.Run(
		"unknown run",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							1,
							0,
							0,
						},
					},
				},
			}

			database := &recordDiscoveryUnitDatabase{
				beginResults: []recordDiscoveryBeginResult{
					{
						tx: tx,
					},
				},
			}

			store := recordDiscoveryStore(
				t,
				database,
				recordDiscoveryUnitClock{
					now: now,
				},
				true,
			)

			result, err := store.recordDiscovery(
				ctx,
				1,
				source,
				[]discovery.Candidate{
					candidate,
				},
			)

			if result != (discovery.RecordResult{}) {
				t.Fatalf(
					"recordDiscovery() result = %#v, want zero",
					result,
				)
			}

			if !errors.Is(
				err,
				errUnknownCrawlRun,
			) ||
				!strings.Contains(
					err.Error(),
					"store: record discovery",
				) {
				t.Fatalf(
					"recordDiscovery() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"automatic crawling success",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							1,
							1,
							1,
						},
					},
				},
			}

			database := &recordDiscoveryUnitDatabase{
				beginResults: []recordDiscoveryBeginResult{
					{
						tx: tx,
					},
				},
			}

			store := recordDiscoveryStore(
				t,
				database,
				recordDiscoveryUnitClock{
					now: now,
				},
				true,
			)

			result, err := store.recordDiscovery(
				ctx,
				1,
				source,
				[]discovery.Candidate{
					candidate,
				},
			)
			if err != nil {
				t.Fatalf(
					"recordDiscovery() error = %v",
					err,
				)
			}

			if result.Accepted != 1 {
				t.Fatalf(
					"recordDiscovery() accepted = %d, want 1",
					result.Accepted,
				)
			}
		},
	)

	t.Run(
		"probe scheduling failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test record discovery scheduling failure",
			)

			recordTx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							1,
							1,
							1,
						},
					},
				},
			}

			database := &recordDiscoveryUnitDatabase{
				beginResults: []recordDiscoveryBeginResult{
					{
						tx: recordTx,
					},
				},
				queryResults: []discoveryUnitQueryResult{
					{
						err: testErr,
					},
				},
			}

			store := recordDiscoveryStore(
				t,
				database,
				recordDiscoveryUnitClock{
					now: now,
				},
				false,
			)

			result, err := store.recordDiscovery(
				ctx,
				0,
				source,
				[]discovery.Candidate{
					candidate,
				},
			)

			if result != (discovery.RecordResult{}) {
				t.Fatalf(
					"recordDiscovery() result = %#v, want zero",
					result,
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
					"recordDiscovery() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"probe scheduling success",
		func(t *testing.T) {
			recordTx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							1,
							1,
							1,
						},
					},
				},
			}

			scheduleTx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"SELECT 1",
						),
					},
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
			}

			database := &recordDiscoveryUnitDatabase{
				beginResults: []recordDiscoveryBeginResult{
					{
						tx: recordTx,
					},
					{
						tx: scheduleTx,
					},
				},
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{},
					},
				},
			}

			store := recordDiscoveryStore(
				t,
				database,
				recordDiscoveryUnitClock{
					now: now,
				},
				false,
			)

			result, err := store.recordDiscovery(
				ctx,
				0,
				source,
				[]discovery.Candidate{
					candidate,
				},
			)
			if err != nil {
				t.Fatalf(
					"recordDiscovery() error = %v",
					err,
				)
			}

			if result.Accepted != 1 {
				t.Fatalf(
					"recordDiscovery() accepted = %d, want 1",
					result.Accepted,
				)
			}
		},
	)
}

func recordDiscoveryStore(
	t *testing.T,
	storeDatabase database.Postgres,
	clock transactionQueueClock,
	automaticEnabled bool,
) *DiscoveryStore {
	t.Helper()

	store, err := newDiscoveryStoreWithConfig(
		storeDatabase,
		clock,
		AutomaticCrawlConfig{
			Enabled: automaticEnabled,
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

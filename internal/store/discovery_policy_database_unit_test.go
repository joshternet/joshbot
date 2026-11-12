package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/database"
	"github.com/joshternet/joshbot/internal/origin"
)

var errDiscoveryPolicyBegin = errors.New(
	"test discovery policy begin failure",
)

type discoveryPolicyUnitDatabase struct {
	*discoveryUnitDatabase

	beginTx  pgx.Tx
	beginErr error
}

var _ database.Postgres = (*discoveryPolicyUnitDatabase)(nil)

func (database *discoveryPolicyUnitDatabase) Begin(
	context.Context,
) (pgx.Tx, error) {
	if database.beginErr != nil {
		return nil, database.beginErr
	}

	if database.beginTx == nil {
		return nil, errUnexpectedDiscoveryUnitDatabaseCall
	}

	return database.beginTx, nil
}

type discoveryPolicyUnitTx struct {
	pgx.Tx

	execResults []discoveryUnitExecResult
	execIndex   int

	queryResults []discoveryUnitQueryResult
	queryIndex   int

	rowResults []discoveryUnitRow
	rowIndex   int

	discoveryScheduleLockErr error
	advisoryLockCalls        int
	failAdvisoryAfter        int
	advisoryLockFailErr      error

	// tryAdvisoryResult overrides the auto-success try-lock helper when set.
	tryAdvisoryResult *bool
	// scriptAdvisoryHelpers disables auto current_schema/try-lock responses.
	scriptAdvisoryHelpers bool
	// lockTimeoutErrs are returned in order for set_config('lock_timeout') calls.
	lockTimeoutErrs []error
	// scriptSavepoints disables auto SAVEPOINT handling so fixtures can fail them.
	scriptSavepoints bool

	commitErr   error
	rollbackErr error
}

func (tx *discoveryPolicyUnitTx) Exec(
	_ context.Context,
	query string,
	arguments ...any,
) (pgconn.CommandTag, error) {
	if strings.Contains(
		query,
		"pg_advisory_xact_lock",
	) &&
		len(arguments) == 1 {
		key, ok := arguments[0].(int64)
		if ok && key < 0 {
			tx.advisoryLockCalls++
			if tx.discoveryScheduleLockErr != nil {
				return pgconn.CommandTag{},
					tx.discoveryScheduleLockErr
			}
			if tx.failAdvisoryAfter > 0 &&
				tx.advisoryLockCalls >= tx.failAdvisoryAfter {
				err := tx.advisoryLockFailErr
				if err == nil {
					err = errUnexpectedDiscoveryUnitDatabaseCall
				}
				return pgconn.CommandTag{}, err
			}

			return pgconn.NewCommandTag(
				"SELECT 1",
			), nil
		}
	}

	// Acquisition-scoped lock_timeout is a local set_config; ignore in mocks
	// unless a scripted error sequence is provided for coverage.
	if strings.Contains(query, "set_config('lock_timeout'") {
		if len(tx.lockTimeoutErrs) > 0 {
			err := tx.lockTimeoutErrs[0]
			tx.lockTimeoutErrs = tx.lockTimeoutErrs[1:]
			if err != nil {
				return pgconn.CommandTag{}, err
			}
		}
		return pgconn.NewCommandTag("SELECT 1"), nil
	}

	// Claim attempt savepoints are protocol scaffolding, not fixture steps.
	if !tx.scriptSavepoints &&
		(strings.Contains(query, "SAVEPOINT claim_attempt") ||
			strings.Contains(query, "RELEASE SAVEPOINT claim_attempt") ||
			strings.Contains(query, "ROLLBACK TO SAVEPOINT claim_attempt") ||
			strings.Contains(query, "SAVEPOINT discovery_claim_attempt") ||
			strings.Contains(query, "RELEASE SAVEPOINT discovery_claim_attempt") ||
			strings.Contains(query, "ROLLBACK TO SAVEPOINT discovery_claim_attempt")) {
		return pgconn.NewCommandTag("SAVEPOINT"), nil
	}

	if tx.execIndex >= len(tx.execResults) {
		return pgconn.CommandTag{},
			errUnexpectedDiscoveryUnitDatabaseCall
	}

	result := tx.execResults[tx.execIndex]
	tx.execIndex++

	return result.tag, result.err
}

func (tx *discoveryPolicyUnitTx) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	if tx.queryIndex >= len(tx.queryResults) {
		return nil, errUnexpectedDiscoveryUnitDatabaseCall
	}

	result := tx.queryResults[tx.queryIndex]
	tx.queryIndex++

	return result.rows, result.err
}

func (tx *discoveryPolicyUnitTx) QueryRow(
	_ context.Context,
	query string,
	_ ...any,
) pgx.Row {
	// Auto-serve neighborhood advisory helpers so renew/release/complete
	// fixtures only script durable row mutations.
	if !tx.scriptAdvisoryHelpers {
		if strings.Contains(query, "current_schema()") {
			return discoveryUnitRow{
				values: []any{"public"},
			}
		}
		if strings.Contains(query, "pg_try_advisory_xact_lock") {
			acquired := true
			if tx.tryAdvisoryResult != nil {
				acquired = *tx.tryAdvisoryResult
			}
			return discoveryUnitRow{
				values: []any{acquired},
			}
		}
	}

	if tx.rowIndex >= len(tx.rowResults) {
		return discoveryUnitRow{
			err: errUnexpectedDiscoveryUnitDatabaseCall,
		}
	}

	result := tx.rowResults[tx.rowIndex]
	tx.rowIndex++

	return result
}

func (tx *discoveryPolicyUnitTx) Commit(
	context.Context,
) error {
	return tx.commitErr
}

func (tx *discoveryPolicyUnitTx) Rollback(
	context.Context,
) error {
	return tx.rollbackErr
}

func TestDiscoveryScheduleOriginLockWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"success",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{}

			err := lockDiscoveryScheduleOrigin(
				ctx,
				tx,
				"https://example.com",
			)
			if err != nil {
				t.Fatalf(
					"lockDiscoveryScheduleOrigin() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test discovery schedule lock failure",
			)

			tx := &discoveryPolicyUnitTx{
				discoveryScheduleLockErr: testErr,
			}

			err := lockDiscoveryScheduleOrigin(
				ctx,
				tx,
				"https://example.com",
			)

			if !errors.Is(err, testErr) ||
				!strings.Contains(
					err.Error(),
					"store: lock discovery schedule origin",
				) {
				t.Fatalf(
					"lockDiscoveryScheduleOrigin() error = %v",
					err,
				)
			}
		},
	)
}

func TestSetCrawlBlockedDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	t.Run(
		"validation",
		func(t *testing.T) {
			var store *DiscoveryStore

			err := store.SetCrawlBlocked(
				ctx,
				source,
				true,
			)

			if !errors.Is(
				err,
				errDiscoveryStoreUnavailable,
			) {
				t.Fatalf(
					"SetCrawlBlocked() error = %v, want %v",
					err,
					errDiscoveryStoreUnavailable,
				)
			}
		},
	)

	t.Run(
		"invalid origin",
		func(t *testing.T) {
			store := discoveryPolicyStore(
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
				},
			)

			err := store.SetCrawlBlocked(
				ctx,
				origin.Origin{},
				true,
			)

			if !errors.Is(
				err,
				errInvalidOrigin,
			) {
				t.Fatalf(
					"SetCrawlBlocked() error = %v, want %v",
					err,
					errInvalidOrigin,
				)
			}
		},
	)

	t.Run(
		"begin failure",
		func(t *testing.T) {
			store := discoveryPolicyStore(
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
					beginErr:              errDiscoveryPolicyBegin,
				},
			)

			err := store.SetCrawlBlocked(
				ctx,
				source,
				true,
			)

			if !errors.Is(
				err,
				errDiscoveryPolicyBegin,
			) ||
				!strings.Contains(
					err.Error(),
					"store: set crawl block",
				) {
				t.Fatalf(
					"SetCrawlBlocked() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"success",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							false,
							false,
						},
					},
				},
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
						),
					},
					{
						tag: pgconn.NewCommandTag(
							"DELETE 1",
						),
					},
				},
			}

			store := discoveryPolicyStore(
				discoveryPolicyDatabase(tx),
			)

			if err := store.SetCrawlBlocked(
				ctx,
				source,
				true,
			); err != nil {
				t.Fatalf(
					"SetCrawlBlocked() error = %v",
					err,
				)
			}
		},
	)
}

func TestReconcileAutomaticCrawlPolicyDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"validation",
		func(t *testing.T) {
			var store *DiscoveryStore

			changed, err :=
				store.ReconcileAutomaticCrawlPolicy(
					ctx,
				)

			if changed != 0 {
				t.Fatalf(
					"ReconcileAutomaticCrawlPolicy() changed = %d, want 0",
					changed,
				)
			}

			if !errors.Is(
				err,
				errDiscoveryStoreUnavailable,
			) {
				t.Fatalf(
					"ReconcileAutomaticCrawlPolicy() error = %v, want %v",
					err,
					errDiscoveryStoreUnavailable,
				)
			}
		},
	)

	t.Run(
		"disabled",
		func(t *testing.T) {
			store := discoveryPolicyStore(
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
				},
			)

			changed, err :=
				store.ReconcileAutomaticCrawlPolicy(
					ctx,
				)
			if err != nil {
				t.Fatalf(
					"ReconcileAutomaticCrawlPolicy() error = %v",
					err,
				)
			}

			if changed != 0 {
				t.Fatalf(
					"ReconcileAutomaticCrawlPolicy() changed = %d, want 0",
					changed,
				)
			}
		},
	)

	t.Run(
		"begin failure",
		func(t *testing.T) {
			store := discoveryPolicyStore(
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
					beginErr:              errDiscoveryPolicyBegin,
				},
			)
			store.automatic.Enabled = true

			changed, err :=
				store.ReconcileAutomaticCrawlPolicy(
					ctx,
				)

			if changed != 0 {
				t.Fatalf(
					"ReconcileAutomaticCrawlPolicy() changed = %d, want 0",
					changed,
				)
			}

			if !errors.Is(
				err,
				errDiscoveryPolicyBegin,
			) ||
				!strings.Contains(
					err.Error(),
					"store: reconcile automatic crawl policy",
				) {
				t.Fatalf(
					"ReconcileAutomaticCrawlPolicy() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"success",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{},
					},
					{
						rows: &discoveryUnitRows{},
					},
				},
			}

			store := discoveryPolicyStore(
				discoveryPolicyDatabase(tx),
			)
			store.automatic.Enabled = true

			changed, err :=
				store.ReconcileAutomaticCrawlPolicy(
					ctx,
				)
			if err != nil {
				t.Fatalf(
					"ReconcileAutomaticCrawlPolicy() error = %v",
					err,
				)
			}

			if changed != 0 {
				t.Fatalf(
					"ReconcileAutomaticCrawlPolicy() changed = %d, want 0",
					changed,
				)
			}
		},
	)
}

func discoveryPolicyDatabase(
	tx pgx.Tx,
) *discoveryPolicyUnitDatabase {
	return &discoveryPolicyUnitDatabase{
		discoveryUnitDatabase: &discoveryUnitDatabase{},
		beginTx:               tx,
	}
}

func discoveryPolicyStore(
	storeDatabase database.Postgres,
) *DiscoveryStore {
	return &DiscoveryStore{
		pool:  storeDatabase,
		clock: databaseQueueClock{},
	}
}

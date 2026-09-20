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

	commitErr   error
	rollbackErr error
}

func (tx *discoveryPolicyUnitTx) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
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
	context.Context,
	string,
	...any,
) pgx.Row {
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

func TestAddDomainAvoidRuleDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"validation",
		func(t *testing.T) {
			var store *DiscoveryStore

			changed, err := store.AddDomainAvoidRule(
				ctx,
				"example.com",
			)

			if changed != 0 {
				t.Fatalf(
					"AddDomainAvoidRule() changed = %d, want 0",
					changed,
				)
			}

			if !errors.Is(
				err,
				errDiscoveryStoreUnavailable,
			) {
				t.Fatalf(
					"AddDomainAvoidRule() error = %v, want %v",
					err,
					errDiscoveryStoreUnavailable,
				)
			}
		},
	)

	t.Run(
		"invalid pattern",
		func(t *testing.T) {
			store := discoveryPolicyStore(
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
				},
			)

			changed, err := store.AddDomainAvoidRule(
				ctx,
				"",
			)

			if changed != 0 {
				t.Fatalf(
					"AddDomainAvoidRule() changed = %d, want 0",
					changed,
				)
			}

			if err == nil ||
				!strings.Contains(
					err.Error(),
					"crawl domain avoid rule is invalid",
				) {
				t.Fatalf(
					"AddDomainAvoidRule() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"insert failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test add domain avoid rule failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store := discoveryPolicyStore(
				discoveryPolicyDatabase(tx),
			)

			changed, err := store.AddDomainAvoidRule(
				ctx,
				"example.com",
			)

			if changed != 0 {
				t.Fatalf(
					"AddDomainAvoidRule() changed = %d, want 0",
					changed,
				)
			}

			if !errors.Is(err, testErr) ||
				!strings.Contains(
					err.Error(),
					"store: add crawl domain avoid rule",
				) ||
				!strings.Contains(
					err.Error(),
					"add crawl domain avoid rule",
				) {
				t.Fatalf(
					"AddDomainAvoidRule() error = %v",
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
							"INSERT 0 1",
						),
					},
				},
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

			changed, err := store.AddDomainAvoidRule(
				ctx,
				" Example.COM ",
			)
			if err != nil {
				t.Fatalf(
					"AddDomainAvoidRule() error = %v",
					err,
				)
			}

			if changed != 0 {
				t.Fatalf(
					"AddDomainAvoidRule() changed = %d, want 0",
					changed,
				)
			}
		},
	)
}

func TestRemoveDomainAvoidRuleDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"validation",
		func(t *testing.T) {
			var store *DiscoveryStore

			err := store.RemoveDomainAvoidRule(
				ctx,
				"example.com",
			)

			if !errors.Is(
				err,
				errDiscoveryStoreUnavailable,
			) {
				t.Fatalf(
					"RemoveDomainAvoidRule() error = %v, want %v",
					err,
					errDiscoveryStoreUnavailable,
				)
			}
		},
	)

	t.Run(
		"invalid pattern",
		func(t *testing.T) {
			store := discoveryPolicyStore(
				&discoveryPolicyUnitDatabase{
					discoveryUnitDatabase: &discoveryUnitDatabase{},
				},
			)

			err := store.RemoveDomainAvoidRule(
				ctx,
				"example.com,example.org",
			)

			if err == nil ||
				!strings.Contains(
					err.Error(),
					"crawl domain avoid rule is invalid",
				) {
				t.Fatalf(
					"RemoveDomainAvoidRule() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"delete failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test remove domain avoid rule failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store := discoveryPolicyStore(
				discoveryPolicyDatabase(tx),
			)

			err := store.RemoveDomainAvoidRule(
				ctx,
				"example.com",
			)

			if !errors.Is(err, testErr) ||
				!strings.Contains(
					err.Error(),
					"store: remove crawl domain avoid rule",
				) {
				t.Fatalf(
					"RemoveDomainAvoidRule() error = %v",
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
							"DELETE 1",
						),
					},
				},
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

			if err := store.RemoveDomainAvoidRule(
				ctx,
				" EXAMPLE.COM ",
			); err != nil {
				t.Fatalf(
					"RemoveDomainAvoidRule() error = %v",
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

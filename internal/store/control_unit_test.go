package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/control"
)

type controlUnitBeginner struct {
	tx  pgx.Tx
	err error
}

func (beginner *controlUnitBeginner) Begin(
	context.Context,
) (pgx.Tx, error) {
	if beginner.err != nil {
		return nil, beginner.err
	}

	return beginner.tx, nil
}

type controlUnitExecResult struct {
	tag pgconn.CommandTag
	err error
}

type controlUnitTx struct {
	pgx.Tx

	execResults []controlUnitExecResult
	execIndex   int

	queryResults []storeFakeQueryResult
	queryIndex   int

	rowResults []storeFakeRow
	rowIndex   int

	commitErr   error
	rollbackErr error
	committed   bool
	rolledBack  bool
}

func (tx *controlUnitTx) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	if tx.execIndex >= len(tx.execResults) {
		return pgconn.CommandTag{},
			errUnexpectedStoreDatabaseCall
	}

	result := tx.execResults[tx.execIndex]
	tx.execIndex++

	return result.tag, result.err
}

func (tx *controlUnitTx) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	if tx.queryIndex >= len(tx.queryResults) {
		return nil,
			errUnexpectedStoreDatabaseCall
	}

	result := tx.queryResults[tx.queryIndex]
	tx.queryIndex++

	return result.rows, result.err
}

func (tx *controlUnitTx) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	if tx.rowIndex >= len(tx.rowResults) {
		return storeFakeRow{
			err: errUnexpectedStoreDatabaseCall,
		}
	}

	result := tx.rowResults[tx.rowIndex]
	tx.rowIndex++

	return result
}

func (tx *controlUnitTx) Commit(
	context.Context,
) error {
	tx.committed = true

	return tx.commitErr
}

func (tx *controlUnitTx) Rollback(
	context.Context,
) error {
	tx.rolledBack = true

	return tx.rollbackErr
}

func TestControlStoreConstructorsWithoutDatabase(
	t *testing.T,
) {
	if _, err := NewControlStore(nil); !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"NewControlStore(nil) error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}

	store, err := NewControlStore(
		new(pgxpool.Pool),
	)
	if err != nil {
		t.Fatalf(
			"NewControlStore(pool) error = %v",
			err,
		)
	}

	if store == nil {
		t.Fatal(
			"NewControlStore(pool) = nil",
		)
	}

	if _, err := newControlStore(nil); !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"newControlStore(nil) error = %v, want %v",
			err,
			errPoolUnavailable,
		)
	}

	unitStore, err := newControlStore(
		&controlUnitBeginner{
			tx: &controlUnitTx{},
		},
	)
	if err != nil {
		t.Fatalf(
			"newControlStore() error = %v",
			err,
		)
	}

	if unitStore == nil {
		t.Fatal(
			"newControlStore() = nil",
		)
	}
}

func TestControlStoreSetProcessorPausedWithoutDatabase(
	t *testing.T,
) {
	t.Run(
		"pause discovery",
		func(t *testing.T) {
			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
						),
					},
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.SetProcessorPaused(
				context.Background(),
				"discovery",
				true,
				controlUnitAudit(
					"processor.pause",
					"discovery",
					control.ResultSuccess,
				),
			)
			if err != nil {
				t.Fatalf(
					"SetProcessorPaused() error = %v",
					err,
				)
			}

			if !tx.committed {
				t.Fatal(
					"SetProcessorPaused() did not commit",
				)
			}
		},
	)

	t.Run(
		"resume verification",
		func(t *testing.T) {
			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
						),
					},
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.SetProcessorPaused(
				context.Background(),
				"verification",
				false,
				controlUnitAudit(
					"processor.resume",
					"verification",
					control.ResultSuccess,
				),
			)
			if err != nil {
				t.Fatalf(
					"SetProcessorPaused() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"invalid processor",
		func(t *testing.T) {
			store := mustControlUnitStore(
				t,
				&controlUnitTx{},
			)

			err := store.SetProcessorPaused(
				context.Background(),
				"other",
				true,
				controlUnitAudit(
					"processor.pause",
					"other",
					control.ResultSuccess,
				),
			)

			if !errors.Is(
				err,
				errInvalidServiceState,
			) {
				t.Fatalf(
					"SetProcessorPaused() error = %v, want %v",
					err,
					errInvalidServiceState,
				)
			}
		},
	)

	t.Run(
		"inconsistent audit",
		func(t *testing.T) {
			store := mustControlUnitStore(
				t,
				&controlUnitTx{},
			)

			err := store.SetProcessorPaused(
				context.Background(),
				"discovery",
				true,
				controlUnitAudit(
					"processor.resume",
					"discovery",
					control.ResultSuccess,
				),
			)

			if err == nil {
				t.Fatal(
					"SetProcessorPaused() error = nil",
				)
			}
		},
	)

	t.Run(
		"update failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test processor update failure",
			)

			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.SetProcessorPaused(
				context.Background(),
				"discovery",
				true,
				controlUnitAudit(
					"processor.pause",
					"discovery",
					control.ResultSuccess,
				),
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"set processor state",
				) {
				t.Fatalf(
					"SetProcessorPaused() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"singleton unavailable",
		func(t *testing.T) {
			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 0",
						),
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.SetProcessorPaused(
				context.Background(),
				"discovery",
				true,
				controlUnitAudit(
					"processor.pause",
					"discovery",
					control.ResultSuccess,
				),
			)

			if err == nil ||
				!strings.Contains(
					err.Error(),
					"singleton unavailable",
				) {
				t.Fatalf(
					"SetProcessorPaused() error = %v",
					err,
				)
			}
		},
	)
}

func TestControlStoreAddDomainAvoidWithoutDatabase(
	t *testing.T,
) {
	t.Run(
		"success",
		func(t *testing.T) {
			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
				queryResults: []storeFakeQueryResult{
					{
						rows: newStoreFakeRows(),
					},
					{
						rows: newStoreFakeRows(),
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.AddDomainAvoid(
				context.Background(),
				" Example.COM ",
				controlUnitAudit(
					"domain-avoid.add",
					"example.com",
					control.ResultSuccess,
				),
			)
			if err != nil {
				t.Fatalf(
					"AddDomainAvoid() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"invalid pattern",
		func(t *testing.T) {
			store := mustControlUnitStore(
				t,
				&controlUnitTx{},
			)

			err := store.AddDomainAvoid(
				context.Background(),
				"bad,rule",
				controlUnitAudit(
					"domain-avoid.add",
					"bad,rule",
					control.ResultSuccess,
				),
			)

			if err == nil {
				t.Fatal(
					"AddDomainAvoid() error = nil",
				)
			}
		},
	)

	t.Run(
		"inconsistent audit",
		func(t *testing.T) {
			store := mustControlUnitStore(
				t,
				&controlUnitTx{},
			)

			err := store.AddDomainAvoid(
				context.Background(),
				"example.com",
				controlUnitAudit(
					"wrong",
					"example.com",
					control.ResultSuccess,
				),
			)

			if err == nil {
				t.Fatal(
					"AddDomainAvoid() error = nil",
				)
			}
		},
	)

	t.Run(
		"insert failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test add rule failure",
			)

			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.AddDomainAvoid(
				context.Background(),
				"example.com",
				controlUnitAudit(
					"domain-avoid.add",
					"example.com",
					control.ResultSuccess,
				),
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"add domain avoid rule",
				) {
				t.Fatalf(
					"AddDomainAvoid() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"reconciliation failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test add reconciliation failure",
			)

			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
				queryResults: []storeFakeQueryResult{
					{
						err: testErr,
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.AddDomainAvoid(
				context.Background(),
				"example.com",
				controlUnitAudit(
					"domain-avoid.add",
					"example.com",
					control.ResultSuccess,
				),
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"reconcile domain avoid rule",
				) {
				t.Fatalf(
					"AddDomainAvoid() error = %v",
					err,
				)
			}
		},
	)
}

func TestControlStoreRemoveDomainAvoidWithoutDatabase(
	t *testing.T,
) {
	t.Run(
		"success",
		func(t *testing.T) {
			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"DELETE 1",
						),
					},
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
				queryResults: []storeFakeQueryResult{
					{
						rows: newStoreFakeRows(),
					},
					{
						rows: newStoreFakeRows(),
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.RemoveDomainAvoid(
				context.Background(),
				" Example.COM ",
				controlUnitAudit(
					"domain-avoid.remove",
					"example.com",
					control.ResultSuccess,
				),
			)
			if err != nil {
				t.Fatalf(
					"RemoveDomainAvoid() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"invalid pattern",
		func(t *testing.T) {
			store := mustControlUnitStore(
				t,
				&controlUnitTx{},
			)

			err := store.RemoveDomainAvoid(
				context.Background(),
				"",
				controlUnitAudit(
					"domain-avoid.remove",
					"",
					control.ResultSuccess,
				),
			)

			if err == nil {
				t.Fatal(
					"RemoveDomainAvoid() error = nil",
				)
			}
		},
	)

	t.Run(
		"inconsistent audit",
		func(t *testing.T) {
			store := mustControlUnitStore(
				t,
				&controlUnitTx{},
			)

			err := store.RemoveDomainAvoid(
				context.Background(),
				"example.com",
				controlUnitAudit(
					"wrong",
					"example.com",
					control.ResultSuccess,
				),
			)

			if err == nil {
				t.Fatal(
					"RemoveDomainAvoid() error = nil",
				)
			}
		},
	)

	t.Run(
		"delete failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test remove rule failure",
			)

			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.RemoveDomainAvoid(
				context.Background(),
				"example.com",
				controlUnitAudit(
					"domain-avoid.remove",
					"example.com",
					control.ResultSuccess,
				),
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"remove domain avoid rule",
				) {
				t.Fatalf(
					"RemoveDomainAvoid() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"reconciliation failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test remove reconciliation failure",
			)

			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"DELETE 1",
						),
					},
				},
				queryResults: []storeFakeQueryResult{
					{
						err: testErr,
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.RemoveDomainAvoid(
				context.Background(),
				"example.com",
				controlUnitAudit(
					"domain-avoid.remove",
					"example.com",
					control.ResultSuccess,
				),
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"reconcile removed domain avoid rule",
				) {
				t.Fatalf(
					"RemoveDomainAvoid() error = %v",
					err,
				)
			}
		},
	)
}

func TestControlStoreSetOriginBlockedWithoutDatabase(
	t *testing.T,
) {
	t.Run(
		"block",
		func(t *testing.T) {
			tx := &controlUnitTx{
				rowResults: []storeFakeRow{
					{
						values: []any{
							false,
							false,
						},
					},
				},
				execResults: []controlUnitExecResult{
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
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.SetOriginBlocked(
				context.Background(),
				"https://example.com",
				true,
				controlUnitAudit(
					"origin.block",
					"https://example.com",
					control.ResultSuccess,
				),
			)
			if err != nil {
				t.Fatalf(
					"SetOriginBlocked() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"allow",
		func(t *testing.T) {
			tx := &controlUnitTx{
				rowResults: []storeFakeRow{
					{
						values: []any{
							false,
							false,
						},
					},
				},
				execResults: []controlUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
						),
					},
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.SetOriginBlocked(
				context.Background(),
				"https://example.com",
				false,
				controlUnitAudit(
					"origin.allow",
					"https://example.com",
					control.ResultSuccess,
				),
			)
			if err != nil {
				t.Fatalf(
					"SetOriginBlocked() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"invalid origin",
		func(t *testing.T) {
			store := mustControlUnitStore(
				t,
				&controlUnitTx{},
			)

			err := store.SetOriginBlocked(
				context.Background(),
				"not-an-origin",
				true,
				controlUnitAudit(
					"origin.block",
					"not-an-origin",
					control.ResultSuccess,
				),
			)

			if !errors.Is(
				err,
				errInvalidOrigin,
			) {
				t.Fatalf(
					"SetOriginBlocked() error = %v, want %v",
					err,
					errInvalidOrigin,
				)
			}
		},
	)

	t.Run(
		"inconsistent audit",
		func(t *testing.T) {
			store := mustControlUnitStore(
				t,
				&controlUnitTx{},
			)

			err := store.SetOriginBlocked(
				context.Background(),
				"https://example.com",
				true,
				controlUnitAudit(
					"origin.allow",
					"https://example.com",
					control.ResultSuccess,
				),
			)

			if err == nil {
				t.Fatal(
					"SetOriginBlocked() error = nil",
				)
			}
		},
	)

	t.Run(
		"transaction helper failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test exact block failure",
			)

			tx := &controlUnitTx{
				rowResults: []storeFakeRow{
					{
						err: testErr,
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.SetOriginBlocked(
				context.Background(),
				"https://example.com",
				true,
				controlUnitAudit(
					"origin.block",
					"https://example.com",
					control.ResultSuccess,
				),
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"set origin block",
				) {
				t.Fatalf(
					"SetOriginBlocked() error = %v",
					err,
				)
			}
		},
	)
}

func TestControlStoreRecordRejectedWithoutDatabase(
	t *testing.T,
) {
	t.Run(
		"success",
		func(t *testing.T) {
			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.RecordRejected(
				context.Background(),
				controlUnitAudit(
					"origin.block",
					"https://example.com",
					control.ResultRejected,
				),
			)
			if err != nil {
				t.Fatalf(
					"RecordRejected() error = %v",
					err,
				)
			}

			if !tx.committed {
				t.Fatal(
					"RecordRejected() did not commit",
				)
			}
		},
	)

	t.Run(
		"validation failure",
		func(t *testing.T) {
			store := &ControlStore{}

			err := store.RecordRejected(
				context.Background(),
				controlUnitAudit(
					"test",
					"test",
					control.ResultRejected,
				),
			)

			if !errors.Is(
				err,
				errPoolUnavailable,
			) {
				t.Fatalf(
					"RecordRejected() error = %v, want %v",
					err,
					errPoolUnavailable,
				)
			}
		},
	)

	t.Run(
		"invalid result",
		func(t *testing.T) {
			store := mustControlUnitStore(
				t,
				&controlUnitTx{},
			)

			err := store.RecordRejected(
				context.Background(),
				controlUnitAudit(
					"test",
					"test",
					control.ResultSuccess,
				),
			)

			if err == nil {
				t.Fatal(
					"RecordRejected() error = nil",
				)
			}
		},
	)

	t.Run(
		"begin failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test rejected begin failure",
			)

			store, err := newControlStore(
				&controlUnitBeginner{
					err: testErr,
				},
			)
			if err != nil {
				t.Fatalf(
					"newControlStore() error = %v",
					err,
				)
			}

			err = store.RecordRejected(
				context.Background(),
				controlUnitAudit(
					"test",
					"test",
					control.ResultRejected,
				),
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: record rejected control audit",
				) {
				t.Fatalf(
					"RecordRejected() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"audit failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test rejected audit failure",
			)

			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.RecordRejected(
				context.Background(),
				controlUnitAudit(
					"test",
					"test",
					control.ResultRejected,
				),
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"insert operator audit",
				) {
				t.Fatalf(
					"RecordRejected() error = %v",
					err,
				)
			}
		},
	)
}

func TestControlStoreMutateWithoutDatabase(
	t *testing.T,
) {
	successAudit := controlUnitAudit(
		"test",
		"test",
		control.ResultSuccess,
	)

	t.Run(
		"validation failure",
		func(t *testing.T) {
			store := &ControlStore{}

			err := store.mutate(
				context.Background(),
				successAudit,
				func(pgx.Tx) error {
					return nil
				},
			)

			if !errors.Is(
				err,
				errPoolUnavailable,
			) {
				t.Fatalf(
					"mutate() error = %v, want %v",
					err,
					errPoolUnavailable,
				)
			}
		},
	)

	t.Run(
		"nil mutation",
		func(t *testing.T) {
			store := mustControlUnitStore(
				t,
				&controlUnitTx{},
			)

			if err := store.mutate(
				context.Background(),
				successAudit,
				nil,
			); err == nil {
				t.Fatal(
					"mutate(nil) error = nil",
				)
			}
		},
	)

	t.Run(
		"rejected audit",
		func(t *testing.T) {
			store := mustControlUnitStore(
				t,
				&controlUnitTx{},
			)

			err := store.mutate(
				context.Background(),
				controlUnitAudit(
					"test",
					"test",
					control.ResultRejected,
				),
				func(pgx.Tx) error {
					return nil
				},
			)

			if err == nil {
				t.Fatal(
					"mutate(rejected audit) error = nil",
				)
			}
		},
	)

	t.Run(
		"mutation failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test mutation failure",
			)

			tx := &controlUnitTx{}
			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.mutate(
				context.Background(),
				successAudit,
				func(pgx.Tx) error {
					return testErr
				},
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: apply control mutation",
				) {
				t.Fatalf(
					"mutate() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"audit failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test audit failure",
			)

			tx := &controlUnitTx{
				execResults: []controlUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store := mustControlUnitStore(
				t,
				tx,
			)

			err := store.mutate(
				context.Background(),
				successAudit,
				func(pgx.Tx) error {
					return nil
				},
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"insert operator audit",
				) {
				t.Fatalf(
					"mutate() error = %v",
					err,
				)
			}
		},
	)
}

func TestControlStoreValidateWithoutDatabase(
	t *testing.T,
) {
	var nilStore *ControlStore

	if err := nilStore.validate(
		context.Background(),
	); !errors.Is(
		err,
		errControlStoreUnavailable,
	) {
		t.Fatalf(
			"nil validate() error = %v, want %v",
			err,
			errControlStoreUnavailable,
		)
	}

	store := mustControlUnitStore(
		t,
		&controlUnitTx{},
	)

	if err := store.validate(nil); !errors.Is(
		err,
		errInvalidContext,
	) {
		t.Fatalf(
			"validate(nil) error = %v, want %v",
			err,
			errInvalidContext,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := store.validate(ctx); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf(
			"validate(canceled) error = %v",
			err,
		)
	}

	if err := (&ControlStore{}).validate(
		context.Background(),
	); !errors.Is(
		err,
		errPoolUnavailable,
	) {
		t.Fatalf(
			"validate(nil pool) error = %v",
			err,
		)
	}

	if err := store.validate(
		context.Background(),
	); err != nil {
		t.Fatalf(
			"validate() error = %v",
			err,
		)
	}
}

func TestControlAuditMatchesWithoutDatabase(
	t *testing.T,
) {
	audit := controlUnitAudit(
		"origin.block",
		"https://example.com",
		control.ResultSuccess,
	)

	if !controlAuditMatches(
		audit,
		"origin.block",
		"https://example.com",
	) {
		t.Fatal(
			"controlAuditMatches() = false, want true",
		)
	}

	if controlAuditMatches(
		audit,
		"origin.allow",
		"https://example.com",
	) {
		t.Fatal(
			"controlAuditMatches() = true for wrong action",
		)
	}

	if controlAuditMatches(
		audit,
		"origin.block",
		"https://other.example",
	) {
		t.Fatal(
			"controlAuditMatches() = true for wrong target",
		)
	}
}

func mustControlUnitStore(
	t *testing.T,
	tx pgx.Tx,
) *ControlStore {
	t.Helper()

	store, err := newControlStore(
		&controlUnitBeginner{
			tx: tx,
		},
	)
	if err != nil {
		t.Fatalf(
			"newControlStore() error = %v",
			err,
		)
	}

	return store
}

func controlUnitAudit(
	action string,
	target string,
	result string,
) control.Audit {
	return control.Audit{
		OccurredAt: time.Date(
			2026,
			time.September,
			18,
			12,
			0,
			0,
			0,
			time.UTC,
		),
		Action: action,
		Target: target,
		Caller: "192.0.2.1",
		Actor:  control.DefaultActor,
		Result: result,
		Reason: "unit test",
	}
}

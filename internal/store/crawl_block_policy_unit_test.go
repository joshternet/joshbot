package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestSetExactCrawlBlockTransactionWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"block non automatic source",
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
				},
			}

			err := setExactCrawlBlockTransaction(
				ctx,
				tx,
				mustStoreOrigin(
					t,
					"https://example.com",
				),
				true,
				AutomaticCrawlConfig{},
			)
			if err != nil {
				t.Fatalf(
					"setExactCrawlBlockTransaction() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"allow non automatic source",
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
				},
			}

			err := setExactCrawlBlockTransaction(
				ctx,
				tx,
				mustStoreOrigin(
					t,
					"https://example.com",
				),
				false,
				AutomaticCrawlConfig{},
			)
			if err != nil {
				t.Fatalf(
					"setExactCrawlBlockTransaction() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"automatic source remains blocked by exclusion",
		func(t *testing.T) {
			tx := &controlUnitTx{
				rowResults: []storeFakeRow{
					{
						values: []any{
							false,
							true,
						},
					},
				},
				queryResults: []storeFakeQueryResult{
					{
						rows: newStoreFakeRows(
							[]any{
								"example.com",
							},
						),
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
				},
			}

			err := setExactCrawlBlockTransaction(
				ctx,
				tx,
				mustStoreOrigin(
					t,
					"https://auto.example.com",
				),
				false,
				AutomaticCrawlConfig{},
			)
			if err != nil {
				t.Fatalf(
					"setExactCrawlBlockTransaction() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"automatic source allowed without exclusion",
		func(t *testing.T) {
			tx := &controlUnitTx{
				rowResults: []storeFakeRow{
					{
						values: []any{
							false,
							true,
						},
					},
				},
				queryResults: []storeFakeQueryResult{
					{
						rows: newStoreFakeRows(
							[]any{
								"other.test",
							},
						),
					},
				},
				execResults: []controlUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
						),
					},
				},
			}

			err := setExactCrawlBlockTransaction(
				ctx,
				tx,
				mustStoreOrigin(
					t,
					"https://auto.example.com",
				),
				false,
				AutomaticCrawlConfig{},
			)
			if err != nil {
				t.Fatalf(
					"setExactCrawlBlockTransaction() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"seed skips automatic exclusion",
		func(t *testing.T) {
			tx := &controlUnitTx{
				rowResults: []storeFakeRow{
					{
						values: []any{
							true,
							true,
						},
					},
				},
				execResults: []controlUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
						),
					},
				},
			}

			err := setExactCrawlBlockTransaction(
				ctx,
				tx,
				mustStoreOrigin(
					t,
					"https://seed.example.com",
				),
				false,
				AutomaticCrawlConfig{},
			)
			if err != nil {
				t.Fatalf(
					"setExactCrawlBlockTransaction() error = %v",
					err,
				)
			}
		},
	)
}

func TestSetExactCrawlBlockTransactionFailuresWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"state query",
		func(t *testing.T) {
			testErr := errors.New(
				"test exact state failure",
			)

			tx := &controlUnitTx{
				rowResults: []storeFakeRow{
					{
						err: testErr,
					},
				},
			}

			err := setExactCrawlBlockTransaction(
				ctx,
				tx,
				mustStoreOrigin(
					t,
					"https://example.com",
				),
				true,
				AutomaticCrawlConfig{},
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store exact crawl block",
				) {
				t.Fatalf(
					"setExactCrawlBlockTransaction() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"exclusion query",
		func(t *testing.T) {
			testErr := errors.New(
				"test exclusion query failure",
			)

			tx := &controlUnitTx{
				rowResults: []storeFakeRow{
					{
						values: []any{
							false,
							true,
						},
					},
				},
				queryResults: []storeFakeQueryResult{
					{
						err: testErr,
					},
				},
			}

			err := setExactCrawlBlockTransaction(
				ctx,
				tx,
				mustStoreOrigin(
					t,
					"https://auto.example.com",
				),
				false,
				AutomaticCrawlConfig{},
			)

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"setExactCrawlBlockTransaction() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"exclusion collection",
		func(t *testing.T) {
			tx := &controlUnitTx{
				rowResults: []storeFakeRow{
					{
						values: []any{
							false,
							true,
						},
					},
				},
				queryResults: []storeFakeQueryResult{
					{
						rows: newStoreFakeRows(
							[]any{},
						),
					},
				},
			}

			err := setExactCrawlBlockTransaction(
				ctx,
				tx,
				mustStoreOrigin(
					t,
					"https://auto.example.com",
				),
				false,
				AutomaticCrawlConfig{},
			)

			if err == nil ||
				!strings.Contains(
					err.Error(),
					"store: collect crawl domain avoid rules",
				) {
				t.Fatalf(
					"setExactCrawlBlockTransaction() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"effective state update",
		func(t *testing.T) {
			testErr := errors.New(
				"test effective block update failure",
			)

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
						err: testErr,
					},
				},
			}

			err := setExactCrawlBlockTransaction(
				ctx,
				tx,
				mustStoreOrigin(
					t,
					"https://example.com",
				),
				false,
				AutomaticCrawlConfig{},
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"update effective crawl block",
				) {
				t.Fatalf(
					"setExactCrawlBlockTransaction() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"blocked probe removal",
		func(t *testing.T) {
			testErr := errors.New(
				"test blocked probe removal failure",
			)

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
						err: testErr,
					},
				},
			}

			err := setExactCrawlBlockTransaction(
				ctx,
				tx,
				mustStoreOrigin(
					t,
					"https://example.com",
				),
				true,
				AutomaticCrawlConfig{},
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"remove blocked origin probe",
				) {
				t.Fatalf(
					"setExactCrawlBlockTransaction() error = %v",
					err,
				)
			}
		},
	)
}

func TestRecomputeAutomaticCrawlBlocksTransactionWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	tx := &controlUnitTx{
		queryResults: []storeFakeQueryResult{
			{
				rows: newStoreFakeRows(
					[]any{
						"blocked.example",
					},
				),
			},
			{
				rows: newStoreFakeRows(
					[]any{
						"https://same.blocked.example",
						false,
						true,
					},
					[]any{
						"https://change.blocked.example",
						false,
						false,
					},
					[]any{
						"https://operator.test",
						true,
						false,
					},
					[]any{
						"https://allow.test",
						false,
						true,
					},
					[]any{
						"https://same-allowed.test",
						false,
						false,
					},
				),
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
					"UPDATE 1",
				),
			},
			{
				tag: pgconn.NewCommandTag(
					"UPDATE 1",
				),
			},
			{
				tag: pgconn.NewCommandTag(
					"DELETE 3",
				),
			},
		},
	}

	changed, err := recomputeAutomaticCrawlBlocksTransaction(
		ctx,
		tx,
		AutomaticCrawlConfig{},
	)
	if err != nil {
		t.Fatalf(
			"recomputeAutomaticCrawlBlocksTransaction() error = %v",
			err,
		)
	}

	if changed != 3 {
		t.Fatalf(
			"recomputeAutomaticCrawlBlocksTransaction() changed = %d, want 3",
			changed,
		)
	}
}

func TestRecomputeAutomaticCrawlBlocksTransactionWithNoSources(
	t *testing.T,
) {
	tx := &controlUnitTx{
		queryResults: []storeFakeQueryResult{
			{
				rows: newStoreFakeRows(),
			},
			{
				rows: newStoreFakeRows(),
			},
		},
	}

	changed, err := recomputeAutomaticCrawlBlocksTransaction(
		context.Background(),
		tx,
		AutomaticCrawlConfig{},
	)
	if err != nil {
		t.Fatalf(
			"recomputeAutomaticCrawlBlocksTransaction() error = %v",
			err,
		)
	}

	if changed != 0 {
		t.Fatalf(
			"recomputeAutomaticCrawlBlocksTransaction() changed = %d, want 0",
			changed,
		)
	}
}

func TestRecomputeAutomaticCrawlBlocksTransactionFailuresWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"exclusion query",
		func(t *testing.T) {
			testErr := errors.New(
				"test exclusion failure",
			)

			tx := &controlUnitTx{
				queryResults: []storeFakeQueryResult{
					{
						err: testErr,
					},
				},
			}

			changed, err := recomputeAutomaticCrawlBlocksTransaction(
				ctx,
				tx,
				AutomaticCrawlConfig{},
			)

			if changed != 0 ||
				!errors.Is(
					err,
					testErr,
				) {
				t.Fatalf(
					"recomputeAutomaticCrawlBlocksTransaction() = %d, %v",
					changed,
					err,
				)
			}
		},
	)

	t.Run(
		"state query",
		func(t *testing.T) {
			testErr := errors.New(
				"test state query failure",
			)

			tx := &controlUnitTx{
				queryResults: []storeFakeQueryResult{
					{
						rows: newStoreFakeRows(),
					},
					{
						err: testErr,
					},
				},
			}

			changed, err := recomputeAutomaticCrawlBlocksTransaction(
				ctx,
				tx,
				AutomaticCrawlConfig{},
			)

			if changed != 0 ||
				!errors.Is(
					err,
					testErr,
				) ||
				!strings.Contains(
					err.Error(),
					"list automatic crawl blocks",
				) {
				t.Fatalf(
					"recomputeAutomaticCrawlBlocksTransaction() = %d, %v",
					changed,
					err,
				)
			}
		},
	)

	t.Run(
		"state collection",
		func(t *testing.T) {
			tx := &controlUnitTx{
				queryResults: []storeFakeQueryResult{
					{
						rows: newStoreFakeRows(),
					},
					{
						rows: newStoreFakeRows(
							[]any{
								"https://example.com",
							},
						),
					},
				},
			}

			changed, err := recomputeAutomaticCrawlBlocksTransaction(
				ctx,
				tx,
				AutomaticCrawlConfig{},
			)

			if changed != 0 ||
				err == nil ||
				!strings.Contains(
					err.Error(),
					"collect automatic crawl blocks",
				) {
				t.Fatalf(
					"recomputeAutomaticCrawlBlocksTransaction() = %d, %v",
					changed,
					err,
				)
			}
		},
	)

	t.Run(
		"invalid stored origin",
		func(t *testing.T) {
			tx := &controlUnitTx{
				queryResults: []storeFakeQueryResult{
					{
						rows: newStoreFakeRows(),
					},
					{
						rows: newStoreFakeRows(
							[]any{
								"not-an-origin",
								false,
								false,
							},
						),
					},
				},
			}

			changed, err := recomputeAutomaticCrawlBlocksTransaction(
				ctx,
				tx,
				AutomaticCrawlConfig{},
			)

			if changed != 0 ||
				err == nil ||
				!strings.Contains(
					err.Error(),
					"invalid automatic crawl source",
				) {
				t.Fatalf(
					"recomputeAutomaticCrawlBlocksTransaction() = %d, %v",
					changed,
					err,
				)
			}
		},
	)

	t.Run(
		"state update",
		func(t *testing.T) {
			testErr := errors.New(
				"test automatic block update failure",
			)

			tx := &controlUnitTx{
				queryResults: []storeFakeQueryResult{
					{
						rows: newStoreFakeRows(),
					},
					{
						rows: newStoreFakeRows(
							[]any{
								"https://example.com",
								false,
								true,
							},
						),
					},
				},
				execResults: []controlUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			changed, err := recomputeAutomaticCrawlBlocksTransaction(
				ctx,
				tx,
				AutomaticCrawlConfig{},
			)

			if changed != 0 ||
				!errors.Is(
					err,
					testErr,
				) ||
				!strings.Contains(
					err.Error(),
					"update automatic crawl block",
				) {
				t.Fatalf(
					"recomputeAutomaticCrawlBlocksTransaction() = %d, %v",
					changed,
					err,
				)
			}
		},
	)

	t.Run(
		"blocked probe cleanup",
		func(t *testing.T) {
			testErr := errors.New(
				"test automatic probe cleanup failure",
			)

			tx := &controlUnitTx{
				queryResults: []storeFakeQueryResult{
					{
						rows: newStoreFakeRows(
							[]any{
								"example.com",
							},
						),
					},
					{
						rows: newStoreFakeRows(
							[]any{
								"https://auto.example.com",
								false,
								false,
							},
						),
					},
				},
				execResults: []controlUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
						),
					},
					{
						err: testErr,
					},
				},
			}

			changed, err := recomputeAutomaticCrawlBlocksTransaction(
				ctx,
				tx,
				AutomaticCrawlConfig{},
			)

			if changed != 0 ||
				!errors.Is(
					err,
					testErr,
				) ||
				!strings.Contains(
					err.Error(),
					"remove blocked automatic probes",
				) {
				t.Fatalf(
					"recomputeAutomaticCrawlBlocksTransaction() = %d, %v",
					changed,
					err,
				)
			}
		},
	)
}

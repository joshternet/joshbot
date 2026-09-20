package store

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/testutil"
)

type crawlBlockGoldenTx struct {
	pgx.Tx

	rows []storeFakeRow
	row  int

	queries []storeFakeQueryResult
	query   int

	operations []string
}

func (tx *crawlBlockGoldenTx) QueryRow(
	_ context.Context,
	_ string,
	args ...any,
) pgx.Row {
	if len(args) == 2 {
		source, sourceOK := args[0].(string)
		blocked, blockedOK := args[1].(bool)

		if sourceOK && blockedOK {
			tx.operations = append(
				tx.operations,
				fmt.Sprintf(
					"intent source=%s blocked=%t",
					source,
					blocked,
				),
			)
		}
	}

	if tx.row >= len(tx.rows) {
		return storeFakeRow{
			err: errUnexpectedStoreDatabaseCall,
		}
	}

	result := tx.rows[tx.row]
	tx.row++

	return result
}

func (tx *crawlBlockGoldenTx) Query(
	_ context.Context,
	_ string,
	_ ...any,
) (pgx.Rows, error) {
	if tx.query >= len(tx.queries) {
		return nil,
			errUnexpectedStoreDatabaseCall
	}

	result := tx.queries[tx.query]
	tx.query++

	return result.rows,
		result.err
}

func (tx *crawlBlockGoldenTx) Exec(
	_ context.Context,
	_ string,
	args ...any,
) (pgconn.CommandTag, error) {
	switch {
	case len(args) == 2:
		source, sourceOK := args[0].(string)
		blocked, blockedOK := args[1].(bool)

		if sourceOK && blockedOK {
			tx.operations = append(
				tx.operations,
				fmt.Sprintf(
					"update source=%s blocked=%t",
					source,
					blocked,
				),
			)

			return pgconn.NewCommandTag(
				"UPDATE 1",
			), nil
		}

	case len(args) == 1:
		if source, ok := args[0].(string); ok {
			tx.operations = append(
				tx.operations,
				fmt.Sprintf(
					"remove_probe source=%s",
					source,
				),
			)

			return pgconn.NewCommandTag(
				"DELETE 1",
			), nil
		}

		if sources, ok := args[0].([]string); ok {
			tx.operations = append(
				tx.operations,
				fmt.Sprintf(
					"remove_probes sources=%s",
					strings.Join(
						sources,
						",",
					),
				),
			)

			return pgconn.NewCommandTag(
				"DELETE 1",
			), nil
		}
	}

	return pgconn.CommandTag{},
		errUnexpectedStoreDatabaseCall
}

func TestCrawlBlockPolicyGolden(
	t *testing.T,
) {
	var snapshot bytes.Buffer

	renderExactBlockGolden(
		t,
		&snapshot,
	)

	snapshot.WriteByte('\n')

	renderAutomaticAvoidGolden(
		t,
		&snapshot,
	)

	snapshot.WriteByte('\n')

	renderReconciliationGolden(
		t,
		&snapshot,
	)

	if err := testutil.CheckGolden(
		"testdata/golden/crawl-block-policy.golden",
		snapshot.Bytes(),
	); err != nil {
		t.Fatalf(
			"crawl block policy golden error = %v",
			err,
		)
	}
}

func renderExactBlockGolden(
	t *testing.T,
	snapshot *bytes.Buffer,
) {
	t.Helper()

	tx := &crawlBlockGoldenTx{
		rows: []storeFakeRow{
			{
				values: []any{
					false,
					false,
				},
			},
		},
	}

	err := setExactCrawlBlockTransaction(
		context.Background(),
		tx,
		mustStoreOrigin(
			t,
			"https://example.com",
		),
		true,
		AutomaticCrawlConfig{},
	)

	fmt.Fprintln(
		snapshot,
		"=== exact block ===",
	)
	writeGoldenResult(
		snapshot,
		err,
	)
	writeGoldenOperations(
		snapshot,
		tx.operations,
	)
}

func renderAutomaticAvoidGolden(
	t *testing.T,
	snapshot *bytes.Buffer,
) {
	t.Helper()

	tx := &crawlBlockGoldenTx{
		rows: []storeFakeRow{
			{
				values: []any{
					false,
					true,
				},
			},
		},
		queries: []storeFakeQueryResult{
			{
				rows: newStoreFakeRows(
					[]any{
						"example.com",
					},
				),
			},
		},
	}

	err := setExactCrawlBlockTransaction(
		context.Background(),
		tx,
		mustStoreOrigin(
			t,
			"https://auto.example.com",
		),
		false,
		AutomaticCrawlConfig{},
	)

	fmt.Fprintln(
		snapshot,
		"=== allow automatic source with domain avoid ===",
	)
	writeGoldenResult(
		snapshot,
		err,
	)
	writeGoldenOperations(
		snapshot,
		tx.operations,
	)
}

func renderReconciliationGolden(
	t *testing.T,
	snapshot *bytes.Buffer,
) {
	t.Helper()

	tx := &crawlBlockGoldenTx{
		queries: []storeFakeQueryResult{
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
						"https://same.example.com",
						false,
						true,
					},
					[]any{
						"https://change.example.com",
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
					[]any{
						"https://configured.configured.test",
						false,
						false,
					},
				),
			},
		},
	}

	changed, err :=
		recomputeAutomaticCrawlBlocksTransaction(
			context.Background(),
			tx,
			AutomaticCrawlConfig{
				ExcludedHostSuffixes: "configured.test",
			},
		)

	fmt.Fprintln(
		snapshot,
		"=== reconcile automatic sources ===",
	)
	writeGoldenResult(
		snapshot,
		err,
	)
	fmt.Fprintf(
		snapshot,
		"changed=%d\n",
		changed,
	)
	writeGoldenOperations(
		snapshot,
		tx.operations,
	)
}

func writeGoldenResult(
	snapshot *bytes.Buffer,
	err error,
) {
	if err == nil {
		fmt.Fprintln(
			snapshot,
			"result=ok",
		)

		return
	}

	fmt.Fprintf(
		snapshot,
		"result=error:%s\n",
		err,
	)
}

func writeGoldenOperations(
	snapshot *bytes.Buffer,
	operations []string,
) {
	if len(operations) == 0 {
		fmt.Fprintln(
			snapshot,
			"operations=none",
		)

		return
	}

	for _, operation := range operations {
		fmt.Fprintf(
			snapshot,
			"operation=%s\n",
			operation,
		)
	}
}

package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/database"
	"github.com/joshternet/joshbot/internal/discovery"
)

var errUnexpectedObservabilityDatabaseCall = errors.New(
	"unexpected observability database call",
)

type observabilityUnitExecResult struct {
	tag pgconn.CommandTag
	err error
}

type observabilityUnitDatabase struct {
	beginTx  pgx.Tx
	beginErr error

	execResults []observabilityUnitExecResult
	execIndex   int

	rowResults []observabilityUnitRow
	rowIndex   int
}

var _ database.Postgres = (*observabilityUnitDatabase)(nil)

func (database *observabilityUnitDatabase) Begin(
	context.Context,
) (pgx.Tx, error) {
	if database.beginTx == nil &&
		database.beginErr == nil {
		return nil,
			errUnexpectedObservabilityDatabaseCall
	}

	return database.beginTx,
		database.beginErr
}

func (database *observabilityUnitDatabase) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	if database.execIndex >=
		len(database.execResults) {
		return pgconn.CommandTag{},
			errUnexpectedObservabilityDatabaseCall
	}

	result :=
		database.execResults[database.execIndex]
	database.execIndex++

	return result.tag, result.err
}

func (database *observabilityUnitDatabase) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	return nil,
		errUnexpectedObservabilityDatabaseCall
}

func (database *observabilityUnitDatabase) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	if database.rowIndex >=
		len(database.rowResults) {
		return observabilityUnitRow{
			err: errUnexpectedObservabilityDatabaseCall,
		}
	}

	result :=
		database.rowResults[database.rowIndex]
	database.rowIndex++

	return result
}

type observabilityUnitRow struct {
	values []any
	err    error
}

func (row observabilityUnitRow) Scan(
	destinations ...any,
) error {
	if row.err != nil {
		return row.err
	}

	if len(destinations) != len(row.values) {
		return fmt.Errorf(
			"observability row destination count = %d, want %d",
			len(destinations),
			len(row.values),
		)
	}

	for index, destination := range destinations {
		target := reflect.ValueOf(destination)
		if !target.IsValid() ||
			target.Kind() != reflect.Pointer ||
			target.IsNil() {
			return fmt.Errorf(
				"observability row destination %d is not a writable pointer",
				index,
			)
		}

		value := row.values[index]
		if value == nil {
			target.Elem().SetZero()
			continue
		}

		source := reflect.ValueOf(value)
		targetType := target.Elem().Type()

		switch {
		case source.Type().
			AssignableTo(targetType):
			target.Elem().Set(source)

		case source.Type().
			ConvertibleTo(targetType):
			target.Elem().Set(
				source.Convert(targetType),
			)

		default:
			return fmt.Errorf(
				"observability row value %d has type %s, want %s",
				index,
				source.Type(),
				targetType,
			)
		}
	}

	return nil
}

func TestBeginCrawlDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)
	config := validObservabilityCrawlConfig()

	t.Run(
		"success",
		func(t *testing.T) {
			database := &observabilityUnitDatabase{
				rowResults: []observabilityUnitRow{
					{
						values: []any{
							int64(42),
						},
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			runID, err := store.BeginCrawl(
				ctx,
				source,
				config,
			)
			if err != nil {
				t.Fatalf(
					"BeginCrawl() error = %v",
					err,
				)
			}

			if runID != 42 {
				t.Fatalf(
					"BeginCrawl() run ID = %d, want 42",
					runID,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test begin crawl failure",
			)

			database := &observabilityUnitDatabase{
				rowResults: []observabilityUnitRow{
					{
						err: testErr,
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			runID, err := store.BeginCrawl(
				ctx,
				source,
				config,
			)

			if runID != 0 {
				t.Fatalf(
					"BeginCrawl() run ID = %d, want 0",
					runID,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: begin crawl",
				) {
				t.Fatalf(
					"BeginCrawl() error = %v",
					err,
				)
			}
		},
	)
}

func TestRecordPageAttemptDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"success without status",
		func(t *testing.T) {
			database := &observabilityUnitDatabase{
				execResults: []observabilityUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			attempt :=
				validObservabilityPageAttempt()
			attempt.StatusCode = nil

			if err := store.RecordPageAttempt(
				ctx,
				1,
				attempt,
			); err != nil {
				t.Fatalf(
					"RecordPageAttempt() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"success with status",
		func(t *testing.T) {
			database := &observabilityUnitDatabase{
				execResults: []observabilityUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			status := 200
			attempt :=
				validObservabilityPageAttempt()
			attempt.StatusCode = &status

			if err := store.RecordPageAttempt(
				ctx,
				1,
				attempt,
			); err != nil {
				t.Fatalf(
					"RecordPageAttempt() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test record page failure",
			)

			database := &observabilityUnitDatabase{
				execResults: []observabilityUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			err := store.RecordPageAttempt(
				ctx,
				1,
				validObservabilityPageAttempt(),
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: record crawl page",
				) {
				t.Fatalf(
					"RecordPageAttempt() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"unknown run",
		func(t *testing.T) {
			database := &observabilityUnitDatabase{
				execResults: []observabilityUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 0",
						),
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			err := store.RecordPageAttempt(
				ctx,
				1,
				validObservabilityPageAttempt(),
			)

			if !errors.Is(
				err,
				errUnknownCrawlRun,
			) {
				t.Fatalf(
					"RecordPageAttempt() error = %v, want %v",
					err,
					errUnknownCrawlRun,
				)
			}
		},
	)
}

func TestFinishCrawlSummaryDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"success",
		func(t *testing.T) {
			database := &observabilityUnitDatabase{
				execResults: []observabilityUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
						),
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			err := store.FinishCrawlSummary(
				ctx,
				1,
				discovery.CrawlResult{},
				discovery.CrawlRunComplete,
				"",
				0,
			)
			if err != nil {
				t.Fatalf(
					"FinishCrawlSummary() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test finish crawl failure",
			)

			database := &observabilityUnitDatabase{
				execResults: []observabilityUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			err := store.FinishCrawlSummary(
				ctx,
				1,
				discovery.CrawlResult{},
				discovery.CrawlRunComplete,
				"",
				0,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: finish crawl",
				) {
				t.Fatalf(
					"FinishCrawlSummary() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"unknown run",
		func(t *testing.T) {
			database := &observabilityUnitDatabase{
				execResults: []observabilityUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 0",
						),
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			err := store.FinishCrawlSummary(
				ctx,
				1,
				discovery.CrawlResult{},
				discovery.CrawlRunComplete,
				"",
				0,
			)

			if !errors.Is(
				err,
				errUnknownCrawlRun,
			) {
				t.Fatalf(
					"FinishCrawlSummary() error = %v, want %v",
					err,
					errUnknownCrawlRun,
				)
			}
		},
	)
}

func TestPurgeCrawlTelemetryDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"success",
		func(t *testing.T) {
			database := &observabilityUnitDatabase{
				execResults: []observabilityUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"DELETE 3",
						),
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			count, err :=
				store.PurgeCrawlTelemetry(
					ctx,
					24*time.Hour,
				)
			if err != nil {
				t.Fatalf(
					"PurgeCrawlTelemetry() error = %v",
					err,
				)
			}

			if count != 3 {
				t.Fatalf(
					"PurgeCrawlTelemetry() count = %d, want 3",
					count,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test purge telemetry failure",
			)

			database := &observabilityUnitDatabase{
				execResults: []observabilityUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			count, err :=
				store.PurgeCrawlTelemetry(
					ctx,
					24*time.Hour,
				)

			if count != 0 {
				t.Fatalf(
					"PurgeCrawlTelemetry() count = %d, want 0",
					count,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: purge crawl telemetry",
				) {
				t.Fatalf(
					"PurgeCrawlTelemetry() error = %v",
					err,
				)
			}
		},
	)
}

func TestCrawlControlDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	updatedAt := time.Date(
		2026,
		time.September,
		18,
		18,
		0,
		0,
		0,
		time.UTC,
	)

	t.Run(
		"success",
		func(t *testing.T) {
			database := &observabilityUnitDatabase{
				rowResults: []observabilityUnitRow{
					{
						values: []any{
							true,
							false,
							updatedAt,
						},
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			control, err :=
				store.CrawlControl(ctx)
			if err != nil {
				t.Fatalf(
					"CrawlControl() error = %v",
					err,
				)
			}

			want := CrawlControl{
				DiscoveryPaused:    true,
				VerificationPaused: false,
				UpdatedAt:          updatedAt,
			}

			if control != want {
				t.Fatalf(
					"CrawlControl() = %#v, want %#v",
					control,
					want,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test crawl control failure",
			)

			database := &observabilityUnitDatabase{
				rowResults: []observabilityUnitRow{
					{
						err: testErr,
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			control, err :=
				store.CrawlControl(ctx)

			if control != (CrawlControl{}) {
				t.Fatalf(
					"CrawlControl() = %#v, want zero",
					control,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read crawl control",
				) {
				t.Fatalf(
					"CrawlControl() error = %v",
					err,
				)
			}
		},
	)
}

func TestDiscoveryPausedDatabasePathWithoutDatabase(
	t *testing.T,
) {
	database := &observabilityUnitDatabase{
		rowResults: []observabilityUnitRow{
			{
				values: []any{
					true,
					false,
					time.Date(
						2026,
						time.September,
						18,
						18,
						0,
						0,
						0,
						time.UTC,
					),
				},
			},
		},
	}

	store :=
		observabilityDatabaseStore(database)

	paused, err := store.DiscoveryPaused(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"DiscoveryPaused() error = %v",
			err,
		)
	}

	if !paused {
		t.Fatal(
			"DiscoveryPaused() = false, want true",
		)
	}
}

func TestSetProcessorPausedDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	for _, processor := range []string{
		"discovery",
		"verification",
	} {
		t.Run(
			processor,
			func(t *testing.T) {
				database := &observabilityUnitDatabase{
					execResults: []observabilityUnitExecResult{
						{
							tag: pgconn.NewCommandTag(
								"UPDATE 1",
							),
						},
					},
				}

				store :=
					observabilityDatabaseStore(
						database,
					)

				if err := store.SetProcessorPaused(
					ctx,
					processor,
					true,
				); err != nil {
					t.Fatalf(
						"SetProcessorPaused(%q) error = %v",
						processor,
						err,
					)
				}
			},
		)
	}

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test pause update failure",
			)

			database := &observabilityUnitDatabase{
				execResults: []observabilityUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			err := store.SetProcessorPaused(
				ctx,
				"discovery",
				true,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: set crawl control",
				) {
				t.Fatalf(
					"SetProcessorPaused() error = %v",
					err,
				)
			}
		},
	)
}

func TestUpsertServiceHeartbeatDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"success",
		func(t *testing.T) {
			database := &observabilityUnitDatabase{
				execResults: []observabilityUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			if err := store.UpsertServiceHeartbeat(
				ctx,
				validObservabilityHeartbeat(t),
			); err != nil {
				t.Fatalf(
					"UpsertServiceHeartbeat() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test heartbeat failure",
			)

			database := &observabilityUnitDatabase{
				execResults: []observabilityUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store :=
				observabilityDatabaseStore(database)

			err := store.UpsertServiceHeartbeat(
				ctx,
				validObservabilityHeartbeat(t),
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: write service heartbeat",
				) {
				t.Fatalf(
					"UpsertServiceHeartbeat() error = %v",
					err,
				)
			}
		},
	)
}

func observabilityDatabaseStore(
	storeDatabase database.Postgres,
) *DiscoveryStore {
	return &DiscoveryStore{
		pool:  storeDatabase,
		clock: databaseQueueClock{},
	}
}

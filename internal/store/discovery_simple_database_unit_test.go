package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/database"
	"github.com/joshternet/joshbot/internal/origin"
)

var errUnexpectedDiscoveryUnitDatabaseCall = errors.New(
	"unexpected discovery unit database call",
)

type discoveryUnitExecResult struct {
	tag pgconn.CommandTag
	err error
}

type discoveryUnitQueryResult struct {
	rows pgx.Rows
	err  error
}

type discoveryUnitDatabase struct {
	execResults []discoveryUnitExecResult
	execIndex   int

	queryResults []discoveryUnitQueryResult
	queryIndex   int

	rowResults []discoveryUnitRow
	rowIndex   int
}

var _ database.Postgres = (*discoveryUnitDatabase)(nil)

func (database *discoveryUnitDatabase) Begin(
	context.Context,
) (pgx.Tx, error) {
	return nil, errUnexpectedDiscoveryUnitDatabaseCall
}

func (database *discoveryUnitDatabase) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	if database.execIndex >= len(database.execResults) {
		return pgconn.CommandTag{},
			errUnexpectedDiscoveryUnitDatabaseCall
	}

	result := database.execResults[database.execIndex]
	database.execIndex++

	return result.tag, result.err
}

func (database *discoveryUnitDatabase) Query(
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

func (database *discoveryUnitDatabase) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	if database.rowIndex >= len(database.rowResults) {
		return discoveryUnitRow{
			err: errUnexpectedDiscoveryUnitDatabaseCall,
		}
	}

	result := database.rowResults[database.rowIndex]
	database.rowIndex++

	return result
}

type discoveryUnitRow struct {
	values []any
	err    error
}

func (row discoveryUnitRow) Scan(
	destinations ...any,
) error {
	if row.err != nil {
		return row.err
	}

	return assignDiscoveryUnitValues(
		destinations,
		row.values,
	)
}

type discoveryUnitRows struct {
	pgx.Rows

	rows  []discoveryUnitRow
	index int
	err   error
}

func (rows *discoveryUnitRows) Next() bool {
	if rows.index >= len(rows.rows) {
		return false
	}

	rows.index++
	return true
}

func (rows *discoveryUnitRows) Scan(
	destinations ...any,
) error {
	if rows.index == 0 ||
		rows.index > len(rows.rows) {
		return errUnexpectedDiscoveryUnitDatabaseCall
	}

	return rows.rows[rows.index-1].Scan(
		destinations...,
	)
}

func (rows *discoveryUnitRows) Err() error {
	return rows.err
}

func (rows *discoveryUnitRows) Close() {}

func assignDiscoveryUnitValues(
	destinations []any,
	values []any,
) error {
	if len(destinations) != len(values) {
		return fmt.Errorf(
			"discovery row destination count = %d, want %d",
			len(destinations),
			len(values),
		)
	}

	for index, destination := range destinations {
		target := reflect.ValueOf(destination)
		if !target.IsValid() ||
			target.Kind() != reflect.Pointer ||
			target.IsNil() {
			return fmt.Errorf(
				"discovery row destination %d is not a writable pointer",
				index,
			)
		}

		value := values[index]
		if value == nil {
			target.Elem().SetZero()
			continue
		}

		source := reflect.ValueOf(value)
		targetType := target.Elem().Type()

		switch {
		case source.Type().AssignableTo(targetType):
			target.Elem().Set(source)

		case source.Type().ConvertibleTo(targetType):
			target.Elem().Set(
				source.Convert(targetType),
			)

		default:
			return fmt.Errorf(
				"discovery row value %d has type %s, want %s",
				index,
				source.Type(),
				targetType,
			)
		}
	}

	return nil
}

func TestAutomaticExclusionsDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"stored and configured exclusions",
		func(t *testing.T) {
			database := &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{
									values: []any{
										"blogspot.*",
									},
								},
								{
									values: []any{
										"example.com",
									},
								},
							},
						},
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)
			store.automatic.ExcludedHostSuffixes =
				" configured.test "

			config, err := store.automaticExclusions(
				ctx,
			)
			if err != nil {
				t.Fatalf(
					"automaticExclusions() error = %v",
					err,
				)
			}

			if config.ExcludedHostSuffixes !=
				"blogspot.*,example.com,configured.test" {
				t.Fatalf(
					"automaticExclusions() = %q",
					config.ExcludedHostSuffixes,
				)
			}
		},
	)

	t.Run(
		"no configured exclusions",
		func(t *testing.T) {
			database := &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{
									values: []any{
										"example.com",
									},
								},
							},
						},
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			config, err := store.automaticExclusions(
				ctx,
			)
			if err != nil {
				t.Fatalf(
					"automaticExclusions() error = %v",
					err,
				)
			}

			if config.ExcludedHostSuffixes !=
				"example.com" {
				t.Fatalf(
					"automaticExclusions() = %q",
					config.ExcludedHostSuffixes,
				)
			}
		},
	)

	t.Run(
		"query failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test automatic exclusion query failure",
			)

			database := &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						err: testErr,
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			config, err := store.automaticExclusions(
				ctx,
			)

			if config != (AutomaticCrawlConfig{}) {
				t.Fatalf(
					"automaticExclusions() = %#v, want zero",
					config,
				)
			}

			if !errors.Is(err, testErr) ||
				!strings.Contains(
					err.Error(),
					"store: read crawl domain avoid rules",
				) {
				t.Fatalf(
					"automaticExclusions() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"scan failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test automatic exclusion scan failure",
			)

			database := &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{
									err: testErr,
								},
							},
						},
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			config, err := store.automaticExclusions(
				ctx,
			)

			if config != (AutomaticCrawlConfig{}) {
				t.Fatalf(
					"automaticExclusions() = %#v, want zero",
					config,
				)
			}

			if !errors.Is(err, testErr) ||
				!strings.Contains(
					err.Error(),
					"store: collect crawl domain avoid rules",
				) {
				t.Fatalf(
					"automaticExclusions() error = %v",
					err,
				)
			}
		},
	)
}

func TestAddCrawlSeedDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	t.Run(
		"success",
		func(t *testing.T) {
			database := &discoveryUnitDatabase{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			if err := store.AddCrawlSeed(
				ctx,
				source,
			); err != nil {
				t.Fatalf(
					"AddCrawlSeed() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test add seed failure",
			)

			database := &discoveryUnitDatabase{
				execResults: []discoveryUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			err := store.AddCrawlSeed(
				ctx,
				source,
			)

			if !errors.Is(err, testErr) ||
				!strings.Contains(
					err.Error(),
					"store: add crawl seed",
				) {
				t.Fatalf(
					"AddCrawlSeed() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"invalid origin",
		func(t *testing.T) {
			store := discoverySimpleDatabaseStore(
				&discoveryUnitDatabase{},
			)

			err := store.AddCrawlSeed(
				ctx,
				origin.Origin{},
			)

			if !errors.Is(
				err,
				errInvalidOrigin,
			) {
				t.Fatalf(
					"AddCrawlSeed() error = %v, want %v",
					err,
					errInvalidOrigin,
				)
			}
		},
	)
}

func TestRemoveCrawlSeedDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	t.Run(
		"success",
		func(t *testing.T) {
			database := &discoveryUnitDatabase{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"UPDATE 1",
						),
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			if err := store.RemoveCrawlSeed(
				ctx,
				source,
			); err != nil {
				t.Fatalf(
					"RemoveCrawlSeed() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test remove seed failure",
			)

			database := &discoveryUnitDatabase{
				execResults: []discoveryUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			err := store.RemoveCrawlSeed(
				ctx,
				source,
			)

			if !errors.Is(err, testErr) ||
				!strings.Contains(
					err.Error(),
					"store: remove crawl seed",
				) {
				t.Fatalf(
					"RemoveCrawlSeed() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"invalid origin",
		func(t *testing.T) {
			store := discoverySimpleDatabaseStore(
				&discoveryUnitDatabase{},
			)

			err := store.RemoveCrawlSeed(
				ctx,
				origin.Origin{},
			)

			if !errors.Is(
				err,
				errInvalidOrigin,
			) {
				t.Fatalf(
					"RemoveCrawlSeed() error = %v, want %v",
					err,
					errInvalidOrigin,
				)
			}
		},
	)
}

func TestCrawlSeedsDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run(
		"success",
		func(t *testing.T) {
			database := &discoveryUnitDatabase{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							[]string{
								"https://example.com",
								"https://example.org",
							},
						},
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			seeds, err := store.CrawlSeeds(ctx)
			if err != nil {
				t.Fatalf(
					"CrawlSeeds() error = %v",
					err,
				)
			}

			want := []origin.Origin{
				mustStoreOrigin(
					t,
					"https://example.com",
				),
				mustStoreOrigin(
					t,
					"https://example.org",
				),
			}

			if !reflect.DeepEqual(seeds, want) {
				t.Fatalf(
					"CrawlSeeds() = %#v, want %#v",
					seeds,
					want,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test crawl seeds failure",
			)

			database := &discoveryUnitDatabase{
				rowResults: []discoveryUnitRow{
					{
						err: testErr,
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			seeds, err := store.CrawlSeeds(ctx)

			if seeds != nil {
				t.Fatalf(
					"CrawlSeeds() = %#v, want nil",
					seeds,
				)
			}

			if !errors.Is(err, testErr) ||
				!strings.Contains(
					err.Error(),
					"store: list crawl seeds",
				) {
				t.Fatalf(
					"CrawlSeeds() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"invalid stored origin",
		func(t *testing.T) {
			database := &discoveryUnitDatabase{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							[]string{
								"not-an-origin",
							},
						},
					},
				},
			}

			store := discoverySimpleDatabaseStore(
				database,
			)

			seeds, err := store.CrawlSeeds(ctx)

			if seeds != nil {
				t.Fatalf(
					"CrawlSeeds() = %#v, want nil",
					seeds,
				)
			}

			if err == nil ||
				!strings.Contains(
					err.Error(),
					"store: invalid crawl seed",
				) {
				t.Fatalf(
					"CrawlSeeds() error = %v",
					err,
				)
			}
		},
	)
}

func discoverySimpleDatabaseStore(
	storeDatabase database.Postgres,
) *DiscoveryStore {
	return &DiscoveryStore{
		pool:  storeDatabase,
		clock: databaseQueueClock{},
	}
}

package reporting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresReaderConstructorsWithoutDatabase(
	t *testing.T,
) {
	config := PostgresConfig{
		AutomaticCrawlEnabled: true,
		MaxPendingProbes:      10,
	}

	if reader, err := NewPostgresReader(
		nil,
		config,
	); reader != nil ||
		!errors.Is(
			err,
			errPostgresPoolUnavailable,
		) {
		t.Fatalf(
			"NewPostgresReader(nil) = %#v, %v",
			reader,
			err,
		)
	}

	pool := new(pgxpool.Pool)

	if reader, err := NewPostgresReader(
		pool,
		PostgresConfig{},
	); reader != nil ||
		!errors.Is(
			err,
			errInvalidPostgresConfig,
		) {
		t.Fatalf(
			"NewPostgresReader(invalid config) = %#v, %v",
			reader,
			err,
		)
	}

	reader, err := NewPostgresReader(
		pool,
		config,
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	if reader == nil {
		t.Fatal(
			"NewPostgresReader() = nil",
		)
	}

	if reader, err := newPostgresReader(
		nil,
		config,
	); reader != nil ||
		!errors.Is(
			err,
			errPostgresPoolUnavailable,
		) {
		t.Fatalf(
			"newPostgresReader(nil) = %#v, %v",
			reader,
			err,
		)
	}
}

func TestPostgresReaderStatusWithoutDatabase(
	t *testing.T,
) {
	now := time.Date(
		2026,
		time.September,
		18,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	queryer := &fakeQueryer{
		rowResults: []fakeRow{
			{
				values: []any{
					true,
					false,
					now,
					int64(9),
					int64(3),
					int64(2),
					int64(4),
					int64(2),
					&now,
					int64(11),
					int64(2),
					int64(5),
					int64(3),
					int64(1),
					int64(9),
				},
			},
		},
		queryResults: []fakeQueryResult{
			{
				rows: newFakeRows(
					[]any{
						"worker",
						"worker-1",
						"running",
						"https://example.com",
						"working",
						now,
						now,
					},
				),
			},
		},
	}

	reader := mustUnitPostgresReader(
		t,
		queryer,
		PostgresConfig{
			AutomaticCrawlEnabled: true,
			MaxPendingProbes:      3,
		},
	)

	status, err := reader.Status(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"Status() error = %v",
			err,
		)
	}

	if !status.Control.DiscoveryPaused ||
		status.Control.VerificationPaused {
		t.Errorf(
			"control = %#v",
			status.Control,
		)
	}

	if !status.Backpressure.Active ||
		status.Backpressure.PendingProbes != 3 ||
		status.Backpressure.MaxPendingProbes != 3 {
		t.Errorf(
			"backpressure = %#v",
			status.Backpressure,
		)
	}

	if status.Queue.Total != 9 ||
		status.Queue.Probe != 3 ||
		status.Queue.Reprobe != 2 ||
		status.Queue.Recurring != 4 ||
		status.Queue.Leased != 2 {
		t.Errorf(
			"queue = %#v",
			status.Queue,
		)
	}

	if len(status.Services) != 1 ||
		status.Services[0].Service != "worker" {
		t.Errorf(
			"services = %#v",
			status.Services,
		)
	}
}

func TestPostgresReaderStatusFailuresWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test status failure",
	)
	now := time.Now().UTC()

	t.Run(
		"status row",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{
					rowResults: []fakeRow{
						{
							err: testErr,
						},
					},
				},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			_, err := reader.Status(
				context.Background(),
			)
			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"Status() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"services",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{
					rowResults: []fakeRow{
						{
							values: []any{
								false,
								false,
								now,
								int64(0),
								int64(0),
								int64(0),
								int64(0),
								int64(0),
								(*time.Time)(nil),
								int64(0),
								int64(0),
								int64(0),
								int64(0),
								int64(0),
								int64(0),
							},
						},
					},
					queryResults: []fakeQueryResult{
						{
							err: testErr,
						},
					},
				},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			_, err := reader.Status(
				context.Background(),
			)
			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"Status() error = %v",
					err,
				)
			}
		},
	)
}

func TestPostgresReaderSourcesWithoutDatabase(
	t *testing.T,
) {
	now := time.Now().UTC()

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					rows: newFakeRows(
						[]any{
							"https://example.com",
							true,
							true,
							false,
							true,
							true,
							&now,
							&now,
						},
					),
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	sources, err := reader.Sources(
		context.Background(),
		1,
	)
	if err != nil {
		t.Fatalf(
			"Sources() error = %v",
			err,
		)
	}

	if len(sources) != 1 ||
		sources[0].Origin !=
			"https://example.com" {
		t.Fatalf(
			"Sources() = %#v",
			sources,
		)
	}
}

func TestPostgresReaderSourcesFailuresWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test sources failure",
	)

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	if _, err := reader.Sources(
		context.Background(),
		0,
	); !errors.Is(
		err,
		errInvalidLimit,
	) {
		t.Fatalf(
			"Sources(0) error = %v",
			err,
		)
	}

	reader = mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					err: testErr,
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	if _, err := reader.Sources(
		context.Background(),
		1,
	); !errors.Is(
		err,
		testErr,
	) {
		t.Fatalf(
			"Sources() error = %v",
			err,
		)
	}
}

func TestPostgresReaderCrawlsWithoutDatabase(
	t *testing.T,
) {
	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					rows: newFakeRows(
						unitCrawlRunValues(),
					),
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	crawls, err := reader.Crawls(
		context.Background(),
		1,
	)
	if err != nil {
		t.Fatalf(
			"Crawls() error = %v",
			err,
		)
	}

	if len(crawls) != 1 ||
		crawls[0].ID != 7 {
		t.Fatalf(
			"Crawls() = %#v",
			crawls,
		)
	}
}

func TestPostgresReaderCrawlsFailuresWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test crawls failure",
	)

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	if _, err := reader.Crawls(
		context.Background(),
		maxLimit+1,
	); !errors.Is(
		err,
		errInvalidLimit,
	) {
		t.Fatalf(
			"Crawls() error = %v",
			err,
		)
	}

	reader = mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					err: testErr,
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	if _, err := reader.Crawls(
		context.Background(),
		1,
	); !errors.Is(
		err,
		testErr,
	) {
		t.Fatalf(
			"Crawls() error = %v",
			err,
		)
	}
}

func TestPostgresReaderCrawlWithoutDatabase(
	t *testing.T,
) {
	now := time.Now().UTC()
	status := 200

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			rowResults: []fakeRow{
				{
					values: unitCrawlRunValues(),
				},
			},
			queryResults: []fakeQueryResult{
				{
					rows: newFakeRows(
						[]any{
							1,
							"https://example.com",
							"https://example.com/",
							0,
							now,
							int64(25),
							&status,
							int64(512),
							"text/html",
							0,
							"allowed",
							2,
							3,
							"complete",
							"",
							5,
							4,
						},
					),
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	detail, found, err := reader.Crawl(
		context.Background(),
		7,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v",
			err,
		)
	}

	if !found ||
		detail.Run.ID != 7 ||
		len(detail.Pages) != 1 ||
		detail.Pages[0].StatusCode == nil ||
		*detail.Pages[0].StatusCode != 200 {
		t.Fatalf(
			"Crawl() = %#v, %t",
			detail,
			found,
		)
	}
}

func TestPostgresReaderCrawlFailuresWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test crawl failure",
	)

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	if _, _, err := reader.Crawl(
		context.Background(),
		0,
	); !errors.Is(
		err,
		errInvalidCrawlID,
	) {
		t.Fatalf(
			"Crawl(0) error = %v",
			err,
		)
	}

	reader = mustUnitPostgresReader(
		t,
		&fakeQueryer{
			rowResults: []fakeRow{
				{
					err: pgx.ErrNoRows,
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	detail, found, err := reader.Crawl(
		context.Background(),
		1,
	)
	if err != nil ||
		found ||
		detail.Run != (CrawlRun{}) ||
		len(detail.Pages) != 0 {
		t.Fatalf(
			"Crawl(not found) = %#v, %t, %v",
			detail,
			found,
			err,
		)
	}

	reader = mustUnitPostgresReader(
		t,
		&fakeQueryer{
			rowResults: []fakeRow{
				{
					err: testErr,
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	if _, _, err := reader.Crawl(
		context.Background(),
		1,
	); !errors.Is(
		err,
		testErr,
	) {
		t.Fatalf(
			"Crawl(row failure) error = %v",
			err,
		)
	}

	reader = mustUnitPostgresReader(
		t,
		&fakeQueryer{
			rowResults: []fakeRow{
				{
					values: unitCrawlRunValues(),
				},
			},
			queryResults: []fakeQueryResult{
				{
					err: testErr,
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	if _, _, err := reader.Crawl(
		context.Background(),
		1,
	); !errors.Is(
		err,
		testErr,
	) {
		t.Fatalf(
			"Crawl(page failure) error = %v",
			err,
		)
	}
}

func TestPostgresReaderQueueWithoutDatabase(
	t *testing.T,
) {
	now := time.Now().UTC()

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					rows: newFakeRows(
						[]any{
							"https://example.com",
							"probe",
							now,
							int64(3),
							"worker-1",
							&now,
							&now,
						},
					),
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	items, err := reader.Queue(
		context.Background(),
		1,
	)
	if err != nil {
		t.Fatalf(
			"Queue() error = %v",
			err,
		)
	}

	if len(items) != 1 ||
		items[0].Origin !=
			"https://example.com" {
		t.Fatalf(
			"Queue() = %#v",
			items,
		)
	}
}

func TestPostgresReaderQueueFailuresWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test queue failure",
	)

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	if _, err := reader.Queue(
		context.Background(),
		0,
	); !errors.Is(
		err,
		errInvalidLimit,
	) {
		t.Fatalf(
			"Queue(0) error = %v",
			err,
		)
	}

	reader = mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					err: testErr,
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	if _, err := reader.Queue(
		context.Background(),
		1,
	); !errors.Is(
		err,
		testErr,
	) {
		t.Fatalf(
			"Queue() error = %v",
			err,
		)
	}
}

func TestPostgresReaderQueueEventsWithoutDatabase(
	t *testing.T,
) {
	now := time.Now().UTC()

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					rows: newFakeRows(
						[]any{
							int64(9),
							"https://example.com",
							now,
							"scheduled",
							"probe",
							&now,
							"worker-1",
							int64(2),
							&now,
						},
					),
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	events, err := reader.QueueEvents(
		context.Background(),
		1,
	)
	if err != nil {
		t.Fatalf(
			"QueueEvents() error = %v",
			err,
		)
	}

	if len(events) != 1 ||
		events[0].ID != 9 {
		t.Fatalf(
			"QueueEvents() = %#v",
			events,
		)
	}
}

func TestPostgresReaderQueueEventsFailuresWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test queue events failure",
	)

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	if _, err := reader.QueueEvents(
		context.Background(),
		0,
	); !errors.Is(
		err,
		errInvalidLimit,
	) {
		t.Fatalf(
			"QueueEvents(0) error = %v",
			err,
		)
	}

	reader = mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					err: testErr,
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	if _, err := reader.QueueEvents(
		context.Background(),
		1,
	); !errors.Is(
		err,
		testErr,
	) {
		t.Fatalf(
			"QueueEvents() error = %v",
			err,
		)
	}
}

func TestPostgresReaderServicesWithoutDatabase(
	t *testing.T,
) {
	now := time.Now().UTC()

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					rows: newFakeRows(
						[]any{
							"discovery",
							"instance-1",
							"running",
							"https://example.com",
							"crawling",
							now,
							now,
						},
					),
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	services, err := reader.Services(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"Services() error = %v",
			err,
		)
	}

	if len(services) != 1 ||
		services[0].Service !=
			"discovery" {
		t.Fatalf(
			"Services() = %#v",
			services,
		)
	}
}

func TestPostgresReaderServicesFailureWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test services failure",
	)

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					err: testErr,
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	if _, err := reader.Services(
		context.Background(),
	); !errors.Is(
		err,
		testErr,
	) {
		t.Fatalf(
			"Services() error = %v",
			err,
		)
	}
}

func mustUnitPostgresReader(
	t *testing.T,
	queryer *fakeQueryer,
	config PostgresConfig,
) *PostgresReader {
	t.Helper()

	reader, err := newPostgresReader(
		queryer,
		config,
	)
	if err != nil {
		t.Fatalf(
			"newPostgresReader() error = %v",
			err,
		)
	}

	return reader
}

func unitCrawlRunValues() []any {
	started := time.Date(
		2026,
		time.September,
		18,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	finished := started.Add(
		time.Minute,
	)

	return []any{
		int64(7),
		"https://example.com",
		started,
		&finished,
		"complete",
		"",
		2,
		2,
		3,
		false,
		2,
		32,
		int64(1024),
		int64(250),
		5,
		int64(5000),
		1,
		2,
		"",
		0,
		0,
		4,
		3,
		0,
	}
}

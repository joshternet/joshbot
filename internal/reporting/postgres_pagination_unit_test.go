package reporting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPostgresReaderSourcesPageWithoutDatabase(
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
	trueValue := true
	falseValue := false

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					rows: newFakeRows(
						[]any{
							"https://alpha.example",
							true,
							true,
							false,
							true,
							true,
							&now,
							&now,
						},
						[]any{
							"https://beta.example",
							false,
							true,
							false,
							false,
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

	page, err := reader.SourcesPage(
		context.Background(),
		sourceQuery{
			Limit:         1,
			Cursor:        &sourceCursor{Origin: "https://cursor.example"},
			Origin:        "https://alpha.example",
			Seeded:        &trueValue,
			Automatic:     &trueValue,
			Verified:      &trueValue,
			Blocked:       &falseValue,
			CrawlEligible: &trueValue,
		},
	)
	if err != nil {
		t.Fatalf(
			"SourcesPage() error = %v",
			err,
		)
	}

	if len(page.Items) != 1 {
		t.Fatalf(
			"SourcesPage() length = %d, want 1",
			len(page.Items),
		)
	}

	if page.Items[0].Origin !=
		"https://alpha.example" {
		t.Fatalf(
			"SourcesPage()[0] = %#v",
			page.Items[0],
		)
	}

	if page.NextCursor == "" {
		t.Fatal(
			"SourcesPage() next cursor is empty",
		)
	}
}

func TestPostgresReaderSourcesPageFailuresWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test source page failure",
	)

	t.Run(
		"invalid limit",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			_, err := reader.SourcesPage(
				context.Background(),
				sourceQuery{
					Limit: 0,
				},
			)

			if !errors.Is(
				err,
				errInvalidLimit,
			) {
				t.Fatalf(
					"SourcesPage() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"query",
		func(t *testing.T) {
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

			_, err := reader.SourcesPage(
				context.Background(),
				sourceQuery{
					Limit: 1,
				},
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"reporting: read source page",
				) {
				t.Fatalf(
					"SourcesPage() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"collect",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{
					queryResults: []fakeQueryResult{
						{
							rows: newFakeRows(
								[]any{
									"https://example.com",
								},
							),
						},
					},
				},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			_, err := reader.SourcesPage(
				context.Background(),
				sourceQuery{
					Limit: 1,
				},
			)

			if err == nil ||
				!strings.Contains(
					err.Error(),
					"reporting: collect source page",
				) {
				t.Fatalf(
					"SourcesPage() error = %v",
					err,
				)
			}
		},
	)
}

func TestPostgresReaderCrawlsPageWithoutDatabase(
	t *testing.T,
) {
	first := unitCrawlRunValues()
	second := unitCrawlRunValues()

	first[0] = int64(8)
	second[0] = int64(7)

	firstStarted := time.Date(
		2026,
		time.September,
		18,
		13,
		0,
		0,
		0,
		time.UTC,
	)
	secondStarted := firstStarted.Add(
		-time.Hour,
	)

	first[2] = firstStarted
	second[2] = secondStarted

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					rows: newFakeRows(
						first,
						second,
					),
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	page, err := reader.CrawlsPage(
		context.Background(),
		crawlQuery{
			Limit: 1,
			Cursor: &crawlCursor{
				StartedAt: firstStarted.Add(time.Hour),
				ID:        9,
			},
			Origin:  "https://example.com",
			Outcome: "complete",
		},
	)
	if err != nil {
		t.Fatalf(
			"CrawlsPage() error = %v",
			err,
		)
	}

	if len(page.Items) != 1 {
		t.Fatalf(
			"CrawlsPage() length = %d, want 1",
			len(page.Items),
		)
	}

	if page.Items[0].ID != 8 {
		t.Fatalf(
			"CrawlsPage()[0] = %#v",
			page.Items[0],
		)
	}

	if page.NextCursor == "" {
		t.Fatal(
			"CrawlsPage() next cursor is empty",
		)
	}
}

func TestPostgresReaderCrawlsPageFailuresWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test crawl page failure",
	)

	t.Run(
		"invalid limit",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			_, err := reader.CrawlsPage(
				context.Background(),
				crawlQuery{
					Limit: maxLimit + 1,
				},
			)

			if !errors.Is(
				err,
				errInvalidLimit,
			) {
				t.Fatalf(
					"CrawlsPage() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"query",
		func(t *testing.T) {
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

			_, err := reader.CrawlsPage(
				context.Background(),
				crawlQuery{
					Limit: 1,
				},
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"reporting: read crawl page",
				) {
				t.Fatalf(
					"CrawlsPage() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"collect",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{
					queryResults: []fakeQueryResult{
						{
							rows: newFakeRows(
								[]any{
									int64(1),
								},
							),
						},
					},
				},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			_, err := reader.CrawlsPage(
				context.Background(),
				crawlQuery{
					Limit: 1,
				},
			)

			if err == nil ||
				!strings.Contains(
					err.Error(),
					"reporting: collect crawl page",
				) {
				t.Fatalf(
					"CrawlsPage() error = %v",
					err,
				)
			}
		},
	)
}

func TestPostgresReaderQueuePageWithoutDatabase(
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
	leased := true

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					rows: newFakeRows(
						[]any{
							"https://alpha.example",
							"probe",
							now,
							int64(1),
							"worker-1",
							&now,
							&now,
						},
						[]any{
							"https://beta.example",
							"probe",
							now.Add(time.Minute),
							int64(2),
							"",
							(*time.Time)(nil),
							(*time.Time)(nil),
						},
					),
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	page, err := reader.QueuePage(
		context.Background(),
		queueQuery{
			Limit: 1,
			Cursor: &queueCursor{
				AvailableAt: now.Add(-time.Minute),
				Origin:      "https://cursor.example",
			},
			Origin: "https://alpha.example",
			Mode:   "probe",
			Leased: &leased,
		},
	)
	if err != nil {
		t.Fatalf(
			"QueuePage() error = %v",
			err,
		)
	}

	if len(page.Items) != 1 {
		t.Fatalf(
			"QueuePage() length = %d, want 1",
			len(page.Items),
		)
	}

	if page.Items[0].Origin !=
		"https://alpha.example" {
		t.Fatalf(
			"QueuePage()[0] = %#v",
			page.Items[0],
		)
	}

	if page.NextCursor == "" {
		t.Fatal(
			"QueuePage() next cursor is empty",
		)
	}
}

func TestPostgresReaderQueuePageFailuresWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test queue page failure",
	)

	t.Run(
		"invalid limit",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			_, err := reader.QueuePage(
				context.Background(),
				queueQuery{
					Limit: -1,
				},
			)

			if !errors.Is(
				err,
				errInvalidLimit,
			) {
				t.Fatalf(
					"QueuePage() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"query",
		func(t *testing.T) {
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

			_, err := reader.QueuePage(
				context.Background(),
				queueQuery{
					Limit: 1,
				},
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"reporting: read queue page",
				) {
				t.Fatalf(
					"QueuePage() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"collect",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{
					queryResults: []fakeQueryResult{
						{
							rows: newFakeRows(
								[]any{
									"https://example.com",
								},
							),
						},
					},
				},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			_, err := reader.QueuePage(
				context.Background(),
				queueQuery{
					Limit: 1,
				},
			)

			if err == nil ||
				!strings.Contains(
					err.Error(),
					"reporting: collect queue page",
				) {
				t.Fatalf(
					"QueuePage() error = %v",
					err,
				)
			}
		},
	)
}

func TestPostgresReaderQueueEventsPageWithoutDatabase(
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

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					rows: newFakeRows(
						[]any{
							int64(8),
							"https://example.com",
							now,
							"claimed",
							"probe",
							&now,
							"worker-1",
							int64(2),
							&now,
						},
						[]any{
							int64(7),
							"https://example.com",
							now.Add(-time.Minute),
							"renewed",
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

	page, err := reader.QueueEventsPage(
		context.Background(),
		queueEventQuery{
			Limit: 1,
			Cursor: &queueEventCursor{
				OccurredAt: now.Add(time.Minute),
				ID:         9,
			},
			Origin: "https://example.com",
			Event:  "claimed",
			Mode:   "probe",
		},
	)
	if err != nil {
		t.Fatalf(
			"QueueEventsPage() error = %v",
			err,
		)
	}

	if len(page.Items) != 1 {
		t.Fatalf(
			"QueueEventsPage() length = %d, want 1",
			len(page.Items),
		)
	}

	if page.Items[0].ID != 8 {
		t.Fatalf(
			"QueueEventsPage()[0] = %#v",
			page.Items[0],
		)
	}

	if page.NextCursor == "" {
		t.Fatal(
			"QueueEventsPage() next cursor is empty",
		)
	}
}

func TestPostgresReaderQueueEventsPageFailuresWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test queue event page failure",
	)

	t.Run(
		"invalid limit",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			_, err := reader.QueueEventsPage(
				context.Background(),
				queueEventQuery{
					Limit: 0,
				},
			)

			if !errors.Is(
				err,
				errInvalidLimit,
			) {
				t.Fatalf(
					"QueueEventsPage() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"query",
		func(t *testing.T) {
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

			_, err := reader.QueueEventsPage(
				context.Background(),
				queueEventQuery{
					Limit: 1,
				},
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"reporting: read queue event page",
				) {
				t.Fatalf(
					"QueueEventsPage() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"collect",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{
					queryResults: []fakeQueryResult{
						{
							rows: newFakeRows(
								[]any{
									int64(1),
								},
							),
						},
					},
				},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			_, err := reader.QueueEventsPage(
				context.Background(),
				queueEventQuery{
					Limit: 1,
				},
			)

			if err == nil ||
				!strings.Contains(
					err.Error(),
					"reporting: collect queue event page",
				) {
				t.Fatalf(
					"QueueEventsPage() error = %v",
					err,
				)
			}
		},
	)
}

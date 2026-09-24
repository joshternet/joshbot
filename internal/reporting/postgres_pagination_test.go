package reporting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPostgresReaderPaginatesAndFiltersSources(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)
	seedReportingFixture(t, pool)

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{
			AutomaticCrawlEnabled: true,
			MaxPendingProbes:      1000,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	first, err := reader.SourcesPage(
		ctx,
		sourceQuery{Limit: 1},
	)
	if err != nil {
		t.Fatalf(
			"SourcesPage(first) error = %v",
			err,
		)
	}

	if len(first.Items) != 1 ||
		first.Items[0].Origin != "https://auto.example" ||
		first.NextCursor == "" {
		t.Fatalf(
			"first source page = %#v",
			first,
		)
	}

	cursor, err := decodeSourceCursor(
		first.NextCursor,
	)
	if err != nil {
		t.Fatalf(
			"decode source cursor: %v",
			err,
		)
	}

	second, err := reader.SourcesPage(
		ctx,
		sourceQuery{
			Limit:  1,
			Cursor: cursor,
		},
	)
	if err != nil {
		t.Fatalf(
			"SourcesPage(second) error = %v",
			err,
		)
	}

	if len(second.Items) != 1 ||
		second.Items[0].Origin != "https://blocked.example" ||
		second.NextCursor == "" {
		t.Fatalf(
			"second source page = %#v",
			second,
		)
	}

	trueValue := true
	falseValue := false

	filters := []struct {
		name  string
		query sourceQuery
		want  string
	}{
		{
			name: "exact origin",
			query: sourceQuery{
				Limit:  10,
				Origin: "https://seed.example",
			},
			want: "https://seed.example",
		},
		{
			name: "seeded",
			query: sourceQuery{
				Limit:  10,
				Seeded: &trueValue,
			},
			want: "https://seed.example",
		},
		{
			name: "automatic and unblocked",
			query: sourceQuery{
				Limit:     10,
				Automatic: &trueValue,
				Blocked:   &falseValue,
			},
			want: "https://auto.example",
		},
		{
			name: "verified",
			query: sourceQuery{
				Limit:    10,
				Verified: &trueValue,
			},
			want: "https://seed.example",
		},
		{
			name: "blocked",
			query: sourceQuery{
				Limit:   10,
				Blocked: &trueValue,
			},
			want: "https://blocked.example",
		},
		{
			name: "not crawl eligible",
			query: sourceQuery{
				Limit:         10,
				CrawlEligible: &falseValue,
			},
			want: "https://blocked.example",
		},
	}

	for _, test := range filters {
		t.Run(
			test.name,
			func(t *testing.T) {
				page, err := reader.SourcesPage(
					ctx,
					test.query,
				)
				if err != nil {
					t.Fatalf(
						"SourcesPage() error = %v",
						err,
					)
				}

				if len(page.Items) != 1 ||
					page.Items[0].Origin != test.want ||
					page.NextCursor != "" {
					t.Errorf(
						"source page = %#v, want %q",
						page,
						test.want,
					)
				}
			},
		)
	}
}

func TestPostgresReaderPaginatesAndFiltersCrawls(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)
	fixture := seedReportingFixture(t, pool)

	var olderID int64
	err := pool.QueryRow(
		ctx,
		`
			INSERT INTO crawl_runs (
				source_origin,
				started_at,
				finished_at,
				outcome,
				stop_reason,
				pages_attempted,
				pages_parsed,
				candidates_discovered,
				budget_exhausted,
				max_depth,
				max_pages,
				max_page_bytes,
				request_delay_milliseconds,
				redirect_limit,
				page_timeout_milliseconds
			)
			SELECT
				source_origin,
				started_at - interval '1 hour',
				finished_at - interval '1 hour',
				'failed',
				'validation',
				pages_attempted,
				pages_parsed,
				candidates_discovered,
				budget_exhausted,
				max_depth,
				max_pages,
				max_page_bytes,
				request_delay_milliseconds,
				redirect_limit,
				page_timeout_milliseconds
			FROM crawl_runs
			WHERE id = $1
			RETURNING id
		`,
		fixture.crawlID,
	).Scan(&olderID)
	if err != nil {
		t.Fatalf(
			"insert older crawl: %v",
			err,
		)
	}

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{MaxPendingProbes: 1000},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	first, err := reader.CrawlsPage(
		ctx,
		crawlQuery{Limit: 1},
	)
	if err != nil {
		t.Fatalf(
			"CrawlsPage(first) error = %v",
			err,
		)
	}

	if len(first.Items) != 1 ||
		first.Items[0].ID != fixture.crawlID ||
		first.NextCursor == "" {
		t.Fatalf(
			"first crawl page = %#v",
			first,
		)
	}

	cursor, err := decodeCrawlCursor(
		first.NextCursor,
	)
	if err != nil {
		t.Fatalf(
			"decode crawl cursor: %v",
			err,
		)
	}

	second, err := reader.CrawlsPage(
		ctx,
		crawlQuery{
			Limit:  1,
			Cursor: cursor,
		},
	)
	if err != nil {
		t.Fatalf(
			"CrawlsPage(second) error = %v",
			err,
		)
	}

	if len(second.Items) != 1 ||
		second.Items[0].ID != olderID ||
		second.NextCursor != "" {
		t.Errorf(
			"second crawl page = %#v",
			second,
		)
	}

	failed, err := reader.CrawlsPage(
		ctx,
		crawlQuery{
			Limit:   10,
			Origin:  "https://seed.example",
			Outcome: "failed",
		},
	)
	if err != nil {
		t.Fatalf(
			"CrawlsPage(filtered) error = %v",
			err,
		)
	}

	if len(failed.Items) != 1 ||
		failed.Items[0].ID != olderID {
		t.Errorf(
			"filtered crawls = %#v",
			failed,
		)
	}
}

func TestPostgresReaderPaginatesAndFiltersQueue(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)
	seedReportingFixture(t, pool)

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{MaxPendingProbes: 1000},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	first, err := reader.QueuePage(
		ctx,
		queueQuery{Limit: 1},
	)
	if err != nil {
		t.Fatalf(
			"QueuePage(first) error = %v",
			err,
		)
	}

	if len(first.Items) != 1 ||
		first.Items[0].Origin != "https://seed.example" ||
		first.NextCursor == "" {
		t.Fatalf(
			"first queue page = %#v",
			first,
		)
	}

	cursor, err := decodeQueueCursor(
		first.NextCursor,
	)
	if err != nil {
		t.Fatalf(
			"decode queue cursor: %v",
			err,
		)
	}

	second, err := reader.QueuePage(
		ctx,
		queueQuery{
			Limit:  1,
			Cursor: cursor,
		},
	)
	if err != nil {
		t.Fatalf(
			"QueuePage(second) error = %v",
			err,
		)
	}

	if len(second.Items) != 1 ||
		second.Items[0].Origin != "https://auto.example" ||
		second.NextCursor != "" {
		t.Errorf(
			"second queue page = %#v",
			second,
		)
	}

	leased := true
	filtered, err := reader.QueuePage(
		ctx,
		queueQuery{
			Limit:  10,
			Origin: "https://auto.example",
			Mode:   "probe",
			Leased: &leased,
		},
	)
	if err != nil {
		t.Fatalf(
			"QueuePage(filtered) error = %v",
			err,
		)
	}

	if len(filtered.Items) != 1 ||
		filtered.Items[0].Origin != "https://auto.example" {
		t.Errorf(
			"filtered queue = %#v",
			filtered,
		)
	}
}

func TestPostgresReaderPaginatesAndFiltersQueueEvents(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)
	seedReportingFixture(t, pool)

	now := time.Now().UTC().Truncate(time.Microsecond)

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO verification_queue_events (
				origin,
				occurred_at,
				event,
				mode,
				lease_owner,
				lease_generation
			)
			VALUES
				(
					'https://events.example',
					$1,
					'claimed',
					'probe',
					'worker-a',
					1
				),
				(
					'https://events.example',
					$2,
					'renewed',
					'probe',
					'worker-a',
					1
				)
		`,
		now.Add(2*time.Hour),
		now.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf(
			"insert queue events: %v",
			err,
		)
	}

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{MaxPendingProbes: 1000},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	first, err := reader.QueueEventsPage(
		ctx,
		queueEventQuery{
			Limit:  1,
			Origin: "https://events.example",
		},
	)
	if err != nil {
		t.Fatalf(
			"QueueEventsPage(first) error = %v",
			err,
		)
	}

	if len(first.Items) != 1 ||
		first.Items[0].Event != "claimed" ||
		first.NextCursor == "" {
		t.Fatalf(
			"first event page = %#v",
			first,
		)
	}

	cursor, err := decodeQueueEventCursor(
		first.NextCursor,
	)
	if err != nil {
		t.Fatalf(
			"decode event cursor: %v",
			err,
		)
	}

	second, err := reader.QueueEventsPage(
		ctx,
		queueEventQuery{
			Limit:  1,
			Origin: "https://events.example",
			Cursor: cursor,
		},
	)
	if err != nil {
		t.Fatalf(
			"QueueEventsPage(second) error = %v",
			err,
		)
	}

	if len(second.Items) != 1 ||
		second.Items[0].Event != "renewed" ||
		second.NextCursor != "" {
		t.Errorf(
			"second event page = %#v",
			second,
		)
	}

	filtered, err := reader.QueueEventsPage(
		ctx,
		queueEventQuery{
			Limit:  10,
			Origin: "https://events.example",
			Event:  "claimed",
			Mode:   "probe",
		},
	)
	if err != nil {
		t.Fatalf(
			"QueueEventsPage(filtered) error = %v",
			err,
		)
	}

	if len(filtered.Items) != 1 ||
		filtered.Items[0].Event != "claimed" {
		t.Errorf(
			"filtered events = %#v",
			filtered,
		)
	}
}

func TestPostgresReaderPaginationRejectsInvalidLimits(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{MaxPendingProbes: 1000},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	checks := []struct {
		name string
		call func() error
	}{
		{
			name: "sources",
			call: func() error {
				_, err := reader.SourcesPage(
					ctx,
					sourceQuery{Limit: 0},
				)
				return err
			},
		},
		{
			name: "crawls",
			call: func() error {
				_, err := reader.CrawlsPage(
					ctx,
					crawlQuery{Limit: maxLimit + 1},
				)
				return err
			},
		},
		{
			name: "queue",
			call: func() error {
				_, err := reader.QueuePage(
					ctx,
					queueQuery{Limit: -1},
				)
				return err
			},
		},
		{
			name: "events",
			call: func() error {
				_, err := reader.QueueEventsPage(
					ctx,
					queueEventQuery{Limit: 0},
				)
				return err
			},
		},
		{
			name: "audits",
			call: func() error {
				_, err := reader.AuditsPage(
					ctx,
					auditQuery{Limit: 0},
				)
				return err
			},
		},
	}

	for _, check := range checks {
		t.Run(
			check.name,
			func(t *testing.T) {
				if err := check.call(); !errors.Is(
					err,
					errInvalidLimit,
				) {
					t.Errorf(
						"error = %v, want %v",
						err,
						errInvalidLimit,
					)
				}
			},
		)
	}
}

func TestPostgresReaderPaginationPreservesDatabaseFailures(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{MaxPendingProbes: 1000},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	pool.Close()

	checks := []struct {
		name string
		call func() error
	}{
		{
			name: "sources",
			call: func() error {
				_, err := reader.SourcesPage(
					ctx,
					sourceQuery{Limit: 1},
				)
				return err
			},
		},
		{
			name: "crawls",
			call: func() error {
				_, err := reader.CrawlsPage(
					ctx,
					crawlQuery{Limit: 1},
				)
				return err
			},
		},
		{
			name: "queue",
			call: func() error {
				_, err := reader.QueuePage(
					ctx,
					queueQuery{Limit: 1},
				)
				return err
			},
		},
		{
			name: "events",
			call: func() error {
				_, err := reader.QueueEventsPage(
					ctx,
					queueEventQuery{Limit: 1},
				)
				return err
			},
		},
	}

	for _, check := range checks {
		t.Run(
			check.name,
			func(t *testing.T) {
				if err := check.call(); err == nil {
					t.Error(
						"error = nil, want database failure",
					)
				}
			},
		)
	}
}

func TestPostgresReaderPaginationWrapsCollectionFailures(
	t *testing.T,
) {
	ctx := context.Background()

	tests := []struct {
		name              string
		setupSQL          string
		expectedContext   string
		expectedColumn    int
		expectedFieldName string
		call              func(
			context.Context,
			*PostgresReader,
		) error
	}{
		{
			name: "sources",
			setupSQL: `
				WITH candidate AS (
					INSERT INTO discovery_candidates (
						origin,
						first_discovered_at,
						last_discovered_at
					)
					VALUES (
						'https://scan-source.example',
						TIMESTAMPTZ 'infinity',
						TIMESTAMPTZ 'infinity'
					)
					RETURNING origin
				)
				INSERT INTO discovery_source_state (
					source_origin
				)
				SELECT origin
				FROM candidate
			`,
			expectedContext:   "reporting: collect source page",
			expectedColumn:    6,
			expectedFieldName: "first_discovered_at",
			call: func(
				ctx context.Context,
				reader *PostgresReader,
			) error {
				_, err := reader.SourcesPage(
					ctx,
					sourceQuery{
						Limit:  1,
						Origin: "https://scan-source.example",
					},
				)

				return err
			},
		},
		{
			name: "crawls",
			setupSQL: `
				INSERT INTO crawl_runs (
					source_origin,
					started_at,
					max_depth,
					max_pages,
					max_page_bytes,
					request_delay_milliseconds,
					redirect_limit,
					page_timeout_milliseconds
				)
				VALUES (
					'https://scan-crawl.example',
					TIMESTAMPTZ 'infinity',
					0,
					1,
					1,
					0,
					0,
					1
				)
			`,
			expectedContext:   "reporting: collect crawl page",
			expectedColumn:    2,
			expectedFieldName: "started_at",
			call: func(
				ctx context.Context,
				reader *PostgresReader,
			) error {
				_, err := reader.CrawlsPage(
					ctx,
					crawlQuery{
						Limit:  1,
						Origin: "https://scan-crawl.example",
					},
				)

				return err
			},
		},
		{
			name: "queue",
			setupSQL: `
				INSERT INTO verification_queue (
					origin,
					available_at
				)
				VALUES (
					'https://scan-queue.example',
					TIMESTAMPTZ 'infinity'
				)
			`,
			expectedContext:   "reporting: collect queue page",
			expectedColumn:    2,
			expectedFieldName: "available_at",
			call: func(
				ctx context.Context,
				reader *PostgresReader,
			) error {
				_, err := reader.QueuePage(
					ctx,
					queueQuery{
						Limit:  1,
						Origin: "https://scan-queue.example",
					},
				)

				return err
			},
		},
		{
			name: "queue events",
			setupSQL: `
				INSERT INTO verification_queue_events (
					origin,
					occurred_at,
					event,
					mode
				)
				VALUES (
					'https://scan-event.example',
					TIMESTAMPTZ 'infinity',
					'scheduled',
					'recurring'
				)
			`,
			expectedContext:   "reporting: collect queue event page",
			expectedColumn:    2,
			expectedFieldName: "occurred_at",
			call: func(
				ctx context.Context,
				reader *PostgresReader,
			) error {
				_, err := reader.QueueEventsPage(
					ctx,
					queueEventQuery{
						Limit:  1,
						Origin: "https://scan-event.example",
					},
				)

				return err
			},
		},
		{
			name: "audit",
			setupSQL: `
				INSERT INTO operator_audit_events (
					occurred_at, action, target, caller, actor, result
				)
				VALUES (
					TIMESTAMPTZ 'infinity', 'pause', 'discovery',
					'integration', 'operator', 'success'
				)
			`,
			expectedContext:   "reporting: collect audit page",
			expectedColumn:    1,
			expectedFieldName: "occurred_at",
			call: func(
				ctx context.Context,
				reader *PostgresReader,
			) error {
				_, err := reader.AuditsPage(
					ctx,
					auditQuery{Limit: 1},
				)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				pool := newReportingTestPool(t)

				if _, err := pool.Exec(
					ctx,
					test.setupSQL,
				); err != nil {
					t.Fatalf(
						"insert scan-failure fixture: %v",
						err,
					)
				}

				reader, err := NewPostgresReader(
					pool,
					PostgresConfig{
						MaxPendingProbes: 1000,
					},
				)
				if err != nil {
					t.Fatalf(
						"NewPostgresReader() error = %v",
						err,
					)
				}

				err = test.call(
					ctx,
					reader,
				)
				if err == nil {
					t.Fatal(
						"pagination error = nil, want collection failure",
					)
				}

				if !strings.HasPrefix(
					err.Error(),
					test.expectedContext+":",
				) {
					t.Errorf(
						"error = %q, want prefix %q",
						err,
						test.expectedContext+":",
					)
				}

				var scanError pgx.ScanArgError
				if !errors.As(
					err,
					&scanError,
				) {
					t.Fatalf(
						"error = %v, want pgx.ScanArgError",
						err,
					)
				}

				if scanError.ColumnIndex != test.expectedColumn ||
					scanError.FieldName != test.expectedFieldName {
					t.Errorf(
						"scan error = %#v, want column %d field %q",
						scanError,
						test.expectedColumn,
						test.expectedFieldName,
					)
				}
			},
		)
	}
}

func TestPostgresReaderPaginationReturnsEmptyPages(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newReportingTestPool(t)

	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v",
			err,
		)
	}

	sources, err := reader.SourcesPage(
		ctx,
		sourceQuery{Limit: 1},
	)
	if err != nil {
		t.Fatalf(
			"SourcesPage() error = %v",
			err,
		)
	}
	if sources.Items == nil ||
		len(sources.Items) != 0 ||
		sources.NextCursor != "" {
		t.Errorf(
			"SourcesPage() = %#v, want empty page",
			sources,
		)
	}

	crawls, err := reader.CrawlsPage(
		ctx,
		crawlQuery{Limit: 1},
	)
	if err != nil {
		t.Fatalf(
			"CrawlsPage() error = %v",
			err,
		)
	}
	if crawls.Items == nil ||
		len(crawls.Items) != 0 ||
		crawls.NextCursor != "" {
		t.Errorf(
			"CrawlsPage() = %#v, want empty page",
			crawls,
		)
	}

	queue, err := reader.QueuePage(
		ctx,
		queueQuery{Limit: 1},
	)
	if err != nil {
		t.Fatalf(
			"QueuePage() error = %v",
			err,
		)
	}
	if queue.Items == nil ||
		len(queue.Items) != 0 ||
		queue.NextCursor != "" {
		t.Errorf(
			"QueuePage() = %#v, want empty page",
			queue,
		)
	}

	events, err := reader.QueueEventsPage(
		ctx,
		queueEventQuery{Limit: 1},
	)
	if err != nil {
		t.Fatalf(
			"QueueEventsPage() error = %v",
			err,
		)
	}
	if events.Items == nil ||
		len(events.Items) != 0 ||
		events.NextCursor != "" {
		t.Errorf(
			"QueueEventsPage() = %#v, want empty page",
			events,
		)
	}

	audits, err := reader.AuditsPage(
		ctx,
		auditQuery{Limit: 1},
	)
	if err != nil {
		t.Fatalf(
			"AuditsPage() error = %v",
			err,
		)
	}
	if audits.Items == nil ||
		len(audits.Items) != 0 ||
		audits.NextCursor != "" {
		t.Errorf(
			"AuditsPage() = %#v, want empty page",
			audits,
		)
	}
}

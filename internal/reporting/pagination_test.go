package reporting

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakePagedReader struct {
	*fakeReader

	sourcePageResult sourcePage
	sourcePageErr    error
	sourceQuery      sourceQuery

	crawlPageResult crawlPage
	crawlPageErr    error
	crawlQuery      crawlQuery

	queuePageResult queuePage
	queuePageErr    error
	queueQuery      queueQuery

	queueEventPageResult queueEventPage
	queueEventPageErr    error
	queueEventQuery      queueEventQuery

	auditPageResult auditPage
	auditPageErr    error
	auditQuery      auditQuery
}

func (reader *fakePagedReader) SourcesPage(
	_ context.Context,
	query sourceQuery,
) (sourcePage, error) {
	reader.sourceQuery = query

	return reader.sourcePageResult,
		reader.sourcePageErr
}

func (reader *fakePagedReader) CrawlsPage(
	_ context.Context,
	query crawlQuery,
) (crawlPage, error) {
	reader.crawlQuery = query

	return reader.crawlPageResult,
		reader.crawlPageErr
}

func (reader *fakePagedReader) QueuePage(
	_ context.Context,
	query queueQuery,
) (queuePage, error) {
	reader.queueQuery = query

	return reader.queuePageResult,
		reader.queuePageErr
}

func (reader *fakePagedReader) QueueEventsPage(
	_ context.Context,
	query queueEventQuery,
) (queueEventPage, error) {
	reader.queueEventQuery = query

	return reader.queueEventPageResult,
		reader.queueEventPageErr
}

func (reader *fakePagedReader) AuditsPage(
	_ context.Context,
	query auditQuery,
) (auditPage, error) {
	reader.auditQuery = query
	return reader.auditPageResult, reader.auditPageErr
}

func TestPagedHandlersPreserveArrayBodiesAndExposeNextCursor(
	t *testing.T,
) {
	startedAt := testNow.Add(-2 * time.Hour)
	availableAt := testNow.Add(-time.Hour)
	occurredAt := testNow.Add(-30 * time.Minute)

	sourceCursorValue := sourceNextCursor(
		CrawlSource{
			Origin: "https://after.example",
		},
	)
	crawlCursorValue := crawlNextCursor(
		CrawlRun{
			ID:        7,
			StartedAt: startedAt,
		},
	)
	queueCursorValue := queueNextCursor(
		QueueItem{
			Origin:      "https://queue.example",
			AvailableAt: availableAt,
		},
	)
	eventCursorValue := queueEventNextCursor(
		QueueEvent{
			ID:         9,
			OccurredAt: occurredAt,
		},
	)

	reader := &fakePagedReader{
		fakeReader: &fakeReader{},
		sourcePageResult: sourcePage{
			Items: []CrawlSource{
				{
					Origin: "https://source.example",
				},
			},
			NextCursor: "next-source",
		},
		crawlPageResult: crawlPage{
			Items: []CrawlRun{
				{
					ID:           12,
					SourceOrigin: "https://crawl.example",
					StartedAt:    testNow,
				},
			},
			NextCursor: "next-crawl",
		},
		queuePageResult: queuePage{
			Items: []QueueItem{
				{
					Origin:      "https://queued.example",
					Mode:        "probe",
					AvailableAt: testNow,
				},
			},
			NextCursor: "next-queue",
		},
		queueEventPageResult: queueEventPage{
			Items: []QueueEvent{
				{
					ID:         13,
					Origin:     "https://event.example",
					OccurredAt: testNow,
					Event:      "claimed",
					Mode:       "probe",
				},
			},
			NextCursor: "next-event",
		},
	}

	handler := mustReportingHandler(
		t,
		reader,
	)

	tests := []struct {
		name       string
		path       string
		wantCursor string
		wantBody   string
		check      func(*testing.T)
	}{
		{
			name: "sources",
			path: "/api/v1/sources?limit=7" +
				"&origin=https%3A%2F%2FEXAMPLE.com" +
				"&seeded=true" +
				"&automatic=false" +
				"&verified=true" +
				"&blocked=false" +
				"&crawl_eligible=true" +
				"&cursor=" + sourceCursorValue,
			wantCursor: "next-source",
			wantBody:   `"origin":"https://source.example"`,
			check: func(t *testing.T) {
				t.Helper()

				query := reader.sourceQuery
				if query.Limit != 7 ||
					query.Origin != "https://example.com" ||
					query.Cursor == nil ||
					query.Cursor.Origin !=
						"https://after.example" ||
					query.Seeded == nil ||
					!*query.Seeded ||
					query.Automatic == nil ||
					*query.Automatic ||
					query.Verified == nil ||
					!*query.Verified ||
					query.Blocked == nil ||
					*query.Blocked ||
					query.CrawlEligible == nil ||
					!*query.CrawlEligible {
					t.Errorf(
						"source query = %#v",
						query,
					)
				}
			},
		},
		{
			name: "crawls",
			path: "/api/v1/crawls?limit=8" +
				"&origin=https%3A%2F%2Fcrawl.example" +
				"&outcome=complete" +
				"&cursor=" + crawlCursorValue,
			wantCursor: "next-crawl",
			wantBody:   `"source_origin":"https://crawl.example"`,
			check: func(t *testing.T) {
				t.Helper()

				query := reader.crawlQuery
				if query.Limit != 8 ||
					query.Origin != "https://crawl.example" ||
					query.Outcome != "complete" ||
					query.Cursor == nil ||
					query.Cursor.ID != 7 ||
					!query.Cursor.StartedAt.Equal(
						startedAt,
					) {
					t.Errorf(
						"crawl query = %#v",
						query,
					)
				}
			},
		},
		{
			name: "queue",
			path: "/api/v1/queue?limit=9" +
				"&origin=https%3A%2F%2Fqueue.example" +
				"&mode=probe" +
				"&leased=true" +
				"&cursor=" + queueCursorValue,
			wantCursor: "next-queue",
			wantBody:   `"origin":"https://queued.example"`,
			check: func(t *testing.T) {
				t.Helper()

				query := reader.queueQuery
				if query.Limit != 9 ||
					query.Origin != "https://queue.example" ||
					query.Mode != "probe" ||
					query.Leased == nil ||
					!*query.Leased ||
					query.Cursor == nil ||
					query.Cursor.Origin !=
						"https://queue.example" ||
					!query.Cursor.AvailableAt.Equal(
						availableAt,
					) {
					t.Errorf(
						"queue query = %#v",
						query,
					)
				}
			},
		},
		{
			name:       "queue reprobe",
			path:       "/api/v1/queue?mode=reprobe",
			wantCursor: "next-queue",
			wantBody:   `"origin":"https://queued.example"`,
			check: func(t *testing.T) {
				t.Helper()

				if reader.queueQuery.Mode != "reprobe" {
					t.Errorf(
						"queue mode = %q, want reprobe",
						reader.queueQuery.Mode,
					)
				}
			},
		},
		{
			name:       "queue event reprobe",
			path:       "/api/v1/queue/events?mode=reprobe",
			wantCursor: "next-event",
			wantBody:   `"event":"claimed"`,
			check: func(t *testing.T) {
				t.Helper()

				if reader.queueEventQuery.Mode != "reprobe" {
					t.Errorf(
						"queue event mode = %q, want reprobe",
						reader.queueEventQuery.Mode,
					)
				}
			},
		},
		{
			name: "queue events",
			path: "/api/v1/queue/events?limit=10" +
				"&origin=https%3A%2F%2Fevent.example" +
				"&event=claimed" +
				"&mode=recurring" +
				"&cursor=" + eventCursorValue,
			wantCursor: "next-event",
			wantBody:   `"event":"claimed"`,
			check: func(t *testing.T) {
				t.Helper()

				query := reader.queueEventQuery
				if query.Limit != 10 ||
					query.Origin != "https://event.example" ||
					query.Event != "claimed" ||
					query.Mode != "recurring" ||
					query.Cursor == nil ||
					query.Cursor.ID != 9 ||
					!query.Cursor.OccurredAt.Equal(
						occurredAt,
					) {
					t.Errorf(
						"queue event query = %#v",
						query,
					)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				response := performRequest(
					t,
					handler,
					test.path,
					"Bearer "+testToken,
				)

				if response.Code != http.StatusOK {
					t.Fatalf(
						"status = %d, want %d; body=%q",
						response.Code,
						http.StatusOK,
						response.Body.String(),
					)
				}

				if got := response.Header().Get(
					nextCursorHeader,
				); got != test.wantCursor {
					t.Errorf(
						"next cursor = %q, want %q",
						got,
						test.wantCursor,
					)
				}

				if body := response.Body.String(); !strings.Contains(
					body,
					test.wantBody,
				) {
					t.Errorf(
						"body = %q, want fragment %q",
						body,
						test.wantBody,
					)
				}

				test.check(t)
			},
		)
	}
}

func TestPagedHandlersUseDefaultsAndNoCursorHeader(
	t *testing.T,
) {
	reader := &fakePagedReader{
		fakeReader: &fakeReader{},
		sourcePageResult: sourcePage{
			Items: []CrawlSource{},
		},
	}

	handler := mustReportingHandler(
		t,
		reader,
	)

	response := performRequest(
		t,
		handler,
		"/api/v1/sources",
		"Bearer "+testToken,
	)

	if response.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, want %d",
			response.Code,
			http.StatusOK,
		)
	}

	if response.Body.String() != "[]\n" {
		t.Errorf(
			"body = %q, want empty array",
			response.Body.String(),
		)
	}

	if response.Header().Get(nextCursorHeader) != "" {
		t.Error(
			"unexpected next cursor header",
		)
	}

	if reader.sourceQuery.Limit != defaultLimit ||
		reader.sourceQuery.Cursor != nil ||
		reader.sourceQuery.Origin != "" ||
		reader.sourceQuery.Seeded != nil ||
		reader.sourceQuery.Automatic != nil ||
		reader.sourceQuery.Verified != nil ||
		reader.sourceQuery.Blocked != nil ||
		reader.sourceQuery.CrawlEligible != nil {
		t.Errorf(
			"default source query = %#v",
			reader.sourceQuery,
		)
	}
}

func TestPagedHandlersRequireAuthentication(
	t *testing.T,
) {
	reader := &fakePagedReader{
		fakeReader: &fakeReader{},
	}

	handler := mustReportingHandler(
		t,
		reader,
	)

	response := performRequest(
		t,
		handler,
		"/api/v1/sources",
		"",
	)

	if response.Code != http.StatusUnauthorized {
		t.Errorf(
			"status = %d, want %d",
			response.Code,
			http.StatusUnauthorized,
		)
	}
}

func TestPaginationMiddlewarePassesOtherRequestsThrough(
	t *testing.T,
) {
	reader := &fakePagedReader{
		fakeReader: &fakeReader{},
	}

	handler := mustReportingHandler(
		t,
		reader,
	)

	health := performRequest(
		t,
		handler,
		"/healthz",
		"",
	)
	if health.Code != http.StatusOK {
		t.Errorf(
			"health status = %d, want %d",
			health.Code,
			http.StatusOK,
		)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sources",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf(
			"POST status = %d, want %d",
			response.Code,
			http.StatusMethodNotAllowed,
		)
	}
}

func TestPagedHandlersRejectInvalidQueries(
	t *testing.T,
) {
	reader := &fakePagedReader{
		fakeReader: &fakeReader{},
	}

	handler := mustReportingHandler(
		t,
		reader,
	)

	wrongCursor := sourceNextCursor(
		CrawlSource{
			Origin: "https://example.com",
		},
	)

	tests := []struct {
		name     string
		path     string
		wantCode string
	}{
		{
			name:     "invalid limit",
			path:     "/api/v1/sources?limit=0",
			wantCode: "invalid_limit",
		},
		{
			name:     "invalid origin",
			path:     "/api/v1/sources?origin=not-an-origin",
			wantCode: "invalid_origin",
		},
		{
			name:     "duplicate origin",
			path:     "/api/v1/sources?origin=https%3A%2F%2Fa.example&origin=https%3A%2F%2Fb.example",
			wantCode: "invalid_origin",
		},
		{
			name:     "invalid boolean",
			path:     "/api/v1/sources?seeded=1",
			wantCode: "invalid_filter",
		},
		{
			name:     "invalid automatic boolean",
			path:     "/api/v1/sources?automatic=1",
			wantCode: "invalid_filter",
		},
		{
			name:     "invalid verified boolean",
			path:     "/api/v1/sources?verified=1",
			wantCode: "invalid_filter",
		},
		{
			name:     "invalid crawl eligible boolean",
			path:     "/api/v1/sources?crawl_eligible=1",
			wantCode: "invalid_filter",
		},
		{
			name:     "duplicate boolean",
			path:     "/api/v1/sources?blocked=true&blocked=false",
			wantCode: "invalid_filter",
		},
		{
			name:     "invalid crawl limit",
			path:     "/api/v1/crawls?limit=0",
			wantCode: "invalid_limit",
		},
		{
			name:     "invalid crawl origin",
			path:     "/api/v1/crawls?origin=nope",
			wantCode: "invalid_origin",
		},
		{
			name:     "invalid crawl outcome",
			path:     "/api/v1/crawls?outcome=nope",
			wantCode: "invalid_filter",
		},
		{
			name:     "invalid queue limit",
			path:     "/api/v1/queue?limit=0",
			wantCode: "invalid_limit",
		},
		{
			name:     "invalid queue origin",
			path:     "/api/v1/queue?origin=nope",
			wantCode: "invalid_origin",
		},
		{
			name:     "invalid queue mode",
			path:     "/api/v1/queue?mode=nope",
			wantCode: "invalid_filter",
		},
		{
			name:     "invalid leased boolean",
			path:     "/api/v1/queue?leased=1",
			wantCode: "invalid_filter",
		},
		{
			name:     "invalid queue cursor",
			path:     "/api/v1/queue?cursor=bad!",
			wantCode: "invalid_cursor",
		},
		{
			name:     "invalid event limit",
			path:     "/api/v1/queue/events?limit=0",
			wantCode: "invalid_limit",
		},
		{
			name:     "invalid event origin",
			path:     "/api/v1/queue/events?origin=nope",
			wantCode: "invalid_origin",
		},
		{
			name:     "invalid queue event",
			path:     "/api/v1/queue/events?event=nope",
			wantCode: "invalid_filter",
		},
		{
			name:     "invalid event mode",
			path:     "/api/v1/queue/events?mode=nope",
			wantCode: "invalid_filter",
		},
		{
			name:     "invalid event cursor",
			path:     "/api/v1/queue/events?cursor=bad!",
			wantCode: "invalid_cursor",
		},
		{
			name:     "malformed cursor",
			path:     "/api/v1/sources?cursor=not-base64!",
			wantCode: "invalid_cursor",
		},
		{
			name:     "wrong cursor kind",
			path:     "/api/v1/crawls?cursor=" + wrongCursor,
			wantCode: "invalid_cursor",
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				response := performRequest(
					t,
					handler,
					test.path,
					"Bearer "+testToken,
				)

				if response.Code != http.StatusBadRequest {
					t.Errorf(
						"status = %d, want %d",
						response.Code,
						http.StatusBadRequest,
					)
				}

				if body := response.Body.String(); !strings.Contains(
					body,
					test.wantCode,
				) {
					t.Errorf(
						"body = %q, want %q",
						body,
						test.wantCode,
					)
				}
			},
		)
	}
}

func TestPagedHandlersDoNotExposeReaderFailures(
	t *testing.T,
) {
	failure := errors.New("private pagination failure")

	reader := &fakePagedReader{
		fakeReader:        &fakeReader{},
		sourcePageErr:     failure,
		crawlPageErr:      failure,
		queuePageErr:      failure,
		queueEventPageErr: failure,
	}

	handler := mustReportingHandler(
		t,
		reader,
	)

	for _, path := range []string{
		"/api/v1/sources",
		"/api/v1/crawls",
		"/api/v1/queue",
		"/api/v1/queue/events",
	} {
		response := performRequest(
			t,
			handler,
			path,
			"Bearer "+testToken,
		)

		if response.Code !=
			http.StatusInternalServerError {
			t.Errorf(
				"%s status = %d, want %d",
				path,
				response.Code,
				http.StatusInternalServerError,
			)
		}

		if body := response.Body.String(); body != "{\"error\":\"internal_error\"}\n" ||
			strings.Contains(body, failure.Error()) {
			t.Errorf(
				"%s body = %q",
				path,
				body,
			)
		}
	}
}

func TestCursorValidationRejectsMalformedPayloads(
	t *testing.T,
) {
	encodeRaw := func(value string) string {
		return base64.RawURLEncoding.EncodeToString(
			[]byte(value),
		)
	}

	tests := []struct {
		name string
		raw  string
		kind string
	}{
		{
			name: "bad base64",
			raw:  "%%%",
			kind: "sources",
		},
		{
			name: "bad json",
			raw:  encodeRaw("{"),
			kind: "sources",
		},
		{
			name: "trailing json",
			raw: encodeRaw(
				`{"v":1,"kind":"sources","origin":"https://a.example"}{}`,
			),
			kind: "sources",
		},
		{
			name: "unknown field",
			raw: encodeRaw(
				`{"v":1,"kind":"sources","origin":"https://a.example","extra":true}`,
			),
			kind: "sources",
		},
		{
			name: "wrong version",
			raw: encodeRaw(
				`{"v":2,"kind":"sources","origin":"https://a.example"}`,
			),
			kind: "sources",
		},
		{
			name: "wrong kind",
			raw: encodeRaw(
				`{"v":1,"kind":"queue","origin":"https://a.example"}`,
			),
			kind: "sources",
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				if _, err := decodeCursor(
					test.raw,
					test.kind,
				); !errors.Is(
					err,
					errInvalidCursor,
				) {
					t.Errorf(
						"decodeCursor() error = %v, want %v",
						err,
						errInvalidCursor,
					)
				}
			},
		)
	}
}

func TestTypedCursorValidationRejectsWrongShapes(
	t *testing.T,
) {
	encode := func(payload cursorPayload) string {
		return encodeCursor(payload)
	}

	if _, err := decodeSourceCursor(
		encode(cursorPayload{
			Version: cursorVersion,
			Kind:    "sources",
		}),
	); !errors.Is(err, errInvalidCursor) {
		t.Errorf(
			"source cursor error = %v, want invalid cursor",
			err,
		)
	}

	if _, err := decodeSourceCursor(
		encode(cursorPayload{
			Version: cursorVersion,
			Kind:    "sources",
			Origin:  "https://a.example",
			ID:      1,
		}),
	); !errors.Is(err, errInvalidCursor) {
		t.Errorf(
			"source cursor shape error = %v",
			err,
		)
	}

	if _, err := decodeSourceCursor(
		encode(cursorPayload{
			Version: cursorVersion,
			Kind:    "sources",
			Origin:  "https://A.example/path",
		}),
	); !errors.Is(err, errInvalidCursor) {
		t.Errorf(
			"source cursor canonical error = %v",
			err,
		)
	}

	if _, err := decodeCrawlCursor(
		encode(cursorPayload{
			Version: cursorVersion,
			Kind:    "crawls",
			At:      "bad-time",
			ID:      1,
		}),
	); !errors.Is(err, errInvalidCursor) {
		t.Errorf(
			"crawl cursor time error = %v",
			err,
		)
	}

	if _, err := decodeCrawlCursor(
		encode(cursorPayload{
			Version: cursorVersion,
			Kind:    "crawls",
			At:      testNow.Format(time.RFC3339Nano),
			ID:      0,
		}),
	); !errors.Is(err, errInvalidCursor) {
		t.Errorf(
			"crawl cursor ID error = %v",
			err,
		)
	}

	if _, err := decodeQueueCursor(
		encode(cursorPayload{
			Version: cursorVersion,
			Kind:    "queue",
			Origin:  "https://a.example",
			At:      "bad-time",
		}),
	); !errors.Is(err, errInvalidCursor) {
		t.Errorf(
			"queue cursor time error = %v",
			err,
		)
	}

	if _, err := decodeQueueCursor(
		encode(cursorPayload{
			Version: cursorVersion,
			Kind:    "queue",
			At:      testNow.Format(time.RFC3339Nano),
		}),
	); !errors.Is(err, errInvalidCursor) {
		t.Errorf(
			"queue cursor origin error = %v",
			err,
		)
	}

	if _, err := decodeQueueEventCursor(
		encode(cursorPayload{
			Version: cursorVersion,
			Kind:    "queue_events",
			At:      "bad-time",
			ID:      1,
		}),
	); !errors.Is(err, errInvalidCursor) {
		t.Errorf(
			"queue event cursor time error = %v",
			err,
		)
	}

	if _, err := decodeQueueEventCursor(
		encode(cursorPayload{
			Version: cursorVersion,
			Kind:    "queue_events",
			At:      testNow.Format(time.RFC3339Nano),
			ID:      0,
		}),
	); !errors.Is(err, errInvalidCursor) {
		t.Errorf(
			"queue event cursor ID error = %v",
			err,
		)
	}

	for name, call := range map[string]func() error{
		"source": func() error {
			cursor, err := decodeSourceCursor("")
			if cursor != nil {
				t.Errorf(
					"empty source cursor = %#v, want nil",
					cursor,
				)
			}
			return err
		},
		"crawl": func() error {
			cursor, err := decodeCrawlCursor("")
			if cursor != nil {
				t.Errorf(
					"empty crawl cursor = %#v, want nil",
					cursor,
				)
			}
			return err
		},
		"queue": func() error {
			cursor, err := decodeQueueCursor("")
			if cursor != nil {
				t.Errorf(
					"empty queue cursor = %#v, want nil",
					cursor,
				)
			}
			return err
		},
		"event": func() error {
			cursor, err := decodeQueueEventCursor("")
			if cursor != nil {
				t.Errorf(
					"empty event cursor = %#v, want nil",
					cursor,
				)
			}
			return err
		},
	} {
		t.Run(
			"empty "+name,
			func(t *testing.T) {
				if err := call(); err != nil {
					t.Errorf(
						"empty cursor error = %v",
						err,
					)
				}
			},
		)
	}
}

func TestCanonicalCursorOriginValidation(
	t *testing.T,
) {
	if canonicalCursorOrigin("") {
		t.Error("empty cursor origin accepted")
	}

	if canonicalCursorOrigin("not-an-origin") {
		t.Error("invalid cursor origin accepted")
	}

	if canonicalCursorOrigin("https://EXAMPLE.com/path") {
		t.Error("noncanonical cursor origin accepted")
	}

	if !canonicalCursorOrigin("https://example.com") {
		t.Error("canonical cursor origin rejected")
	}
}

func TestPageTrimmingAndLimitValidation(
	t *testing.T,
) {
	if _, err := pageLimit(0); !errors.Is(
		err,
		errInvalidLimit,
	) {
		t.Errorf(
			"pageLimit(0) error = %v, want %v",
			err,
			errInvalidLimit,
		)
	}

	if value, err := pageLimit(2); err != nil || value != 3 {
		t.Errorf(
			"pageLimit(2) = %d, %v; want 3, nil",
			value,
			err,
		)
	}

	sources := trimSourcePage(
		[]CrawlSource{
			{Origin: "https://a.example"},
			{Origin: "https://b.example"},
		},
		1,
	)
	if len(sources.Items) != 1 ||
		sources.NextCursor == "" {
		t.Errorf(
			"source page = %#v",
			sources,
		)
	}

	crawls := trimCrawlPage(
		[]CrawlRun{
			{ID: 2, StartedAt: testNow},
			{ID: 1, StartedAt: testNow.Add(-time.Hour)},
		},
		1,
	)
	if len(crawls.Items) != 1 ||
		crawls.NextCursor == "" {
		t.Errorf(
			"crawl page = %#v",
			crawls,
		)
	}

	queue := trimQueuePage(
		[]QueueItem{
			{
				Origin:      "https://a.example",
				AvailableAt: testNow,
			},
			{
				Origin:      "https://b.example",
				AvailableAt: testNow,
			},
		},
		1,
	)
	if len(queue.Items) != 1 ||
		queue.NextCursor == "" {
		t.Errorf(
			"queue page = %#v",
			queue,
		)
	}

	events := trimQueueEventPage(
		[]QueueEvent{
			{ID: 2, OccurredAt: testNow},
			{ID: 1, OccurredAt: testNow.Add(-time.Hour)},
		},
		1,
	)
	if len(events.Items) != 1 ||
		events.NextCursor == "" {
		t.Errorf(
			"event page = %#v",
			events,
		)
	}

	emptySources := trimSourcePage(
		[]CrawlSource{},
		1,
	)
	if emptySources.Items == nil ||
		len(emptySources.Items) != 0 ||
		emptySources.NextCursor != "" {
		t.Errorf(
			"empty source page = %#v",
			emptySources,
		)
	}

	emptyCrawls := trimCrawlPage(
		[]CrawlRun{},
		1,
	)
	if emptyCrawls.Items == nil ||
		len(emptyCrawls.Items) != 0 ||
		emptyCrawls.NextCursor != "" {
		t.Errorf(
			"empty crawl page = %#v",
			emptyCrawls,
		)
	}

	emptyQueue := trimQueuePage(
		[]QueueItem{},
		1,
	)
	if emptyQueue.Items == nil ||
		len(emptyQueue.Items) != 0 ||
		emptyQueue.NextCursor != "" {
		t.Errorf(
			"empty queue page = %#v",
			emptyQueue,
		)
	}

	emptyEvents := trimQueueEventPage(
		[]QueueEvent{},
		1,
	)
	if emptyEvents.Items == nil ||
		len(emptyEvents.Items) != 0 ||
		emptyEvents.NextCursor != "" {
		t.Errorf(
			"empty event page = %#v",
			emptyEvents,
		)
	}

}

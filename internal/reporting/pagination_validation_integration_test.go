package reporting

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const reportingPaginationIntegrationToken = "integration-reporting-pagination-token"

var reportingPaginationIntegrationTime = time.Date(
	2026,
	time.September,
	19,
	12,
	30,
	0,
	0,
	time.UTC,
)

type reportingPaginationIntegrationReader struct {
	sourceQueries []sourceQuery
	crawlQueries  []crawlQuery
	queueQueries  []queueQuery
	eventQueries  []queueEventQuery
	auditQueries  []auditQuery

	failure           string
	internalErrorText string
}

func (
	reader *reportingPaginationIntegrationReader,
) Status(
	context.Context,
) (Status, error) {
	return Status{}, nil
}

func (
	reader *reportingPaginationIntegrationReader,
) Sources(
	context.Context,
	int,
) ([]CrawlSource, error) {
	return nil, nil
}

func (
	reader *reportingPaginationIntegrationReader,
) Source(
	context.Context,
	string,
	int,
) (SourceDetail, bool, error) {
	return SourceDetail{}, false, nil
}

func (
	reader *reportingPaginationIntegrationReader,
) Crawls(
	context.Context,
	int,
) ([]CrawlRun, error) {
	return nil, nil
}

func (
	reader *reportingPaginationIntegrationReader,
) Crawl(
	context.Context,
	int64,
) (CrawlDetail, bool, error) {
	return CrawlDetail{}, false, nil
}

func (
	reader *reportingPaginationIntegrationReader,
) Queue(
	context.Context,
	int,
) ([]QueueItem, error) {
	return nil, nil
}

func (
	reader *reportingPaginationIntegrationReader,
) QueueEvents(
	context.Context,
	int,
) ([]QueueEvent, error) {
	return nil, nil
}

func (
	reader *reportingPaginationIntegrationReader,
) Audits(
	context.Context,
	int,
) ([]AuditEvent, error) {
	return nil, nil
}

func (
	reader *reportingPaginationIntegrationReader,
) Services(
	context.Context,
) ([]ServiceStatus, error) {
	return nil, nil
}

func (
	reader *reportingPaginationIntegrationReader,
) SourcesPage(
	_ context.Context,
	query sourceQuery,
) (sourcePage, error) {
	reader.sourceQueries = append(
		reader.sourceQueries,
		query,
	)

	if reader.failure == "sources" {
		return sourcePage{},
			errors.New(reader.internalErrorText)
	}

	item := CrawlSource{
		Origin: "https://cursor.example",
	}

	return sourcePage{
		Items: []CrawlSource{
			item,
		},
		NextCursor: sourceNextCursor(item),
	}, nil
}

func (
	reader *reportingPaginationIntegrationReader,
) CrawlsPage(
	_ context.Context,
	query crawlQuery,
) (crawlPage, error) {
	reader.crawlQueries = append(
		reader.crawlQueries,
		query,
	)

	if reader.failure == "crawls" {
		return crawlPage{},
			errors.New(reader.internalErrorText)
	}

	item := CrawlRun{
		ID:           42,
		SourceOrigin: "https://example.com",
		StartedAt:    reportingPaginationIntegrationTime,
	}

	return crawlPage{
		Items: []CrawlRun{
			item,
		},
		NextCursor: crawlNextCursor(item),
	}, nil
}

func (
	reader *reportingPaginationIntegrationReader,
) QueuePage(
	_ context.Context,
	query queueQuery,
) (queuePage, error) {
	reader.queueQueries = append(
		reader.queueQueries,
		query,
	)

	if reader.failure == "queue" {
		return queuePage{},
			errors.New(reader.internalErrorText)
	}

	item := QueueItem{
		Origin:      "https://example.com",
		Mode:        "probe",
		AvailableAt: reportingPaginationIntegrationTime,
	}

	return queuePage{
		Items: []QueueItem{
			item,
		},
		NextCursor: queueNextCursor(item),
	}, nil
}

func (
	reader *reportingPaginationIntegrationReader,
) QueueEventsPage(
	_ context.Context,
	query queueEventQuery,
) (queueEventPage, error) {
	reader.eventQueries = append(
		reader.eventQueries,
		query,
	)

	if reader.failure == "events" {
		return queueEventPage{},
			errors.New(reader.internalErrorText)
	}

	item := QueueEvent{
		ID:         9,
		Origin:     "https://example.com",
		OccurredAt: reportingPaginationIntegrationTime,
		Event:      "claimed",
		Mode:       "probe",
	}

	return queueEventPage{
		Items: []QueueEvent{
			item,
		},
		NextCursor: queueEventNextCursor(item),
	}, nil
}

func (
	reader *reportingPaginationIntegrationReader,
) AuditsPage(
	_ context.Context,
	query auditQuery,
) (auditPage, error) {
	reader.auditQueries = append(
		reader.auditQueries,
		query,
	)

	if reader.failure == "audit" {
		return auditPage{},
			errors.New(reader.internalErrorText)
	}

	item := AuditEvent{
		ID:         11,
		OccurredAt: reportingPaginationIntegrationTime,
		Action:     "processor.pause",
		Target:     "discovery",
		Result:     "success",
	}

	return auditPage{
		Items: []AuditEvent{
			item,
		},
		NextCursor: auditNextCursor(item),
	}, nil
}

func TestReportingPaginationIntegrationRoundTripsFiltersAndCursors(
	t *testing.T,
) {
	reader := &reportingPaginationIntegrationReader{}

	server := newReportingPaginationIntegrationServer(
		t,
		reader,
	)
	defer server.Close()

	t.Run(
		"sources",
		func(t *testing.T) {
			response := reportingPaginationIntegrationGET(
				t,
				server,
				"/api/v1/sources"+
					"?limit=7"+
					"&origin="+
					url.QueryEscape(
						"HTTPS://EXAMPLE.COM/path",
					)+
					"&seeded=true"+
					"&automatic=false"+
					"&verified=true"+
					"&blocked=false"+
					"&crawl_eligible=true",
			)

			if response.StatusCode != http.StatusOK {
				t.Fatalf(
					"status = %d, want %d; body = %q",
					response.StatusCode,
					http.StatusOK,
					response.body,
				)
			}

			cursor := response.Header.Get(
				nextCursorHeader,
			)
			if cursor == "" {
				t.Fatal(
					"sources response has no next cursor",
				)
			}

			if len(reader.sourceQueries) != 1 {
				t.Fatalf(
					"source queries = %d, want 1",
					len(reader.sourceQueries),
				)
			}

			query := reader.sourceQueries[0]

			if query.Limit != 7 {
				t.Errorf(
					"source limit = %d, want 7",
					query.Limit,
				)
			}

			if query.Origin != "https://example.com" {
				t.Errorf(
					"source origin = %q, want https://example.com",
					query.Origin,
				)
			}

			if query.Seeded == nil ||
				!*query.Seeded {
				t.Errorf(
					"source Seeded = %v, want true",
					query.Seeded,
				)
			}

			if query.Automatic == nil ||
				*query.Automatic {
				t.Errorf(
					"source Automatic = %v, want false",
					query.Automatic,
				)
			}

			if query.Verified == nil ||
				!*query.Verified {
				t.Errorf(
					"source Verified = %v, want true",
					query.Verified,
				)
			}

			if query.Blocked == nil ||
				*query.Blocked {
				t.Errorf(
					"source Blocked = %v, want false",
					query.Blocked,
				)
			}

			if query.CrawlEligible == nil ||
				!*query.CrawlEligible {
				t.Errorf(
					"source CrawlEligible = %v, want true",
					query.CrawlEligible,
				)
			}

			response = reportingPaginationIntegrationGET(
				t,
				server,
				"/api/v1/sources?cursor="+
					url.QueryEscape(cursor),
			)

			if response.StatusCode != http.StatusOK {
				t.Fatalf(
					"cursor status = %d, want %d; body = %q",
					response.StatusCode,
					http.StatusOK,
					response.body,
				)
			}

			if len(reader.sourceQueries) != 2 {
				t.Fatalf(
					"source queries = %d, want 2",
					len(reader.sourceQueries),
				)
			}

			cursorQuery := reader.sourceQueries[1]

			if cursorQuery.Cursor == nil {
				t.Fatal(
					"source cursor = nil",
				)
			}

			if cursorQuery.Cursor.Origin !=
				"https://cursor.example" {
				t.Errorf(
					"source cursor origin = %q, want https://cursor.example",
					cursorQuery.Cursor.Origin,
				)
			}
		},
	)

	t.Run(
		"crawls",
		func(t *testing.T) {
			response := reportingPaginationIntegrationGET(
				t,
				server,
				"/api/v1/crawls"+
					"?limit=8"+
					"&origin="+
					url.QueryEscape(
						"https://example.com/path",
					)+
					"&outcome=complete",
			)

			if response.StatusCode != http.StatusOK {
				t.Fatalf(
					"status = %d, want %d; body = %q",
					response.StatusCode,
					http.StatusOK,
					response.body,
				)
			}

			cursor := response.Header.Get(
				nextCursorHeader,
			)
			if cursor == "" {
				t.Fatal(
					"crawls response has no next cursor",
				)
			}

			query := reader.crawlQueries[len(reader.crawlQueries)-1]

			if query.Limit != 8 ||
				query.Origin != "https://example.com" ||
				query.Outcome != "complete" {
				t.Errorf(
					"crawl query = %#v",
					query,
				)
			}

			response = reportingPaginationIntegrationGET(
				t,
				server,
				"/api/v1/crawls?cursor="+
					url.QueryEscape(cursor),
			)

			if response.StatusCode != http.StatusOK {
				t.Fatalf(
					"cursor status = %d, want %d; body = %q",
					response.StatusCode,
					http.StatusOK,
					response.body,
				)
			}

			query = reader.crawlQueries[len(reader.crawlQueries)-1]

			if query.Cursor == nil {
				t.Fatal(
					"crawl cursor = nil",
				)
			}

			if query.Cursor.ID != 42 {
				t.Errorf(
					"crawl cursor ID = %d, want 42",
					query.Cursor.ID,
				)
			}

			if !query.Cursor.StartedAt.Equal(
				reportingPaginationIntegrationTime,
			) {
				t.Errorf(
					"crawl cursor time = %v, want %v",
					query.Cursor.StartedAt,
					reportingPaginationIntegrationTime,
				)
			}
		},
	)

	t.Run(
		"queue",
		func(t *testing.T) {
			response := reportingPaginationIntegrationGET(
				t,
				server,
				"/api/v1/queue"+
					"?limit=9"+
					"&origin=https%3A%2F%2Fexample.com"+
					"&mode=probe"+
					"&leased=true",
			)

			if response.StatusCode != http.StatusOK {
				t.Fatalf(
					"status = %d, want %d; body = %q",
					response.StatusCode,
					http.StatusOK,
					response.body,
				)
			}

			cursor := response.Header.Get(
				nextCursorHeader,
			)
			if cursor == "" {
				t.Fatal(
					"queue response has no next cursor",
				)
			}

			query := reader.queueQueries[len(reader.queueQueries)-1]

			if query.Limit != 9 ||
				query.Origin != "https://example.com" ||
				query.Mode != "probe" {
				t.Errorf(
					"queue query = %#v",
					query,
				)
			}

			if query.Leased == nil ||
				!*query.Leased {
				t.Errorf(
					"queue leased = %v, want true",
					query.Leased,
				)
			}

			response = reportingPaginationIntegrationGET(
				t,
				server,
				"/api/v1/queue?cursor="+
					url.QueryEscape(cursor),
			)

			if response.StatusCode != http.StatusOK {
				t.Fatalf(
					"cursor status = %d, want %d; body = %q",
					response.StatusCode,
					http.StatusOK,
					response.body,
				)
			}

			query = reader.queueQueries[len(reader.queueQueries)-1]

			if query.Cursor == nil {
				t.Fatal(
					"queue cursor = nil",
				)
			}

			if query.Cursor.Origin !=
				"https://example.com" {
				t.Errorf(
					"queue cursor origin = %q, want https://example.com",
					query.Cursor.Origin,
				)
			}

			if !query.Cursor.AvailableAt.Equal(
				reportingPaginationIntegrationTime,
			) {
				t.Errorf(
					"queue cursor time = %v, want %v",
					query.Cursor.AvailableAt,
					reportingPaginationIntegrationTime,
				)
			}
		},
	)

	t.Run(
		"queue events",
		func(t *testing.T) {
			response := reportingPaginationIntegrationGET(
				t,
				server,
				"/api/v1/queue/events"+
					"?limit=10"+
					"&origin=https%3A%2F%2Fexample.com"+
					"&event=claimed"+
					"&mode=probe",
			)

			if response.StatusCode != http.StatusOK {
				t.Fatalf(
					"status = %d, want %d; body = %q",
					response.StatusCode,
					http.StatusOK,
					response.body,
				)
			}

			cursor := response.Header.Get(
				nextCursorHeader,
			)
			if cursor == "" {
				t.Fatal(
					"queue events response has no next cursor",
				)
			}

			query := reader.eventQueries[len(reader.eventQueries)-1]

			if query.Limit != 10 ||
				query.Origin != "https://example.com" ||
				query.Event != "claimed" ||
				query.Mode != "probe" {
				t.Errorf(
					"queue event query = %#v",
					query,
				)
			}

			response = reportingPaginationIntegrationGET(
				t,
				server,
				"/api/v1/queue/events?cursor="+
					url.QueryEscape(cursor),
			)

			if response.StatusCode != http.StatusOK {
				t.Fatalf(
					"cursor status = %d, want %d; body = %q",
					response.StatusCode,
					http.StatusOK,
					response.body,
				)
			}

			query = reader.eventQueries[len(reader.eventQueries)-1]

			if query.Cursor == nil {
				t.Fatal(
					"queue event cursor = nil",
				)
			}

			if query.Cursor.ID != 9 {
				t.Errorf(
					"queue event cursor ID = %d, want 9",
					query.Cursor.ID,
				)
			}

			if !query.Cursor.OccurredAt.Equal(
				reportingPaginationIntegrationTime,
			) {
				t.Errorf(
					"queue event cursor time = %v, want %v",
					query.Cursor.OccurredAt,
					reportingPaginationIntegrationTime,
				)
			}
		},
	)

	t.Run(
		"audit",
		func(t *testing.T) {
			response := reportingPaginationIntegrationGET(
				t,
				server,
				"/api/v1/audit?limit=11",
			)

			if response.StatusCode != http.StatusOK {
				t.Fatalf(
					"status = %d, want %d; body = %q",
					response.StatusCode,
					http.StatusOK,
					response.body,
				)
			}

			cursor := response.Header.Get(
				nextCursorHeader,
			)
			if cursor == "" {
				t.Fatal(
					"audit response has no next cursor",
				)
			}

			response = reportingPaginationIntegrationGET(
				t,
				server,
				"/api/v1/audit?cursor="+
					url.QueryEscape(cursor),
			)

			if response.StatusCode != http.StatusOK {
				t.Fatalf(
					"cursor status = %d, want %d; body = %q",
					response.StatusCode,
					http.StatusOK,
					response.body,
				)
			}

			query := reader.auditQueries[len(reader.auditQueries)-1]

			if query.Cursor == nil {
				t.Fatal(
					"audit cursor = nil",
				)
			}

			if query.Cursor.ID != 11 {
				t.Errorf(
					"audit cursor ID = %d, want 11",
					query.Cursor.ID,
				)
			}

			if !query.Cursor.OccurredAt.Equal(
				reportingPaginationIntegrationTime,
			) {
				t.Errorf(
					"audit cursor time = %v, want %v",
					query.Cursor.OccurredAt,
					reportingPaginationIntegrationTime,
				)
			}
		},
	)
}

func TestReportingPaginationIntegrationRejectsInvalidQueries(
	t *testing.T,
) {
	reader := &reportingPaginationIntegrationReader{}

	server := newReportingPaginationIntegrationServer(
		t,
		reader,
	)
	defer server.Close()

	sourceCursor := sourceNextCursor(
		CrawlSource{
			Origin: "https://example.com",
		},
	)

	tests := []struct {
		name string
		path string
		code string
	}{
		{
			name: "invalid limit",
			path: "/api/v1/sources?limit=0",
			code: "invalid_limit",
		},
		{
			name: "duplicate origin",
			path: "/api/v1/sources" +
				"?origin=https%3A%2F%2Fa.example" +
				"&origin=https%3A%2F%2Fb.example",
			code: "invalid_origin",
		},
		{
			name: "invalid origin scheme",
			path: "/api/v1/sources" +
				"?origin=ftp%3A%2F%2Fexample.com",
			code: "invalid_origin",
		},
		{
			name: "invalid source boolean",
			path: "/api/v1/sources?seeded=yes",
			code: "invalid_filter",
		},
		{
			name: "duplicate source boolean",
			path: "/api/v1/sources" +
				"?seeded=true&seeded=false",
			code: "invalid_filter",
		},
		{
			name: "invalid crawl outcome",
			path: "/api/v1/crawls?outcome=exploded",
			code: "invalid_filter",
		},
		{
			name: "invalid queue mode",
			path: "/api/v1/queue?mode=other",
			code: "invalid_filter",
		},
		{
			name: "invalid leased filter",
			path: "/api/v1/queue?leased=maybe",
			code: "invalid_filter",
		},
		{
			name: "invalid queue event",
			path: "/api/v1/queue/events?event=nope",
			code: "invalid_filter",
		},
		{
			name: "invalid event mode",
			path: "/api/v1/queue/events?mode=other",
			code: "invalid_filter",
		},
		{
			name: "malformed cursor",
			path: "/api/v1/audit?cursor=not-base64",
			code: "invalid_cursor",
		},
		{
			name: "wrong cursor kind",
			path: "/api/v1/crawls?cursor=" +
				url.QueryEscape(sourceCursor),
			code: "invalid_cursor",
		},
		{
			name: "duplicate cursor",
			path: "/api/v1/sources" +
				"?cursor=one&cursor=two",
			code: "invalid_cursor",
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				response := reportingPaginationIntegrationGET(
					t,
					server,
					test.path,
				)

				if response.StatusCode !=
					http.StatusBadRequest {
					t.Fatalf(
						"status = %d, want %d; body = %q",
						response.StatusCode,
						http.StatusBadRequest,
						response.body,
					)
				}

				if !strings.Contains(
					response.body,
					test.code,
				) {
					t.Errorf(
						"body = %q, want %q",
						response.body,
						test.code,
					)
				}
			},
		)
	}
}

func TestReportingPaginationIntegrationSanitizesReaderFailures(
	t *testing.T,
) {
	const privateFailure = "private pagination database failure"

	reader := &reportingPaginationIntegrationReader{
		internalErrorText: privateFailure,
	}

	server := newReportingPaginationIntegrationServer(
		t,
		reader,
	)
	defer server.Close()

	tests := []struct {
		failure string
		path    string
	}{
		{
			failure: "sources",
			path:    "/api/v1/sources",
		},
		{
			failure: "crawls",
			path:    "/api/v1/crawls",
		},
		{
			failure: "queue",
			path:    "/api/v1/queue",
		},
		{
			failure: "events",
			path:    "/api/v1/queue/events",
		},
		{
			failure: "audit",
			path:    "/api/v1/audit",
		},
	}

	for _, test := range tests {
		t.Run(
			test.failure,
			func(t *testing.T) {
				reader.failure = test.failure

				response := reportingPaginationIntegrationGET(
					t,
					server,
					test.path,
				)

				if response.StatusCode !=
					http.StatusInternalServerError {
					t.Fatalf(
						"status = %d, want %d",
						response.StatusCode,
						http.StatusInternalServerError,
					)
				}

				if response.body !=
					"{\"error\":\"internal_error\"}\n" {
					t.Errorf(
						"body = %q",
						response.body,
					)
				}

				if strings.Contains(
					response.body,
					privateFailure,
				) {
					t.Fatal(
						"response exposed reader failure",
					)
				}
			},
		)
	}
}

func TestReportingPaginationIntegrationFallsThroughNonPagedRequests(
	t *testing.T,
) {
	reader := &reportingPaginationIntegrationReader{}

	server := newReportingPaginationIntegrationServer(
		t,
		reader,
	)
	defer server.Close()

	request, err := http.NewRequest(
		http.MethodPost,
		server.URL+"/api/v1/sources",
		nil,
	)
	if err != nil {
		t.Fatalf(
			"http.NewRequest() error = %v",
			err,
		)
	}

	request.Header.Set(
		"Authorization",
		"Bearer "+
			reportingPaginationIntegrationToken,
	)

	response, err := server.Client().Do(
		request,
	)
	if err != nil {
		t.Fatalf(
			"POST sources error = %v",
			err,
		)
	}
	defer response.Body.Close()

	if response.StatusCode !=
		http.StatusMethodNotAllowed {
		t.Errorf(
			"POST sources status = %d, want %d",
			response.StatusCode,
			http.StatusMethodNotAllowed,
		)
	}

	unknown := reportingPaginationIntegrationGET(
		t,
		server,
		"/api/v1/not-a-route",
	)

	if unknown.StatusCode !=
		http.StatusNotFound {
		t.Errorf(
			"unknown route status = %d, want %d",
			unknown.StatusCode,
			http.StatusNotFound,
		)
	}
}

type reportingPaginationIntegrationResponse struct {
	StatusCode int
	Header     http.Header
	body       string
}

func newReportingPaginationIntegrationServer(
	t *testing.T,
	reader Reader,
) *httptest.Server {
	t.Helper()

	handler, err := NewHandler(
		reader,
		reportingPaginationIntegrationToken,
	)
	if err != nil {
		t.Fatalf(
			"NewHandler() error = %v",
			err,
		)
	}

	return httptest.NewServer(
		handler,
	)
}

func reportingPaginationIntegrationGET(
	t *testing.T,
	server *httptest.Server,
	path string,
) reportingPaginationIntegrationResponse {
	t.Helper()

	request, err := http.NewRequest(
		http.MethodGet,
		server.URL+path,
		nil,
	)
	if err != nil {
		t.Fatalf(
			"http.NewRequest() error = %v",
			err,
		)
	}

	request.Header.Set(
		"Authorization",
		"Bearer "+
			reportingPaginationIntegrationToken,
	)

	response, err := server.Client().Do(
		request,
	)
	if err != nil {
		t.Fatalf(
			"GET %s error = %v",
			path,
			err,
		)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(
		response.Body,
	)
	if err != nil {
		t.Fatalf(
			"read %s body: %v",
			path,
			err,
		)
	}

	return reportingPaginationIntegrationResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header.Clone(),
		body:       string(body),
	}
}

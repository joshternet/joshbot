package reporting_test

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

	"github.com/joshternet/joshbot/internal/reporting"
)

const unpagedReportingIntegrationToken = "integration-unpaged-reporting-token"

type unpagedReportingIntegrationReader struct {
	sourceLimit int
	crawlLimit  int
	queueLimit  int
	eventLimit  int
	auditLimit  int
	detailLimit int

	sourceOrigin string
	crawlID      int64

	failure string
}

func (reader *unpagedReportingIntegrationReader) Status(
	context.Context,
) (reporting.Status, error) {
	if reader.failure == "status" {
		return reporting.Status{},
			errors.New("private status failure")
	}

	return reporting.Status{
		Services: []reporting.ServiceStatus{
			{
				Service:   "worker",
				State:     "running",
				UpdatedAt: time.Now().UTC(),
			},
		},
	}, nil
}

func (reader *unpagedReportingIntegrationReader) Sources(
	_ context.Context,
	limit int,
) ([]reporting.CrawlSource, error) {
	reader.sourceLimit = limit

	if reader.failure == "sources" {
		return nil,
			errors.New("private sources failure")
	}

	return []reporting.CrawlSource{
		{
			Origin:        "https://source.example",
			Seeded:        true,
			CrawlEligible: true,
		},
	}, nil
}

func (reader *unpagedReportingIntegrationReader) Source(
	_ context.Context,
	source string,
	limit int,
) (reporting.SourceDetail, bool, error) {
	reader.sourceOrigin = source
	reader.detailLimit = limit

	if reader.failure == "source" {
		return reporting.SourceDetail{},
			false,
			errors.New("private source failure")
	}

	if reader.failure == "source missing" {
		return reporting.SourceDetail{},
			false,
			nil
	}

	return reporting.SourceDetail{
		Source: reporting.CrawlSource{
			Origin:        source,
			CrawlEligible: true,
		},
	}, true, nil
}

func (reader *unpagedReportingIntegrationReader) Crawls(
	_ context.Context,
	limit int,
) ([]reporting.CrawlRun, error) {
	reader.crawlLimit = limit

	if reader.failure == "crawls" {
		return nil,
			errors.New("private crawls failure")
	}

	return []reporting.CrawlRun{
		{
			ID:           42,
			SourceOrigin: "https://source.example",
			StartedAt:    time.Now().UTC(),
			Outcome:      "complete",
		},
	}, nil
}

func (reader *unpagedReportingIntegrationReader) Crawl(
	_ context.Context,
	id int64,
) (reporting.CrawlDetail, bool, error) {
	reader.crawlID = id

	if reader.failure == "crawl" {
		return reporting.CrawlDetail{},
			false,
			errors.New("private crawl failure")
	}

	if reader.failure == "crawl missing" {
		return reporting.CrawlDetail{},
			false,
			nil
	}

	return reporting.CrawlDetail{
		Run: reporting.CrawlRun{
			ID:           id,
			SourceOrigin: "https://source.example",
			StartedAt:    time.Now().UTC(),
			Outcome:      "complete",
		},
	}, true, nil
}

func (reader *unpagedReportingIntegrationReader) Queue(
	_ context.Context,
	limit int,
) ([]reporting.QueueItem, error) {
	reader.queueLimit = limit

	if reader.failure == "queue" {
		return nil,
			errors.New("private queue failure")
	}

	return []reporting.QueueItem{
		{
			Origin:      "https://source.example",
			Mode:        "probe",
			AvailableAt: time.Now().UTC(),
		},
	}, nil
}

func (reader *unpagedReportingIntegrationReader) QueueEvents(
	_ context.Context,
	limit int,
) ([]reporting.QueueEvent, error) {
	reader.eventLimit = limit

	if reader.failure == "events" {
		return nil,
			errors.New("private events failure")
	}

	return []reporting.QueueEvent{
		{
			ID:         9,
			Origin:     "https://source.example",
			OccurredAt: time.Now().UTC(),
			Event:      "claimed",
			Mode:       "probe",
		},
	}, nil
}

func (reader *unpagedReportingIntegrationReader) Audits(
	_ context.Context,
	limit int,
) ([]reporting.AuditEvent, error) {
	reader.auditLimit = limit

	if reader.failure == "audit" {
		return nil,
			errors.New("private audit failure")
	}

	return []reporting.AuditEvent{
		{
			ID:         11,
			OccurredAt: time.Now().UTC(),
			Action:     "processor.pause",
			Target:     "discovery",
			Actor:      "operator",
			Result:     "success",
		},
	}, nil
}

func (reader *unpagedReportingIntegrationReader) Services(
	context.Context,
) ([]reporting.ServiceStatus, error) {
	if reader.failure == "services" {
		return nil,
			errors.New("private services failure")
	}

	return []reporting.ServiceStatus{
		{
			Service:    "worker",
			InstanceID: "worker-one",
			State:      "running",
			StartedAt:  time.Now().Add(-time.Hour).UTC(),
			UpdatedAt:  time.Now().UTC(),
		},
	}, nil
}

func TestUnpagedReportingIntegrationServesPublicReaderContract(
	t *testing.T,
) {
	reader := &unpagedReportingIntegrationReader{}

	server := newUnpagedReportingIntegrationServer(
		t,
		reader,
	)
	defer server.Close()

	tests := []struct {
		path         string
		wantFragment string
	}{
		{
			path:         "/api/v1/sources?limit=7",
			wantFragment: `"origin":"https://source.example"`,
		},
		{
			path:         "/api/v1/crawls?limit=8",
			wantFragment: `"id":42`,
		},
		{
			path:         "/api/v1/crawls/42",
			wantFragment: `"source_origin":"https://source.example"`,
		},
		{
			path:         "/api/v1/queue?limit=9",
			wantFragment: `"mode":"probe"`,
		},
		{
			path:         "/api/v1/queue/events?limit=10",
			wantFragment: `"event":"claimed"`,
		},
		{
			path:         "/api/v1/services",
			wantFragment: `"service":"worker"`,
		},
		{
			path:         "/api/v1/audit?limit=11",
			wantFragment: `"action":"processor.pause"`,
		},
		{
			path: "/api/v1/sources/detail?origin=" +
				url.QueryEscape(
					"HTTPS://SOURCE.EXAMPLE/path?secret=yes",
				) +
				"&limit=12",
			wantFragment: `"origin":"https://source.example"`,
		},
	}

	for _, test := range tests {
		t.Run(
			test.path,
			func(t *testing.T) {
				status, body :=
					unpagedReportingIntegrationGET(
						t,
						server,
						test.path,
					)

				if status != http.StatusOK {
					t.Fatalf(
						"status = %d, want %d; body = %q",
						status,
						http.StatusOK,
						body,
					)
				}

				if !strings.Contains(
					body,
					test.wantFragment,
				) {
					t.Errorf(
						"body = %q, want %q",
						body,
						test.wantFragment,
					)
				}
			},
		)
	}

	if reader.sourceLimit != 7 {
		t.Errorf(
			"source limit = %d, want 7",
			reader.sourceLimit,
		)
	}

	if reader.crawlLimit != 8 {
		t.Errorf(
			"crawl limit = %d, want 8",
			reader.crawlLimit,
		)
	}

	if reader.crawlID != 42 {
		t.Errorf(
			"crawl ID = %d, want 42",
			reader.crawlID,
		)
	}

	if reader.queueLimit != 9 {
		t.Errorf(
			"queue limit = %d, want 9",
			reader.queueLimit,
		)
	}

	if reader.eventLimit != 10 {
		t.Errorf(
			"event limit = %d, want 10",
			reader.eventLimit,
		)
	}

	if reader.auditLimit != 11 {
		t.Errorf(
			"audit limit = %d, want 11",
			reader.auditLimit,
		)
	}

	if reader.detailLimit != 12 {
		t.Errorf(
			"source detail limit = %d, want 12",
			reader.detailLimit,
		)
	}

	if reader.sourceOrigin !=
		"https://source.example" {
		t.Errorf(
			"source detail origin = %q, want https://source.example",
			reader.sourceOrigin,
		)
	}
}

func TestUnpagedReportingIntegrationMetricsCrossesGenericStatusPath(
	t *testing.T,
) {
	reader := &unpagedReportingIntegrationReader{}

	server := newUnpagedReportingIntegrationServer(
		t,
		reader,
	)
	defer server.Close()

	status, body := unpagedReportingIntegrationGET(
		t,
		server,
		"/metrics",
	)

	if status != http.StatusOK {
		t.Fatalf(
			"metrics status = %d, want %d; body = %q",
			status,
			http.StatusOK,
			body,
		)
	}

	if !strings.Contains(
		body,
		`joshbot_service_heartbeat_age_seconds{service="worker",state="running"}`,
	) {
		t.Errorf(
			"metrics body missing service heartbeat:\n%s",
			body,
		)
	}

	if !strings.Contains(
		body,
		"# EOF",
	) {
		t.Errorf(
			"metrics body missing OpenMetrics terminator:\n%s",
			body,
		)
	}
}

func TestUnpagedReportingIntegrationSanitizesReaderFailures(
	t *testing.T,
) {
	tests := []struct {
		failure string
		path    string
	}{
		{
			failure: "status",
			path:    "/api/v1/status",
		},
		{
			failure: "sources",
			path:    "/api/v1/sources",
		},
		{
			failure: "source",
			path: "/api/v1/sources/detail?origin=" +
				url.QueryEscape(
					"https://source.example",
				),
		},
		{
			failure: "crawls",
			path:    "/api/v1/crawls",
		},
		{
			failure: "crawl",
			path:    "/api/v1/crawls/42",
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
			failure: "services",
			path:    "/api/v1/services",
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
				reader :=
					&unpagedReportingIntegrationReader{
						failure: test.failure,
					}

				server :=
					newUnpagedReportingIntegrationServer(
						t,
						reader,
					)
				defer server.Close()

				status, body :=
					unpagedReportingIntegrationGET(
						t,
						server,
						test.path,
					)

				if status !=
					http.StatusInternalServerError {
					t.Fatalf(
						"status = %d, want %d; body = %q",
						status,
						http.StatusInternalServerError,
						body,
					)
				}

				if body !=
					"{\"error\":\"internal_error\"}\n" {
					t.Errorf(
						"body = %q",
						body,
					)
				}

				if strings.Contains(
					body,
					"private",
				) {
					t.Errorf(
						"response exposed reader failure: %q",
						body,
					)
				}
			},
		)
	}
}

func TestUnpagedReportingIntegrationHandlesMissingResourcesAndBadInput(
	t *testing.T,
) {
	tests := []struct {
		name    string
		failure string
		path    string
		status  int
		code    string
	}{
		{
			name:    "missing crawl",
			failure: "crawl missing",
			path:    "/api/v1/crawls/99",
			status:  http.StatusNotFound,
			code:    "crawl_not_found",
		},
		{
			name:    "missing source",
			failure: "source missing",
			path: "/api/v1/sources/detail?origin=" +
				url.QueryEscape(
					"https://source.example",
				),
			status: http.StatusNotFound,
			code:   "source_not_found",
		},
		{
			name:   "bad crawl ID",
			path:   "/api/v1/crawls/not-a-number",
			status: http.StatusBadRequest,
			code:   "invalid_crawl_id",
		},
		{
			name:   "bad source origin",
			path:   "/api/v1/sources/detail?origin=ftp%3A%2F%2Fexample.com",
			status: http.StatusBadRequest,
			code:   "invalid_origin",
		},
		{
			name: "bad source limit",
			path: "/api/v1/sources/detail?origin=" +
				url.QueryEscape(
					"https://source.example",
				) +
				"&limit=0",
			status: http.StatusBadRequest,
			code:   "invalid_limit",
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				reader :=
					&unpagedReportingIntegrationReader{
						failure: test.failure,
					}

				server :=
					newUnpagedReportingIntegrationServer(
						t,
						reader,
					)
				defer server.Close()

				status, body :=
					unpagedReportingIntegrationGET(
						t,
						server,
						test.path,
					)

				if status != test.status {
					t.Fatalf(
						"status = %d, want %d; body = %q",
						status,
						test.status,
						body,
					)
				}

				if !strings.Contains(
					body,
					test.code,
				) {
					t.Errorf(
						"body = %q, want %q",
						body,
						test.code,
					)
				}
			},
		)
	}
}

func newUnpagedReportingIntegrationServer(
	t *testing.T,
	reader reporting.Reader,
) *httptest.Server {
	t.Helper()

	handler, err := reporting.NewHandler(
		reader,
		unpagedReportingIntegrationToken,
	)
	if err != nil {
		t.Fatalf(
			"reporting.NewHandler() error = %v",
			err,
		)
	}

	return httptest.NewServer(
		handler,
	)
}

func unpagedReportingIntegrationGET(
	t *testing.T,
	server *httptest.Server,
	path string,
) (int, string) {
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
		"Bearer "+unpagedReportingIntegrationToken,
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
			"read %s response: %v",
			path,
			err,
		)
	}

	return response.StatusCode,
		string(body)
}

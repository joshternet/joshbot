package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "test-reporting-token"

var testNow = time.Date(
	2026,
	time.September,
	16,
	14,
	0,
	0,
	0,
	time.UTC,
)

type fakeReader struct {
	status      Status
	statusErr   error
	sources     []CrawlSource
	sourcesErr  error
	sourceLimit int
	crawls      []CrawlRun
	crawlsErr   error
	crawlLimit  int
	crawl       CrawlDetail
	crawlFound  bool
	crawlErr    error
	crawlID     int64
	queue       []QueueItem
	queueErr    error
	queueLimit  int
	events      []QueueEvent
	eventsErr   error
	eventLimit  int
	services    []ServiceStatus
	servicesErr error
	audits      []AuditEvent
	auditsErr   error
	auditLimit  int
}

func (reader *fakeReader) Status(
	context.Context,
) (Status, error) {
	return reader.status, reader.statusErr
}

func (reader *fakeReader) Sources(
	_ context.Context,
	limit int,
) ([]CrawlSource, error) {
	reader.sourceLimit = limit

	return reader.sources, reader.sourcesErr
}

func (reader *fakeReader) Crawls(
	_ context.Context,
	limit int,
) ([]CrawlRun, error) {
	reader.crawlLimit = limit

	return reader.crawls, reader.crawlsErr
}

func (reader *fakeReader) Crawl(
	_ context.Context,
	id int64,
) (CrawlDetail, bool, error) {
	reader.crawlID = id

	return reader.crawl,
		reader.crawlFound,
		reader.crawlErr
}

func (reader *fakeReader) Queue(
	_ context.Context,
	limit int,
) ([]QueueItem, error) {
	reader.queueLimit = limit

	return reader.queue, reader.queueErr
}

func (reader *fakeReader) QueueEvents(
	_ context.Context,
	limit int,
) ([]QueueEvent, error) {
	reader.eventLimit = limit

	return reader.events, reader.eventsErr
}

func (reader *fakeReader) Services(
	context.Context,
) ([]ServiceStatus, error) {
	return reader.services,
		reader.servicesErr
}

func (reader *fakeReader) Audits(
	_ context.Context,
	limit int,
) ([]AuditEvent, error) {
	reader.auditLimit = limit
	return reader.audits, reader.auditsErr
}

func TestNewHandlerValidatesDependencies(
	t *testing.T,
) {
	reader := &fakeReader{}

	if _, err := NewHandler(
		nil,
		testToken,
	); !errors.Is(err, errReaderUnavailable) {
		t.Errorf(
			"NewHandler(nil) error = %v, want %v",
			err,
			errReaderUnavailable,
		)
	}

	if _, err := newHandler(
		reader,
		"",
		func() time.Time {
			return testNow
		},
	); !errors.Is(err, errTokenUnavailable) {
		t.Errorf(
			"newHandler(empty token) error = %v, want %v",
			err,
			errTokenUnavailable,
		)
	}

	if _, err := newHandler(
		reader,
		testToken,
		nil,
	); !errors.Is(err, errClockUnavailable) {
		t.Errorf(
			"newHandler(nil clock) error = %v, want %v",
			err,
			errClockUnavailable,
		)
	}

	handler, err := NewHandler(
		reader,
		testToken,
	)
	if err != nil {
		t.Fatalf(
			"NewHandler() error = %v, want nil",
			err,
		)
	}

	if handler == nil {
		t.Fatal(
			"NewHandler() = nil, want handler",
		)
	}
}

func TestHandlerHealthAndAuthentication(
	t *testing.T,
) {
	handler := mustReportingHandler(
		t,
		&fakeReader{},
	)

	response := performRequest(
		t,
		handler,
		"/healthz",
		"",
	)

	if response.Code != http.StatusOK {
		t.Errorf(
			"health status = %d, want %d",
			response.Code,
			http.StatusOK,
		)
	}

	if body := response.Body.String(); body !=
		"{\"status\":\"ok\"}\n" {
		t.Errorf(
			"health body = %q, want status JSON",
			body,
		)
	}

	if got := response.Header().Get(
		"Cache-Control",
	); got != "no-store" {
		t.Errorf(
			"Cache-Control = %q, want no-store",
			got,
		)
	}

	if got := response.Header().Get(
		"X-Content-Type-Options",
	); got != "nosniff" {
		t.Errorf(
			"X-Content-Type-Options = %q, want nosniff",
			got,
		)
	}

	for _, authorization := range []string{
		"",
		"Bearer wrong-token",
	} {
		response = performRequest(
			t,
			handler,
			"/api/v1/status",
			authorization,
		)

		if response.Code !=
			http.StatusUnauthorized {
			t.Errorf(
				"authorization %q status = %d, want %d",
				authorization,
				response.Code,
				http.StatusUnauthorized,
			)
		}

		if got := response.Header().Get(
			"WWW-Authenticate",
		); got !=
			`Bearer realm="joshbot-reporting"` {
			t.Errorf(
				"WWW-Authenticate = %q",
				got,
			)
		}

		if body := response.Body.String(); body !=
			"{\"error\":\"unauthorized\"}\n" {
			t.Errorf(
				"unauthorized body = %q",
				body,
			)
		}
	}
}

func TestReportingAuthorizationUsesFixedLengthDigest(t *testing.T) {
	expected := reportingAuthorizationDigest("Bearer " + testToken)
	if len(expected) != 32 {
		t.Fatalf("digest length = %d, want 32", len(expected))
	}
	if !reportingAuthorizationMatches(expected, "Bearer "+testToken) {
		t.Error("matching authorization rejected")
	}
	for _, provided := range []string{
		"", "Bearer x", "Bearer " + testToken + "suffix",
	} {
		if reportingAuthorizationMatches(expected, provided) {
			t.Errorf("authorization %q accepted", provided)
		}
	}
}

func TestExtendedTelemetryJSONAppendsFieldsWithoutReorderingContracts(
	t *testing.T,
) {
	runJSON, err := json.Marshal(CrawlRun{})
	if err != nil {
		t.Fatal(err)
	}
	runFields := []string{
		`"id":`, `"source_origin":`, `"started_at":`, `"outcome":`,
		`"page_timeout_milliseconds":`, `"promotions_admitted":`,
		`"promotions_deferred":`, `"failure_category":`,
		`"pages_blocked":`, `"pages_failed":`, `"urls_found":`,
		`"urls_enqueued":`, `"frontier_remaining":`,
	}
	assertJSONFieldOrder(t, string(runJSON), runFields)

	pageJSON, err := json.Marshal(PageAttempt{})
	if err != nil {
		t.Fatal(err)
	}
	pageFields := []string{
		`"sequence":`, `"requested_url":`, `"final_url":`,
		`"outcome":`, `"failure_category":`, `"urls_found":`,
		`"urls_enqueued":`,
	}
	assertJSONFieldOrder(t, string(pageJSON), pageFields)
}

func assertJSONFieldOrder(t *testing.T, body string, fields []string) {
	t.Helper()
	previous := -1
	for _, field := range fields {
		index := strings.Index(body, field)
		if index <= previous {
			t.Fatalf("JSON field %s missing or reordered in %s", field, body)
		}
		previous = index
	}
}

func TestHandlerReturnsReportingData(
	t *testing.T,
) {
	statusStarted := testNow.Add(-time.Hour)
	statusUpdated := testNow.Add(-time.Minute)
	discovered := testNow.Add(-24 * time.Hour)
	finished := testNow.Add(-30 * time.Minute)
	statusCode := http.StatusOK
	available := testNow.Add(-5 * time.Minute)
	leaseExpires := testNow.Add(time.Minute)
	lastClaimed := testNow.Add(-time.Minute)

	reader := &fakeReader{
		status: Status{
			Control: ControlStatus{
				DiscoveryPaused:    false,
				VerificationPaused: true,
				UpdatedAt: testNow.Add(
					-time.Minute,
				),
			},
			Backpressure: BackpressureStatus{
				AutomaticCrawlEnabled: true,
				Active:                false,
				PendingProbes:         4,
				MaxPendingProbes:      1000,
			},
			Queue: QueueSummary{
				Total:     7,
				Probe:     4,
				Recurring: 3,
				Leased:    1,
			},
			Sources: SourceSummary{
				Total:         12,
				Seeded:        2,
				Automatic:     8,
				Verified:      2,
				Blocked:       1,
				CrawlEligible: 11,
			},
			Services: []ServiceStatus{
				{
					Service:    "worker",
					InstanceID: "worker-1",
					State:      "running",
					StartedAt:  statusStarted,
					UpdatedAt:  statusUpdated,
				},
			},
		},
		sources: []CrawlSource{
			{
				Origin:            "https://example.com",
				Seeded:            true,
				CrawlEligible:     true,
				FirstDiscoveredAt: &discovered,
				LastDiscoveredAt:  &discovered,
			},
		},
		crawls: []CrawlRun{
			{
				ID:           42,
				SourceOrigin: "https://example.com",
				StartedAt:    testNow.Add(-time.Hour),
				FinishedAt:   &finished,
				Outcome:      "complete",
			},
		},
		crawl: CrawlDetail{
			Run: CrawlRun{
				ID:           42,
				SourceOrigin: "https://example.com",
				StartedAt:    testNow.Add(-time.Hour),
				FinishedAt:   &finished,
				Outcome:      "complete",
			},
			Pages: []PageAttempt{
				{
					Sequence:       1,
					RequestedURL:   "https://example.com/",
					FinalURL:       "https://example.com/",
					StartedAt:      testNow.Add(-time.Hour),
					StatusCode:     &statusCode,
					RobotsDecision: "allowed",
					Outcome:        "complete",
				},
			},
		},
		crawlFound: true,
		queue: []QueueItem{
			{
				Origin:          "https://example.com",
				Mode:            "probe",
				AvailableAt:     available,
				LeaseGeneration: 3,
				LeaseOwner:      "worker-1",
				LeaseExpiresAt:  &leaseExpires,
				LastClaimedAt:   &lastClaimed,
			},
		},
		events: []QueueEvent{
			{
				ID:              9,
				Origin:          "https://example.com",
				OccurredAt:      testNow,
				Event:           "claimed",
				Mode:            "probe",
				AvailableAt:     &available,
				LeaseOwner:      "worker-1",
				LeaseGeneration: 3,
				LeaseExpiresAt:  &leaseExpires,
			},
		},
		services: []ServiceStatus{
			{
				Service:    "worker",
				InstanceID: "worker-1",
				State:      "running",
				StartedAt:  statusStarted,
				UpdatedAt:  statusUpdated,
			},
		},
		audits: []AuditEvent{
			{
				ID: 11, OccurredAt: testNow, Action: "pause",
				Target: "discovery", Caller: "control", Actor: "operator",
				Result: "success",
			},
		},
	}

	handler := mustReportingHandler(
		t,
		reader,
	)

	tests := []struct {
		path         string
		wantFragment string
	}{
		{
			path:         "/api/v1/status",
			wantFragment: `"generated_at":"2026-09-16T14:00:00Z"`,
		},
		{
			path:         "/api/v1/sources?limit=7",
			wantFragment: `"origin":"https://example.com"`,
		},
		{
			path:         "/api/v1/crawls?limit=8",
			wantFragment: `"id":42`,
		},
		{
			path:         "/api/v1/crawls/42",
			wantFragment: `"pages"`,
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
			wantFragment: `"action":"pause"`,
		},
	}

	for _, test := range tests {
		t.Run(
			test.path,
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

				if !strings.Contains(
					response.Body.String(),
					test.wantFragment,
				) {
					t.Errorf(
						"body = %q, want fragment %q",
						response.Body.String(),
						test.wantFragment,
					)
				}

				if got := response.Header().Get(
					"Content-Type",
				); got !=
					"application/json; charset=utf-8" {
					t.Errorf(
						"Content-Type = %q",
						got,
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
		t.Errorf("audit limit = %d, want 11", reader.auditLimit)
	}
}

func TestHandlerUsesDefaultLimit(
	t *testing.T,
) {
	reader := &fakeReader{}
	handler := mustReportingHandler(
		t,
		reader,
	)

	for _, path := range []string{
		"/api/v1/sources",
		"/api/v1/crawls",
		"/api/v1/queue",
		"/api/v1/queue/events",
		"/api/v1/audit",
	} {
		response := performRequest(
			t,
			handler,
			path,
			"Bearer "+testToken,
		)

		if response.Code != http.StatusOK {
			t.Fatalf(
				"%s status = %d, want 200",
				path,
				response.Code,
			)
		}
	}

	if reader.sourceLimit != defaultLimit ||
		reader.crawlLimit != defaultLimit ||
		reader.queueLimit != defaultLimit ||
		reader.eventLimit != defaultLimit ||
		reader.auditLimit != defaultLimit {
		t.Errorf(
			"default limits = %d/%d/%d/%d/%d, want %d",
			reader.sourceLimit,
			reader.crawlLimit,
			reader.queueLimit,
			reader.eventLimit,
			reader.auditLimit,
			defaultLimit,
		)
	}
}

func TestHandlerRejectsInvalidInputsAndMissingCrawl(
	t *testing.T,
) {
	reader := &fakeReader{}
	handler := mustReportingHandler(
		t,
		reader,
	)

	tests := []struct {
		path string
		code string
	}{
		{
			path: "/api/v1/sources?limit=0",
			code: "invalid_limit",
		},
		{
			path: "/api/v1/crawls?limit=invalid",
			code: "invalid_limit",
		},
		{
			path: "/api/v1/queue?limit=1001",
			code: "invalid_limit",
		},
		{
			path: "/api/v1/queue/events?limit=-1",
			code: "invalid_limit",
		},
		{
			path: "/api/v1/crawls/not-a-number",
			code: "invalid_crawl_id",
		},
		{
			path: "/api/v1/crawls/0",
			code: "invalid_crawl_id",
		},
	}

	for _, test := range tests {
		response := performRequest(
			t,
			handler,
			test.path,
			"Bearer "+testToken,
		)

		if response.Code != http.StatusBadRequest {
			t.Errorf(
				"%s status = %d, want %d",
				test.path,
				response.Code,
				http.StatusBadRequest,
			)
		}

		if !strings.Contains(
			response.Body.String(),
			test.code,
		) {
			t.Errorf(
				"%s body = %q, want %q",
				test.path,
				response.Body.String(),
				test.code,
			)
		}
	}

	response := performRequest(
		t,
		handler,
		"/api/v1/crawls/99",
		"Bearer "+testToken,
	)

	if response.Code != http.StatusNotFound {
		t.Errorf(
			"missing crawl status = %d, want %d",
			response.Code,
			http.StatusNotFound,
		)
	}

	if body := response.Body.String(); body !=
		"{\"error\":\"crawl_not_found\"}\n" {
		t.Errorf(
			"missing crawl body = %q",
			body,
		)
	}
}

func TestHandlerDoesNotExposeReaderFailures(
	t *testing.T,
) {
	failure := errors.New(
		"private database error with details",
	)

	tests := []struct {
		name   string
		path   string
		reader *fakeReader
	}{
		{
			name: "status",
			path: "/api/v1/status",
			reader: &fakeReader{
				statusErr: failure,
			},
		},
		{
			name: "sources",
			path: "/api/v1/sources",
			reader: &fakeReader{
				sourcesErr: failure,
			},
		},
		{
			name: "crawls",
			path: "/api/v1/crawls",
			reader: &fakeReader{
				crawlsErr: failure,
			},
		},
		{
			name: "crawl",
			path: "/api/v1/crawls/1",
			reader: &fakeReader{
				crawlErr: failure,
			},
		},
		{
			name: "queue",
			path: "/api/v1/queue",
			reader: &fakeReader{
				queueErr: failure,
			},
		},
		{
			name: "events",
			path: "/api/v1/queue/events",
			reader: &fakeReader{
				eventsErr: failure,
			},
		},
		{
			name: "services",
			path: "/api/v1/services",
			reader: &fakeReader{
				servicesErr: failure,
			},
		},
		{
			name: "audit",
			path: "/api/v1/audit",
			reader: &fakeReader{
				auditsErr: failure,
			},
		},
		{
			name: "metrics",
			path: "/metrics",
			reader: &fakeReader{
				statusErr: failure,
			},
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				handler := mustReportingHandler(
					t,
					test.reader,
				)

				response := performRequest(
					t,
					handler,
					test.path,
					"Bearer "+testToken,
				)

				if response.Code !=
					http.StatusInternalServerError {
					t.Errorf(
						"status = %d, want %d",
						response.Code,
						http.StatusInternalServerError,
					)
				}

				body := response.Body.String()

				if body !=
					"{\"error\":\"internal_error\"}\n" {
					t.Errorf(
						"body = %q",
						body,
					)
				}

				if strings.Contains(
					body,
					failure.Error(),
				) {
					t.Error(
						"response exposed internal failure",
					)
				}
			},
		)
	}
}

func TestMetricsExposeBoundedOperationalState(
	t *testing.T,
) {
	reader := &fakeReader{
		status: Status{
			Control: ControlStatus{
				DiscoveryPaused:    true,
				VerificationPaused: false,
			},
			Backpressure: BackpressureStatus{
				AutomaticCrawlEnabled: true,
				Active:                true,
				PendingProbes:         1000,
				MaxPendingProbes:      1000,
			},
			Queue: QueueSummary{
				Total:     1010,
				Probe:     1000,
				Recurring: 10,
				Leased:    2,
			},
			Sources: SourceSummary{
				Total:         50,
				Seeded:        2,
				Automatic:     45,
				Verified:      3,
				Blocked:       4,
				CrawlEligible: 46,
			},
			Services: []ServiceStatus{
				{
					Service:    "worker",
					InstanceID: "worker-old",
					State:      "idle",
					UpdatedAt: testNow.Add(
						-2 * time.Minute,
					),
				},
				{
					Service:    "worker",
					InstanceID: "worker-new",
					State:      "running",
					UpdatedAt: testNow.Add(
						-30 * time.Second,
					),
				},
				{
					Service:    "discovery",
					InstanceID: "future",
					State:      "idle",
					UpdatedAt: testNow.Add(
						time.Minute,
					),
				},
			},
		},
	}

	handler := mustReportingHandler(
		t,
		reader,
	)

	response := performRequest(
		t,
		handler,
		"/metrics",
		"Bearer "+testToken,
	)

	if response.Code != http.StatusOK {
		t.Fatalf(
			"metrics status = %d, want %d",
			response.Code,
			http.StatusOK,
		)
	}

	if got := response.Header().Get(
		"Content-Type",
	); got !=
		"application/openmetrics-text; version=1.0.0; charset=utf-8" {
		t.Errorf(
			"Content-Type = %q",
			got,
		)
	}

	required := []string{
		"joshbot_discovery_paused 1",
		"joshbot_verification_paused 0",
		"joshbot_automatic_crawl_enabled 1",
		"joshbot_backpressure_active 1",
		"joshbot_pending_probes 1000",
		"joshbot_pending_probe_limit 1000",
		`joshbot_verification_queue{mode="all"} 1010`,
		`joshbot_verification_queue{mode="probe"} 1000`,
		`joshbot_verification_queue{mode="recurring"} 10`,
		"joshbot_verification_leases 2",
		`joshbot_crawl_sources{classification="seeded"} 2`,
		`joshbot_crawl_sources{classification="automatic"} 45`,
		`joshbot_crawl_sources{classification="verified"} 3`,
		`joshbot_crawl_sources{classification="blocked"} 4`,
		`joshbot_crawl_sources{classification="crawl_eligible"} 46`,
		`joshbot_service_heartbeat_age_seconds{service="worker",state="running"} 30`,
		`joshbot_service_heartbeat_age_seconds{service="discovery",state="idle"} 0`,
		"# EOF",
	}

	body := response.Body.String()

	for _, value := range required {
		if !strings.Contains(
			body,
			value,
		) {
			t.Errorf(
				"metrics missing %q:\n%s",
				value,
				body,
			)
		}
	}

	for _, forbidden := range []string{
		"instance_id=",
		"worker-old",
		"worker-new",
		`service="worker",state="idle"`,
	} {
		if strings.Contains(
			body,
			forbidden,
		) {
			t.Errorf(
				"metrics unexpectedly contain %q:\n%s",
				forbidden,
				body,
			)
		}
	}
}

func mustReportingHandler(
	t *testing.T,
	reader Reader,
) http.Handler {
	t.Helper()

	handler, err := newHandler(
		reader,
		testToken,
		func() time.Time {
			return testNow
		},
	)
	if err != nil {
		t.Fatalf(
			"newHandler() error = %v",
			err,
		)
	}

	return handler
}

func performRequest(
	t *testing.T,
	handler http.Handler,
	path string,
	authorization string,
) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(
		http.MethodGet,
		path,
		nil,
	)

	if authorization != "" {
		request.Header.Set(
			"Authorization",
			authorization,
		)
	}

	response := httptest.NewRecorder()

	handler.ServeHTTP(
		response,
		request,
	)

	return response
}

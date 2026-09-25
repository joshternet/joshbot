package reporting

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAuditHandlerUsesOpaqueKeysetPagination(t *testing.T) {
	occurredAt := testNow.Add(-time.Minute)
	cursor := auditNextCursor(AuditEvent{ID: 7, OccurredAt: occurredAt})
	reader := &fakePagedReader{
		fakeReader: &fakeReader{},
		auditPageResult: auditPage{
			Items: []AuditEvent{{
				ID: 8, OccurredAt: testNow, Action: "pause",
				Target: "discovery", Caller: "control", Actor: "operator",
				Result: "success", Reason: "maintenance",
			}},
			NextCursor: "next-audit",
		},
	}
	handler := mustReportingHandler(t, reader)
	response := performRequest(
		t, handler, "/api/v1/audit?limit=7&cursor="+cursor,
		"Bearer "+testToken,
	)
	if response.Code != http.StatusOK ||
		response.Header().Get(nextCursorHeader) != "next-audit" ||
		!strings.Contains(response.Body.String(), `"action":"pause"`) {
		t.Fatalf("audit response = %d %#v %q", response.Code, response.Header(), response.Body.String())
	}
	if reader.auditQuery.Limit != 7 || reader.auditQuery.Cursor == nil ||
		reader.auditQuery.Cursor.ID != 7 ||
		!reader.auditQuery.Cursor.OccurredAt.Equal(occurredAt) {
		t.Errorf("audit query = %#v", reader.auditQuery)
	}
}

func TestAuditHandlerRejectsInvalidCursorAndHidesFailures(t *testing.T) {
	reader := &fakePagedReader{
		fakeReader:   &fakeReader{},
		auditPageErr: errors.New("private database failure"),
	}
	handler := mustReportingHandler(t, reader)
	for _, test := range []struct {
		path string
		code int
	}{
		{"/api/v1/audit?cursor=invalid", http.StatusBadRequest},
		{"/api/v1/audit?limit=1001", http.StatusBadRequest},
		{"/api/v1/audit", http.StatusInternalServerError},
	} {
		response := performRequest(t, handler, test.path, "Bearer "+testToken)
		if response.Code != test.code {
			t.Errorf("%s status = %d, want %d", test.path, response.Code, test.code)
		}
	}
}

func TestAuditFallbackRejectsInvalidLimit(t *testing.T) {
	response := performRequest(
		t,
		mustReportingHandler(t, &fakeReader{}),
		"/api/v1/audit?limit=0",
		"Bearer "+testToken,
	)
	if response.Code != http.StatusBadRequest ||
		response.Body.String() != "{\"error\":\"invalid_limit\"}\n" {
		t.Errorf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestAuditPaginationHelpers(t *testing.T) {
	items := []AuditEvent{
		{ID: 2, OccurredAt: testNow},
		{ID: 1, OccurredAt: testNow.Add(-time.Second)},
	}
	page := trimAuditPage(items, 1)
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("trimAuditPage() = %#v", page)
	}
	payload := encodeCursor(cursorPayload{
		Version: cursorVersion,
		Kind:    "audit",
		At:      "not-a-time",
		ID:      1,
	})
	if _, err := decodeAuditCursor(payload); !errors.Is(err, errInvalidCursor) {
		t.Errorf("invalid audit time error = %v", err)
	}
}

type metricsTestReader struct {
	*fakeReader
	metrics    OperationalMetrics
	metricsErr error
}

func (reader *metricsTestReader) Metrics(context.Context) (OperationalMetrics, error) {
	return reader.metrics, reader.metricsErr
}

func TestMetricsExposeDurableLowCardinalityTotals(t *testing.T) {
	reader := &metricsTestReader{
		fakeReader: &fakeReader{},
		metrics: OperationalMetrics{
			Candidates: 1, DiscoveryEdges: 2, CrawlRuns: 3,
			PagesAttempted: 4, PagesParsed: 5, OriginsFound: 6,
			OriginsPromoted: 7, OriginsDeferred: 8, Failures: 9,
			RobotsDenials: 10,
		},
	}
	response := performRequest(
		t, mustReportingHandler(t, reader), "/metrics", "Bearer "+testToken,
	)
	for _, value := range []string{
		"# TYPE joshbot_retained_candidates gauge",
		"joshbot_retained_candidates 1",
		"joshbot_retained_discovery_edges 2",
		"joshbot_retained_crawl_runs 3",
		"joshbot_retained_pages_attempted 4",
		"joshbot_retained_pages_parsed 5",
		"joshbot_retained_origins_found 6",
		"joshbot_retained_origins_promoted 7",
		"joshbot_retained_origins_deferred 8",
		"joshbot_retained_failures 9",
		"joshbot_retained_robots_denials 10",
	} {
		if !strings.Contains(response.Body.String(), value) {
			t.Errorf("metrics missing %q", value)
		}
	}

	reader.metricsErr = errors.New("metrics unavailable")
	response = performRequest(
		t, mustReportingHandler(t, reader), "/metrics", "Bearer "+testToken,
	)
	if response.Code != http.StatusInternalServerError {
		t.Errorf("metrics failure status = %d", response.Code)
	}
}

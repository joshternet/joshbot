package reporting

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func (reader *fakeReader) Source(
	context.Context,
	string,
	int,
) (SourceDetail, bool, error) {
	return SourceDetail{}, false, nil
}

type sourceDetailReader struct {
	*fakeReader
	detail       SourceDetail
	found        bool
	sourceErr    error
	sourceOrigin string
	sourceLimit  int
}

func (reader *sourceDetailReader) Source(
	_ context.Context,
	sourceOrigin string,
	limit int,
) (SourceDetail, bool, error) {
	reader.sourceOrigin = sourceOrigin
	reader.sourceLimit = limit

	return reader.detail,
		reader.found,
		reader.sourceErr
}

func TestHandlerReturnsSourceDetail(
	t *testing.T,
) {
	first := testNow.Add(-24 * time.Hour)
	last := testNow.Add(-time.Hour)
	available := testNow.Add(-time.Minute)
	finished := testNow.Add(-30 * time.Minute)

	reader := &sourceDetailReader{
		fakeReader: &fakeReader{},
		found:      true,
		detail: SourceDetail{
			Source: CrawlSource{
				Origin:                  "https://example.com",
				Seeded:                  true,
				AutomaticallyDiscovered: true,
				Verified:                true,
				CrawlEligible:           true,
				FirstDiscoveredAt:       &first,
				LastDiscoveredAt:        &last,
			},
			DiscoveredBy: []DiscoveryProvenance{
				{
					SourceOrigin:      "https://source.example",
					Kind:              "link",
					FirstDiscoveredAt: first,
					LastDiscoveredAt:  last,
				},
			},
			Queue: &QueueItem{
				Origin:      "https://example.com",
				Mode:        "recurring",
				AvailableAt: available,
			},
			LatestCrawl: &CrawlRun{
				ID:           42,
				SourceOrigin: "https://example.com",
				StartedAt:    testNow.Add(-time.Hour),
				FinishedAt:   &finished,
				Outcome:      "complete",
			},
			LatestRobotsObservation: &RobotsObservation{
				CrawlRunID: 42,
				Sequence:   1,
				ObservedAt: testNow.Add(-time.Hour),
				Decision:   "allowed",
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
		"/api/v1/sources/detail?origin=https%3A%2F%2Fexample.com&limit=7",
		"Bearer "+testToken,
	)

	if response.Code != http.StatusOK {
		t.Fatalf(
			"source detail status = %d, want %d; body=%q",
			response.Code,
			http.StatusOK,
			response.Body.String(),
		)
	}

	body := response.Body.String()

	for _, fragment := range []string{
		`"origin":"https://example.com"`,
		`"source_origin":"https://source.example"`,
		`"mode":"recurring"`,
		`"id":42`,
		`"decision":"allowed"`,
	} {
		if !strings.Contains(
			body,
			fragment,
		) {
			t.Errorf(
				"source detail body missing %q: %s",
				fragment,
				body,
			)
		}
	}

	if reader.sourceOrigin != "https://example.com" {
		t.Errorf(
			"source origin = %q, want canonical origin",
			reader.sourceOrigin,
		)
	}

	if reader.sourceLimit != 7 {
		t.Errorf(
			"source detail limit = %d, want 7",
			reader.sourceLimit,
		)
	}
}

func TestHandlerSourceDetailUsesDefaultLimit(
	t *testing.T,
) {
	reader := &sourceDetailReader{
		fakeReader: &fakeReader{},
		found:      true,
		detail: SourceDetail{
			Source: CrawlSource{
				Origin: "https://example.com",
			},
			DiscoveredBy: []DiscoveryProvenance{},
		},
	}

	handler := mustReportingHandler(
		t,
		reader,
	)

	response := performRequest(
		t,
		handler,
		"/api/v1/sources/detail?origin=https%3A%2F%2Fexample.com",
		"Bearer "+testToken,
	)

	if response.Code != http.StatusOK {
		t.Fatalf(
			"source detail status = %d, want %d",
			response.Code,
			http.StatusOK,
		)
	}

	if reader.sourceLimit != defaultLimit {
		t.Errorf(
			"source detail limit = %d, want %d",
			reader.sourceLimit,
			defaultLimit,
		)
	}
}

func TestHandlerRejectsInvalidSourceDetailInputs(
	t *testing.T,
) {
	handler := mustReportingHandler(
		t,
		&sourceDetailReader{
			fakeReader: &fakeReader{},
		},
	)

	tests := []struct {
		path string
		code string
	}{
		{
			path: "/api/v1/sources/detail",
			code: "invalid_origin",
		},
		{
			path: "/api/v1/sources/detail?origin=not-an-origin",
			code: "invalid_origin",
		},
		{
			path: "/api/v1/sources/detail?origin=https%3A%2F%2Fexample.com&limit=0",
			code: "invalid_limit",
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
}

func TestHandlerReturnsSourceNotFound(
	t *testing.T,
) {
	handler := mustReportingHandler(
		t,
		&sourceDetailReader{
			fakeReader: &fakeReader{},
		},
	)

	response := performRequest(
		t,
		handler,
		"/api/v1/sources/detail?origin=https%3A%2F%2Fmissing.example",
		"Bearer "+testToken,
	)

	if response.Code != http.StatusNotFound {
		t.Errorf(
			"source detail status = %d, want %d",
			response.Code,
			http.StatusNotFound,
		)
	}

	if body := response.Body.String(); body !=
		"{\"error\":\"source_not_found\"}\n" {
		t.Errorf(
			"source detail body = %q",
			body,
		)
	}
}

func TestHandlerDoesNotExposeSourceDetailFailure(
	t *testing.T,
) {
	failure := errors.New(
		"private source detail database failure",
	)

	handler := mustReportingHandler(
		t,
		&sourceDetailReader{
			fakeReader: &fakeReader{},
			sourceErr:  failure,
		},
	)

	response := performRequest(
		t,
		handler,
		"/api/v1/sources/detail?origin=https%3A%2F%2Fexample.com",
		"Bearer "+testToken,
	)

	if response.Code != http.StatusInternalServerError {
		t.Errorf(
			"source detail status = %d, want %d",
			response.Code,
			http.StatusInternalServerError,
		)
	}

	body := response.Body.String()

	if body !=
		"{\"error\":\"internal_error\"}\n" {
		t.Errorf(
			"source detail body = %q",
			body,
		)
	}

	if strings.Contains(
		body,
		failure.Error(),
	) {
		t.Error(
			"source detail response exposed internal failure",
		)
	}
}

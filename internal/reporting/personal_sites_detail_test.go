package reporting

import (
	"net/http"
	"testing"
)

func TestPersonalSiteDetailRejectsInvalidOrigin(t *testing.T) {
	handler := mustReportingHandler(t, &fakeReader{})
	response := performRequest(
		t,
		handler,
		"/api/v1/personal-sites/detail?origin=not-a-url",
		"Bearer "+testToken,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if got := response.Body.String(); got != "{\"error\":\"invalid_origin\"}\n" {
		t.Fatalf("body = %q", got)
	}
}

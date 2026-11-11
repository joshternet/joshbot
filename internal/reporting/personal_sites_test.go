package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type personalSiteFakeReader struct {
	*fakeReader

	page       personalSitePage
	pageErr    error
	pageQuery  personalSiteQuery
	detail     PersonalSite
	detailOK   bool
	detailErr  error
	detailOrig string
}

func (reader *personalSiteFakeReader) PersonalSitesPage(
	_ context.Context,
	query personalSiteQuery,
) (personalSitePage, error) {
	reader.pageQuery = query
	return reader.page, reader.pageErr
}

func (reader *personalSiteFakeReader) PersonalSite(
	_ context.Context,
	origin string,
) (PersonalSite, bool, error) {
	reader.detailOrig = origin
	return reader.detail, reader.detailOK, reader.detailErr
}

func TestPersonalSitesListAndDetailHandlers(t *testing.T) {
	t.Parallel()

	score := 55
	reader := &personalSiteFakeReader{
		fakeReader: &fakeReader{},
		page: personalSitePage{
			Items: []PersonalSite{{
				Origin:         "https://personal.example",
				EffectiveScore: &score,
			}},
			NextCursor: "next-personal",
		},
		detail: PersonalSite{
			Origin:          "https://personal.example",
			EffectiveScore:  &score,
			ReasonCodes:     []string{"person_schema"},
			CalculatedScore: &score,
		},
		detailOK: true,
	}
	handler := mustReportingHandler(t, reader)

	list := performRequest(
		t,
		handler,
		"/api/v1/personal-sites?limit=5&outcome=assessed&awaiting=false",
		"Bearer "+testToken,
	)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%q", list.Code, list.Body.String())
	}
	if reader.pageQuery.Limit != 5 ||
		reader.pageQuery.Outcome != "assessed" ||
		reader.pageQuery.Awaiting == nil ||
		*reader.pageQuery.Awaiting {
		t.Fatalf("query = %#v", reader.pageQuery)
	}
	if !strings.Contains(list.Body.String(), "https://personal.example") {
		t.Fatalf("list body = %q", list.Body.String())
	}

	detail := performRequest(
		t,
		handler,
		"/api/v1/personal-sites/detail?origin=https%3A%2F%2Fpersonal.example",
		"Bearer "+testToken,
	)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%q", detail.Code, detail.Body.String())
	}
	if reader.detailOrig != "https://personal.example" {
		t.Fatalf("detail origin = %q", reader.detailOrig)
	}
}

func TestPersonalSitesHandlerErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("list without capability", func(t *testing.T) {
		t.Parallel()
		handler := mustReportingHandler(t, &fakeReader{})
		response := performRequest(
			t,
			handler,
			"/api/v1/personal-sites",
			"Bearer "+testToken,
		)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d", response.Code)
		}
	})

	t.Run("list query error", func(t *testing.T) {
		t.Parallel()
		handler := mustReportingHandler(t, &personalSiteFakeReader{
			fakeReader: &fakeReader{},
		})
		response := performRequest(
			t,
			handler,
			"/api/v1/personal-sites?limit=nope",
			"Bearer "+testToken,
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d", response.Code)
		}
	})

	t.Run("list reader error", func(t *testing.T) {
		t.Parallel()
		handler := mustReportingHandler(t, &personalSiteFakeReader{
			fakeReader: &fakeReader{},
			pageErr:    errors.New("boom"),
		})
		response := performRequest(
			t,
			handler,
			"/api/v1/personal-sites",
			"Bearer "+testToken,
		)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d", response.Code)
		}
	})

	t.Run("detail without capability", func(t *testing.T) {
		t.Parallel()
		handler := mustReportingHandler(t, &fakeReader{})
		response := performRequest(
			t,
			handler,
			"/api/v1/personal-sites/detail?origin=https%3A%2F%2Fpersonal.example",
			"Bearer "+testToken,
		)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d", response.Code)
		}
	})

	t.Run("detail not found", func(t *testing.T) {
		t.Parallel()
		handler := mustReportingHandler(t, &personalSiteFakeReader{
			fakeReader: &fakeReader{},
		})
		response := performRequest(
			t,
			handler,
			"/api/v1/personal-sites/detail?origin=https%3A%2F%2Fmissing.example",
			"Bearer "+testToken,
		)
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d", response.Code)
		}
	})

	t.Run("detail invalid origin filter", func(t *testing.T) {
		t.Parallel()
		handler := mustReportingHandler(t, &personalSiteFakeReader{
			fakeReader: &fakeReader{},
			detailErr:  errInvalidOriginFilter,
		})
		response := performRequest(
			t,
			handler,
			"/api/v1/personal-sites/detail?origin=https%3A%2F%2Fpersonal.example",
			"Bearer "+testToken,
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d", response.Code)
		}
	})

	t.Run("detail reader error", func(t *testing.T) {
		t.Parallel()
		handler := mustReportingHandler(t, &personalSiteFakeReader{
			fakeReader: &fakeReader{},
			detailErr:  errors.New("boom"),
		})
		response := performRequest(
			t,
			handler,
			"/api/v1/personal-sites/detail?origin=https%3A%2F%2Fpersonal.example",
			"Bearer "+testToken,
		)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d", response.Code)
		}
	})
}

func TestBuildIdentityHandler(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		services: []ServiceStatus{{
			Service:      "discovery",
			BuildVersion: "1.2.3",
			BuildCommit:  "deadbeef",
			State:        "available",
		}},
	}
	handler := mustReportingHandler(t, reader)
	response := performRequest(t, handler, "/api/v1/build", "Bearer "+testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	var payload BuildStatus
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode = %v", err)
	}
	if payload.Reporter.Version != "test" || len(payload.Services) != 1 {
		t.Fatalf("payload = %#v", payload)
	}

	failing := mustReportingHandler(t, &fakeReader{
		servicesErr: errors.New("services failed"),
	})
	failed := performRequest(t, failing, "/api/v1/build", "Bearer "+testToken)
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", failed.Code)
	}
}

func TestEnrichAndPersonalSiteCursorHelpers(t *testing.T) {
	t.Parallel()

	site := PersonalSite{}
	enrichPersonalSite(&site)
	if site.ReasonCodes == nil || site.ScoreContributions == nil {
		t.Fatal("nil slices not normalized")
	}

	score := 25
	site = PersonalSite{
		CalculatedScore: &score,
		ReasonCodes:     []string{"person_schema", "not-real"},
	}
	enrichPersonalSite(&site)
	if len(site.ScoreContributions) != 1 {
		t.Fatalf("contributions = %#v", site.ScoreContributions)
	}

	encoded := encodePersonalSiteCursor(PersonalSite{
		Origin:         "https://personal.example",
		EffectiveScore: &score,
	})
	cursor, err := decodePersonalSiteCursor(encoded)
	if err != nil || cursor == nil || !cursor.HasScore ||
		cursor.Origin != "https://personal.example" ||
		cursor.EffectiveScore == nil ||
		*cursor.EffectiveScore != score {
		t.Fatalf("cursor = %#v err=%v", cursor, err)
	}

	nullEncoded := encodePersonalSiteCursor(PersonalSite{
		Origin: "https://null.example",
	})
	nullCursor, err := decodePersonalSiteCursor(nullEncoded)
	if err != nil || nullCursor == nil || nullCursor.HasScore {
		t.Fatalf("null cursor = %#v err=%v", nullCursor, err)
	}

	if _, err := decodePersonalSiteCursor(""); err != nil {
		t.Fatalf("empty = %v", err)
	}
	if _, err := decodePersonalSiteCursor("%%%"); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	badKind := encodeCursor(cursorPayload{
		Version: cursorVersion,
		Kind:    "sources",
		Origin:  "https://personal.example",
		At:      "score",
		ID:      1,
	})
	if _, err := decodePersonalSiteCursor(badKind); err == nil {
		t.Fatal("wrong kind accepted")
	}
	badAt := encodeCursor(cursorPayload{
		Version: cursorVersion,
		Kind:    "personal_sites",
		Origin:  "https://personal.example",
		At:      "other",
	})
	if _, err := decodePersonalSiteCursor(badAt); err == nil {
		t.Fatal("bad at accepted")
	}
	missingOrigin := encodeCursor(cursorPayload{
		Version: cursorVersion,
		Kind:    "personal_sites",
		At:      "null",
	})
	if _, err := decodePersonalSiteCursor(missingOrigin); err == nil {
		t.Fatal("missing origin accepted")
	}
}

func TestParsePersonalSiteQueryFilters(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/personal-sites?origin=https%3A%2F%2Fpersonal.example&awaiting=true&outcome=timeout",
		nil,
	)
	query, err := parsePersonalSiteQuery(request)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if query.Origin != "https://personal.example" ||
		query.Outcome != "timeout" ||
		query.Awaiting == nil ||
		!*query.Awaiting {
		t.Fatalf("query = %#v", query)
	}

	badOutcome := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/personal-sites?outcome=nope",
		nil,
	)
	if _, err := parsePersonalSiteQuery(badOutcome); err == nil {
		t.Fatal("bad outcome accepted")
	}

	badAwaiting := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/personal-sites?awaiting=maybe",
		nil,
	)
	if _, err := parsePersonalSiteQuery(badAwaiting); err == nil {
		t.Fatal("bad awaiting accepted")
	}

	badOrigin := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/personal-sites?origin=ftp://example.com",
		nil,
	)
	if _, err := parsePersonalSiteQuery(badOrigin); err == nil {
		t.Fatal("bad origin accepted")
	}

	badCursorValue := encodeCursor(cursorPayload{
		Version: cursorVersion,
		Kind:    "sources",
		Origin:  "https://personal.example",
		At:      "score",
		ID:      1,
	})
	badCursor := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/personal-sites?cursor="+badCursorValue,
		nil,
	)
	if _, err := parsePersonalSiteQuery(badCursor); err == nil {
		t.Fatal("bad cursor accepted")
	}
}

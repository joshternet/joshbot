package control

import (
	"net/http"
	"strings"
	"testing"
)

func TestHandlerPersonalSiteOverrideAndRescoreRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantAction string
		wantTarget string
		check      func(*testing.T, *recordingCommander)
	}{
		{
			name:   "set override",
			method: http.MethodPut,
			path:   "/api/v1/control/personal-sites/override",
			body: `{
				"origin":"https://personal.example",
				"score":77,
				"expected_revision":0,
				"reason":"operator adjustment",
				"actor":"console"
			}`,
			wantStatus: http.StatusOK,
			wantAction: "personal-site.override.set",
			wantTarget: "https://personal.example",
			check: func(t *testing.T, commander *recordingCommander) {
				t.Helper()
				if commander.score != 77 || commander.revision != 0 {
					t.Fatalf(
						"override = score=%d revision=%d",
						commander.score,
						commander.revision,
					)
				}
			},
		},
		{
			name:   "clear override",
			method: http.MethodDelete,
			path:   "/api/v1/control/personal-sites/override",
			body: `{
				"origin":"https://personal.example",
				"expected_revision":3,
				"reason":"clear override"
			}`,
			wantStatus: http.StatusOK,
			wantAction: "personal-site.override.clear",
			wantTarget: "https://personal.example",
			check: func(t *testing.T, commander *recordingCommander) {
				t.Helper()
				if commander.revision != 3 {
					t.Fatalf("revision = %d", commander.revision)
				}
			},
		},
		{
			name:       "request rescore",
			method:     http.MethodPost,
			path:       "/api/v1/control/personal-sites/rescore",
			body:       `{"origin":"https://personal.example"}`,
			wantStatus: http.StatusOK,
			wantAction: "personal-site.rescore.request",
			wantTarget: "https://personal.example",
		},
		{
			name:       "request crawl",
			method:     http.MethodPost,
			path:       "/api/v1/control/origins/crawl",
			body:       `{"origin":"https://personal.example"}`,
			wantStatus: http.StatusOK,
			wantAction: "origin.crawl.request",
			wantTarget: "https://personal.example",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			commander := &recordingCommander{}
			handler := mustControlHandler(t, commander)
			response := controlRequest(
				handler,
				test.method,
				test.path,
				"Bearer "+controlTestToken,
				test.body,
			)
			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d body=%q",
					response.Code,
					response.Body.String(),
				)
			}
			if commander.audit.Action != test.wantAction ||
				commander.audit.Target != test.wantTarget ||
				commander.audit.Result != ResultSuccess {
				t.Fatalf("audit = %#v", commander.audit)
			}
			if test.check != nil {
				test.check(t, commander)
			}
		})
	}
}

func TestHandlerPersonalSiteValidationFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "override missing score",
			method: http.MethodPut,
			path:   "/api/v1/control/personal-sites/override",
			body: `{
				"origin":"https://personal.example",
				"expected_revision":0,
				"reason":"missing score"
			}`,
		},
		{
			name:   "override invalid origin",
			method: http.MethodPut,
			path:   "/api/v1/control/personal-sites/override",
			body: `{
				"origin":"ftp://personal.example",
				"score":10,
				"expected_revision":0,
				"reason":"bad origin"
			}`,
		},
		{
			name:   "override missing reason",
			method: http.MethodPut,
			path:   "/api/v1/control/personal-sites/override",
			body: `{
				"origin":"https://personal.example",
				"score":10,
				"expected_revision":0
			}`,
		},
		{
			name:   "clear missing revision",
			method: http.MethodDelete,
			path:   "/api/v1/control/personal-sites/override",
			body: `{
				"origin":"https://personal.example",
				"reason":"clear"
			}`,
		},
		{
			name:   "clear zero revision",
			method: http.MethodDelete,
			path:   "/api/v1/control/personal-sites/override",
			body: `{
				"origin":"https://personal.example",
				"expected_revision":0,
				"reason":"clear"
			}`,
		},
		{
			name:   "rescore invalid origin",
			method: http.MethodPost,
			path:   "/api/v1/control/personal-sites/rescore",
			body:   `{"origin":"not-an-origin"}`,
		},
		{
			name:   "crawl invalid origin",
			method: http.MethodPost,
			path:   "/api/v1/control/origins/crawl",
			body:   `{"origin":"ftp://example.com"}`,
		},
		{
			name:   "override decode failure",
			method: http.MethodPut,
			path:   "/api/v1/control/personal-sites/override",
			body:   `{`,
		},
		{
			name:   "clear decode failure",
			method: http.MethodDelete,
			path:   "/api/v1/control/personal-sites/override",
			body:   `{`,
		},
		{
			name:   "rescore decode failure",
			method: http.MethodPost,
			path:   "/api/v1/control/personal-sites/rescore",
			body:   `{`,
		},
		{
			name:   "crawl decode failure",
			method: http.MethodPost,
			path:   "/api/v1/control/origins/crawl",
			body:   `{`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			commander := &recordingCommander{}
			handler := mustControlHandler(t, commander)
			response := controlRequest(
				handler,
				test.method,
				test.path,
				"Bearer "+controlTestToken,
				test.body,
			)
			if response.Code != http.StatusBadRequest &&
				response.Code != http.StatusUnsupportedMediaType &&
				response.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf(
					"status = %d body=%q",
					response.Code,
					response.Body.String(),
				)
			}
			if len(commander.rejected) != 1 {
				t.Fatalf("rejected = %#v", commander.rejected)
			}
		})
	}
}

func TestHandlerMutateStatusMapsOperatorErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		body       string
		err        error
		status     string
		wantCode   int
		wantBody   string
		wantAction string
	}{
		{
			name: "override conflict remaps action",
			path: "/api/v1/control/personal-sites/override",
			body: `{
				"origin":"https://personal.example",
				"score":40,
				"expected_revision":1,
				"reason":"conflict"
			}`,
			err:        ErrConflict,
			wantCode:   http.StatusConflict,
			wantBody:   "conflict",
			wantAction: "personal-site.override.conflict",
		},
		{
			name: "override not found",
			path: "/api/v1/control/personal-sites/override",
			body: `{
				"origin":"https://personal.example",
				"score":40,
				"expected_revision":1,
				"reason":"missing"
			}`,
			err:      ErrNotFound,
			wantCode: http.StatusNotFound,
			wantBody: "not_found",
		},
		{
			name: "override forbidden",
			path: "/api/v1/control/personal-sites/override",
			body: `{
				"origin":"https://personal.example",
				"score":40,
				"expected_revision":1,
				"reason":"blocked"
			}`,
			err:      ErrForbidden,
			wantCode: http.StatusForbidden,
			wantBody: "forbidden",
		},
		{
			name:     "rescore empty status defaults ok",
			path:     "/api/v1/control/personal-sites/rescore",
			body:     `{"origin":"https://personal.example"}`,
			status:   "",
			wantCode: http.StatusOK,
			wantBody: `"status":"ok"`,
		},
		{
			name:     "crawl already pending",
			path:     "/api/v1/control/origins/crawl",
			body:     `{"origin":"https://personal.example"}`,
			status:   "already_pending",
			wantCode: http.StatusOK,
			wantBody: `"status":"already_pending"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			method := http.MethodPost
			if strings.Contains(test.path, "override") {
				method = http.MethodPut
			}
			commander := &recordingCommander{
				err:    test.err,
				status: test.status,
			}
			handler := mustControlHandler(t, commander)
			response := controlRequest(
				handler,
				method,
				test.path,
				"Bearer "+controlTestToken,
				test.body,
			)
			if response.Code != test.wantCode {
				t.Fatalf(
					"status = %d body=%q",
					response.Code,
					response.Body.String(),
				)
			}
			if !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("body = %q", response.Body.String())
			}
			if test.wantAction != "" {
				if len(commander.rejected) != 1 ||
					commander.rejected[0].Action != test.wantAction {
					t.Fatalf("rejected = %#v", commander.rejected)
				}
			}
		})
	}
}

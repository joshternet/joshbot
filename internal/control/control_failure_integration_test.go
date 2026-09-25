package control_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/control"
	"github.com/joshternet/joshbot/internal/store"
)

func TestControlHTTPIntegrationRejectsInvalidConstruction(
	t *testing.T,
) {
	if _, err := control.NewHandler(
		nil,
		controlIntegrationToken,
	); err == nil {
		t.Fatal("NewHandler(nil commander) error = nil")
	}

	_, pool := newControlIntegrationEnvironment(t)

	commander, err := store.NewControlStore(pool)
	if err != nil {
		t.Fatalf(
			"NewControlStore() error = %v, want nil",
			err,
		)
	}

	for name, token := range map[string]string{
		"invalid length":    "short",
		"invalid character": "0123456789abcdef0123456789abcde\x7f",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := control.NewHandler(
				commander,
				token,
			); err == nil {
				t.Fatalf(
					"NewHandler() accepted token %q",
					token,
				)
			}
		})
	}
}

func TestControlHTTPIntegrationRejectsMutationValidationFailures(
	t *testing.T,
) {
	ctx := context.Background()
	handler, pool := newControlIntegrationEnvironment(t)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{
			name:   "processor malformed body",
			method: http.MethodPost,
			path:   "/api/v1/control/processors/discovery/pause",
			body:   `{`,
			status: http.StatusBadRequest,
		},
		{
			name:   "domain avoid malformed body",
			method: http.MethodPost,
			path:   "/api/v1/control/domain-avoid",
			body:   `{`,
			status: http.StatusBadRequest,
		},
		{
			name:   "domain avoid removal malformed body",
			method: http.MethodDelete,
			path:   "/api/v1/control/domain-avoid/example.com",
			body:   `{`,
			status: http.StatusBadRequest,
		},
		{
			name:   "domain avoid removal invalid pattern",
			method: http.MethodDelete,
			path:   "/api/v1/control/domain-avoid/bad_pattern",
			status: http.StatusBadRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertControlIntegrationResponse(
				t,
				handler,
				test.method,
				test.path,
				test.body,
				test.status,
			)
		})
	}

	var rejected int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*)
		 FROM operator_audit_events
		 WHERE result = 'rejected'`,
	).Scan(&rejected); err != nil {
		t.Fatalf(
			"count rejected audits: %v",
			err,
		)
	}

	if rejected != len(tests) {
		t.Errorf(
			"rejected audit count = %d, want %d",
			rejected,
			len(tests),
		)
	}
}

func TestControlHTTPIntegrationRejectsStreamingOversizeBody(
	t *testing.T,
) {
	ctx := context.Background()
	handler, pool := newControlIntegrationEnvironment(t)

	body := `{"pattern":"example.com"}` +
		strings.Repeat(" ", 4096)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/control/domain-avoid",
		strings.NewReader(body),
	)
	request.ContentLength = -1
	request.Header.Set(
		"Authorization",
		"Bearer "+controlIntegrationToken,
	)
	request.Header.Set(
		"Content-Type",
		"application/json",
	)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"streaming oversize status = %d, want %d; body = %q",
			response.Code,
			http.StatusRequestEntityTooLarge,
			response.Body.String(),
		)
	}

	var rejected int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*)
		 FROM operator_audit_events
		 WHERE result = 'rejected'`,
	).Scan(&rejected); err != nil {
		t.Fatalf(
			"count streaming oversize rejection: %v",
			err,
		)
	}

	if rejected != 1 {
		t.Errorf(
			"rejected audit count = %d, want 1",
			rejected,
		)
	}
}

func TestControlHTTPIntegrationBoundsPersistedAudit(
	t *testing.T,
) {
	ctx := context.Background()
	handler, pool := newControlIntegrationEnvironment(t)

	path := "/api/v1/control/" + strings.Repeat("x", 3000)

	request := httptest.NewRequest(
		http.MethodPost,
		path,
		nil,
	)
	request.RemoteAddr = ""
	request.Header.Set(
		"Authorization",
		"Bearer "+controlIntegrationToken,
	)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf(
			"unknown control request status = %d, want %d; body = %q",
			response.Code,
			http.StatusNotFound,
			response.Body.String(),
		)
	}

	var caller, target string
	if err := pool.QueryRow(
		ctx,
		`SELECT caller, target
		 FROM operator_audit_events
		 WHERE action = 'request.reject'
		 ORDER BY id DESC
		 LIMIT 1`,
	).Scan(&caller, &target); err != nil {
		t.Fatalf(
			"query rejected request audit: %v",
			err,
		)
	}

	if caller != "unknown" {
		t.Errorf(
			"audit caller = %q, want %q",
			caller,
			"unknown",
		)
	}

	if len(target) != 2048 {
		t.Errorf(
			"audit target length = %d, want 2048",
			len(target),
		)
	}
}

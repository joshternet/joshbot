package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const controlTestToken = "0123456789abcdef0123456789abcdef"

type recordingCommander struct {
	processor string
	paused    bool
	pattern   string
	source    string
	blocked   bool
	audit     Audit
	rejected  []Audit
	err       error
	auditErr  error
}

func (commander *recordingCommander) SetProcessorPaused(
	_ context.Context,
	processor string,
	paused bool,
	audit Audit,
) error {
	commander.processor = processor
	commander.paused = paused
	commander.audit = audit
	return commander.err
}

func (commander *recordingCommander) AddDomainAvoid(
	_ context.Context,
	pattern string,
	audit Audit,
) error {
	commander.pattern = pattern
	commander.audit = audit
	return commander.err
}

func (commander *recordingCommander) RemoveDomainAvoid(
	_ context.Context,
	pattern string,
	audit Audit,
) error {
	commander.pattern = pattern
	commander.audit = audit
	return commander.err
}

func (commander *recordingCommander) SetOriginBlocked(
	_ context.Context,
	source string,
	blocked bool,
	audit Audit,
) error {
	commander.source = source
	commander.blocked = blocked
	commander.audit = audit
	return commander.err
}

func (commander *recordingCommander) RecordRejected(
	_ context.Context,
	audit Audit,
) error {
	commander.rejected = append(commander.rejected, audit)
	return commander.auditErr
}

func TestHandlerRequiresIndependentOperatorAuthorization(t *testing.T) {
	handler := mustControlHandler(t, &recordingCommander{})
	for _, authorization := range []string{
		"",
		"Bearer reporting-token",
		"Bearer wrong",
	} {
		response := controlRequest(
			handler,
			http.MethodPost,
			"/api/v1/control/processors/discovery/pause",
			authorization,
			`{}`,
		)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("authorization %q status = %d, want 401", authorization, response.Code)
		}
		if response.Header().Get("WWW-Authenticate") != `Bearer realm="joshbot-operator"` {
			t.Errorf("WWW-Authenticate = %q", response.Header().Get("WWW-Authenticate"))
		}
		if response.Body.String() != "{\"error\":\"unauthorized\"}\n" {
			t.Errorf("body = %q", response.Body.String())
		}
	}
}

func TestAuthorizationComparisonUsesFixedLengthDigests(t *testing.T) {
	expected := authorizationDigest("Bearer " + controlTestToken)
	if len(expected) != 32 {
		t.Fatalf("authorization digest length = %d, want 32", len(expected))
	}
	for _, provided := range []string{
		"",
		"B",
		"Bearer short",
		"Bearer " + controlTestToken + strings.Repeat("x", 4096),
	} {
		digest := authorizationDigest(provided)
		if len(digest) != len(expected) {
			t.Errorf("digest length = %d for input length %d", len(digest), len(provided))
		}
		if authorizationMatches(expected, provided) {
			t.Errorf("authorizationMatches accepted input length %d", len(provided))
		}
	}
	if !authorizationMatches(expected, "Bearer "+controlTestToken) {
		t.Error("authorizationMatches rejected exact bearer value")
	}
}

func TestHandlerHealthAndSecurityHeaders(t *testing.T) {
	handler := mustControlHandler(t, &recordingCommander{})
	response := controlRequest(handler, http.MethodGet, "/healthz", "", "")
	if response.Code != http.StatusOK || response.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("health = %d %q", response.Code, response.Body.String())
	}
	for name, want := range map[string]string{
		"Cache-Control":          "no-store",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := response.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestHandlerRoutesAllMutations(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantAction string
		wantTarget string
		check      func(*testing.T, *recordingCommander)
	}{
		{
			name: "pause discovery", method: http.MethodPost,
			path:       "/api/v1/control/processors/discovery/pause",
			body:       `{"actor":"console","reason":"maintenance"}`,
			wantAction: "processor.pause", wantTarget: "discovery",
			check: func(t *testing.T, commander *recordingCommander) {
				if commander.processor != "discovery" || !commander.paused {
					t.Errorf("processor mutation = %q %v", commander.processor, commander.paused)
				}
			},
		},
		{
			name: "resume verification", method: http.MethodPost,
			path: "/api/v1/control/processors/verification/resume", body: "",
			wantAction: "processor.resume", wantTarget: "verification",
			check: func(t *testing.T, commander *recordingCommander) {
				if commander.processor != "verification" || commander.paused {
					t.Errorf("processor mutation = %q %v", commander.processor, commander.paused)
				}
			},
		},
		{
			name: "add avoid", method: http.MethodPost,
			path:       "/api/v1/control/domain-avoid",
			body:       `{"pattern":"Example.COM","reason":"abuse"}`,
			wantAction: "domain-avoid.add", wantTarget: "example.com",
			check: func(t *testing.T, commander *recordingCommander) {
				if commander.pattern != "example.com" {
					t.Errorf("pattern = %q", commander.pattern)
				}
			},
		},
		{
			name: "remove avoid", method: http.MethodDelete,
			path: "/api/v1/control/domain-avoid/example.com", body: "",
			wantAction: "domain-avoid.remove", wantTarget: "example.com",
			check: func(t *testing.T, commander *recordingCommander) {
				if commander.pattern != "example.com" {
					t.Errorf("pattern = %q", commander.pattern)
				}
			},
		},
		{
			name: "block origin", method: http.MethodPost,
			path:       "/api/v1/control/origins/block",
			body:       `{"origin":"HTTPS://EXAMPLE.COM/path?secret=yes"}`,
			wantAction: "origin.block", wantTarget: "https://example.com",
			check: func(t *testing.T, commander *recordingCommander) {
				if commander.source != "https://example.com" || !commander.blocked {
					t.Errorf("origin mutation = %q %v", commander.source, commander.blocked)
				}
			},
		},
		{
			name: "allow origin", method: http.MethodPost,
			path:       "/api/v1/control/origins/allow",
			body:       `{"origin":"http://example.com:8080/"}`,
			wantAction: "origin.allow", wantTarget: "http://example.com:8080",
			check: func(t *testing.T, commander *recordingCommander) {
				if commander.source != "http://example.com:8080" || commander.blocked {
					t.Errorf("origin mutation = %q %v", commander.source, commander.blocked)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commander := &recordingCommander{}
			handler := mustControlHandler(t, commander)
			response := controlRequest(handler, test.method, test.path, "Bearer "+controlTestToken, test.body)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
			}
			test.check(t, commander)
			if commander.audit.Action != test.wantAction || commander.audit.Target != test.wantTarget ||
				commander.audit.Result != ResultSuccess {
				t.Errorf("audit = %#v", commander.audit)
			}
			if test.name == "pause discovery" &&
				(commander.audit.Actor != "console" || commander.audit.Reason != "maintenance") {
				t.Errorf("metadata audit = %#v", commander.audit)
			}
			if test.name != "pause discovery" && commander.audit.Actor != DefaultActor {
				t.Errorf("default actor = %q", commander.audit.Actor)
			}
		})
	}
}

func TestHandlerRejectsMalformedAuthenticatedAttemptsAndAudits(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		contentType string
		body        string
	}{
		{name: "content type", path: "/api/v1/control/origins/block", contentType: "text/plain", body: `{}`},
		{name: "missing field", path: "/api/v1/control/origins/block", contentType: "application/json", body: `{}`},
		{name: "unknown field", path: "/api/v1/control/origins/block", contentType: "application/json", body: `{"origin":"https://example.com","extra":true}`},
		{name: "trailing data", path: "/api/v1/control/origins/block", contentType: "application/json", body: `{"origin":"https://example.com"} {}`},
		{name: "invalid origin", path: "/api/v1/control/origins/block", contentType: "application/json", body: `{"origin":"ftp://example.com"}`},
		{name: "oversized", path: "/api/v1/control/origins/block", contentType: "application/json", body: strings.Repeat("x", maxRequestBodyBytes+1)},
		{name: "invalid actor", path: "/api/v1/control/processors/discovery/pause", contentType: "application/json", body: `{"actor":"bad\nactor"}`},
		{name: "invalid reason", path: "/api/v1/control/processors/discovery/pause", contentType: "application/json", body: `{"reason":"` + strings.Repeat("x", maxReasonBytes+1) + `"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commander := &recordingCommander{}
			handler := mustControlHandler(t, commander)
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer "+controlTestToken)
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest && response.Code != http.StatusUnsupportedMediaType &&
				response.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("status = %d, body = %q", response.Code, response.Body.String())
			}
			if len(commander.rejected) != 1 || commander.rejected[0].Result != ResultRejected {
				t.Errorf("rejected audits = %#v", commander.rejected)
			}
		})
	}
}

func TestHandlerAuditsStoreFailureWithoutExposingDetails(t *testing.T) {
	secret := "token-and-reason-secret"
	commander := &recordingCommander{err: errors.New(secret)}
	handler := mustControlHandler(t, commander)
	response := controlRequest(
		handler,
		http.MethodPost,
		"/api/v1/control/processors/discovery/pause",
		"Bearer "+controlTestToken,
		`{"reason":"`+secret+`"}`,
	)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), secret) || strings.Contains(response.Body.String(), controlTestToken) {
		t.Errorf("response exposed secret: %q", response.Body.String())
	}
	if len(commander.rejected) != 1 || commander.rejected[0].Result != ResultRejected {
		t.Errorf("failure audits = %#v", commander.rejected)
	}
}

func TestHandlerAuditsMethodAndRouteRejections(t *testing.T) {
	tests := []struct {
		method string
		path   string
		status int
	}{
		{
			method: http.MethodGet,
			path:   "/api/v1/control/origins/block",
			status: http.StatusMethodNotAllowed,
		},
		{
			method: http.MethodPost,
			path:   "/api/v1/control/unknown",
			status: http.StatusNotFound,
		},
	}
	for _, test := range tests {
		commander := &recordingCommander{}
		handler := mustControlHandler(t, commander)
		response := controlRequest(
			handler,
			test.method,
			test.path,
			"Bearer "+controlTestToken,
			"",
		)
		if response.Code != test.status {
			t.Errorf("%s %s status = %d, want %d", test.method, test.path, response.Code, test.status)
		}
		if len(commander.rejected) != 1 ||
			commander.rejected[0].Action != "request.reject" {
			t.Errorf("rejected audits = %#v", commander.rejected)
		}
	}
}

func TestNewHandlerValidatesDependenciesAndToken(t *testing.T) {
	if _, err := NewHandler(nil, controlTestToken); err == nil {
		t.Error("nil commander accepted")
	}
	for _, token := range []string{
		"", "short", strings.Repeat(" ", 32), strings.Repeat("x", 32),
		"0123456789abcdef\n0123456789abcdef",
	} {
		if _, err := NewHandler(&recordingCommander{}, token); err == nil {
			t.Errorf("token %q accepted", token)
		}
	}
}

func TestControlValidationHelpersCoverBoundaries(t *testing.T) {
	for _, token := range []string{
		"0123456789abcdef0123456789abcde\x7f",
		strings.Repeat("01234567", (maxRequestBodyBytes/8)+1),
	} {
		if validToken(token) {
			t.Errorf("validToken(%q) = true", token)
		}
	}
	for path, want := range map[string]bool{
		"/api/v1/control/domain-avoid":                   true,
		"/api/v1/control/domain-avoid/example.com":       true,
		"/api/v1/control/domain-avoid/":                  false,
		"/api/v1/control/processors/discovery/pause":     true,
		"/api/v1/control/processors/verification/resume": true,
		"/api/v1/control/processors/other/pause":         false,
		"/api/v1/control/processors/discovery/other":     false,
		"/api/v1/control/processors/discovery":           false,
	} {
		if got := knownControlPath(path); got != want {
			t.Errorf("knownControlPath(%q) = %v, want %v", path, got, want)
		}
	}
	for _, pattern := range []string{
		"",
		strings.Repeat("a", 254),
		"bad_pattern.example",
		strings.Repeat("a", 64) + ".example",
		"-bad.example",
		"bad-.example",
	} {
		if validDomainPattern(pattern) {
			t.Errorf("validDomainPattern(%q) = true", pattern)
		}
	}
	if got := bounded("abcdef", 3); got != "abc" {
		t.Errorf("bounded() = %q", got)
	}
}

func TestHandlerRejectsEveryMutationValidationPath(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"processor identity", http.MethodPost, "/api/v1/control/processors/other/pause", `{}`},
		{"processor operation", http.MethodPost, "/api/v1/control/processors/discovery/other", `{}`},
		{"processor body", http.MethodPost, "/api/v1/control/processors/discovery/pause", `{`},
		{"add body", http.MethodPost, "/api/v1/control/domain-avoid", `{`},
		{"add pattern", http.MethodPost, "/api/v1/control/domain-avoid", `{"pattern":"bad_pattern"}`},
		{"remove body", http.MethodDelete, "/api/v1/control/domain-avoid/example.com", `{`},
		{"remove pattern", http.MethodDelete, "/api/v1/control/domain-avoid/bad_pattern", ``},
		{"origin operation", http.MethodPost, "/api/v1/control/origins/other", `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commander := &recordingCommander{}
			response := controlRequest(
				mustControlHandler(t, commander),
				test.method,
				test.path,
				"Bearer "+controlTestToken,
				test.body,
			)
			if response.Code < 400 {
				t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
			}
			if len(commander.rejected) != 1 {
				t.Errorf("rejected audits = %#v", commander.rejected)
			}
		})
	}
}

func TestHandlerHandlesRejectedAuditFailure(t *testing.T) {
	commander := &recordingCommander{auditErr: errors.New("audit unavailable")}
	response := controlRequest(
		mustControlHandler(t, commander),
		http.MethodPost,
		"/api/v1/control/origins/block",
		"Bearer "+controlTestToken,
		`{}`,
	)
	if response.Code != http.StatusInternalServerError ||
		response.Body.String() != "{\"error\":\"internal_error\"}\n" {
		t.Errorf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestHandlerBuildsUnknownAndBoundedAuditCaller(t *testing.T) {
	runtime := &handler{
		commander: &recordingCommander{},
		now:       func() time.Time { return time.Unix(123, 0) },
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.RemoteAddr = ""
	audit := runtime.newAudit(
		request,
		strings.Repeat("a", maxActionBytes+1),
		strings.Repeat("t", maxTargetBytes+1),
	)
	if audit.Caller != "unknown" || len(audit.Action) != maxActionBytes ||
		len(audit.Target) != maxTargetBytes || !audit.OccurredAt.Equal(time.Unix(123, 0).UTC()) {
		t.Errorf("audit = %#v", audit)
	}
}

func TestDecodeJSONRejectsStreamingOversize(t *testing.T) {
	for _, body := range []string{
		strings.Repeat(" ", maxRequestBodyBytes) + `{}`,
		`{}` + strings.Repeat(" ", maxRequestBodyBytes),
	} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		request.ContentLength = -1
		request.Header.Set("Content-Type", "application/json")
		status, code := decodeJSON(httptest.NewRecorder(), request, &struct{}{})
		if status != http.StatusRequestEntityTooLarge || code != "request_too_large" {
			t.Errorf("decodeJSON() = %d, %q", status, code)
		}
	}
}

func mustControlHandler(t *testing.T, commander Commander) http.Handler {
	t.Helper()
	handler, err := NewHandler(commander, controlTestToken)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func controlRequest(handler http.Handler, method, path, authorization, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

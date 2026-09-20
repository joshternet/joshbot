package control

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const controlValidationIntegrationToken = "0123456789abcdef0123456789abcdef"

type controlValidationIntegrationCommander struct {
	rejected    []Audit
	patterns    []string
	mutationErr error
	auditErr    error
}

func (
	commander *controlValidationIntegrationCommander,
) SetProcessorPaused(
	context.Context,
	string,
	bool,
	Audit,
) error {
	return commander.mutationErr
}

func (
	commander *controlValidationIntegrationCommander,
) AddDomainAvoid(
	_ context.Context,
	pattern string,
	_ Audit,
) error {
	commander.patterns = append(
		commander.patterns,
		pattern,
	)

	return commander.mutationErr
}

func (
	commander *controlValidationIntegrationCommander,
) RemoveDomainAvoid(
	context.Context,
	string,
	Audit,
) error {
	return commander.mutationErr
}

func (
	commander *controlValidationIntegrationCommander,
) SetOriginBlocked(
	context.Context,
	string,
	bool,
	Audit,
) error {
	return commander.mutationErr
}

func (
	commander *controlValidationIntegrationCommander,
) RecordRejected(
	_ context.Context,
	audit Audit,
) error {
	commander.rejected = append(
		commander.rejected,
		audit,
	)

	return commander.auditErr
}

func TestControlValidationIntegrationClassifiesRoutes(
	t *testing.T,
) {
	commander :=
		&controlValidationIntegrationCommander{}

	server :=
		newControlValidationIntegrationServer(
			t,
			commander,
		)
	defer server.Close()

	tests := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{
			name:   "domain avoid wrong method",
			method: http.MethodGet,
			path:   "/api/v1/control/domain-avoid",
			status: http.StatusMethodNotAllowed,
		},
		{
			name:   "domain removal wrong method",
			method: http.MethodGet,
			path: "/api/v1/control/" +
				"domain-avoid/example.com",
			status: http.StatusMethodNotAllowed,
		},
		{
			name:   "processor wrong method",
			method: http.MethodGet,
			path: "/api/v1/control/" +
				"processors/discovery/pause",
			status: http.StatusMethodNotAllowed,
		},
		{
			name:   "origin wrong method",
			method: http.MethodGet,
			path: "/api/v1/control/" +
				"origins/block",
			status: http.StatusMethodNotAllowed,
		},
		{
			name:   "unknown route",
			method: http.MethodPost,
			path: "/api/v1/control/" +
				"does-not-exist",
			status: http.StatusNotFound,
		},
		{
			name:   "unknown processor",
			method: http.MethodPost,
			path: "/api/v1/control/" +
				"processors/other/pause",
			status: http.StatusNotFound,
		},
		{
			name:   "unknown processor operation",
			method: http.MethodPost,
			path: "/api/v1/control/" +
				"processors/discovery/stop",
			status: http.StatusNotFound,
		},
		{
			name:   "unknown origin operation",
			method: http.MethodPost,
			path: "/api/v1/control/" +
				"origins/freeze",
			status: http.StatusNotFound,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				status, body :=
					controlValidationIntegrationRequest(
						t,
						server.Client(),
						server.URL+test.path,
						test.method,
						"",
						"",
						nil,
					)

				if status != test.status {
					t.Fatalf(
						"status = %d, want %d; body = %q",
						status,
						test.status,
						body,
					)
				}
			},
		)
	}

	if len(commander.rejected) != len(tests) {
		t.Errorf(
			"rejected audits = %d, want %d",
			len(commander.rejected),
			len(tests),
		)
	}
}

func TestControlValidationIntegrationRejectsMalformedBodies(
	t *testing.T,
) {
	tests := []struct {
		name          string
		path          string
		contentType   string
		body          string
		contentLength *int64
		status        int
	}{
		{
			name:        "unsupported media type",
			path:        "/api/v1/control/origins/block",
			contentType: "text/plain",
			body:        `{}`,
			status:      http.StatusUnsupportedMediaType,
		},
		{
			name:        "malformed JSON",
			path:        "/api/v1/control/origins/block",
			contentType: "application/json",
			body:        `{"origin":`,
			status:      http.StatusBadRequest,
		},
		{
			name:        "unknown JSON field",
			path:        "/api/v1/control/origins/block",
			contentType: "application/json",
			body: `{"origin":"https://example.com",` +
				`"surprise":true}`,
			status: http.StatusBadRequest,
		},
		{
			name:        "trailing JSON value",
			path:        "/api/v1/control/origins/block",
			contentType: "application/json",
			body: `{"origin":"https://example.com"}` +
				` {}`,
			status: http.StatusBadRequest,
		},
		{
			name:        "invalid origin",
			path:        "/api/v1/control/origins/block",
			contentType: "application/json",
			body:        `{"origin":"ftp://example.com"}`,
			status:      http.StatusBadRequest,
		},
		{
			name: "actor surrounding whitespace",
			path: "/api/v1/control/" +
				"processors/discovery/pause",
			contentType: "application/json",
			body:        `{"actor":" operator "}`,
			status:      http.StatusBadRequest,
		},
		{
			name: "reason too long",
			path: "/api/v1/control/" +
				"processors/discovery/pause",
			contentType: "application/json",
			body: `{"reason":"` +
				strings.Repeat(
					"x",
					maxReasonBytes+1,
				) +
				`"}`,
			status: http.StatusBadRequest,
		},
	}

	knownLength :=
		int64(maxRequestBodyBytes + 1)

	tests = append(
		tests,
		struct {
			name          string
			path          string
			contentType   string
			body          string
			contentLength *int64
			status        int
		}{
			name:        "known oversized body",
			path:        "/api/v1/control/origins/block",
			contentType: "application/json",
			body: strings.Repeat(
				"x",
				maxRequestBodyBytes+1,
			),
			contentLength: &knownLength,
			status:        http.StatusRequestEntityTooLarge,
		},
	)

	streamingLength := int64(-1)
	streamingOversizedBody :=
		`{"origin":"https://example.com/` +
			strings.Repeat(
				"a",
				maxRequestBodyBytes,
			) +
			`"}`

	tests = append(
		tests,
		struct {
			name          string
			path          string
			contentType   string
			body          string
			contentLength *int64
			status        int
		}{
			name:          "streaming oversized body",
			path:          "/api/v1/control/origins/block",
			contentType:   "application/json",
			body:          streamingOversizedBody,
			contentLength: &streamingLength,
			status:        http.StatusRequestEntityTooLarge,
		},
	)

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				commander :=
					&controlValidationIntegrationCommander{}

				server :=
					newControlValidationIntegrationServer(
						t,
						commander,
					)
				defer server.Close()

				status, body :=
					controlValidationIntegrationRequest(
						t,
						server.Client(),
						server.URL+test.path,
						http.MethodPost,
						test.contentType,
						test.body,
						test.contentLength,
					)

				if status != test.status {
					t.Fatalf(
						"status = %d, want %d; body = %q",
						status,
						test.status,
						body,
					)
				}

				if len(commander.rejected) != 1 {
					t.Errorf(
						"rejected audits = %d, want 1",
						len(commander.rejected),
					)
				}
			},
		)
	}
}

func TestControlValidationIntegrationValidatesDomainPatterns(
	t *testing.T,
) {
	invalid := []string{
		"",
		"-example.com",
		"example-.com",
		strings.Repeat("a", 64) + ".com",
		strings.Repeat("a", 254),
	}

	for _, pattern := range invalid {
		t.Run(
			pattern,
			func(t *testing.T) {
				commander :=
					&controlValidationIntegrationCommander{}

				server :=
					newControlValidationIntegrationServer(
						t,
						commander,
					)
				defer server.Close()

				status, body :=
					controlValidationIntegrationRequest(
						t,
						server.Client(),
						server.URL+
							"/api/v1/control/domain-avoid",
						http.MethodPost,
						"application/json",
						`{"pattern":"`+
							pattern+
							`"}`,
						nil,
					)

				if status != http.StatusBadRequest {
					t.Fatalf(
						"pattern %q status = %d, want %d; body = %q",
						pattern,
						status,
						http.StatusBadRequest,
						body,
					)
				}

				if len(commander.patterns) != 0 {
					t.Errorf(
						"invalid pattern %q reached mutation",
						pattern,
					)
				}
			},
		)
	}

	commander :=
		&controlValidationIntegrationCommander{}

	server :=
		newControlValidationIntegrationServer(
			t,
			commander,
		)
	defer server.Close()

	status, body :=
		controlValidationIntegrationRequest(
			t,
			server.Client(),
			server.URL+
				"/api/v1/control/domain-avoid",
			http.MethodPost,
			"application/json",
			`{"pattern":"Example.COM.*"}`,
			nil,
		)

	if status != http.StatusOK {
		t.Fatalf(
			"wildcard status = %d, want %d; body = %q",
			status,
			http.StatusOK,
			body,
		)
	}

	if len(commander.patterns) != 1 ||
		commander.patterns[0] !=
			"example.com.*" {
		t.Errorf(
			"stored patterns = %#v, want example.com.*",
			commander.patterns,
		)
	}
}

func TestControlValidationIntegrationSanitizesMutationAndAuditFailures(
	t *testing.T,
) {
	privateFailure := errors.New(
		"private control implementation failure",
	)

	t.Run(
		"mutation failure",
		func(t *testing.T) {
			commander :=
				&controlValidationIntegrationCommander{
					mutationErr: privateFailure,
				}

			server :=
				newControlValidationIntegrationServer(
					t,
					commander,
				)
			defer server.Close()

			status, body :=
				controlValidationIntegrationRequest(
					t,
					server.Client(),
					server.URL+
						"/api/v1/control/processors/discovery/pause",
					http.MethodPost,
					"application/json",
					`{}`,
					nil,
				)

			if status !=
				http.StatusInternalServerError {
				t.Fatalf(
					"status = %d, want %d",
					status,
					http.StatusInternalServerError,
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
				privateFailure.Error(),
			) {
				t.Fatal(
					"response exposed mutation failure",
				)
			}

			if len(commander.rejected) != 1 {
				t.Errorf(
					"rejected audits = %d, want 1",
					len(commander.rejected),
				)
			}
		},
	)

	t.Run(
		"rejected audit failure",
		func(t *testing.T) {
			commander :=
				&controlValidationIntegrationCommander{
					auditErr: privateFailure,
				}

			server :=
				newControlValidationIntegrationServer(
					t,
					commander,
				)
			defer server.Close()

			status, body :=
				controlValidationIntegrationRequest(
					t,
					server.Client(),
					server.URL+
						"/api/v1/control/origins/block",
					http.MethodPost,
					"application/json",
					`{"origin":"ftp://example.com"}`,
					nil,
				)

			if status !=
				http.StatusInternalServerError {
				t.Fatalf(
					"status = %d, want %d",
					status,
					http.StatusInternalServerError,
				)
			}

			if body !=
				"{\"error\":\"internal_error\"}\n" {
				t.Errorf(
					"body = %q",
					body,
				)
			}
		},
	)
}

func newControlValidationIntegrationServer(
	t *testing.T,
	commander Commander,
) *httptest.Server {
	t.Helper()

	handler, err := NewHandler(
		commander,
		controlValidationIntegrationToken,
	)
	if err != nil {
		t.Fatalf(
			"NewHandler() error = %v",
			err,
		)
	}

	return httptest.NewServer(handler)
}

func controlValidationIntegrationRequest(
	t *testing.T,
	client *http.Client,
	target string,
	method string,
	contentType string,
	body string,
	contentLength *int64,
) (int, string) {
	t.Helper()

	request, err := http.NewRequest(
		method,
		target,
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf(
			"http.NewRequest() error = %v",
			err,
		)
	}

	request.Header.Set(
		"Authorization",
		"Bearer "+controlValidationIntegrationToken,
	)

	if contentType != "" {
		request.Header.Set(
			"Content-Type",
			contentType,
		)
	}

	if contentLength != nil {
		request.ContentLength = *contentLength
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf(
			"HTTP request error = %v",
			err,
		)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(
		response.Body,
	)
	if err != nil {
		t.Fatalf(
			"read response body: %v",
			err,
		)
	}

	return response.StatusCode, string(data)
}

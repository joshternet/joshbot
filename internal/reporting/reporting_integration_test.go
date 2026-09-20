package reporting_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/reporting"
	"github.com/joshternet/joshbot/internal/store"
)

const reportingIntegrationToken = "integration-reporting-token"

var reportingIntegrationSchemaSequence uint64

func TestReportingHTTPIntegrationReadsMigratedPostgresState(
	t *testing.T,
) {
	handler, _ := newReportingIntegrationEnvironment(t)

	health := reportingIntegrationRequest(
		t,
		handler,
		"/healthz",
		"",
	)
	if health.Code != http.StatusOK {
		t.Fatalf(
			"health status = %d, want %d; body = %q",
			health.Code,
			http.StatusOK,
			health.Body.String(),
		)
	}

	for name, want := range map[string]string{
		"Cache-Control":          "no-store",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := health.Header().Get(name); got != want {
			t.Errorf(
				"health %s = %q, want %q",
				name,
				got,
				want,
			)
		}
	}

	unauthorized := reportingIntegrationRequest(
		t,
		handler,
		"/api/v1/status",
		"",
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf(
			"unauthorized status = %d, want %d; body = %q",
			unauthorized.Code,
			http.StatusUnauthorized,
			unauthorized.Body.String(),
		)
	}

	if got := unauthorized.Header().Get("WWW-Authenticate"); got !=
		`Bearer realm="joshbot-reporting"` {
		t.Errorf(
			"WWW-Authenticate = %q",
			got,
		)
	}

	tests := []struct {
		path         string
		wantStatus   int
		wantFragment string
	}{
		{
			path:         "/api/v1/status",
			wantStatus:   http.StatusOK,
			wantFragment: `"control"`,
		},
		{
			path:         "/api/v1/sources",
			wantStatus:   http.StatusOK,
			wantFragment: "[]",
		},
		{
			path:         "/api/v1/crawls",
			wantStatus:   http.StatusOK,
			wantFragment: "[]",
		},
		{
			path:         "/api/v1/crawls/1",
			wantStatus:   http.StatusNotFound,
			wantFragment: `"crawl_not_found"`,
		},
		{
			path:         "/api/v1/queue",
			wantStatus:   http.StatusOK,
			wantFragment: "[]",
		},
		{
			path:         "/api/v1/queue/events",
			wantStatus:   http.StatusOK,
			wantFragment: "[]",
		},
		{
			path:         "/api/v1/services",
			wantStatus:   http.StatusOK,
			wantFragment: "[]",
		},
		{
			path:         "/api/v1/audit",
			wantStatus:   http.StatusOK,
			wantFragment: "[]",
		},
		{
			path: "/api/v1/sources/detail?origin=" +
				url.QueryEscape("https://example.com"),
			wantStatus:   http.StatusNotFound,
			wantFragment: `"source_not_found"`,
		},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := reportingIntegrationRequest(
				t,
				handler,
				test.path,
				"Bearer "+reportingIntegrationToken,
			)

			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d; body = %q",
					response.Code,
					test.wantStatus,
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
		})
	}

	metrics := reportingIntegrationRequest(
		t,
		handler,
		"/metrics",
		"Bearer "+reportingIntegrationToken,
	)
	if metrics.Code != http.StatusOK {
		t.Fatalf(
			"metrics status = %d, want %d; body = %q",
			metrics.Code,
			http.StatusOK,
			metrics.Body.String(),
		)
	}

	if got := metrics.Header().Get("Content-Type"); got !=
		"application/openmetrics-text; version=1.0.0; charset=utf-8" {
		t.Errorf(
			"metrics Content-Type = %q",
			got,
		)
	}

	for _, fragment := range []string{
		"joshbot_pending_probes 0",
		"joshbot_retained_crawl_runs 0",
		"# EOF",
	} {
		if !strings.Contains(metrics.Body.String(), fragment) {
			t.Errorf(
				"metrics missing %q:\n%s",
				fragment,
				metrics.Body.String(),
			)
		}
	}
}

func TestReportingHTTPIntegrationRejectsInvalidQueries(
	t *testing.T,
) {
	handler, _ := newReportingIntegrationEnvironment(t)

	tests := []struct {
		path string
		code string
	}{
		{
			path: "/api/v1/sources?limit=0",
			code: "invalid_limit",
		},
		{
			path: "/api/v1/sources?origin=ftp%3A%2F%2Fexample.com",
			code: "invalid_origin",
		},
		{
			path: "/api/v1/sources?seeded=maybe",
			code: "invalid_filter",
		},
		{
			path: "/api/v1/sources?cursor=not-base64",
			code: "invalid_cursor",
		},
		{
			path: "/api/v1/crawls?outcome=unknown",
			code: "invalid_filter",
		},
		{
			path: "/api/v1/crawls/not-a-number",
			code: "invalid_crawl_id",
		},
		{
			path: "/api/v1/queue?mode=unknown",
			code: "invalid_filter",
		},
		{
			path: "/api/v1/queue?leased=maybe",
			code: "invalid_filter",
		},
		{
			path: "/api/v1/queue/events?event=unknown",
			code: "invalid_filter",
		},
		{
			path: "/api/v1/audit?cursor=not-base64",
			code: "invalid_cursor",
		},
		{
			path: "/api/v1/sources/detail",
			code: "invalid_origin",
		},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := reportingIntegrationRequest(
				t,
				handler,
				test.path,
				"Bearer "+reportingIntegrationToken,
			)

			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want %d; body = %q",
					response.Code,
					http.StatusBadRequest,
					response.Body.String(),
				)
			}

			if !strings.Contains(
				response.Body.String(),
				test.code,
			) {
				t.Errorf(
					"body = %q, want %q",
					response.Body.String(),
					test.code,
				)
			}
		})
	}
}

func TestReportingHTTPIntegrationSanitizesDatabaseFailure(
	t *testing.T,
) {
	handler, _ := newReportingIntegrationEnvironment(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/status",
		nil,
	).WithContext(ctx)
	request.Header.Set(
		"Authorization",
		"Bearer "+reportingIntegrationToken,
	)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf(
			"cancelled status request = %d, want %d; body = %q",
			response.Code,
			http.StatusInternalServerError,
			response.Body.String(),
		)
	}

	if response.Body.String() != "{\"error\":\"internal_error\"}\n" {
		t.Errorf(
			"cancelled status body = %q",
			response.Body.String(),
		)
	}
}

func newReportingIntegrationEnvironment(
	t *testing.T,
) (http.Handler, *pgxpool.Pool) {
	t.Helper()

	databaseURL := os.Getenv("JOSHBOT_TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("JOSHBOT_REQUIRE_DATABASE_TESTS") == "1" {
			t.Fatal("JOSHBOT_TEST_DATABASE_URL is required")
		}

		t.Skip("JOSHBOT_TEST_DATABASE_URL is not configured")
	}

	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf(
			"create reporting integration admin pool: %v",
			err,
		)
	}
	t.Cleanup(adminPool.Close)

	if err := adminPool.Ping(ctx); err != nil {
		t.Fatalf(
			"ping reporting integration database: %v",
			err,
		)
	}

	schema := fmt.Sprintf(
		"reporting_test_%d_%d",
		os.Getpid(),
		atomic.AddUint64(
			&reportingIntegrationSchemaSequence,
			1,
		),
	)

	if _, err := adminPool.Exec(
		ctx,
		"CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize(),
	); err != nil {
		t.Fatalf(
			"create reporting integration schema: %v",
			err,
		)
	}

	schemaURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf(
			"parse reporting integration database URL: %v",
			err,
		)
	}

	query := schemaURL.Query()
	query.Set("search_path", schema)
	schemaURL.RawQuery = query.Encode()

	pool, err := pgxpool.New(ctx, schemaURL.String())
	if err != nil {
		t.Fatalf(
			"create reporting integration pool: %v",
			err,
		)
	}

	t.Cleanup(func() {
		pool.Close()

		if _, cleanupErr := adminPool.Exec(
			context.Background(),
			"DROP SCHEMA "+
				pgx.Identifier{schema}.Sanitize()+
				" CASCADE",
		); cleanupErr != nil {
			t.Errorf(
				"drop reporting integration schema: %v",
				cleanupErr,
			)
		}
	})

	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf(
			"migrate reporting integration schema: %v",
			err,
		)
	}

	reader, err := reporting.NewPostgresReader(
		pool,
		reporting.PostgresConfig{
			AutomaticCrawlEnabled: true,
			MaxPendingProbes:      1000,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewPostgresReader() error = %v, want nil",
			err,
		)
	}

	handler, err := reporting.NewHandler(
		reader,
		reportingIntegrationToken,
	)
	if err != nil {
		t.Fatalf(
			"reporting.NewHandler() error = %v, want nil",
			err,
		)
	}

	return handler, pool
}

func reportingIntegrationRequest(
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
	handler.ServeHTTP(response, request)

	return response
}

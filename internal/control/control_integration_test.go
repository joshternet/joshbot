package control_test

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
	"github.com/joshternet/joshbot/internal/control"
	"github.com/joshternet/joshbot/internal/store"
)

const controlIntegrationToken = "0123456789abcdef0123456789abcdef"

var controlIntegrationSchemaSequence uint64

func TestControlHTTPIntegrationPersistsMutationsAndAudits(
	t *testing.T,
) {
	ctx := context.Background()
	handler, pool := newControlIntegrationEnvironment(t)

	health := controlIntegrationRequest(
		t,
		handler,
		http.MethodGet,
		"/healthz",
		"",
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
		"Referrer-Policy":        "no-referrer",
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

	unauthorized := controlIntegrationRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/control/processors/discovery/pause",
		"",
		`{}`,
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf(
			"unauthorized status = %d, want %d; body = %q",
			unauthorized.Code,
			http.StatusUnauthorized,
			unauthorized.Body.String(),
		)
	}

	assertControlIntegrationResponse(
		t,
		handler,
		http.MethodPost,
		"/api/v1/control/processors/discovery/pause",
		`{"actor":"integration","reason":"maintenance"}`,
		http.StatusOK,
	)

	var discoveryPaused bool
	if err := pool.QueryRow(
		ctx,
		`SELECT discovery_paused FROM crawl_control WHERE singleton`,
	).Scan(&discoveryPaused); err != nil {
		t.Fatalf(
			"query discovery pause state: %v",
			err,
		)
	}

	if !discoveryPaused {
		t.Fatal("discovery_paused = false, want true")
	}

	assertControlIntegrationResponse(
		t,
		handler,
		http.MethodPost,
		"/api/v1/control/processors/discovery/resume",
		`{}`,
		http.StatusOK,
	)

	if err := pool.QueryRow(
		ctx,
		`SELECT discovery_paused FROM crawl_control WHERE singleton`,
	).Scan(&discoveryPaused); err != nil {
		t.Fatalf(
			"query discovery resume state: %v",
			err,
		)
	}

	if discoveryPaused {
		t.Fatal("discovery_paused = true, want false")
	}

	assertControlIntegrationResponse(
		t,
		handler,
		http.MethodPost,
		"/api/v1/control/domain-avoid",
		`{"pattern":"Example.COM.","reason":"integration"}`,
		http.StatusOK,
	)

	var avoidCount int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM crawl_domain_avoid_rules WHERE pattern = 'example.com'`,
	).Scan(&avoidCount); err != nil {
		t.Fatalf(
			"query domain avoid rule: %v",
			err,
		)
	}

	if avoidCount != 1 {
		t.Fatalf(
			"domain avoid rule count = %d, want 1",
			avoidCount,
		)
	}

	assertControlIntegrationResponse(
		t,
		handler,
		http.MethodPost,
		"/api/v1/control/origins/block",
		`{"origin":"HTTPS://EXAMPLE.ORG/path?secret=yes"}`,
		http.StatusOK,
	)

	var operatorBlocked, crawlBlocked bool
	if err := pool.QueryRow(
		ctx,
		`SELECT operator_blocked, crawl_blocked
		 FROM discovery_source_state
		 WHERE source_origin = 'https://example.org'`,
	).Scan(&operatorBlocked, &crawlBlocked); err != nil {
		t.Fatalf(
			"query blocked origin state: %v",
			err,
		)
	}

	if !operatorBlocked || !crawlBlocked {
		t.Fatalf(
			"blocked origin state = operator:%t crawl:%t, want true/true",
			operatorBlocked,
			crawlBlocked,
		)
	}

	assertControlIntegrationResponse(
		t,
		handler,
		http.MethodPost,
		"/api/v1/control/origins/allow",
		`{"origin":"https://example.org"}`,
		http.StatusOK,
	)

	if err := pool.QueryRow(
		ctx,
		`SELECT operator_blocked, crawl_blocked
		 FROM discovery_source_state
		 WHERE source_origin = 'https://example.org'`,
	).Scan(&operatorBlocked, &crawlBlocked); err != nil {
		t.Fatalf(
			"query allowed origin state: %v",
			err,
		)
	}

	if operatorBlocked || crawlBlocked {
		t.Fatalf(
			"allowed origin state = operator:%t crawl:%t, want false/false",
			operatorBlocked,
			crawlBlocked,
		)
	}

	assertControlIntegrationResponse(
		t,
		handler,
		http.MethodDelete,
		"/api/v1/control/domain-avoid/example.com",
		"",
		http.StatusOK,
	)

	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM crawl_domain_avoid_rules WHERE pattern = 'example.com'`,
	).Scan(&avoidCount); err != nil {
		t.Fatalf(
			"query removed domain avoid rule: %v",
			err,
		)
	}

	if avoidCount != 0 {
		t.Fatalf(
			"removed domain avoid rule count = %d, want 0",
			avoidCount,
		)
	}

	assertControlIntegrationResponse(
		t,
		handler,
		http.MethodGet,
		"/api/v1/control/origins/block",
		"",
		http.StatusMethodNotAllowed,
	)

	assertControlIntegrationResponse(
		t,
		handler,
		http.MethodPost,
		"/api/v1/control/origins/block",
		`{"origin":"ftp://example.org"}`,
		http.StatusBadRequest,
	)

	assertControlIntegrationResponse(
		t,
		handler,
		http.MethodPost,
		"/api/v1/control/domain-avoid",
		`{"pattern":"bad_pattern"}`,
		http.StatusBadRequest,
	)

	plainRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/control/origins/block",
		strings.NewReader(`{"origin":"https://example.net"}`),
	)
	plainRequest.Header.Set(
		"Authorization",
		"Bearer "+controlIntegrationToken,
	)
	plainRequest.Header.Set(
		"Content-Type",
		"text/plain",
	)

	plainResponse := httptest.NewRecorder()
	handler.ServeHTTP(
		plainResponse,
		plainRequest,
	)

	if plainResponse.Code != http.StatusUnsupportedMediaType {
		t.Fatalf(
			"plain request status = %d, want %d; body = %q",
			plainResponse.Code,
			http.StatusUnsupportedMediaType,
			plainResponse.Body.String(),
		)
	}

	var successAudits int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*)
		 FROM operator_audit_events
		 WHERE result = 'success'`,
	).Scan(&successAudits); err != nil {
		t.Fatalf(
			"count successful audits: %v",
			err,
		)
	}

	if successAudits != 6 {
		t.Errorf(
			"successful audit count = %d, want 6",
			successAudits,
		)
	}

	var rejectedAudits int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*)
		 FROM operator_audit_events
		 WHERE result = 'rejected'`,
	).Scan(&rejectedAudits); err != nil {
		t.Fatalf(
			"count rejected audits: %v",
			err,
		)
	}

	if rejectedAudits != 4 {
		t.Errorf(
			"rejected audit count = %d, want 4",
			rejectedAudits,
		)
	}

	var actor, reason string
	if err := pool.QueryRow(
		ctx,
		`SELECT actor, reason
		 FROM operator_audit_events
		 WHERE action = 'processor.pause'
		 ORDER BY id DESC
		 LIMIT 1`,
	).Scan(&actor, &reason); err != nil {
		t.Fatalf(
			"query processor audit metadata: %v",
			err,
		)
	}

	if actor != "integration" || reason != "maintenance" {
		t.Errorf(
			"processor audit metadata = actor:%q reason:%q, want integration/maintenance",
			actor,
			reason,
		)
	}
}

func TestControlHTTPIntegrationHandlesDatabaseContextFailure(
	t *testing.T,
) {
	handler, _ := newControlIntegrationEnvironment(t)

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/control/processors/discovery/pause",
		strings.NewReader(`{}`),
	).WithContext(ctx)
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

	if response.Code != http.StatusInternalServerError {
		t.Fatalf(
			"cancelled mutation status = %d, want %d; body = %q",
			response.Code,
			http.StatusInternalServerError,
			response.Body.String(),
		)
	}

	request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/control/origins/block",
		strings.NewReader(`{"origin":"ftp://example.org"}`),
	).WithContext(ctx)
	request.Header.Set(
		"Authorization",
		"Bearer "+controlIntegrationToken,
	)
	request.Header.Set(
		"Content-Type",
		"application/json",
	)

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf(
			"cancelled rejection status = %d, want %d; body = %q",
			response.Code,
			http.StatusInternalServerError,
			response.Body.String(),
		)
	}
}

func newControlIntegrationEnvironment(
	t *testing.T,
) (http.Handler, *pgxpool.Pool) {
	t.Helper()

	databaseURL := os.Getenv(
		"JOSHBOT_TEST_DATABASE_URL",
	)
	if databaseURL == "" {
		if os.Getenv(
			"JOSHBOT_REQUIRE_DATABASE_TESTS",
		) == "1" {
			t.Fatal(
				"JOSHBOT_TEST_DATABASE_URL is required",
			)
		}

		t.Skip(
			"JOSHBOT_TEST_DATABASE_URL is not configured",
		)
	}

	ctx := context.Background()

	adminPool, err := pgxpool.New(
		ctx,
		databaseURL,
	)
	if err != nil {
		t.Fatalf(
			"create control integration admin pool: %v",
			err,
		)
	}
	t.Cleanup(adminPool.Close)

	if err := adminPool.Ping(ctx); err != nil {
		t.Fatalf(
			"ping control integration database: %v",
			err,
		)
	}

	schema := fmt.Sprintf(
		"control_test_%d_%d",
		os.Getpid(),
		atomic.AddUint64(
			&controlIntegrationSchemaSequence,
			1,
		),
	)

	if _, err := adminPool.Exec(
		ctx,
		"CREATE SCHEMA "+
			pgx.Identifier{schema}.Sanitize(),
	); err != nil {
		t.Fatalf(
			"create control integration schema: %v",
			err,
		)
	}

	schemaURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf(
			"parse control integration database URL: %v",
			err,
		)
	}

	query := schemaURL.Query()
	query.Set(
		"search_path",
		schema,
	)
	schemaURL.RawQuery = query.Encode()

	pool, err := pgxpool.New(
		ctx,
		schemaURL.String(),
	)
	if err != nil {
		t.Fatalf(
			"create control integration pool: %v",
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
				"drop control integration schema: %v",
				cleanupErr,
			)
		}
	})

	if err := store.Migrate(
		ctx,
		pool,
	); err != nil {
		t.Fatalf(
			"migrate control integration schema: %v",
			err,
		)
	}

	commander, err := store.NewControlStore(pool)
	if err != nil {
		t.Fatalf(
			"NewControlStore() error = %v, want nil",
			err,
		)
	}

	handler, err := control.NewHandler(
		commander,
		controlIntegrationToken,
	)
	if err != nil {
		t.Fatalf(
			"control.NewHandler() error = %v, want nil",
			err,
		)
	}

	return handler, pool
}

func controlIntegrationRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	authorization string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(
		method,
		path,
		strings.NewReader(body),
	)

	if authorization != "" {
		request.Header.Set(
			"Authorization",
			authorization,
		)
	}

	if body != "" {
		request.Header.Set(
			"Content-Type",
			"application/json",
		)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	return response
}

func assertControlIntegrationResponse(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body string,
	wantStatus int,
) {
	t.Helper()

	response := controlIntegrationRequest(
		t,
		handler,
		method,
		path,
		"Bearer "+controlIntegrationToken,
		body,
	)

	if response.Code != wantStatus {
		t.Fatalf(
			"%s %s status = %d, want %d; body = %q",
			method,
			path,
			response.Code,
			wantStatus,
			response.Body.String(),
		)
	}
}

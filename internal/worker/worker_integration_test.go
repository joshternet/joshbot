package worker_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/robots"
	"github.com/joshternet/joshbot/internal/store"
	"github.com/joshternet/joshbot/internal/worker"
)

var workerIntegrationSchemaSequence uint64

type workerIntegrationResolver struct{}

func (workerIntegrationResolver) LookupNetIP(
	context.Context,
	string,
	string,
) ([]netip.Addr, error) {
	return []netip.Addr{
		netip.MustParseAddr(
			"93.184.216.34",
		),
	}, nil
}

type workerIntegrationDialer struct {
	target string
}

func (dialer workerIntegrationDialer) DialContext(
	ctx context.Context,
	network string,
	_ string,
) (net.Conn, error) {
	var system net.Dialer

	return system.DialContext(
		ctx,
		network,
		dialer.target,
	)
}

func TestWorkerIntegrationProcessesQueuedVerificationIntoPostgres(
	t *testing.T,
) {
	ctx := context.Background()

	pool := newWorkerIntegrationPool(t)

	source, err := origin.Parse(
		"http://example.com",
	)
	if err != nil {
		t.Fatalf(
			"origin.Parse() error = %v",
			err,
		)
	}

	queue, err := store.NewQueue(
		pool,
		store.QueueConfig{
			LeaseDuration:     30 * time.Second,
			MinOriginInterval: time.Second,
		},
	)
	if err != nil {
		t.Fatalf(
			"store.NewQueue() error = %v",
			err,
		)
	}

	if err := queue.Schedule(
		ctx,
		source,
		time.Now().Add(-time.Minute),
	); err != nil {
		t.Fatalf(
			"Queue.Schedule() error = %v",
			err,
		)
	}

	var robotsRequests int
	var declarationRequests int

	server := httptest.NewServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			if request.UserAgent() != robots.UserAgent {
				t.Errorf(
					"User-Agent = %q, want %q",
					request.UserAgent(),
					robots.UserAgent,
				)
			}

			switch request.URL.Path {
			case "/robots.txt":
				robotsRequests++

				writer.Header().Set(
					"Content-Type",
					"text/plain",
				)

				_, _ = writer.Write(
					[]byte(
						"User-agent: Joshternet-Joshbot\n" +
							"Allow: /\n",
					),
				)

			case declaration.WellKnownPath:
				declarationRequests++

				writer.Header().Set(
					"Content-Type",
					"application/json",
				)

				_, _ = writer.Write(
					[]byte(
						`{"version":1,"josh":true}`,
					),
				)

			default:
				http.NotFound(
					writer,
					request,
				)
			}
		}),
	)
	defer server.Close()

	checker := robots.NewChecker(
		workerIntegrationResolver{},
		workerIntegrationDialer{
			target: server.Listener.Addr().String(),
		},
	)

	verifier := declaration.NewVerifier(
		checker,
	)

	runtime, err := worker.New(
		queue,
		verifier,
		worker.Config{
			WorkerID:        "integration-worker",
			PollInterval:    10 * time.Millisecond,
			JobTimeout:      5 * time.Second,
			CompletionGrace: 2 * time.Second,
			RecheckInterval: time.Hour,
		},
	)
	if err != nil {
		t.Fatalf(
			"worker.New() error = %v",
			err,
		)
	}

	worked, err := runtime.RunOnce(ctx)
	if err != nil {
		t.Fatalf(
			"Worker.RunOnce() error = %v",
			err,
		)
	}

	if !worked {
		t.Fatal(
			"Worker.RunOnce() worked = false, want true",
		)
	}

	if robotsRequests != 1 {
		t.Errorf(
			"robots requests = %d, want 1",
			robotsRequests,
		)
	}

	if declarationRequests != 1 {
		t.Errorf(
			"declaration requests = %d, want 1",
			declarationRequests,
		)
	}

	databaseStore := store.New(pool)

	verified, err := databaseStore.VerifiedOrigins(ctx)
	if err != nil {
		t.Fatalf(
			"VerifiedOrigins() error = %v",
			err,
		)
	}

	if len(verified) != 1 {
		t.Fatalf(
			"VerifiedOrigins() count = %d, want 1",
			len(verified),
		)
	}

	if verified[0].Origin != source {
		t.Errorf(
			"verified origin = %q, want %q",
			verified[0].Origin.String(),
			source.String(),
		)
	}

	wantDeclaration := declaration.Declaration{
		Version:  1,
		Identity: declaration.IdentityAffirmed,
	}

	if verified[0].Declaration != wantDeclaration {
		t.Errorf(
			"verified declaration = %#v, want %#v",
			verified[0].Declaration,
			wantDeclaration,
		)
	}

	var observationCount int
	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_observations
			WHERE origin = $1
				AND outcome = 'valid'
				AND version = 1
				AND identity = 'affirmed'
		`,
		source.String(),
	).Scan(&observationCount); err != nil {
		t.Fatalf(
			"query verification observation: %v",
			err,
		)
	}

	if observationCount != 1 {
		t.Errorf(
			"valid observation count = %d, want 1",
			observationCount,
		)
	}

	var (
		mode       string
		leaseOwner *string
	)

	if err := pool.QueryRow(
		ctx,
		`
			SELECT mode, lease_owner
			FROM verification_queue
			WHERE origin = $1
		`,
		source.String(),
	).Scan(
		&mode,
		&leaseOwner,
	); err != nil {
		t.Fatalf(
			"query verification queue: %v",
			err,
		)
	}

	if mode != "recurring" {
		t.Errorf(
			"queue mode = %q, want recurring",
			mode,
		)
	}

	if leaseOwner != nil {
		t.Errorf(
			"queue lease owner = %q, want nil",
			*leaseOwner,
		)
	}

	worked, err = runtime.RunOnce(ctx)
	if err != nil {
		t.Fatalf(
			"second Worker.RunOnce() error = %v",
			err,
		)
	}

	if worked {
		t.Error(
			"second Worker.RunOnce() worked = true, want false",
		)
	}
}

func newWorkerIntegrationPool(
	t *testing.T,
) *pgxpool.Pool {
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
			"create worker integration admin pool: %v",
			err,
		)
	}
	t.Cleanup(adminPool.Close)

	if err := adminPool.Ping(ctx); err != nil {
		t.Fatalf(
			"ping worker integration database: %v",
			err,
		)
	}

	schema := fmt.Sprintf(
		"worker_test_%d_%d",
		os.Getpid(),
		atomic.AddUint64(
			&workerIntegrationSchemaSequence,
			1,
		),
	)

	if _, err := adminPool.Exec(
		ctx,
		"CREATE SCHEMA "+
			pgx.Identifier{schema}.Sanitize(),
	); err != nil {
		t.Fatalf(
			"create worker integration schema: %v",
			err,
		)
	}

	schemaURL, err := url.Parse(
		databaseURL,
	)
	if err != nil {
		t.Fatalf(
			"parse worker integration database URL: %v",
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
			"create worker integration pool: %v",
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
				"drop worker integration schema: %v",
				cleanupErr,
			)
		}
	})

	if err := store.Migrate(
		ctx,
		pool,
	); err != nil {
		t.Fatalf(
			"migrate worker integration schema: %v",
			err,
		)
	}

	return pool
}

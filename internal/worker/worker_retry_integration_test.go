package worker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/robots"
	"github.com/joshternet/joshbot/internal/store"
	"github.com/joshternet/joshbot/internal/worker"
)

func TestWorkerRetryIntegrationRecoversWithinVerificationCycle(
	t *testing.T,
) {
	ctx := context.Background()

	pool := newWorkerIntegrationPool(t)

	source := mustWorkerIntegrationOrigin(
		t,
		"http://retry.example",
	)

	queue :=
		newWorkerIntegrationQueue(t, pool)

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

	var declarationAttempts atomic.Int32

	server := httptest.NewServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			switch request.URL.Path {
			case "/robots.txt":
				_, _ = writer.Write(
					[]byte(
						"User-agent: Joshternet-Joshbot\n" +
							"Allow: /\n",
					),
				)

			case declaration.WellKnownPath:
				attempt :=
					declarationAttempts.Add(1)

				if attempt < 3 {
					writer.Header().Set(
						"Retry-After",
						"1",
					)
					writer.WriteHeader(
						http.StatusServiceUnavailable,
					)
					return
				}

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

	runtime :=
		newWorkerIntegrationRuntime(
			t,
			queue,
			server.Listener.Addr().String(),
		)

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

	if got := declarationAttempts.Load(); got != 3 {
		t.Fatalf(
			"declaration attempts = %d, want 3",
			got,
		)
	}

	var validObservations int

	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_observations
			WHERE origin = $1
				AND outcome = 'valid'
		`,
		source.String(),
	).Scan(&validObservations); err != nil {
		t.Fatalf(
			"query valid observations: %v",
			err,
		)
	}

	if validObservations != 1 {
		t.Errorf(
			"valid observations = %d, want 1",
			validObservations,
		)
	}
}

func TestWorkerRetryIntegrationPersistsTransientBackoffAfterCycleExhaustion(
	t *testing.T,
) {
	ctx := context.Background()

	pool := newWorkerIntegrationPool(t)

	source := mustWorkerIntegrationOrigin(
		t,
		"http://unavailable.example",
	)

	queue :=
		newWorkerIntegrationQueue(t, pool)

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

	var declarationAttempts atomic.Int32

	server := httptest.NewServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			switch request.URL.Path {
			case "/robots.txt":
				_, _ = writer.Write(
					[]byte(
						"User-agent: Joshternet-Joshbot\n" +
							"Allow: /\n",
					),
				)

			case declaration.WellKnownPath:
				declarationAttempts.Add(1)

				writer.Header().Set(
					"Retry-After",
					"3600",
				)

				writer.WriteHeader(
					http.StatusServiceUnavailable,
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

	runtime :=
		newWorkerIntegrationRuntime(
			t,
			queue,
			server.Listener.Addr().String(),
		)

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

	if got := declarationAttempts.Load(); got != 3 {
		t.Fatalf(
			"declaration attempts = %d, want 3",
			got,
		)
	}

	var (
		failures        int
		failureCategory *string
		nextAttemptAt   *time.Time
	)

	if err := pool.QueryRow(
		ctx,
		`
			SELECT
				consecutive_failures,
				last_failure_category,
				next_attempt_at
			FROM verification_queue
			WHERE origin = $1
		`,
		source.String(),
	).Scan(
		&failures,
		&failureCategory,
		&nextAttemptAt,
	); err != nil {
		t.Fatalf(
			"query retry state: %v",
			err,
		)
	}

	if failures != 1 {
		t.Errorf(
			"consecutive failures = %d, want 1",
			failures,
		)
	}

	if failureCategory == nil ||
		*failureCategory != "http_5xx" {
		t.Errorf(
			"failure category = %v, want http_5xx",
			failureCategory,
		)
	}

	if nextAttemptAt == nil ||
		!nextAttemptAt.After(time.Now()) {
		t.Errorf(
			"next attempt = %v, want future time",
			nextAttemptAt,
		)
	}
}

func newWorkerIntegrationQueue(
	t *testing.T,
	pool *pgxpool.Pool,
) *store.Queue {
	t.Helper()

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

	return queue
}

func newWorkerIntegrationRuntime(
	t *testing.T,
	queue *store.Queue,
	target string,
) *worker.Worker {
	t.Helper()

	checker := robots.NewCheckerWithRequestDelayAndSigner(
		workerIntegrationResolver{},
		workerIntegrationDialer{
			target: target,
		},
		0,
		nil,
	)

	verifier :=
		declaration.NewVerifier(checker)

	runtime, err := worker.New(
		queue,
		verifier,
		worker.Config{
			WorkerID: "integration-retry-worker",
			PollInterval: 10 *
				time.Millisecond,
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

	return runtime
}

func mustWorkerIntegrationOrigin(
	t *testing.T,
	raw string,
) origin.Origin {
	t.Helper()

	parsed, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			raw,
			err,
		)
	}

	return parsed
}

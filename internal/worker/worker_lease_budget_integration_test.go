package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/store"
)

// Goal & Constraints: cover mid-cycle lease renewal after a job timeout
// consumes the remaining verification budget so independent integration
// coverage includes ensureVerificationLeaseBudget on attempt two.
// Inputs: short initial lease, injectable clock, timeout then success.
// Outputs: Renew once between attempts; second origin still processed.
// Error cases: renew must not be skipped when remaining wall time is short.
// Acceptance: claim continues after renewal without parking on PollInterval.

func TestWorkerLeaseBudgetIntegrationRenewsAcrossTimeoutAttempts(
	t *testing.T,
) {
	source := workerFailureIntegrationOrigin(
		t,
		"https://renew.example",
	)
	next := workerFailureIntegrationOrigin(
		t,
		"https://next.example",
	)

	clock := time.Now().UTC()
	jobTimeout := 2 * time.Minute
	completionGrace := 30 * time.Second
	initialLease := store.Lease{
		Origin:     source,
		WorkerID:   "integration-worker",
		Generation: 1,
		ClaimedAt:  clock,
		ExpiresAt: clock.Add(
			jobTimeout + completionGrace + time.Second,
		),
	}

	renewCalls := 0
	claims := 0
	stopError := errors.New("integration lease budget stop")

	queue := &workerFailureIntegrationQueue{
		claim: func(
			context.Context,
			string,
		) (store.Lease, bool, error) {
			claims++

			switch claims {
			case 1:
				return initialLease, true, nil
			case 2:
				return store.Lease{
					Origin:     next,
					WorkerID:   "integration-worker",
					Generation: 1,
					ClaimedAt:  clock,
					ExpiresAt: clock.Add(
						10 * time.Minute,
					),
				}, true, nil
			default:
				return store.Lease{}, false, nil
			}
		},
		renew: func(
			_ context.Context,
			lease store.Lease,
		) (store.Lease, error) {
			renewCalls++

			renewed := lease
			renewed.ExpiresAt = clock.Add(10 * time.Minute)

			return renewed, nil
		},
	}

	var delays []time.Duration
	waiter := &workerFailureIntegrationWaiter{
		wait: func(
			_ context.Context,
			delay time.Duration,
		) error {
			delays = append(delays, delay)
			if claims >= 3 {
				return stopError
			}
			return nil
		},
	}

	timeoutCalls := 0
	factory := timeoutFactoryFunc(
		func(
			parent context.Context,
			_ time.Duration,
		) (context.Context, context.CancelFunc) {
			timeoutCalls++
			if timeoutCalls == 1 {
				return context.WithDeadline(
					parent,
					time.Unix(0, 0),
				)
			}
			return context.WithCancel(parent)
		},
	)

	config := workerFailureIntegrationConfig()
	config.JobTimeout = jobTimeout
	config.CompletionGrace = completionGrace
	config.PollInterval = time.Minute

	runtime, err := newWorker(
		queue,
		&fakeVerifier{
			verify: func(
				jobContext context.Context,
				originSource origin.Origin,
			) (declaration.Result, error) {
				if originSource == source &&
					timeoutCalls == 1 {
					<-jobContext.Done()
					clock = clock.Add(jobTimeout)
					return declaration.Result{},
						jobContext.Err()
				}
				return declaration.Result{
					Origin:  originSource,
					Outcome: declaration.OutcomeValid,
				}, nil
			},
		},
		config,
		waiter,
		factory,
	)
	if err != nil {
		t.Fatalf("newWorker() error = %v", err)
	}
	runtime.now = func() time.Time { return clock }

	err = runtime.Run(context.Background())
	if !errors.Is(err, stopError) {
		t.Fatalf(
			"Run() error = %v, want %v",
			err,
			stopError,
		)
	}

	if renewCalls != 1 {
		t.Fatalf(
			"Renew() calls = %d, want 1 between timeout attempts",
			renewCalls,
		)
	}

	if claims < 3 {
		t.Fatalf(
			"Claim() calls = %d, want at least 3",
			claims,
		)
	}

	for _, delay := range delays {
		if delay >= 5*time.Minute {
			t.Fatalf(
				"wait delays = %v include durable origin schedule",
				delays,
			)
		}
	}
}

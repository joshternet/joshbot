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

var errWorkerRunBoundaryIntegrationStop = errors.New(
	"integration worker run stop",
)

type workerRunBoundaryIntegrationQueue struct {
	lease         store.Lease
	claimCalls    int
	completeCalls int
}

func (queue *workerRunBoundaryIntegrationQueue) Claim(
	context.Context,
	string,
) (store.Lease, bool, error) {
	queue.claimCalls++

	if queue.claimCalls == 1 {
		return queue.lease, true, nil
	}

	return store.Lease{}, false, errWorkerRunBoundaryIntegrationStop
}

func (queue *workerRunBoundaryIntegrationQueue) Renew(
	_ context.Context,
	lease store.Lease,
) (store.Lease, error) {
	renewed := lease
	renewed.ExpiresAt = time.Now().UTC().Add(10 * time.Minute)
	return renewed, nil
}

func (*workerRunBoundaryIntegrationQueue) AbandonVerification(
	context.Context,
	store.Lease,
) error {
	return nil
}

func (queue *workerRunBoundaryIntegrationQueue) CompleteVerification(
	context.Context,
	store.Lease,
	declaration.Result,
	time.Duration,
) error {
	queue.completeCalls++
	return nil
}

type workerRunBoundaryIntegrationVerifier struct {
	result declaration.Result
	calls  int
}

func (verifier *workerRunBoundaryIntegrationVerifier) Verify(
	context.Context,
	origin.Origin,
) (declaration.Result, error) {
	verifier.calls++
	return verifier.result, nil
}

type workerRunBoundaryIntegrationWaiter struct {
	calls int
}

func (waiter *workerRunBoundaryIntegrationWaiter) Wait(
	context.Context,
	time.Duration,
) error {
	waiter.calls++
	return errWorkerRunBoundaryIntegrationStop
}

func TestWorkerRunBoundaryIntegrationImmediatelyClaimsAfterWork(
	t *testing.T,
) {
	source, err := origin.Parse("https://example.com")
	if err != nil {
		t.Fatalf("origin.Parse() error = %v", err)
	}

	claimedAt := time.Now().UTC()

	queue := &workerRunBoundaryIntegrationQueue{
		lease: store.Lease{
			Origin:     source,
			WorkerID:   "integration-worker",
			Generation: 1,
			ClaimedAt:  claimedAt,
			ExpiresAt:  claimedAt.Add(10 * time.Minute),
		},
	}

	verifier := &workerRunBoundaryIntegrationVerifier{
		result: declaration.Result{
			Outcome: declaration.OutcomeValid,
			Origin:  source,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityAffirmed,
			},
		},
	}

	waiter := &workerRunBoundaryIntegrationWaiter{}

	runtime, err := newWorker(
		queue,
		verifier,
		Config{
			WorkerID:        "integration-worker",
			PollInterval:    time.Minute,
			JobTimeout:      2 * time.Minute,
			CompletionGrace: time.Minute,
			RecheckInterval: 24 * time.Hour,
		},
		waiter,
		contextTimeoutFactory{},
	)
	if err != nil {
		t.Fatalf("newWorker() error = %v", err)
	}

	err = runtime.Run(context.Background())
	if !errors.Is(err, errWorkerRunBoundaryIntegrationStop) {
		t.Fatalf(
			"Run() error = %v, want %v",
			err,
			errWorkerRunBoundaryIntegrationStop,
		)
	}

	if queue.claimCalls != 2 {
		t.Errorf(
			"Claim() calls = %d, want 2",
			queue.claimCalls,
		)
	}

	if queue.completeCalls != 1 {
		t.Errorf(
			"CompleteVerification() calls = %d, want 1",
			queue.completeCalls,
		)
	}

	if verifier.calls != 1 {
		t.Errorf(
			"Verify() calls = %d, want 1",
			verifier.calls,
		)
	}

	if waiter.calls != 1 {
		t.Errorf(
			"Wait() calls = %d, want 1",
			waiter.calls,
		)
	}
}

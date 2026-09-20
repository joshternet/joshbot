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

var errWorkerRetryContinueIntegrationStop = errors.New(
	"integration worker retry stop",
)

type workerRetryContinueIntegrationQueue struct {
	claimCalls int
}

func (queue *workerRetryContinueIntegrationQueue) Claim(
	context.Context,
	string,
) (store.Lease, bool, error) {
	queue.claimCalls++
	return store.Lease{}, false, errWorkerRetryContinueIntegrationStop
}

func (*workerRetryContinueIntegrationQueue) CompleteVerification(
	context.Context,
	store.Lease,
	declaration.Result,
	time.Duration,
) error {
	return nil
}

type workerRetryContinueIntegrationVerifier struct{}

func (*workerRetryContinueIntegrationVerifier) Verify(
	context.Context,
	origin.Origin,
) (declaration.Result, error) {
	return declaration.Result{}, nil
}

type workerRetryContinueIntegrationWaiter struct {
	calls int
}

func (waiter *workerRetryContinueIntegrationWaiter) Wait(
	context.Context,
	time.Duration,
) error {
	waiter.calls++
	if waiter.calls == 1 {
		return nil
	}
	return errWorkerRetryContinueIntegrationStop
}

func TestWorkerRetryContinueIntegration(t *testing.T) {
	queue := &workerRetryContinueIntegrationQueue{}
	waiter := &workerRetryContinueIntegrationWaiter{}

	runtime, err := newWorker(
		queue,
		&workerRetryContinueIntegrationVerifier{},
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
	if !errors.Is(err, errWorkerRetryContinueIntegrationStop) {
		t.Fatalf(
			"Run() error = %v, want %v",
			err,
			errWorkerRetryContinueIntegrationStop,
		)
	}
	if queue.claimCalls != 2 {
		t.Errorf("Claim() calls = %d, want 2", queue.claimCalls)
	}
	if waiter.calls != 2 {
		t.Errorf("Wait() calls = %d, want 2", waiter.calls)
	}
}

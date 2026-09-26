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

func (*workerRetryContinueIntegrationQueue) Renew(
	context.Context,
	store.Lease,
) (store.Lease, error) {
	return store.Lease{}, nil
}

func (*workerRetryContinueIntegrationQueue) AbandonVerification(
	context.Context,
	store.Lease,
) error {
	return nil
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
	calls  int
	delays []time.Duration
}

func (waiter *workerRetryContinueIntegrationWaiter) Wait(
	_ context.Context,
	delay time.Duration,
) error {
	waiter.calls++
	waiter.delays = append(waiter.delays, delay)
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
	wantDelays := []time.Duration{time.Minute, time.Minute}
	if len(waiter.delays) != len(wantDelays) {
		t.Fatalf("Wait delays = %v, want %v", waiter.delays, wantDelays)
	}
	for index, delay := range waiter.delays {
		if delay != wantDelays[index] {
			t.Fatalf("Wait delays = %v, want %v", waiter.delays, wantDelays)
		}
	}
}

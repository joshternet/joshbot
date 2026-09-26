package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/store"
)

type cancelingAbandonQueue struct {
	lease         store.Lease
	cancel        context.CancelFunc
	completionErr error
	abandonErr    error
}

func (queue *cancelingAbandonQueue) Claim(
	context.Context,
	string,
) (store.Lease, bool, error) {
	return queue.lease, true, nil
}

func (queue *cancelingAbandonQueue) Renew(
	context.Context,
	store.Lease,
) (store.Lease, error) {
	return queue.lease, nil
}

func (queue *cancelingAbandonQueue) CompleteVerification(
	context.Context,
	store.Lease,
	declaration.Result,
	time.Duration,
) error {
	return queue.completionErr
}

func (queue *cancelingAbandonQueue) AbandonVerification(
	context.Context,
	store.Lease,
) error {
	queue.cancel()
	return queue.abandonErr
}

type cancelingAbandonVerifier struct {
	calls int
}

func (verifier *cancelingAbandonVerifier) Verify(
	ctx context.Context,
	source origin.Origin,
) (declaration.Result, error) {
	verifier.calls++

	<-ctx.Done()

	return declaration.Result{
		Origin: source,
	}, ctx.Err()
}

func TestRunOncePreservesParentCancellationWhenAbandonFails(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	defer cancel()

	completionErr := errors.New(
		"test completion failure",
	)
	abandonErr := errors.New(
		"test abandonment failure",
	)

	queue := &cancelingAbandonQueue{
		lease:         workerTestLease(source),
		cancel:        cancel,
		completionErr: completionErr,
		abandonErr:    abandonErr,
	}

	verifier := &cancelingAbandonVerifier{}

	timeoutCalls := 0
	factory := timeoutFactoryFunc(
		func(
			parent context.Context,
			_ time.Duration,
		) (context.Context, context.CancelFunc) {
			timeoutCalls++

			if timeoutCalls <= retry.MaxAttemptsPerCycle {
				return context.WithDeadline(
					parent,
					time.Unix(0, 0),
				)
			}

			return context.WithCancel(parent)
		},
	)

	runtime, err := newWorker(
		queue,
		verifier,
		workerTestConfig(),
		timerWaitStrategy{},
		factory,
	)
	if err != nil {
		t.Fatalf(
			"newWorker() error = %v, want nil",
			err,
		)
	}

	worked, err := runtime.RunOnce(ctx)

	if !worked {
		t.Fatal(
			"RunOnce() worked = false, want true",
		)
	}

	if !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf(
			"RunOnce() error = %v, want context.Canceled",
			err,
		)
	}

	if errors.Is(
		err,
		completionErr,
	) {
		t.Errorf(
			"RunOnce() error = %v, unexpectedly preserved completion failure",
			err,
		)
	}

	if errors.Is(
		err,
		abandonErr,
	) {
		t.Errorf(
			"RunOnce() error = %v, unexpectedly preserved abandonment failure",
			err,
		)
	}

	if verifier.calls != retry.MaxAttemptsPerCycle {
		t.Errorf(
			"Verify() calls = %d, want %d",
			verifier.calls,
			retry.MaxAttemptsPerCycle,
		)
	}

	wantTimeoutCalls :=
		retry.MaxAttemptsPerCycle + 2

	if timeoutCalls != wantTimeoutCalls {
		t.Errorf(
			"timeout factory calls = %d, want %d",
			timeoutCalls,
			wantTimeoutCalls,
		)
	}
}

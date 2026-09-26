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

// AbandonVerification is defined here for the existing fakeQueue so it will
// continue satisfying the worker Queue contract once abandonment becomes a
// required queue operation.
//
// Tests that need to inspect abandonment use recordingAbandonQueue below.
func (queue *fakeQueue) AbandonVerification(
	context.Context,
	store.Lease,
) error {
	return nil
}

type recordingAbandonQueue struct {
	*fakeQueue

	abandonCalls  int
	abandoned     []store.Lease
	abandonError  error
	abandon       func(context.Context, store.Lease) error
	abandonCtxErr []error
}

func (queue *recordingAbandonQueue) AbandonVerification(
	ctx context.Context,
	lease store.Lease,
) error {
	queue.abandonCalls++
	queue.abandoned = append(
		queue.abandoned,
		lease,
	)
	queue.abandonCtxErr = append(
		queue.abandonCtxErr,
		ctx.Err(),
	)

	if queue.abandon != nil {
		return queue.abandon(
			ctx,
			lease,
		)
	}

	return queue.abandonError
}

func TestRunOnceAbandonsTimedOutVerificationAfterCompletionFailure(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	lease := workerTestLease(source)
	completeError := errors.New(
		"test completion failure",
	)

	queue := &recordingAbandonQueue{
		fakeQueue: &fakeQueue{
			claims: []fakeClaim{
				{
					lease: lease,
					found: true,
				},
			},
			completeError: completeError,
		},
	}

	verifier := &fakeVerifier{
		verify: func(
			ctx context.Context,
			_ origin.Origin,
		) (declaration.Result, error) {
			<-ctx.Done()

			return declaration.Result{},
				ctx.Err()
		},
	}

	timeoutCalls := 0
	factory := timeoutFactoryFunc(
		func(
			ctx context.Context,
			_ time.Duration,
		) (context.Context, context.CancelFunc) {
			timeoutCalls++

			if timeoutCalls <= retry.MaxAttemptsPerCycle {
				return context.WithDeadline(
					ctx,
					time.Unix(0, 0),
				)
			}

			return context.WithCancel(ctx)
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

	worked, err := runtime.RunOnce(
		context.Background(),
	)
	if !errors.Is(
		err,
		completeError,
	) {
		t.Errorf(
			"RunOnce() error = %v, want completion failure",
			err,
		)
	}

	if !worked {
		t.Error(
			"RunOnce() worked = false, want true",
		)
	}

	if queue.completeCalls != 1 {
		t.Errorf(
			"CompleteVerification() calls = %d, want 1",
			queue.completeCalls,
		)
	}

	if queue.abandonCalls != 1 {
		t.Fatalf(
			"AbandonVerification() calls = %d, want 1",
			queue.abandonCalls,
		)
	}

	if len(queue.abandoned) != 1 {
		t.Fatalf(
			"abandoned leases = %d, want 1",
			len(queue.abandoned),
		)
	}

	if queue.abandoned[0] != lease {
		t.Errorf(
			"abandoned lease = %#v, want %#v",
			queue.abandoned[0],
			lease,
		)
	}

	if len(queue.abandonCtxErr) != 1 {
		t.Fatalf(
			"abandon contexts = %d, want 1",
			len(queue.abandonCtxErr),
		)
	}

	if queue.abandonCtxErr[0] != nil {
		t.Errorf(
			"abandon context error = %v, want nil",
			queue.abandonCtxErr[0],
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

func TestRunOnceAbandonsTimedOutVerificationAfterCompletionDeadline(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	lease := workerTestLease(source)

	queue := &recordingAbandonQueue{
		fakeQueue: &fakeQueue{
			claims: []fakeClaim{
				{
					lease: lease,
					found: true,
				},
			},
			complete: func(
				ctx context.Context,
				_ store.Lease,
				_ declaration.Result,
				_ time.Duration,
			) error {
				<-ctx.Done()

				return ctx.Err()
			},
		},
	}

	verifier := &fakeVerifier{
		verify: func(
			ctx context.Context,
			_ origin.Origin,
		) (declaration.Result, error) {
			<-ctx.Done()

			return declaration.Result{},
				ctx.Err()
		},
	}

	timeoutCalls := 0
	factory := timeoutFactoryFunc(
		func(
			ctx context.Context,
			_ time.Duration,
		) (context.Context, context.CancelFunc) {
			timeoutCalls++

			if timeoutCalls <=
				retry.MaxAttemptsPerCycle+1 {
				return context.WithDeadline(
					ctx,
					time.Unix(0, 0),
				)
			}

			return context.WithCancel(ctx)
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

	worked, err := runtime.RunOnce(
		context.Background(),
	)
	if !errors.Is(
		err,
		context.DeadlineExceeded,
	) {
		t.Errorf(
			"RunOnce() error = %v, want context.DeadlineExceeded",
			err,
		)
	}

	if !worked {
		t.Error(
			"RunOnce() worked = false, want true",
		)
	}

	if queue.completeCalls != 1 {
		t.Errorf(
			"CompleteVerification() calls = %d, want 1",
			queue.completeCalls,
		)
	}

	if queue.abandonCalls != 1 {
		t.Fatalf(
			"AbandonVerification() calls = %d, want 1",
			queue.abandonCalls,
		)
	}

	if len(queue.abandonCtxErr) != 1 {
		t.Fatalf(
			"abandon contexts = %d, want 1",
			len(queue.abandonCtxErr),
		)
	}

	if queue.abandonCtxErr[0] != nil {
		t.Errorf(
			"abandon context error = %v, want nil",
			queue.abandonCtxErr[0],
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

func TestRunOncePreservesTimedOutCompletionAndAbandonFailures(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	lease := workerTestLease(source)

	completeError := errors.New(
		"test completion failure",
	)
	abandonError := errors.New(
		"test abandonment failure",
	)

	queue := &recordingAbandonQueue{
		fakeQueue: &fakeQueue{
			claims: []fakeClaim{
				{
					lease: lease,
					found: true,
				},
			},
			completeError: completeError,
		},
		abandonError: abandonError,
	}

	verifier := &fakeVerifier{
		verify: func(
			ctx context.Context,
			_ origin.Origin,
		) (declaration.Result, error) {
			<-ctx.Done()

			return declaration.Result{},
				ctx.Err()
		},
	}

	timeoutCalls := 0
	factory := timeoutFactoryFunc(
		func(
			ctx context.Context,
			_ time.Duration,
		) (context.Context, context.CancelFunc) {
			timeoutCalls++

			if timeoutCalls <= retry.MaxAttemptsPerCycle {
				return context.WithDeadline(
					ctx,
					time.Unix(0, 0),
				)
			}

			return context.WithCancel(ctx)
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

	worked, err := runtime.RunOnce(
		context.Background(),
	)

	if !errors.Is(
		err,
		completeError,
	) {
		t.Errorf(
			"RunOnce() error = %v, want completion failure",
			err,
		)
	}

	if !errors.Is(
		err,
		abandonError,
	) {
		t.Errorf(
			"RunOnce() error = %v, want abandonment failure",
			err,
		)
	}

	if !worked {
		t.Error(
			"RunOnce() worked = false, want true",
		)
	}

	if queue.abandonCalls != 1 {
		t.Errorf(
			"AbandonVerification() calls = %d, want 1",
			queue.abandonCalls,
		)
	}
}

func TestRunOnceDoesNotAbandonNonTimeoutCompletionFailure(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	completeError := errors.New(
		"test completion failure",
	)

	queue := &recordingAbandonQueue{
		fakeQueue: &fakeQueue{
			claims: []fakeClaim{
				{
					lease: workerTestLease(source),
					found: true,
				},
			},
			completeError: completeError,
		},
	}

	verifier := &fakeVerifier{
		result: workerTestResult(source),
	}

	runtime := newWorkerForTest(
		t,
		queue,
		verifier,
		workerTestConfig(),
	)

	worked, err := runtime.RunOnce(
		context.Background(),
	)
	if !errors.Is(
		err,
		completeError,
	) {
		t.Errorf(
			"RunOnce() error = %v, want completion failure",
			err,
		)
	}

	if !worked {
		t.Error(
			"RunOnce() worked = false, want true",
		)
	}

	if queue.abandonCalls != 0 {
		t.Errorf(
			"AbandonVerification() calls = %d, want 0",
			queue.abandonCalls,
		)
	}
}

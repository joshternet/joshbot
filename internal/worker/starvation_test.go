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

func TestRunRepeatedTimeoutsDoNotStarveLaterWork(
	t *testing.T,
) {
	origins := []origin.Origin{
		mustWorkerOrigin(t),
		mustStarvationOrigin(
			t,
			"https://second.example",
		),
		mustStarvationOrigin(
			t,
			"https://third.example",
		),
		mustStarvationOrigin(
			t,
			"https://healthy.example",
		),
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: workerTestLease(origins[0]),
				found: true,
			},
			{
				lease: workerTestLease(origins[1]),
				found: true,
			},
			{
				lease: workerTestLease(origins[2]),
				found: true,
			},
			{
				lease: workerTestLease(origins[3]),
				found: true,
			},
		},
	}

	queue.complete = func(
		_ context.Context,
		_ store.Lease,
		_ declaration.Result,
		_ time.Duration,
	) error {
		if queue.completeCalls == len(origins) {
			cancel()
		}

		return nil
	}

	verifier := &fakeVerifier{
		verify: func(
			jobContext context.Context,
			source origin.Origin,
		) (declaration.Result, error) {
			if source != origins[len(origins)-1] {
				<-jobContext.Done()

				return declaration.Result{},
					jobContext.Err()
			}

			return workerTestResult(source), nil
		},
	}

	timeoutCalls := 0
	factory := timeoutFactoryFunc(
		func(
			parent context.Context,
			_ time.Duration,
		) (context.Context, context.CancelFunc) {
			timeoutCalls++

			switch timeoutCalls {
			case 1, 3, 5:
				return context.WithDeadline(
					parent,
					time.Unix(0, 0),
				)

			default:
				return context.WithCancel(parent)
			}
		},
	)

	waitCalls := 0
	waiter := waitStrategyFunc(
		func(
			context.Context,
			time.Duration,
		) error {
			waitCalls++

			return nil
		},
	)

	runtime, err := newWorker(
		queue,
		verifier,
		workerTestConfig(),
		waiter,
		factory,
	)
	if err != nil {
		t.Fatalf(
			"newWorker() error = %v, want nil",
			err,
		)
	}

	err = runtime.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf(
			"Run() error = %v, want context.Canceled",
			err,
		)
	}

	if verifier.calls != len(origins) {
		t.Errorf(
			"Verify() calls = %d, want %d",
			verifier.calls,
			len(origins),
		)
	}

	if queue.completeCalls != len(origins) {
		t.Errorf(
			"CompleteVerification() calls = %d, want %d",
			queue.completeCalls,
			len(origins),
		)
	}

	for index := 0; index < len(origins)-1; index++ {
		result := queue.completedResults[index]

		if result.Outcome !=
			declaration.OutcomeUnavailable {
			t.Errorf(
				"result %d outcome = %v, want unavailable",
				index,
				result.Outcome,
			)
		}

		if result.Origin != origins[index] {
			t.Errorf(
				"result %d origin = %v, want %v",
				index,
				result.Origin,
				origins[index],
			)
		}
	}

	wantHealthyResult := workerTestResult(
		origins[len(origins)-1],
	)
	gotHealthyResult :=
		queue.completedResults[len(origins)-1]

	if gotHealthyResult != wantHealthyResult {
		t.Errorf(
			"healthy result = %#v, want %#v",
			gotHealthyResult,
			wantHealthyResult,
		)
	}

	if waitCalls != 0 {
		t.Errorf(
			"wait calls = %d, want 0",
			waitCalls,
		)
	}
}

func mustStarvationOrigin(
	t *testing.T,
	raw string,
) origin.Origin {
	t.Helper()

	source, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v, want nil",
			raw,
			err,
		)
	}

	return source
}

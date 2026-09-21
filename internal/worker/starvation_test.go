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
			case 1, 2, 3, 5, 6, 7, 9, 10, 11:
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

	wantVerifierCalls := (len(origins)-1)*retry.MaxAttemptsPerCycle + 1
	if verifier.calls != wantVerifierCalls {
		t.Errorf(
			"Verify() calls = %d, want %d",
			verifier.calls,
			wantVerifierCalls,
		)
	}

	wantRenewCalls := 0
	if queue.renewCalls != wantRenewCalls {
		t.Errorf(
			"Renew() calls = %d, want %d",
			queue.renewCalls,
			wantRenewCalls,
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

func TestRunCompletionFailureDoesNotParkUnrelatedWork(
	t *testing.T,
) {
	first := mustWorkerOrigin(t)
	second := mustStarvationOrigin(
		t,
		"https://second.example",
	)
	completionError := errors.New("completion_failed")
	stopError := errors.New("stop")

	queue := &fakeQueue{
		claims: []fakeClaim{
			{lease: workerTestLease(first), found: true},
			{lease: workerTestLease(second), found: true},
		},
		completeError: completionError,
	}

	var delays []time.Duration
	waiter := waitStrategyFunc(
		func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			if queue.completeCalls >= 2 && queue.claimCalls >= 2 {
				return stopError
			}
			return nil
		},
	)

	runtime, err := newWorker(
		queue,
		&fakeVerifier{
			verify: func(
				_ context.Context,
				source origin.Origin,
			) (declaration.Result, error) {
				return workerTestResult(source), nil
			},
		},
		workerTestConfig(),
		waiter,
		contextTimeoutFactory{},
	)
	if err != nil {
		t.Fatalf("newWorker() error = %v", err)
	}

	err = runtime.Run(context.Background())
	if !errors.Is(err, stopError) {
		t.Fatalf("Run() error = %v, want stop", err)
	}

	if queue.claimCalls < 2 {
		t.Fatalf("Claim() calls = %d, want at least 2", queue.claimCalls)
	}
	if queue.completeCalls != 2 {
		t.Fatalf(
			"CompleteVerification() calls = %d, want 2",
			queue.completeCalls,
		)
	}
	if len(delays) == 0 {
		t.Fatal("expected idle PollInterval wait after both claims")
	}
	if delays[0] != workerTestConfig().PollInterval {
		t.Fatalf(
			"first wait = %v, want PollInterval after both claimed failures",
			delays[0],
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

func TestRunRenewsLeaseAcrossTimeoutAttempts(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	next := mustStarvationOrigin(
		t,
		"https://next.example",
	)
	ctx, cancel := context.WithCancel(context.Background())

	clock := time.Now().UTC()
	jobTimeout := 2 * time.Minute
	completionGrace := 30 * time.Second
	initialLease := store.Lease{
		Origin:     source,
		WorkerID:   "worker-a",
		Generation: 1,
		ClaimedAt:  clock,
		ExpiresAt: clock.Add(
			jobTimeout + completionGrace + time.Second,
		),
	}

	queue := &fakeQueue{
		claims: []fakeClaim{
			{lease: initialLease, found: true},
			{lease: workerTestLease(next), found: true},
		},
	}
	queue.renew = func(
		_ context.Context,
		lease store.Lease,
	) (store.Lease, error) {
		renewed := lease
		renewed.ExpiresAt = clock.Add(
			jobTimeout + completionGrace + time.Second,
		)
		return renewed, nil
	}
	queue.complete = func(
		_ context.Context,
		lease store.Lease,
		_ declaration.Result,
		_ time.Duration,
	) error {
		if queue.completeCalls == 2 {
			cancel()
		}
		if lease.Origin == source &&
			!lease.ExpiresAt.After(initialLease.ExpiresAt) {
			t.Errorf(
				"completed with unrenewed lease expiry %v",
				lease.ExpiresAt,
			)
		}
		return nil
	}

	verifier := &fakeVerifier{
		verify: func(
			jobContext context.Context,
			originSource origin.Origin,
		) (declaration.Result, error) {
			if originSource == source {
				<-jobContext.Done()
				clock = clock.Add(jobTimeout)
				return declaration.Result{}, jobContext.Err()
			}
			return workerTestResult(originSource), nil
		},
	}

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

	waitCalls := 0
	waiter := waitStrategyFunc(
		func(context.Context, time.Duration) error {
			waitCalls++
			return nil
		},
	)

	config := workerTestConfig()
	config.JobTimeout = jobTimeout
	config.CompletionGrace = completionGrace

	runtime, err := newWorker(
		queue,
		verifier,
		config,
		waiter,
		factory,
	)
	if err != nil {
		t.Fatalf("newWorker() error = %v", err)
	}
	runtime.now = func() time.Time { return clock }

	err = runtime.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want canceled", err)
	}

	wantRenewals := retry.MaxAttemptsPerCycle - 1
	if queue.renewCalls != wantRenewals {
		t.Fatalf(
			"Renew() calls = %d, want %d",
			queue.renewCalls,
			wantRenewals,
		)
	}
	if verifier.calls != retry.MaxAttemptsPerCycle+1 {
		t.Fatalf(
			"Verify() calls = %d, want %d",
			verifier.calls,
			retry.MaxAttemptsPerCycle+1,
		)
	}
	if queue.completeCalls != 2 {
		t.Fatalf(
			"CompleteVerification() calls = %d, want 2",
			queue.completeCalls,
		)
	}
	if queue.completedResults[0].Outcome != declaration.OutcomeUnavailable {
		t.Fatalf(
			"first outcome = %v, want unavailable",
			queue.completedResults[0].Outcome,
		)
	}
	if !queue.completedLeases[0].ExpiresAt.After(initialLease.ExpiresAt) {
		t.Fatalf(
			"completed lease expiry %v, want after %v",
			queue.completedLeases[0].ExpiresAt,
			initialLease.ExpiresAt,
		)
	}
	if waitCalls != 0 {
		t.Fatalf("wait calls = %d, want 0", waitCalls)
	}
}

func TestRunLeaseRenewalFailureStopsWithoutStaleCompletion(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	next := mustStarvationOrigin(
		t,
		"https://next.example",
	)
	stopError := errors.New("stop")
	clock := time.Now().UTC()
	jobTimeout := 2 * time.Minute
	completionGrace := time.Minute

	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: store.Lease{
					Origin:     source,
					WorkerID:   "worker-a",
					Generation: 1,
					ClaimedAt:  clock,
					ExpiresAt: clock.Add(
						jobTimeout + completionGrace + time.Second,
					),
				},
				found: true,
			},
			{lease: workerTestLease(next), found: true},
		},
		renewError: store.ErrLeaseLost,
	}

	var delays []time.Duration
	waiter := waitStrategyFunc(
		func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			if queue.completeCalls >= 1 {
				return stopError
			}
			return nil
		},
	)

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

	observer := &recordingLifecycleObserver{}
	config := workerTestConfig()
	config.JobTimeout = jobTimeout
	config.CompletionGrace = completionGrace
	runtime, err := newWorker(
		queue,
		&fakeVerifier{
			verify: func(
				jobContext context.Context,
				originSource origin.Origin,
			) (declaration.Result, error) {
				if originSource == source {
					<-jobContext.Done()
					clock = clock.Add(jobTimeout)
					return declaration.Result{}, jobContext.Err()
				}
				return workerTestResult(originSource), nil
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
	runtime.SetLifecycleObserver(observer)

	err = runtime.Run(context.Background())
	if !errors.Is(err, stopError) {
		t.Fatalf("Run() error = %v, want stop", err)
	}

	if queue.renewCalls != 1 {
		t.Fatalf("Renew() calls = %d, want 1", queue.renewCalls)
	}
	if queue.completeCalls != 1 {
		t.Fatalf(
			"CompleteVerification() calls = %d, want 1 for next origin only",
			queue.completeCalls,
		)
	}
	if queue.completedLeases[0].Origin != next {
		t.Fatalf(
			"completed origin = %v, want %v",
			queue.completedLeases[0].Origin,
			next,
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

	foundRenewalFailure := false
	for _, event := range observer.events {
		if event.state == "failed" &&
			event.message == "lease_renewal_failed" {
			foundRenewalFailure = true
			break
		}
	}
	if !foundRenewalFailure {
		t.Fatalf(
			"lifecycle events = %#v, want lease_renewal_failed",
			observer.events,
		)
	}
}

func TestRunOnceRenewalFailurePreservesParentCancellation(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	ctx, cancel := context.WithCancel(context.Background())
	clock := time.Now().UTC()
	jobTimeout := 2 * time.Minute
	completionGrace := time.Minute

	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: store.Lease{
					Origin:     source,
					WorkerID:   "worker-a",
					Generation: 1,
					ClaimedAt:  clock,
					ExpiresAt: clock.Add(
						jobTimeout + completionGrace + time.Second,
					),
				},
				found: true,
			},
		},
		renew: func(
			context.Context,
			store.Lease,
		) (store.Lease, error) {
			cancel()
			return store.Lease{}, store.ErrLeaseLost
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

	config := workerTestConfig()
	config.JobTimeout = jobTimeout
	config.CompletionGrace = completionGrace
	runtime, err := newWorker(
		queue,
		&fakeVerifier{
			verify: func(
				jobContext context.Context,
				_ origin.Origin,
			) (declaration.Result, error) {
				<-jobContext.Done()
				clock = clock.Add(jobTimeout)
				return declaration.Result{}, jobContext.Err()
			},
		},
		config,
		waitStrategyFunc(func(context.Context, time.Duration) error {
			return nil
		}),
		factory,
	)
	if err != nil {
		t.Fatalf("newWorker() error = %v", err)
	}
	runtime.now = func() time.Time { return clock }

	worked, err := runtime.RunOnce(ctx)
	if !worked {
		t.Fatal("RunOnce() worked = false, want true")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce() error = %v, want canceled", err)
	}
	if queue.completeCalls != 0 {
		t.Fatalf(
			"CompleteVerification() calls = %d, want 0",
			queue.completeCalls,
		)
	}
}

func TestRunOnceRenewedLeaseInsufficientBudget(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	clock := time.Now().UTC()
	jobTimeout := 2 * time.Minute
	completionGrace := time.Minute
	lease := store.Lease{
		Origin:     source,
		WorkerID:   "worker-a",
		Generation: 1,
		ClaimedAt:  clock,
		ExpiresAt: clock.Add(
			jobTimeout + completionGrace + time.Second,
		),
	}

	queue := &fakeQueue{
		claims: []fakeClaim{
			{lease: lease, found: true},
		},
		renew: func(
			_ context.Context,
			current store.Lease,
		) (store.Lease, error) {
			renewed := current
			renewed.ExpiresAt = clock.Add(time.Minute)
			return renewed, nil
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

	config := workerTestConfig()
	config.JobTimeout = jobTimeout
	config.CompletionGrace = completionGrace

	observer := &recordingLifecycleObserver{}
	runtime, err := newWorker(
		queue,
		&fakeVerifier{
			verify: func(
				jobContext context.Context,
				_ origin.Origin,
			) (declaration.Result, error) {
				<-jobContext.Done()
				clock = clock.Add(jobTimeout)
				return declaration.Result{}, jobContext.Err()
			},
		},
		config,
		waitStrategyFunc(func(context.Context, time.Duration) error {
			return nil
		}),
		factory,
	)
	if err != nil {
		t.Fatalf("newWorker() error = %v", err)
	}
	runtime.now = func() time.Time { return clock }
	runtime.SetLifecycleObserver(observer)

	worked, err := runtime.RunOnce(context.Background())
	if !worked {
		t.Fatal("RunOnce() worked = false, want true")
	}
	if !errors.Is(err, ErrInsufficientLeaseBudget) {
		t.Fatalf(
			"RunOnce() error = %v, want %v",
			err,
			ErrInsufficientLeaseBudget,
		)
	}
	if queue.completeCalls != 0 {
		t.Fatalf(
			"CompleteVerification() calls = %d, want 0",
			queue.completeCalls,
		)
	}

	foundBudgetFailure := false
	for _, event := range observer.events {
		if event.state == "failed" &&
			event.message == "insufficient_lease_budget" {
			foundBudgetFailure = true
			break
		}
	}
	if !foundBudgetFailure {
		t.Fatalf(
			"lifecycle events = %#v, want insufficient_lease_budget",
			observer.events,
		)
	}
}

type recordingLifecycleObserver struct {
	events []lifecycleEvent
}

type lifecycleEvent struct {
	state   string
	source  origin.Origin
	message string
}

func (observer *recordingLifecycleObserver) Observe(
	state string,
	source origin.Origin,
	message string,
) {
	observer.events = append(observer.events, lifecycleEvent{
		state:   state,
		source:  source,
		message: message,
	})
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

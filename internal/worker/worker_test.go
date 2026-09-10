package worker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/store"
)

func TestRunOnceProcessesOneVerification(t *testing.T) {
	source := mustWorkerOrigin(t)
	lease := workerTestLease(source)
	result := workerTestResult(source)

	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: lease,
				found: true,
			},
		},
	}
	verifier := &fakeVerifier{
		result: result,
	}
	config := workerTestConfig()

	runtime, err := New(queue, verifier, config)
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}

	worked, err := runtime.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v, want nil", err)
	}

	if !worked {
		t.Error("RunOnce() worked = false, want true")
	}

	if queue.claimCalls != 1 {
		t.Errorf(
			"Claim() calls = %d, want 1",
			queue.claimCalls,
		)
	}

	if queue.claimWorkerIDs[0] != config.WorkerID {
		t.Errorf(
			"Claim() worker ID = %q, want %q",
			queue.claimWorkerIDs[0],
			config.WorkerID,
		)
	}

	if verifier.calls != 1 {
		t.Errorf(
			"Verify() calls = %d, want 1",
			verifier.calls,
		)
	}

	if verifier.origins[0] != source {
		t.Errorf(
			"Verify() origin = %q, want %q",
			verifier.origins[0],
			source,
		)
	}

	if queue.completeCalls != 1 {
		t.Errorf(
			"CompleteVerification() calls = %d, want 1",
			queue.completeCalls,
		)
	}

	if queue.completedLeases[0] != lease {
		t.Errorf(
			"completed lease = %#v, want %#v",
			queue.completedLeases[0],
			lease,
		)
	}

	if queue.completedResults[0] != result {
		t.Errorf(
			"completed result = %#v, want %#v",
			queue.completedResults[0],
			result,
		)
	}

	if queue.recheckIntervals[0] !=
		config.RecheckInterval {
		t.Errorf(
			"recheck interval = %v, want %v",
			queue.recheckIntervals[0],
			config.RecheckInterval,
		)
	}
}

func TestRunOnceReturnsNoWork(t *testing.T) {
	queue := &fakeQueue{
		claims: []fakeClaim{
			{},
		},
	}
	verifier := &fakeVerifier{}

	runtime := newWorkerForTest(
		t,
		queue,
		verifier,
		workerTestConfig(),
	)

	worked, err := runtime.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v, want nil", err)
	}

	if worked {
		t.Error("RunOnce() worked = true, want false")
	}

	if verifier.calls != 0 {
		t.Errorf(
			"Verify() calls = %d, want 0",
			verifier.calls,
		)
	}

	if queue.completeCalls != 0 {
		t.Errorf(
			"CompleteVerification() calls = %d, want 0",
			queue.completeCalls,
		)
	}
}

func TestRunOnceRejectsInsufficientLeaseBudget(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	lease := workerTestLease(source)
	lease.ExpiresAt = lease.ClaimedAt.Add(
		3 * time.Minute,
	)

	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: lease,
				found: true,
			},
		},
	}
	verifier := &fakeVerifier{}
	config := workerTestConfig()
	config.JobTimeout = 2 * time.Minute
	config.CompletionGrace = time.Minute

	runtime := newWorkerForTest(
		t,
		queue,
		verifier,
		config,
	)

	worked, err := runtime.RunOnce(context.Background())
	if !errors.Is(err, ErrInsufficientLeaseBudget) {
		t.Errorf(
			"RunOnce() error = %v, want ErrInsufficientLeaseBudget",
			err,
		)
	}

	if !worked {
		t.Error("RunOnce() worked = false, want true")
	}

	if verifier.calls != 0 {
		t.Errorf(
			"Verify() calls = %d, want 0",
			verifier.calls,
		)
	}

	if queue.completeCalls != 0 {
		t.Errorf(
			"CompleteVerification() calls = %d, want 0",
			queue.completeCalls,
		)
	}
}

func TestRunOncePreservesClaimFailure(t *testing.T) {
	claimError := errors.New("test claim failure")
	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				err: claimError,
			},
		},
	}

	runtime := newWorkerForTest(
		t,
		queue,
		&fakeVerifier{},
		workerTestConfig(),
	)

	worked, err := runtime.RunOnce(context.Background())
	if !errors.Is(err, claimError) {
		t.Errorf(
			"RunOnce() error = %v, want claim failure",
			err,
		)
	}

	if worked {
		t.Error("RunOnce() worked = true, want false")
	}
}

func TestRunOncePreservesVerificationFailure(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	verifyError := errors.New("test verification failure")
	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: workerTestLease(source),
				found: true,
			},
		},
	}
	verifier := &fakeVerifier{
		err: verifyError,
	}

	runtime := newWorkerForTest(
		t,
		queue,
		verifier,
		workerTestConfig(),
	)

	worked, err := runtime.RunOnce(context.Background())
	if !errors.Is(err, verifyError) {
		t.Errorf(
			"RunOnce() error = %v, want verification failure",
			err,
		)
	}

	if !worked {
		t.Error("RunOnce() worked = false, want true")
	}

	if queue.completeCalls != 0 {
		t.Errorf(
			"CompleteVerification() calls = %d, want 0",
			queue.completeCalls,
		)
	}
}

func TestRunOnceCompletesTimedOutVerificationAsUnavailable(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: workerTestLease(source),
				found: true,
			},
		},
	}
	verifier := &fakeVerifier{
		verify: func(
			ctx context.Context,
			_ origin.Origin,
		) (declaration.Result, error) {
			<-ctx.Done()

			return declaration.Result{}, ctx.Err()
		},
	}

	timeoutCalls := 0
	factory := timeoutFactoryFunc(
		func(
			ctx context.Context,
			_ time.Duration,
		) (context.Context, context.CancelFunc) {
			timeoutCalls++

			return context.WithDeadline(
				ctx,
				time.Unix(0, 0),
			)
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
		t.Fatalf("newWorker() error = %v, want nil", err)
	}

	worked, err := runtime.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v, want nil", err)
	}

	if !worked {
		t.Error("RunOnce() worked = false, want true")
	}

	if timeoutCalls != 2 {
		t.Errorf(
			"timeout factory calls = %d, want 2",
			timeoutCalls,
		)
	}

	if queue.completeCalls != 1 {
		t.Errorf(
			"CompleteVerification() calls = %d, want 1",
			queue.completeCalls,
		)
	}

	wantResult := declaration.Result{
		Outcome: declaration.OutcomeUnavailable,
		Origin:  source,
	}
	if queue.completedResults[0] != wantResult {
		t.Errorf(
			"completed result = %#v, want %#v",
			queue.completedResults[0],
			wantResult,
		)
	}
}

func TestRunOncePreservesParentCancellationDuringVerification(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: workerTestLease(source),
				found: true,
			},
		},
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	verifier := &fakeVerifier{
		verify: func(
			context.Context,
			origin.Origin,
		) (declaration.Result, error) {
			cancel()

			return declaration.Result{},
				errors.New("canceled verification")
		},
	}

	runtime := newWorkerForTest(
		t,
		queue,
		verifier,
		workerTestConfig(),
	)

	worked, err := runtime.RunOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"RunOnce() error = %v, want context.Canceled",
			err,
		)
	}

	if !worked {
		t.Error("RunOnce() worked = false, want true")
	}

	if queue.completeCalls != 0 {
		t.Errorf(
			"CompleteVerification() calls = %d, want 0",
			queue.completeCalls,
		)
	}
}

func TestRunOnceDoesNotCompleteAfterParentCancellation(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: workerTestLease(source),
				found: true,
			},
		},
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	verifier := &fakeVerifier{
		verify: func(
			context.Context,
			origin.Origin,
		) (declaration.Result, error) {
			cancel()

			return workerTestResult(source), nil
		},
	}

	runtime := newWorkerForTest(
		t,
		queue,
		verifier,
		workerTestConfig(),
	)

	worked, err := runtime.RunOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"RunOnce() error = %v, want context.Canceled",
			err,
		)
	}

	if !worked {
		t.Error("RunOnce() worked = false, want true")
	}

	if queue.completeCalls != 0 {
		t.Errorf(
			"CompleteVerification() calls = %d, want 0",
			queue.completeCalls,
		)
	}
}

func TestRunOncePreservesCompletionFailure(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	completeError := errors.New("test completion failure")
	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: workerTestLease(source),
				found: true,
			},
		},
		completeError: completeError,
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

	worked, err := runtime.RunOnce(context.Background())
	if !errors.Is(err, completeError) {
		t.Errorf(
			"RunOnce() error = %v, want completion failure",
			err,
		)
	}

	if !worked {
		t.Error("RunOnce() worked = false, want true")
	}
}

func TestRunOncePreservesCompletionDeadline(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: workerTestLease(source),
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
	}
	verifier := &fakeVerifier{
		result: workerTestResult(source),
	}

	timeoutCalls := 0
	factory := timeoutFactoryFunc(
		func(
			ctx context.Context,
			_ time.Duration,
		) (context.Context, context.CancelFunc) {
			timeoutCalls++
			if timeoutCalls == 1 {
				return context.WithCancel(ctx)
			}

			return context.WithDeadline(
				ctx,
				time.Unix(0, 0),
			)
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
		t.Fatalf("newWorker() error = %v, want nil", err)
	}

	worked, err := runtime.RunOnce(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf(
			"RunOnce() error = %v, want context.DeadlineExceeded",
			err,
		)
	}

	if !worked {
		t.Error("RunOnce() worked = false, want true")
	}

	if timeoutCalls != 2 {
		t.Errorf(
			"timeout factory calls = %d, want 2",
			timeoutCalls,
		)
	}
}

func TestRunOncePreservesParentCancellationDuringCompletion(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: workerTestLease(source),
				found: true,
			},
		},
		complete: func(
			context.Context,
			store.Lease,
			declaration.Result,
			time.Duration,
		) error {
			cancel()

			return errors.New("canceled completion")
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

	worked, err := runtime.RunOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"RunOnce() error = %v, want context.Canceled",
			err,
		)
	}

	if !worked {
		t.Error("RunOnce() worked = false, want true")
	}
}

func TestRunWaitsOnlyWhenIdle(t *testing.T) {
	stopError := errors.New("test stop")
	queue := &fakeQueue{
		claims: []fakeClaim{
			{},
			{
				err: stopError,
			},
		},
	}
	verifier := &fakeVerifier{}
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
		contextTimeoutFactory{},
	)
	if err != nil {
		t.Fatalf("newWorker() error = %v, want nil", err)
	}

	err = runtime.Run(context.Background())
	if !errors.Is(err, stopError) {
		t.Errorf(
			"Run() error = %v, want stop error",
			err,
		)
	}

	if waitCalls != 1 {
		t.Errorf(
			"wait calls = %d, want 1",
			waitCalls,
		)
	}
}

func TestRunImmediatelyClaimsAfterSuccessfulWork(
	t *testing.T,
) {
	source := mustWorkerOrigin(t)
	stopError := errors.New("test stop")
	queue := &fakeQueue{
		claims: []fakeClaim{
			{
				lease: workerTestLease(source),
				found: true,
			},
			{
				err: stopError,
			},
		},
	}
	verifier := &fakeVerifier{
		result: workerTestResult(source),
	}
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
		contextTimeoutFactory{},
	)
	if err != nil {
		t.Fatalf("newWorker() error = %v, want nil", err)
	}

	err = runtime.Run(context.Background())
	if !errors.Is(err, stopError) {
		t.Errorf(
			"Run() error = %v, want stop error",
			err,
		)
	}

	if queue.claimCalls != 2 {
		t.Errorf(
			"Claim() calls = %d, want 2",
			queue.claimCalls,
		)
	}

	if waitCalls != 0 {
		t.Errorf(
			"wait calls = %d, want 0",
			waitCalls,
		)
	}
}

func TestRunContinuesAfterTimedOutWork(t *testing.T) {
	first := mustWorkerOrigin(t)
	second, err := origin.Parse("https://second.example")
	if err != nil {
		t.Fatalf("parse second origin: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	queue := &fakeQueue{
		claims: []fakeClaim{
			{lease: workerTestLease(first), found: true},
			{lease: workerTestLease(second), found: true},
		},
	}
	queue.complete = func(
		_ context.Context,
		_ store.Lease,
		_ declaration.Result,
		_ time.Duration,
	) error {
		if queue.completeCalls == 2 {
			cancel()
		}
		return nil
	}

	verifier := &fakeVerifier{}
	verifier.verify = func(
		jobContext context.Context,
		source origin.Origin,
	) (declaration.Result, error) {
		if verifier.calls == 1 {
			<-jobContext.Done()
			return declaration.Result{}, jobContext.Err()
		}
		return workerTestResult(source), nil
	}

	timeoutCalls := 0
	factory := timeoutFactoryFunc(
		func(
			parent context.Context,
			_ time.Duration,
		) (context.Context, context.CancelFunc) {
			timeoutCalls++
			if timeoutCalls == 1 {
				return context.WithDeadline(parent, time.Unix(0, 0))
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
		t.Fatalf("newWorker() error = %v, want nil", err)
	}

	err = runtime.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if verifier.calls != 2 {
		t.Errorf("Verify() calls = %d, want 2", verifier.calls)
	}
	if queue.completeCalls != 2 {
		t.Errorf(
			"CompleteVerification() calls = %d, want 2",
			queue.completeCalls,
		)
	}
	if queue.completedResults[0].Outcome !=
		declaration.OutcomeUnavailable {
		t.Errorf(
			"first outcome = %v, want unavailable",
			queue.completedResults[0].Outcome,
		)
	}
	if queue.completedResults[1] != workerTestResult(second) {
		t.Errorf(
			"second result = %#v, want successful result",
			queue.completedResults[1],
		)
	}
}

func TestRunCancellationInterruptsIdleWait(
	t *testing.T,
) {
	queue := &fakeQueue{
		claims: []fakeClaim{
			{},
		},
	}
	entered := make(chan struct{})
	var once sync.Once

	waiter := waitStrategyFunc(
		func(
			ctx context.Context,
			_ time.Duration,
		) error {
			once.Do(func() {
				close(entered)
			})
			<-ctx.Done()

			return ctx.Err()
		},
	)

	runtime, err := newWorker(
		queue,
		&fakeVerifier{},
		workerTestConfig(),
		waiter,
		contextTimeoutFactory{},
	)
	if err != nil {
		t.Fatalf("newWorker() error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	result := make(chan error, 1)
	go func() {
		result <- runtime.Run(ctx)
	}()

	<-entered
	cancel()

	err = <-result
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Run() error = %v, want context.Canceled",
			err,
		)
	}
}

func TestWorkerValidatesDependenciesAndConfiguration(
	t *testing.T,
) {
	validQueue := &fakeQueue{}
	validVerifier := &fakeVerifier{}
	validConfig := workerTestConfig()

	tests := []struct {
		name           string
		queue          Queue
		verifier       Verifier
		config         Config
		waiter         waitStrategy
		timeoutFactory timeoutFactory
		wantError      error
	}{
		{
			name:           "missing queue",
			verifier:       validVerifier,
			config:         validConfig,
			waiter:         timerWaitStrategy{},
			timeoutFactory: contextTimeoutFactory{},
			wantError:      errQueueUnavailable,
		},
		{
			name:           "missing verifier",
			queue:          validQueue,
			config:         validConfig,
			waiter:         timerWaitStrategy{},
			timeoutFactory: contextTimeoutFactory{},
			wantError:      errVerifierUnavailable,
		},
		{
			name:           "invalid config",
			queue:          validQueue,
			verifier:       validVerifier,
			waiter:         timerWaitStrategy{},
			timeoutFactory: contextTimeoutFactory{},
			wantError:      errInvalidConfig,
		},
		{
			name:           "missing waiter",
			queue:          validQueue,
			verifier:       validVerifier,
			config:         validConfig,
			timeoutFactory: contextTimeoutFactory{},
			wantError:      errWaiterUnavailable,
		},
		{
			name:      "missing timeout factory",
			queue:     validQueue,
			verifier:  validVerifier,
			config:    validConfig,
			waiter:    timerWaitStrategy{},
			wantError: errTimeoutFactoryUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime, err := newWorker(
				test.queue,
				test.verifier,
				test.config,
				test.waiter,
				test.timeoutFactory,
			)
			if !errors.Is(err, test.wantError) {
				t.Errorf(
					"newWorker() error = %v, want %v",
					err,
					test.wantError,
				)
			}

			if runtime != nil {
				t.Errorf(
					"newWorker() worker = %#v, want nil",
					runtime,
				)
			}
		})
	}

	invalidConfigs := []Config{
		{},
		{
			WorkerID:        "",
			PollInterval:    time.Second,
			JobTimeout:      time.Second,
			CompletionGrace: time.Second,
			RecheckInterval: time.Second,
		},
		{
			WorkerID: strings.Repeat(
				"x",
				maxWorkerIDLength+1,
			),
			PollInterval:    time.Second,
			JobTimeout:      time.Second,
			CompletionGrace: time.Second,
			RecheckInterval: time.Second,
		},
		{
			WorkerID:        "worker-a",
			PollInterval:    0,
			JobTimeout:      time.Second,
			CompletionGrace: time.Second,
			RecheckInterval: time.Second,
		},
		{
			WorkerID:        "worker-a",
			PollInterval:    time.Second,
			JobTimeout:      0,
			CompletionGrace: time.Second,
			RecheckInterval: time.Second,
		},
		{
			WorkerID:        "worker-a",
			PollInterval:    time.Second,
			JobTimeout:      time.Second,
			CompletionGrace: 0,
			RecheckInterval: time.Second,
		},
		{
			WorkerID:        "worker-a",
			PollInterval:    time.Second,
			JobTimeout:      time.Second,
			CompletionGrace: time.Second,
			RecheckInterval: 0,
		},
	}

	for _, config := range invalidConfigs {
		runtime, err := New(
			validQueue,
			validVerifier,
			config,
		)
		if !errors.Is(err, errInvalidConfig) {
			t.Errorf(
				"New(%#v) error = %v, want errInvalidConfig",
				config,
				err,
			)
		}

		if runtime != nil {
			t.Errorf(
				"New(%#v) worker = %#v, want nil",
				config,
				runtime,
			)
		}
	}
}

func TestWorkerValidatesRuntimeStateAndContext(
	t *testing.T,
) {
	var nilWorker *Worker

	worked, err := nilWorker.RunOnce(
		context.Background(),
	)
	if !errors.Is(err, errWorkerUnavailable) {
		t.Errorf(
			"nil RunOnce() error = %v, want errWorkerUnavailable",
			err,
		)
	}

	if worked {
		t.Error("nil RunOnce() worked = true, want false")
	}

	err = nilWorker.Run(context.Background())
	if !errors.Is(err, errWorkerUnavailable) {
		t.Errorf(
			"nil Run() error = %v, want errWorkerUnavailable",
			err,
		)
	}

	runtime := newWorkerForTest(
		t,
		&fakeQueue{},
		&fakeVerifier{},
		workerTestConfig(),
	)

	var nilContext context.Context
	worked, err = runtime.RunOnce(nilContext)
	if !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"RunOnce(nil) error = %v, want errInvalidContext",
			err,
		)
	}

	if worked {
		t.Error("RunOnce(nil) worked = true, want false")
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	worked, err = runtime.RunOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"RunOnce(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	if worked {
		t.Error(
			"RunOnce(canceled) worked = true, want false",
		)
	}

	corruptWorkers := []struct {
		name      string
		runtime   *Worker
		run       bool
		wantError error
	}{
		{
			name: "missing queue",
			runtime: &Worker{
				verifier:       &fakeVerifier{},
				config:         workerTestConfig(),
				waiter:         timerWaitStrategy{},
				timeoutFactory: contextTimeoutFactory{},
			},
			wantError: errQueueUnavailable,
		},
		{
			name: "missing verifier",
			runtime: &Worker{
				queue:          &fakeQueue{},
				config:         workerTestConfig(),
				waiter:         timerWaitStrategy{},
				timeoutFactory: contextTimeoutFactory{},
			},
			wantError: errVerifierUnavailable,
		},
		{
			name: "invalid config",
			runtime: &Worker{
				queue:          &fakeQueue{},
				verifier:       &fakeVerifier{},
				waiter:         timerWaitStrategy{},
				timeoutFactory: contextTimeoutFactory{},
			},
			wantError: errInvalidConfig,
		},
		{
			name: "missing timeout factory",
			runtime: &Worker{
				queue:    &fakeQueue{},
				verifier: &fakeVerifier{},
				config:   workerTestConfig(),
				waiter:   timerWaitStrategy{},
			},
			wantError: errTimeoutFactoryUnavailable,
		},
		{
			name: "missing waiter",
			runtime: &Worker{
				queue:          &fakeQueue{},
				verifier:       &fakeVerifier{},
				config:         workerTestConfig(),
				timeoutFactory: contextTimeoutFactory{},
			},
			run:       true,
			wantError: errWaiterUnavailable,
		},
	}

	for _, test := range corruptWorkers {
		t.Run(test.name, func(t *testing.T) {
			if test.run {
				err := test.runtime.Run(
					context.Background(),
				)
				if !errors.Is(err, test.wantError) {
					t.Errorf(
						"Run() error = %v, want %v",
						err,
						test.wantError,
					)
				}

				return
			}

			worked, err := test.runtime.RunOnce(
				context.Background(),
			)
			if !errors.Is(err, test.wantError) {
				t.Errorf(
					"RunOnce() error = %v, want %v",
					err,
					test.wantError,
				)
			}

			if worked {
				t.Error(
					"RunOnce() worked = true, want false",
				)
			}
		})
	}
}

func TestTimerWaitStrategyReturnsAfterTimerAndCancellation(
	t *testing.T,
) {
	waiter := timerWaitStrategy{}

	if err := waiter.Wait(
		context.Background(),
		time.Nanosecond,
	); err != nil {
		t.Fatalf(
			"timer Wait() error = %v, want nil",
			err,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := waiter.Wait(
		ctx,
		time.Hour,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"canceled Wait() error = %v, want context.Canceled",
			err,
		)
	}
}

type fakeClaim struct {
	lease store.Lease
	found bool
	err   error
}

type fakeQueue struct {
	claims           []fakeClaim
	claimCalls       int
	claimWorkerIDs   []string
	completeCalls    int
	completedLeases  []store.Lease
	completedResults []declaration.Result
	recheckIntervals []time.Duration
	completeError    error
	complete         func(
		context.Context,
		store.Lease,
		declaration.Result,
		time.Duration,
	) error
}

func (queue *fakeQueue) Claim(
	_ context.Context,
	workerID string,
) (store.Lease, bool, error) {
	queue.claimWorkerIDs = append(
		queue.claimWorkerIDs,
		workerID,
	)

	index := queue.claimCalls
	queue.claimCalls++

	if index >= len(queue.claims) {
		return store.Lease{}, false, nil
	}

	claim := queue.claims[index]

	return claim.lease, claim.found, claim.err
}

func (queue *fakeQueue) CompleteVerification(
	ctx context.Context,
	lease store.Lease,
	result declaration.Result,
	recheckAfter time.Duration,
) error {
	queue.completeCalls++
	queue.completedLeases = append(
		queue.completedLeases,
		lease,
	)
	queue.completedResults = append(
		queue.completedResults,
		result,
	)
	queue.recheckIntervals = append(
		queue.recheckIntervals,
		recheckAfter,
	)

	if queue.complete != nil {
		return queue.complete(
			ctx,
			lease,
			result,
			recheckAfter,
		)
	}

	return queue.completeError
}

type fakeVerifier struct {
	result  declaration.Result
	err     error
	calls   int
	origins []origin.Origin
	verify  func(
		context.Context,
		origin.Origin,
	) (declaration.Result, error)
}

func (verifier *fakeVerifier) Verify(
	ctx context.Context,
	source origin.Origin,
) (declaration.Result, error) {
	verifier.calls++
	verifier.origins = append(
		verifier.origins,
		source,
	)

	if verifier.verify != nil {
		return verifier.verify(ctx, source)
	}

	return verifier.result, verifier.err
}

type waitStrategyFunc func(
	context.Context,
	time.Duration,
) error

func (waiter waitStrategyFunc) Wait(
	ctx context.Context,
	delay time.Duration,
) error {
	return waiter(ctx, delay)
}

type timeoutFactoryFunc func(
	context.Context,
	time.Duration,
) (context.Context, context.CancelFunc)

func (factory timeoutFactoryFunc) WithTimeout(
	ctx context.Context,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	return factory(ctx, timeout)
}

func newWorkerForTest(
	t *testing.T,
	queue Queue,
	verifier Verifier,
	config Config,
) *Worker {
	t.Helper()

	runtime, err := New(queue, verifier, config)
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}

	return runtime
}

func workerTestConfig() Config {
	return Config{
		WorkerID:        "worker-a",
		PollInterval:    time.Minute,
		JobTimeout:      2 * time.Minute,
		CompletionGrace: time.Minute,
		RecheckInterval: 24 * time.Hour,
	}
}

func workerTestLease(
	source origin.Origin,
) store.Lease {
	claimedAt := time.Date(
		2026,
		time.September,
		1,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	return store.Lease{
		Origin:     source,
		WorkerID:   "worker-a",
		Generation: 1,
		ClaimedAt:  claimedAt,
		ExpiresAt:  claimedAt.Add(10 * time.Minute),
	}
}

func workerTestResult(
	source origin.Origin,
) declaration.Result {
	return declaration.Result{
		Outcome: declaration.OutcomeValid,
		Origin:  source,
		Declaration: declaration.Declaration{
			Version:  1,
			Identity: declaration.IdentityAffirmed,
		},
	}
}

func mustWorkerOrigin(t *testing.T) origin.Origin {
	t.Helper()

	source, err := origin.Parse(
		"https://example.com",
	)
	if err != nil {
		t.Fatalf(
			"origin.Parse() error = %v, want nil",
			err,
		)
	}

	return source
}

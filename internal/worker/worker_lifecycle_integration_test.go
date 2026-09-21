package worker_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/store"
	"github.com/joshternet/joshbot/internal/worker"
)

type workerLifecycleIntegrationQueue struct {
	lease       store.Lease
	found       bool
	claimErr    error
	completeErr error
	paused      bool
	pauseErr    error
	renew       func(context.Context, store.Lease) (store.Lease, error)

	completed chan declaration.Result
}

func (queue *workerLifecycleIntegrationQueue) Claim(
	context.Context,
	string,
) (store.Lease, bool, error) {
	if queue.claimErr != nil {
		return store.Lease{}, false, queue.claimErr
	}

	return queue.lease, queue.found, nil
}

func (queue *workerLifecycleIntegrationQueue) Renew(
	ctx context.Context,
	lease store.Lease,
) (store.Lease, error) {
	if queue.renew != nil {
		return queue.renew(ctx, lease)
	}

	renewed := lease
	renewed.ExpiresAt = time.Now().UTC().Add(10 * time.Minute)
	return renewed, nil
}

func (queue *workerLifecycleIntegrationQueue) CompleteVerification(
	_ context.Context,
	_ store.Lease,
	result declaration.Result,
	_ time.Duration,
) error {
	if queue.completeErr != nil {
		return queue.completeErr
	}

	if queue.completed != nil {
		queue.completed <- result
	}

	return nil
}

func (queue *workerLifecycleIntegrationQueue) VerificationPaused(
	context.Context,
) (bool, error) {
	if queue.pauseErr != nil {
		return false, queue.pauseErr
	}

	return queue.paused, nil
}

type workerLifecycleIntegrationVerifier struct {
	verify func(
		context.Context,
		origin.Origin,
	) (declaration.Result, error)
}

func (verifier workerLifecycleIntegrationVerifier) Verify(
	ctx context.Context,
	source origin.Origin,
) (declaration.Result, error) {
	return verifier.verify(ctx, source)
}

type workerLifecycleIntegrationEvent struct {
	state   string
	source  origin.Origin
	message string
}

type workerLifecycleIntegrationObserver struct {
	events chan workerLifecycleIntegrationEvent
}

func (
	observer *workerLifecycleIntegrationObserver,
) Observe(
	state string,
	source origin.Origin,
	message string,
) {
	observer.events <- workerLifecycleIntegrationEvent{
		state:   state,
		source:  source,
		message: message,
	}
}

func TestWorkerLifecycleIntegrationReportsPausedAndRunCancellation(
	t *testing.T,
) {
	queue := &workerLifecycleIntegrationQueue{
		paused: true,
	}

	verifier := workerLifecycleIntegrationVerifier{
		verify: func(
			context.Context,
			origin.Origin,
		) (declaration.Result, error) {
			t.Fatal(
				"verifier called with no claimed work",
			)
			return declaration.Result{}, nil
		},
	}

	runtime := newWorkerLifecycleIntegrationWorker(
		t,
		queue,
		verifier,
	)

	observer := &workerLifecycleIntegrationObserver{
		events: make(
			chan workerLifecycleIntegrationEvent,
			8,
		),
	}

	runtime.SetLifecycleObserver(observer)

	worked, err := runtime.RunOnce(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"RunOnce() error = %v",
			err,
		)
	}

	if worked {
		t.Fatal(
			"RunOnce() worked = true, want false",
		)
	}

	event := receiveWorkerLifecycleIntegrationEvent(
		t,
		observer.events,
	)

	if event.state != "paused" ||
		event.message != "" {
		t.Errorf(
			"paused event = %#v",
			event,
		)
	}

	queue.paused = false

	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	runResult := make(chan error, 1)

	go func() {
		runResult <- runtime.Run(ctx)
	}()

	event = receiveWorkerLifecycleIntegrationEvent(
		t,
		observer.events,
	)

	if event.state != "idle" {
		cancel()
		t.Fatalf(
			"Run() event = %#v, want idle",
			event,
		)
	}

	cancel()

	select {
	case err := <-runResult:
		if !errors.Is(
			err,
			context.Canceled,
		) {
			t.Fatalf(
				"Run() error = %v, want %v",
				err,
				context.Canceled,
			)
		}

	case <-time.After(2 * time.Second):
		t.Fatal(
			"Run() did not stop after cancellation",
		)
	}
}

func TestWorkerLifecycleIntegrationRejectsInsufficientLeaseBudget(
	t *testing.T,
) {
	source := mustWorkerLifecycleIntegrationOrigin(
		t,
		"https://example.com",
	)

	claimedAt := time.Now().UTC()

	queue := &workerLifecycleIntegrationQueue{
		found: true,
		lease: store.Lease{
			Origin:     source,
			WorkerID:   "integration-worker",
			Generation: 1,
			ClaimedAt:  claimedAt,
			ExpiresAt: claimedAt.Add(
				25 * time.Millisecond,
			),
		},
		renew: func(
			_ context.Context,
			lease store.Lease,
		) (store.Lease, error) {
			return lease, nil
		},
	}

	verifyCalls := 0

	verifier := workerLifecycleIntegrationVerifier{
		verify: func(
			context.Context,
			origin.Origin,
		) (declaration.Result, error) {
			verifyCalls++

			return declaration.Result{}, nil
		},
	}

	runtime := newWorkerLifecycleIntegrationWorker(
		t,
		queue,
		verifier,
	)

	observer := &workerLifecycleIntegrationObserver{
		events: make(
			chan workerLifecycleIntegrationEvent,
			8,
		),
	}

	runtime.SetLifecycleObserver(observer)

	worked, err := runtime.RunOnce(
		context.Background(),
	)

	if !worked {
		t.Fatal(
			"RunOnce() worked = false, want true after claim",
		)
	}

	if !errors.Is(
		err,
		worker.ErrInsufficientLeaseBudget,
	) {
		t.Fatalf(
			"RunOnce() error = %v, want %v",
			err,
			worker.ErrInsufficientLeaseBudget,
		)
	}

	if verifyCalls != 0 {
		t.Errorf(
			"verifier calls = %d, want 0",
			verifyCalls,
		)
	}

	running := receiveWorkerLifecycleIntegrationEvent(
		t,
		observer.events,
	)
	failed := receiveWorkerLifecycleIntegrationEvent(
		t,
		observer.events,
	)

	if running.state != "running" ||
		running.source != source {
		t.Errorf(
			"running event = %#v",
			running,
		)
	}

	if failed.state != "failed" ||
		failed.source != source ||
		failed.message !=
			"insufficient_lease_budget" {
		t.Errorf(
			"failed event = %#v",
			failed,
		)
	}
}

func TestWorkerLifecycleIntegrationRejectsLeaseRenewalFailure(
	t *testing.T,
) {
	source := mustWorkerLifecycleIntegrationOrigin(
		t,
		"https://example.com",
	)

	claimedAt := time.Now().UTC()
	renewErr := errors.New(
		"integration lease renewal failure",
	)

	queue := &workerLifecycleIntegrationQueue{
		found: true,
		lease: store.Lease{
			Origin:     source,
			WorkerID:   "integration-worker",
			Generation: 1,
			ClaimedAt:  claimedAt,
			ExpiresAt: claimedAt.Add(
				25 * time.Millisecond,
			),
		},
		renew: func(
			_ context.Context,
			_ store.Lease,
		) (store.Lease, error) {
			return store.Lease{}, renewErr
		},
	}

	verifyCalls := 0

	verifier := workerLifecycleIntegrationVerifier{
		verify: func(
			context.Context,
			origin.Origin,
		) (declaration.Result, error) {
			verifyCalls++

			return declaration.Result{}, nil
		},
	}

	runtime := newWorkerLifecycleIntegrationWorker(
		t,
		queue,
		verifier,
	)

	observer := &workerLifecycleIntegrationObserver{
		events: make(
			chan workerLifecycleIntegrationEvent,
			8,
		),
	}

	runtime.SetLifecycleObserver(observer)

	worked, err := runtime.RunOnce(
		context.Background(),
	)

	if !worked {
		t.Fatal(
			"RunOnce() worked = false, want true after claim",
		)
	}

	if !errors.Is(err, renewErr) ||
		!strings.Contains(
			err.Error(),
			"renew lease",
		) {
		t.Fatalf(
			"RunOnce() error = %v, want wrapped lease renewal failure",
			err,
		)
	}

	if verifyCalls != 0 {
		t.Errorf(
			"verifier calls = %d, want 0",
			verifyCalls,
		)
	}

	running := receiveWorkerLifecycleIntegrationEvent(
		t,
		observer.events,
	)
	failed := receiveWorkerLifecycleIntegrationEvent(
		t,
		observer.events,
	)

	if running.state != "running" ||
		running.source != source {
		t.Errorf(
			"running event = %#v",
			running,
		)
	}

	if failed.state != "failed" ||
		failed.source != source ||
		failed.message !=
			"lease_renewal_failed" {
		t.Errorf(
			"failed event = %#v",
			failed,
		)
	}
}

func TestWorkerLifecycleIntegrationLeaseRenewalFailurePreservesCancellation(
	t *testing.T,
) {
	source := mustWorkerLifecycleIntegrationOrigin(
		t,
		"https://example.com",
	)

	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	claimedAt := time.Now().UTC()

	queue := &workerLifecycleIntegrationQueue{
		found: true,
		lease: store.Lease{
			Origin:     source,
			WorkerID:   "integration-worker",
			Generation: 1,
			ClaimedAt:  claimedAt,
			ExpiresAt: claimedAt.Add(
				25 * time.Millisecond,
			),
		},
		renew: func(
			_ context.Context,
			_ store.Lease,
		) (store.Lease, error) {
			cancel()

			return store.Lease{}, errors.New(
				"integration lease lost",
			)
		},
	}

	runtime := newWorkerLifecycleIntegrationWorker(
		t,
		queue,
		workerLifecycleIntegrationVerifier{
			verify: func(
				context.Context,
				origin.Origin,
			) (declaration.Result, error) {
				t.Fatal(
					"verifier called after lease renewal failure",
				)

				return declaration.Result{}, nil
			},
		},
	)

	observer := &workerLifecycleIntegrationObserver{
		events: make(
			chan workerLifecycleIntegrationEvent,
			8,
		),
	}

	runtime.SetLifecycleObserver(observer)

	worked, err := runtime.RunOnce(ctx)

	if !worked {
		t.Fatal(
			"RunOnce() worked = false, want true after claim",
		)
	}

	if !errors.Is(err, context.Canceled) {
		t.Fatalf(
			"RunOnce() error = %v, want context.Canceled",
			err,
		)
	}

	running := receiveWorkerLifecycleIntegrationEvent(
		t,
		observer.events,
	)
	failed := receiveWorkerLifecycleIntegrationEvent(
		t,
		observer.events,
	)

	if running.state != "running" ||
		running.source != source {
		t.Errorf(
			"running event = %#v",
			running,
		)
	}

	if failed.state != "failed" ||
		failed.message !=
			"lease_renewal_failed" {
		t.Errorf(
			"failed event = %#v",
			failed,
		)
	}
}

func TestWorkerLifecycleIntegrationRenewsLeaseBeforeVerification(
	t *testing.T,
) {
	source := mustWorkerLifecycleIntegrationOrigin(
		t,
		"https://example.com",
	)

	claimedAt := time.Now().UTC()
	renewCalls := 0

	queue := &workerLifecycleIntegrationQueue{
		found: true,
		lease: store.Lease{
			Origin:     source,
			WorkerID:   "integration-worker",
			Generation: 1,
			ClaimedAt:  claimedAt,
			ExpiresAt: claimedAt.Add(
				25 * time.Millisecond,
			),
		},
		renew: func(
			_ context.Context,
			lease store.Lease,
		) (store.Lease, error) {
			renewCalls++

			renewed := lease
			renewed.ExpiresAt = time.Now().UTC().Add(
				time.Minute,
			)

			return renewed, nil
		},
		completed: make(
			chan declaration.Result,
			1,
		),
	}

	verifier := workerLifecycleIntegrationVerifier{
		verify: func(
			_ context.Context,
			source origin.Origin,
		) (declaration.Result, error) {
			return declaration.Result{
				Origin:  source,
				Outcome: declaration.OutcomeValid,
			}, nil
		},
	}

	runtime := newWorkerLifecycleIntegrationWorker(
		t,
		queue,
		verifier,
	)

	worked, err := runtime.RunOnce(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"RunOnce() error = %v",
			err,
		)
	}

	if !worked {
		t.Fatal(
			"RunOnce() worked = false, want true after claim",
		)
	}

	if renewCalls != 1 {
		t.Fatalf(
			"Renew() calls = %d, want 1",
			renewCalls,
		)
	}

	select {
	case result := <-queue.completed:
		if result.Outcome !=
			declaration.OutcomeValid {
			t.Errorf(
				"completed outcome = %v, want valid",
				result.Outcome,
			)
		}

	case <-time.After(2 * time.Second):
		t.Fatal(
			"CompleteVerification() was not called",
		)
	}
}

func TestWorkerLifecycleIntegrationTimesOutAndCompletesUnavailableResult(
	t *testing.T,
) {
	source := mustWorkerLifecycleIntegrationOrigin(
		t,
		"https://timeout.example",
	)

	claimedAt := time.Now().UTC()

	queue := &workerLifecycleIntegrationQueue{
		found: true,
		lease: store.Lease{
			Origin:     source,
			WorkerID:   "integration-worker",
			Generation: 1,
			ClaimedAt:  claimedAt,
			ExpiresAt: claimedAt.Add(
				time.Second,
			),
		},
		completed: make(
			chan declaration.Result,
			1,
		),
	}

	verifyCalls := 0

	verifier := workerLifecycleIntegrationVerifier{
		verify: func(
			ctx context.Context,
			source origin.Origin,
		) (declaration.Result, error) {
			verifyCalls++

			<-ctx.Done()

			return declaration.Result{
				Origin: source,
			}, ctx.Err()
		},
	}

	runtime := newWorkerLifecycleIntegrationWorker(
		t,
		queue,
		verifier,
	)

	observer := &workerLifecycleIntegrationObserver{
		events: make(
			chan workerLifecycleIntegrationEvent,
			8,
		),
	}

	runtime.SetLifecycleObserver(observer)

	worked, err := runtime.RunOnce(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"RunOnce() error = %v",
			err,
		)
	}

	if !worked {
		t.Fatal(
			"RunOnce() worked = false, want true",
		)
	}

	if verifyCalls != retry.MaxAttemptsPerCycle {
		t.Errorf(
			"verifier calls = %d, want %d",
			verifyCalls,
			retry.MaxAttemptsPerCycle,
		)
	}

	var result declaration.Result

	select {
	case result = <-queue.completed:
	case <-time.After(2 * time.Second):
		t.Fatal(
			"verification result was not completed",
		)
	}

	if result.Origin != source {
		t.Errorf(
			"result origin = %q, want %q",
			result.Origin.String(),
			source.String(),
		)
	}

	if result.Outcome !=
		declaration.OutcomeUnavailable {
		t.Errorf(
			"result outcome = %v, want %v",
			result.Outcome,
			declaration.OutcomeUnavailable,
		)
	}

	if result.FailureCategory !=
		retry.CategoryTimeout {
		t.Errorf(
			"failure category = %q, want %q",
			result.FailureCategory,
			retry.CategoryTimeout,
		)
	}

	running := receiveWorkerLifecycleIntegrationEvent(
		t,
		observer.events,
	)
	idle := receiveWorkerLifecycleIntegrationEvent(
		t,
		observer.events,
	)

	if running.state != "running" ||
		running.source != source {
		t.Errorf(
			"running event = %#v",
			running,
		)
	}

	if idle.state != "idle" {
		t.Errorf(
			"final event = %#v, want idle",
			idle,
		)
	}
}

func TestWorkerLifecycleIntegrationReportsCompletionFailure(
	t *testing.T,
) {
	source := mustWorkerLifecycleIntegrationOrigin(
		t,
		"https://completion.example",
	)

	claimedAt := time.Now().UTC()

	completionFailure := errors.New(
		"integration completion failure",
	)

	queue := &workerLifecycleIntegrationQueue{
		found: true,
		lease: store.Lease{
			Origin:     source,
			WorkerID:   "integration-worker",
			Generation: 1,
			ClaimedAt:  claimedAt,
			ExpiresAt: claimedAt.Add(
				time.Second,
			),
		},
		completeErr: completionFailure,
	}

	verifier := workerLifecycleIntegrationVerifier{
		verify: func(
			context.Context,
			origin.Origin,
		) (declaration.Result, error) {
			return declaration.Result{
				Outcome: declaration.OutcomeAbsent,
				Origin:  source,
			}, nil
		},
	}

	runtime := newWorkerLifecycleIntegrationWorker(
		t,
		queue,
		verifier,
	)

	observer := &workerLifecycleIntegrationObserver{
		events: make(
			chan workerLifecycleIntegrationEvent,
			8,
		),
	}

	runtime.SetLifecycleObserver(observer)

	worked, err := runtime.RunOnce(
		context.Background(),
	)

	if !worked {
		t.Fatal(
			"RunOnce() worked = false, want true",
		)
	}

	if !errors.Is(
		err,
		completionFailure,
	) {
		t.Fatalf(
			"RunOnce() error = %v, want completion failure",
			err,
		)
	}

	if !strings.Contains(
		err.Error(),
		"complete verification",
	) {
		t.Errorf(
			"RunOnce() error = %v, want completion context",
			err,
		)
	}

	running := receiveWorkerLifecycleIntegrationEvent(
		t,
		observer.events,
	)
	failed := receiveWorkerLifecycleIntegrationEvent(
		t,
		observer.events,
	)

	if running.state != "running" {
		t.Errorf(
			"running event = %#v",
			running,
		)
	}

	if failed.state != "failed" ||
		failed.message != "completion_failed" {
		t.Errorf(
			"failed event = %#v",
			failed,
		)
	}
}

func newWorkerLifecycleIntegrationWorker(
	t *testing.T,
	queue worker.Queue,
	verifier worker.Verifier,
) *worker.Worker {
	t.Helper()

	runtime, err := worker.New(
		queue,
		verifier,
		worker.Config{
			WorkerID:     "integration-worker",
			PollInterval: time.Hour,
			JobTimeout: 20 *
				time.Millisecond,
			CompletionGrace: 10 *
				time.Millisecond,
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

func receiveWorkerLifecycleIntegrationEvent(
	t *testing.T,
	events <-chan workerLifecycleIntegrationEvent,
) workerLifecycleIntegrationEvent {
	t.Helper()

	select {
	case event := <-events:
		return event

	case <-time.After(2 * time.Second):
		t.Fatal(
			"worker lifecycle event was not observed",
		)

		return workerLifecycleIntegrationEvent{}
	}
}

func mustWorkerLifecycleIntegrationOrigin(
	t *testing.T,
	raw string,
) origin.Origin {
	t.Helper()

	source, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			raw,
			err,
		)
	}

	return source
}

// Package worker coordinates queued declaration verification.
package worker

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/store"
)

const maxWorkerIDLength = 128

var (
	// ErrInsufficientLeaseBudget means a claimed lease cannot safely contain
	// both the configured verification timeout and completion grace period.
	ErrInsufficientLeaseBudget = errors.New(
		"worker: insufficient lease budget",
	)

	errWorkerUnavailable = errors.New(
		"worker: unavailable",
	)
	errQueueUnavailable = errors.New(
		"worker: queue unavailable",
	)
	errVerifierUnavailable = errors.New(
		"worker: verifier unavailable",
	)
	errWaiterUnavailable = errors.New(
		"worker: waiter unavailable",
	)
	errTimeoutFactoryUnavailable = errors.New(
		"worker: timeout factory unavailable",
	)
	errInvalidContext = errors.New(
		"worker: invalid context",
	)
	errInvalidConfig = errors.New(
		"worker: invalid configuration",
	)
)

// Queue is the durable queue behavior required by Worker.
type Queue interface {
	Claim(
		context.Context,
		string,
	) (store.Lease, bool, error)

	Renew(
		context.Context,
		store.Lease,
	) (store.Lease, error)

	CompleteVerification(
		context.Context,
		store.Lease,
		declaration.Result,
		time.Duration,
	) error
}

// Verifier is the declaration verification behavior required by Worker.
type Verifier interface {
	Verify(
		context.Context,
		origin.Origin,
	) (declaration.Result, error)
}

// LifecycleObserver receives bounded worker state transitions.
type LifecycleObserver interface {
	Observe(string, origin.Origin, string)
}

type verificationPauseQueue interface {
	VerificationPaused(context.Context) (bool, error)
}

// Config contains the operational policy for one worker.
type Config struct {
	WorkerID        string
	PollInterval    time.Duration
	JobTimeout      time.Duration
	CompletionGrace time.Duration
	RecheckInterval time.Duration
}

// Worker claims and processes declaration-verification work.
//
// Queue delivery and physical verification are at-least-once. Only a worker
// with current lease authority can atomically commit a verification result.
//
// Durable per-origin retry delays are owned by queue completion. Run paces
// processor and infrastructure failures with PollInterval and continues
// immediately after claimed-work failures so unrelated due work is not parked.
// That immediate continue is intentional: durable scheduling belongs to the
// failed origin's lease/completion state, not the processor loop.
type Worker struct {
	queue          Queue
	verifier       Verifier
	config         Config
	waiter         waitStrategy
	timeoutFactory timeoutFactory
	observer       LifecycleObserver
	now            func() time.Time
}

// SetLifecycleObserver attaches state observation without changing work.
func (w *Worker) SetLifecycleObserver(observer LifecycleObserver) {
	if w != nil {
		w.observer = observer
	}
}

func (w *Worker) observe(state string, source origin.Origin, message string) {
	if w.observer != nil {
		w.observer.Observe(state, source, message)
	}
}

type waitStrategy interface {
	Wait(
		context.Context,
		time.Duration,
	) error
}

type timeoutFactory interface {
	WithTimeout(
		context.Context,
		time.Duration,
	) (context.Context, context.CancelFunc)
}

type timerWaitStrategy struct{}

type contextTimeoutFactory struct{}

// New constructs a worker.
func New(
	queue Queue,
	verifier Verifier,
	config Config,
) (*Worker, error) {
	return newWorker(
		queue,
		verifier,
		config,
		timerWaitStrategy{},
		contextTimeoutFactory{},
	)
}

func newWorker(
	queue Queue,
	verifier Verifier,
	config Config,
	waiter waitStrategy,
	timeoutFactory timeoutFactory,
) (*Worker, error) {
	if queue == nil {
		return nil, errQueueUnavailable
	}

	if verifier == nil {
		return nil, errVerifierUnavailable
	}

	if !validConfig(config) {
		return nil, errInvalidConfig
	}

	if waiter == nil {
		return nil, errWaiterUnavailable
	}

	if timeoutFactory == nil {
		return nil, errTimeoutFactoryUnavailable
	}

	return &Worker{
		queue:          queue,
		verifier:       verifier,
		config:         config,
		waiter:         waiter,
		timeoutFactory: timeoutFactory,
	}, nil
}

// RunOnce claims and processes at most one queued origin.
//
// A false worked result means no eligible work was available. Once a lease is
// claimed, worked is true even if verification or completion fails.
func (w *Worker) RunOnce(
	ctx context.Context,
) (bool, error) {
	if err := w.validate(ctx, false); err != nil {
		return false, err
	}

	lease, found, err := w.queue.Claim(
		ctx,
		w.config.WorkerID,
	)
	if err != nil {
		w.observe("failed", origin.Origin{}, "claim_failed")
		return false, fmt.Errorf(
			"worker: claim work: %w",
			err,
		)
	}

	if !found {
		state := "idle"
		if queue, ok := w.queue.(verificationPauseQueue); ok {
			paused, pauseErr := queue.VerificationPaused(ctx)
			if pauseErr != nil {
				w.observe("failed", origin.Origin{}, "control_read_failed")
				return false, pauseErr
			}
			if paused {
				state = "paused"
			}
		}
		w.observe(state, origin.Origin{}, "")
		return false, nil
	}
	w.observe("running", lease.Origin, "")

	result, lease, timedOut, err := w.verifyWithRetries(ctx, lease)
	if err != nil {
		return true, err
	}

	if contextError := ctx.Err(); contextError != nil {
		return true, contextError
	}

	completionContext, cancelCompletion :=
		w.timeoutFactory.WithTimeout(
			ctx,
			w.config.CompletionGrace,
		)
	defer cancelCompletion()

	err = w.queue.CompleteVerification(
		completionContext,
		lease,
		result,
		w.config.RecheckInterval,
	)
	if err != nil {
		w.observe("failed", lease.Origin, "completion_failed")
		if timedOut {
			logVerificationTimeout(
				lease.Origin,
				"abandoned",
			)
		}

		if contextError := ctx.Err(); contextError != nil {
			return true, contextError
		}

		if contextError :=
			completionContext.Err(); contextError != nil {
			return true, contextError
		}

		return true, fmt.Errorf(
			"worker: complete verification: %w",
			err,
		)
	}

	if timedOut {
		logVerificationTimeout(
			lease.Origin,
			"completed",
		)
	}
	w.observe("idle", origin.Origin{}, "")

	return true, nil
}

func (w *Worker) verifyWithRetries(
	ctx context.Context,
	lease store.Lease,
) (declaration.Result, store.Lease, bool, error) {
	anyTimeout := false
	var lastResult declaration.Result
	current := lease

	for attempt := 1; attempt <= retry.MaxAttemptsPerCycle; attempt++ {
		ensured, ensureErr := w.ensureVerificationLeaseBudget(
			ctx,
			current,
		)
		if ensureErr != nil {
			return declaration.Result{}, ensured, anyTimeout, ensureErr
		}
		current = ensured

		jobContext, cancelJob := w.timeoutFactory.WithTimeout(
			ctx,
			w.config.JobTimeout,
		)
		result, err := w.verifier.Verify(jobContext, current.Origin)
		jobContextError := jobContext.Err()
		cancelJob()

		if err != nil {
			if contextError := ctx.Err(); contextError != nil {
				return declaration.Result{}, current, anyTimeout, contextError
			}
			if jobContextError == nil {
				w.observe(
					"failed",
					current.Origin,
					"verification_failed",
				)
				return declaration.Result{}, current, anyTimeout, fmt.Errorf(
					"worker: verify origin: %w",
					err,
				)
			}
			anyTimeout = true
			result = declaration.Result{
				Outcome:         declaration.OutcomeUnavailable,
				Origin:          current.Origin,
				FailureCategory: retry.CategoryTimeout,
			}
		}

		category := result.FailureCategory
		if result.Outcome == declaration.OutcomeUnavailable &&
			category == retry.CategoryNone {
			category = retry.CategoryDeclarationUnavailable
			result.FailureCategory = category
		}
		if result.Outcome != declaration.OutcomeUnavailable ||
			!category.Transient() {
			return result, current, anyTimeout, nil
		}
		lastResult = result
	}
	return lastResult, current, anyTimeout, nil
}

// ensureVerificationLeaseBudget renews only when remaining wall-clock lease
// time cannot hold one JobTimeout plus CompletionGrace.
func (w *Worker) ensureVerificationLeaseBudget(
	ctx context.Context,
	lease store.Lease,
) (store.Lease, error) {
	if hasCompletionBudget(
		lease.ExpiresAt.Sub(w.currentTime()),
		w.config.JobTimeout,
		w.config.CompletionGrace,
	) {
		return lease, nil
	}

	renewed, renewErr := w.queue.Renew(ctx, lease)
	if renewErr != nil {
		w.observe(
			"failed",
			lease.Origin,
			"lease_renewal_failed",
		)
		if contextError := ctx.Err(); contextError != nil {
			return lease, contextError
		}
		return lease, fmt.Errorf(
			"worker: renew lease: %w",
			renewErr,
		)
	}

	if !hasCompletionBudget(
		renewed.ExpiresAt.Sub(w.currentTime()),
		w.config.JobTimeout,
		w.config.CompletionGrace,
	) {
		w.observe(
			"failed",
			renewed.Origin,
			"insufficient_lease_budget",
		)
		return renewed, ErrInsufficientLeaseBudget
	}

	return renewed, nil
}

func (w *Worker) currentTime() time.Time {
	if w != nil && w.now != nil {
		return w.now()
	}
	return time.Now()
}

// Run processes work until the context is canceled or a fatal operation fails.
//
// Successful or claimed-work failures immediately lead to another claim
// attempt. That immediate continue after claimed-work failure is intentional so
// one origin cannot park the processor on the durable origin retry schedule.
// Idle polls and processor failures before a claim wait for PollInterval.
// Durable origin backoff is applied only by queue completion.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.validate(ctx, true); err != nil {
		return err
	}

	for {
		worked, err := w.RunOnce(ctx)
		if err != nil {
			if contextError := ctx.Err(); contextError != nil {
				return contextError
			}
			if worked {
				continue
			}
			if waitErr := w.waiter.Wait(
				ctx,
				w.config.PollInterval,
			); waitErr != nil {
				return waitErr
			}
			continue
		}

		if worked {
			continue
		}

		if err := w.waiter.Wait(
			ctx,
			w.config.PollInterval,
		); err != nil {
			return err
		}
	}
}

func (w *Worker) validate(
	ctx context.Context,
	requireWaiter bool,
) error {
	if w == nil {
		return errWorkerUnavailable
	}

	if ctx == nil {
		return errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if w.queue == nil {
		return errQueueUnavailable
	}

	if w.verifier == nil {
		return errVerifierUnavailable
	}

	if !validConfig(w.config) {
		return errInvalidConfig
	}

	if w.timeoutFactory == nil {
		return errTimeoutFactoryUnavailable
	}

	if requireWaiter && w.waiter == nil {
		return errWaiterUnavailable
	}

	return nil
}

func validConfig(config Config) bool {
	return config.WorkerID != "" &&
		utf8.RuneCountInString(config.WorkerID) <=
			maxWorkerIDLength &&
		config.PollInterval > 0 &&
		config.JobTimeout > 0 &&
		config.CompletionGrace > 0 &&
		config.RecheckInterval > 0
}

func hasCompletionBudget(
	leaseBudget time.Duration,
	jobTimeout time.Duration,
	completionGrace time.Duration,
) bool {
	return leaseBudget > 0 &&
		completionGrace < leaseBudget &&
		jobTimeout < leaseBudget-completionGrace
}

func logVerificationTimeout(
	source origin.Origin,
	leaseAction string,
) {
	sum := sha256.Sum256(
		[]byte(source.String()),
	)

	slog.Info(
		"verification timed out",
		"origin_id",
		fmt.Sprintf("%x", sum[:8]),
		"outcome",
		"unavailable",
		"lease_action",
		leaseAction,
	)
}

func (timerWaitStrategy) Wait(
	ctx context.Context,
	delay time.Duration,
) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (contextTimeoutFactory) WithTimeout(
	ctx context.Context,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, timeout)
}

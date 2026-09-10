// Package worker coordinates queued declaration verification.
package worker

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
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
type Worker struct {
	queue          Queue
	verifier       Verifier
	config         Config
	waiter         waitStrategy
	timeoutFactory timeoutFactory
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
		return false, fmt.Errorf(
			"worker: claim work: %w",
			err,
		)
	}

	if !found {
		return false, nil
	}

	leaseBudget := lease.ExpiresAt.Sub(
		lease.ClaimedAt,
	)
	if !hasCompletionBudget(
		leaseBudget,
		w.config.JobTimeout,
		w.config.CompletionGrace,
	) {
		return true, ErrInsufficientLeaseBudget
	}

	jobContext, cancelJob :=
		w.timeoutFactory.WithTimeout(
			ctx,
			w.config.JobTimeout,
		)

	result, err := w.verifier.Verify(
		jobContext,
		lease.Origin,
	)
	jobContextError := jobContext.Err()
	cancelJob()

	if err != nil {
		if contextError := ctx.Err(); contextError != nil {
			return true, contextError
		}

		if jobContextError != nil {
			result = declaration.Result{
				Outcome: declaration.OutcomeUnavailable,
				Origin:  lease.Origin,
			}
		} else {
			return true, fmt.Errorf(
				"worker: verify origin: %w",
				err,
			)
		}
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

	return true, nil
}

// Run processes work until the context is canceled or a fatal operation fails.
//
// Successful work immediately leads to another claim attempt. An idle worker
// waits for PollInterval before polling again.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.validate(ctx, true); err != nil {
		return err
	}

	for {
		worked, err := w.RunOnce(ctx)
		if err != nil {
			return err
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

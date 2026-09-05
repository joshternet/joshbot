package discovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
)

var (
	errRunnerUnavailable = errors.New(
		"discovery: runner unavailable",
	)
	errStoreUnavailable = errors.New(
		"discovery: store unavailable",
	)
	errDiscovererUnavailable = errors.New(
		"discovery: discoverer unavailable",
	)
	errWaiterUnavailable = errors.New(
		"discovery: waiter unavailable",
	)
	errTimeoutFactoryUnavailable = errors.New(
		"discovery: timeout factory unavailable",
	)
	errInvalidConfig = errors.New(
		"discovery: invalid configuration",
	)
)

// Store is the durable behavior needed by Runner.
type Store interface {
	ClaimDiscoverySource(
		context.Context,
		time.Duration,
	) (origin.Origin, bool, error)

	RecordDiscovery(
		context.Context,
		origin.Origin,
		[]Candidate,
	) (RecordResult, error)
}

// Discoverer is the homepage behavior needed by Runner.
type Discoverer interface {
	Discover(
		context.Context,
		origin.Origin,
	) (Result, error)
}

// Config contains discovery scheduling and timeout policy.
type Config struct {
	DiscoveryInterval time.Duration
	PollInterval      time.Duration
	PageTimeout       time.Duration
}

// Report describes one RunOnce attempt.
type Report struct {
	Worked     bool
	Source     origin.Origin
	Status     Status
	Candidates int
	Accepted   int
}

// Runner coordinates durable source claiming, crawling, and persistence.
type Runner struct {
	store          Store
	discoverer     Discoverer
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

// NewRunner constructs a discovery runner.
func NewRunner(
	store Store,
	discoverer Discoverer,
	config Config,
) (*Runner, error) {
	return newRunner(
		store,
		discoverer,
		config,
		timerWaitStrategy{},
		contextTimeoutFactory{},
	)
}

func newRunner(
	store Store,
	discoverer Discoverer,
	config Config,
	waiter waitStrategy,
	timeoutFactory timeoutFactory,
) (*Runner, error) {
	if store == nil {
		return nil, errStoreUnavailable
	}

	if discoverer == nil {
		return nil, errDiscovererUnavailable
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

	return &Runner{
		store:          store,
		discoverer:     discoverer,
		config:         config,
		waiter:         waiter,
		timeoutFactory: timeoutFactory,
	}, nil
}

// RunOnce claims and processes at most one discovery source.
func (r *Runner) RunOnce(
	ctx context.Context,
) (Report, error) {
	if err := r.validate(ctx, false); err != nil {
		return Report{}, err
	}

	source, found, err := r.store.ClaimDiscoverySource(
		ctx,
		r.config.DiscoveryInterval,
	)
	if err != nil {
		return Report{}, fmt.Errorf(
			"discovery: claim source: %w",
			err,
		)
	}

	if !found {
		return Report{}, nil
	}

	report := Report{
		Worked: true,
		Source: source,
	}

	pageContext, cancelPage :=
		r.timeoutFactory.WithTimeout(
			ctx,
			r.config.PageTimeout,
		)

	result, discoverErr := r.discoverer.Discover(
		pageContext,
		source,
	)
	pageContextErr := pageContext.Err()
	cancelPage()

	if parentErr := ctx.Err(); parentErr != nil {
		return report, parentErr
	}

	if discoverErr != nil || pageContextErr != nil {
		report.Status = StatusUnavailable

		return report, nil
	}

	report.Status = result.Status
	report.Candidates = len(result.Candidates)

	if len(result.Candidates) == 0 {
		return report, nil
	}

	recorded, err := r.store.RecordDiscovery(
		ctx,
		source,
		result.Candidates,
	)
	if err != nil {
		return report, fmt.Errorf(
			"discovery: record candidates: %w",
			err,
		)
	}

	report.Accepted = recorded.Accepted

	return report, nil
}

// Run processes due discovery sources until cancellation or a fatal store
// error. It waits only when no source was available.
func (r *Runner) Run(ctx context.Context) error {
	if err := r.validate(ctx, true); err != nil {
		return err
	}

	for {
		report, err := r.RunOnce(ctx)
		if err != nil {
			return err
		}

		if report.Worked {
			continue
		}

		if err := r.waiter.Wait(
			ctx,
			r.config.PollInterval,
		); err != nil {
			return err
		}
	}
}

func (r *Runner) validate(
	ctx context.Context,
	requireWaiter bool,
) error {
	if r == nil {
		return errRunnerUnavailable
	}

	if ctx == nil {
		return errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if r.store == nil {
		return errStoreUnavailable
	}

	if r.discoverer == nil {
		return errDiscovererUnavailable
	}

	if !validConfig(r.config) {
		return errInvalidConfig
	}

	if r.timeoutFactory == nil {
		return errTimeoutFactoryUnavailable
	}

	if requireWaiter && r.waiter == nil {
		return errWaiterUnavailable
	}

	return nil
}

func validConfig(config Config) bool {
	return config.DiscoveryInterval > 0 &&
		config.PollInterval > 0 &&
		config.PageTimeout > 0
}

func (timerWaitStrategy) Wait(
	ctx context.Context,
	duration time.Duration,
) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (contextTimeoutFactory) WithTimeout(
	ctx context.Context,
	duration time.Duration,
) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, duration)
}

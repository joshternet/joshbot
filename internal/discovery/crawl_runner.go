package discovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

var (
	errCrawlRunnerUnavailable = errors.New(
		"discovery: crawl runner unavailable",
	)
	errCrawlSourceStoreUnavailable = errors.New(
		"discovery: crawl source store unavailable",
	)
	errSourceCrawlerUnavailable = errors.New(
		"discovery: source crawler unavailable",
	)
	errCrawlRunnerWaiterUnavailable = errors.New(
		"discovery: crawl runner waiter unavailable",
	)
	errInvalidCrawlRunnerConfig = errors.New(
		"discovery: invalid crawl runner configuration",
	)
)

// CrawlSourceStore claims durable crawl sources without keeping a database
// transaction open during the network crawl.
type CrawlSourceStore interface {
	ClaimDiscoverySource(
		context.Context,
		time.Duration,
	) (origin.Origin, bool, error)
}

// LifecycleObserver receives bounded operational state transitions.
type LifecycleObserver interface {
	Observe(string, origin.Origin, string)
}

type discoveryPauseStore interface {
	DiscoveryPaused(context.Context) (bool, error)
}

type crawlSourceCompletionStore interface {
	CompleteDiscoverySource(
		context.Context,
		origin.Origin,
		retry.Category,
	) error
}

type crawlSourceRetryCompletionStore interface {
	CompleteDiscoverySourceRetry(
		context.Context,
		origin.Origin,
		retry.Category,
		time.Duration,
	) error
}

// SourceCrawler performs one complete bounded crawl of an origin.
type SourceCrawler interface {
	Crawl(
		context.Context,
		origin.Origin,
	) (CrawlResult, error)
}

// CrawlRunnerConfig contains durable source scheduling policy.
type CrawlRunnerConfig struct {
	DiscoveryInterval time.Duration
	PollInterval      time.Duration
	RetryJitter       retry.Jitter
}

// CrawlReport describes one attempt to claim and crawl a source.
type CrawlReport struct {
	Worked               bool
	Source               origin.Origin
	PagesAttempted       int
	PagesParsed          int
	CandidatesDiscovered int
	BudgetExhausted      bool
}

// CrawlRunner coordinates durable source claiming with bounded multi-page
// crawling.
type CrawlRunner struct {
	store       CrawlSourceStore
	crawler     SourceCrawler
	config      CrawlRunnerConfig
	waiter      crawlRunnerWaiter
	retryPolicy retry.Policy
	observer    LifecycleObserver
}

// SetLifecycleObserver attaches process-state observation without changing
// crawl decisions.
func (runner *CrawlRunner) SetLifecycleObserver(observer LifecycleObserver) {
	if runner != nil {
		runner.observer = observer
	}
}

func (runner *CrawlRunner) observe(
	state string,
	source origin.Origin,
	message string,
) {
	if runner.observer != nil {
		runner.observer.Observe(state, source, message)
	}
}

type crawlRunnerWaiter interface {
	Wait(
		context.Context,
		time.Duration,
	) error
}

type timerCrawlRunnerWaiter struct{}

// NewCrawlRunner constructs a multi-page discovery runner.
func NewCrawlRunner(
	store CrawlSourceStore,
	crawler SourceCrawler,
	config CrawlRunnerConfig,
) (*CrawlRunner, error) {
	return newCrawlRunner(
		store,
		crawler,
		config,
		timerCrawlRunnerWaiter{},
	)
}

func newCrawlRunner(
	store CrawlSourceStore,
	crawler SourceCrawler,
	config CrawlRunnerConfig,
	waiter crawlRunnerWaiter,
) (*CrawlRunner, error) {
	if store == nil {
		return nil, errCrawlSourceStoreUnavailable
	}

	if crawler == nil {
		return nil, errSourceCrawlerUnavailable
	}

	if !validCrawlRunnerConfig(config) {
		return nil, errInvalidCrawlRunnerConfig
	}

	if waiter == nil {
		return nil, errCrawlRunnerWaiterUnavailable
	}

	return &CrawlRunner{
		store:       store,
		crawler:     crawler,
		config:      config,
		waiter:      waiter,
		retryPolicy: retry.NewPolicy(config.RetryJitter),
	}, nil
}

// RunOnce claims and processes at most one crawl source.
//
// A successful claim represents one source crawl attempt, regardless of how
// many same-origin pages the configured crawler fetches.
func (runner *CrawlRunner) RunOnce(
	ctx context.Context,
) (CrawlReport, error) {
	if err := runner.validate(ctx, false); err != nil {
		return CrawlReport{}, err
	}

	source, found, err :=
		runner.store.ClaimDiscoverySource(
			ctx,
			runner.config.DiscoveryInterval,
		)
	if err != nil {
		runner.observe("failed", origin.Origin{}, "claim_failed")
		return CrawlReport{}, fmt.Errorf(
			"discovery: claim crawl source: %w",
			err,
		)
	}

	if !found {
		state := "idle"
		if store, ok := runner.store.(discoveryPauseStore); ok {
			paused, pauseErr := store.DiscoveryPaused(ctx)
			if pauseErr != nil {
				runner.observe("failed", origin.Origin{}, "control_read_failed")
				return CrawlReport{}, pauseErr
			}
			if paused {
				state = "paused"
			}
		}
		runner.observe(state, origin.Origin{}, "")
		return CrawlReport{}, nil
	}
	runner.observe("running", source, "")

	report := CrawlReport{
		Worked: true,
		Source: source,
	}

	result, err := runner.crawler.Crawl(
		ctx,
		source,
	)

	if parentErr := ctx.Err(); parentErr != nil {
		return report, parentErr
	}

	if err != nil {
		category := result.FailureCategory
		retryAfter := result.RetryAfter
		if result.PagesParsed > 0 {
			category = retry.CategoryNone
			retryAfter = 0
		} else if category == retry.CategoryNone {
			category = retry.CategoryProcessor
		}
		if completionErr := runner.completeSource(
			ctx, source, category, retryAfter,
		); completionErr != nil {
			runner.observe("failed", source, "completion_failed")
			return report, fmt.Errorf(
				"discovery: complete failed crawl source: %w",
				completionErr,
			)
		}
		runner.observe("failed", source, "crawl_failed")
		return report, fmt.Errorf(
			"discovery: crawl source: %w",
			err,
		)
	}

	report.PagesAttempted = result.PagesAttempted
	report.PagesParsed = result.PagesParsed
	report.CandidatesDiscovered =
		result.CandidatesDiscovered
	report.BudgetExhausted = result.BudgetExhausted
	if err := runner.completeSource(
		ctx,
		source,
		result.FailureCategory,
		result.RetryAfter,
	); err != nil {
		runner.observe("failed", source, "completion_failed")
		return report, fmt.Errorf(
			"discovery: complete crawl source: %w",
			err,
		)
	}
	runner.observe("idle", origin.Origin{}, "")

	return report, nil
}

func (runner *CrawlRunner) completeSource(
	ctx context.Context,
	source origin.Origin,
	category retry.Category,
	retryAfter time.Duration,
) error {
	if store, ok := runner.store.(crawlSourceRetryCompletionStore); ok {
		return store.CompleteDiscoverySourceRetry(
			ctx, source, category, retryAfter,
		)
	}
	if store, ok := runner.store.(crawlSourceCompletionStore); ok {
		return store.CompleteDiscoverySource(ctx, source, category)
	}
	return nil
}

// Run processes due crawl sources until cancellation or a fatal store,
// persistence, or crawler error. It waits only when no source was available.
func (runner *CrawlRunner) Run(
	ctx context.Context,
) error {
	if err := runner.validate(ctx, true); err != nil {
		return err
	}

	consecutiveFailures := 0
	for {
		report, err := runner.RunOnce(ctx)
		if err != nil {
			if contextError := ctx.Err(); contextError != nil {
				return contextError
			}
			consecutiveFailures++
			if waitErr := runner.waiter.Wait(
				ctx,
				runner.retryPolicy.Delay(consecutiveFailures, 0),
			); waitErr != nil {
				return waitErr
			}
			continue
		}
		consecutiveFailures = 0

		if report.Worked {
			continue
		}

		if err := runner.waiter.Wait(
			ctx,
			runner.config.PollInterval,
		); err != nil {
			return err
		}
	}
}

func (runner *CrawlRunner) validate(
	ctx context.Context,
	requireWaiter bool,
) error {
	if runner == nil {
		return errCrawlRunnerUnavailable
	}

	if ctx == nil {
		return errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if runner.store == nil {
		return errCrawlSourceStoreUnavailable
	}

	if runner.crawler == nil {
		return errSourceCrawlerUnavailable
	}

	if !validCrawlRunnerConfig(runner.config) {
		return errInvalidCrawlRunnerConfig
	}

	if requireWaiter && runner.waiter == nil {
		return errCrawlRunnerWaiterUnavailable
	}

	return nil
}

func validCrawlRunnerConfig(
	config CrawlRunnerConfig,
) bool {
	return config.DiscoveryInterval > 0 &&
		config.PollInterval > 0
}

func (timerCrawlRunnerWaiter) Wait(
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

package discovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

const defaultCrawlSourceLeaseDuration = 5 * time.Minute

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

// CrawlSourceStore leases durable crawl sources without keeping a database
// transaction open during the network crawl.
//
// Claim advances lease_generation and sets a renewable expiration. It does not
// touch last_attempted_at. Renew extends the active expiration for the same
// generation. Complete records last_attempted_at and clears the lease. Renew
// and complete both require a matching generation and an unexpired lease so a
// stale claimant cannot overwrite a newer claim.
type CrawlSourceStore interface {
	ClaimDiscoverySourceLease(
		context.Context,
		time.Duration,
		time.Duration,
	) (CrawlSourceLease, bool, error)

	RenewDiscoverySourceLease(
		context.Context,
		CrawlSourceLease,
		time.Duration,
	) (CrawlSourceLease, error)

	CompleteDiscoverySourceLeaseRetry(
		context.Context,
		CrawlSourceLease,
		retry.Category,
		time.Duration,
	) error
}

// LifecycleObserver receives bounded operational state transitions.
type LifecycleObserver interface {
	Observe(string, origin.Origin, string)
}

type discoveryPauseStore interface {
	DiscoveryPaused(context.Context) (bool, error)
}

// SourceCrawler performs one complete bounded crawl of an origin.
type SourceCrawler interface {
	Crawl(
		context.Context,
		origin.Origin,
	) (CrawlResult, error)
}

// CrawlRunnerConfig contains durable source scheduling policy.
//
// A zero LeaseDuration uses the bounded default. A positive value overrides
// that default.
type CrawlRunnerConfig struct {
	DiscoveryInterval time.Duration
	PollInterval      time.Duration
	LeaseDuration     time.Duration
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
//
// Durable per-source retry delays are owned by lease completion. Run paces
// processor and infrastructure failures with PollInterval and continues
// immediately after claimed-work failures so unrelated due sources are not
// parked.
type CrawlRunner struct {
	store    CrawlSourceStore
	crawler  SourceCrawler
	config   CrawlRunnerConfig
	waiter   crawlRunnerWaiter
	observer LifecycleObserver
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

type crawlLeaseRenewalResult struct {
	lease CrawlSourceLease
	err   error
}

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
		store:   store,
		crawler: crawler,
		config:  config,
		waiter:  waiter,
	}, nil
}

// RunOnce claims and processes at most one crawl source using renewable,
// generation-fenced lease authority.
func (runner *CrawlRunner) RunOnce(
	ctx context.Context,
) (CrawlReport, error) {
	if err := runner.validate(ctx, false); err != nil {
		return CrawlReport{}, err
	}

	return runner.runOnceWithLease(
		ctx,
		runner.store,
	)
}

func (runner *CrawlRunner) runOnceWithLease(
	ctx context.Context,
	store CrawlSourceStore,
) (CrawlReport, error) {
	lease, found, err :=
		store.ClaimDiscoverySourceLease(
			ctx,
			runner.config.DiscoveryInterval,
			runner.config.leaseDuration(),
		)
	if err != nil {
		runner.observe(
			"failed",
			origin.Origin{},
			"claim_failed",
		)
		return CrawlReport{}, fmt.Errorf(
			"discovery: claim crawl source: %w",
			err,
		)
	}

	if !found {
		return runner.noSource(ctx)
	}

	source := lease.Origin
	runner.observe("running", source, "")

	report := CrawlReport{
		Worked: true,
		Source: source,
	}

	result, crawlErr, currentLease, renewalErr :=
		runner.crawlWithLease(
			ctx,
			store,
			lease,
		)

	if parentErr := ctx.Err(); parentErr != nil {
		return report, parentErr
	}

	if renewalErr != nil {
		runner.observe(
			"failed",
			source,
			"lease_renewal_failed",
		)
		return report, fmt.Errorf(
			"discovery: renew crawl source lease: %w",
			renewalErr,
		)
	}

	if crawlErr != nil {
		category := result.FailureCategory
		retryAfter := result.RetryAfter

		if result.PagesParsed > 0 {
			category = retry.CategoryNone
			retryAfter = 0
		} else if category == retry.CategoryNone {
			category = retry.CategoryProcessor
		}

		if completionErr :=
			store.CompleteDiscoverySourceLeaseRetry(
				ctx,
				currentLease,
				category,
				retryAfter,
			); completionErr != nil {
			runner.observe(
				"failed",
				source,
				"completion_failed",
			)
			return report, fmt.Errorf(
				"discovery: complete failed crawl source: %w",
				completionErr,
			)
		}

		runner.observe(
			"failed",
			source,
			"crawl_failed",
		)
		return report, fmt.Errorf(
			"discovery: crawl source: %w",
			crawlErr,
		)
	}

	report.PagesAttempted =
		result.PagesAttempted
	report.PagesParsed =
		result.PagesParsed
	report.CandidatesDiscovered =
		result.CandidatesDiscovered
	report.BudgetExhausted =
		result.BudgetExhausted

	if err := store.CompleteDiscoverySourceLeaseRetry(
		ctx,
		currentLease,
		result.FailureCategory,
		result.RetryAfter,
	); err != nil {
		runner.observe(
			"failed",
			source,
			"completion_failed",
		)
		return report, fmt.Errorf(
			"discovery: complete crawl source: %w",
			err,
		)
	}

	runner.observe(
		"idle",
		origin.Origin{},
		"",
	)

	return report, nil
}

func (runner *CrawlRunner) crawlWithLease(
	ctx context.Context,
	store CrawlSourceStore,
	lease CrawlSourceLease,
) (
	CrawlResult,
	error,
	CrawlSourceLease,
	error,
) {
	crawlCtx, cancelCrawl :=
		context.WithCancel(ctx)
	defer cancelCrawl()

	renewCtx, cancelRenew :=
		context.WithCancel(ctx)

	renewalResults := make(
		chan crawlLeaseRenewalResult,
		1,
	)

	go runner.renewCrawlSourceLease(
		renewCtx,
		cancelCrawl,
		store,
		lease,
		runner.config.leaseDuration(),
		renewalResults,
	)

	result, crawlErr := runner.crawler.Crawl(
		crawlCtx,
		lease.Origin,
	)

	cancelRenew()

	renewal := <-renewalResults

	return result,
		crawlErr,
		renewal.lease,
		renewal.err
}

func (runner *CrawlRunner) renewCrawlSourceLease(
	ctx context.Context,
	cancelCrawl context.CancelFunc,
	store CrawlSourceStore,
	lease CrawlSourceLease,
	leaseDuration time.Duration,
	results chan<- crawlLeaseRenewalResult,
) {
	renewInterval :=
		discoveryLeaseRenewInterval(
			leaseDuration,
		)

	timer := time.NewTimer(renewInterval)
	defer timer.Stop()

	current := lease

	for {
		select {
		case <-ctx.Done():
			results <- crawlLeaseRenewalResult{
				lease: current,
			}
			return

		case <-timer.C:
			renewed, err :=
				store.RenewDiscoverySourceLease(
					ctx,
					current,
					leaseDuration,
				)
			if err != nil {
				if ctx.Err() != nil {
					results <- crawlLeaseRenewalResult{
						lease: current,
					}
					return
				}

				cancelCrawl()

				results <- crawlLeaseRenewalResult{
					lease: current,
					err:   err,
				}
				return
			}

			current = renewed
			timer.Reset(renewInterval)
		}
	}
}

func (runner *CrawlRunner) noSource(
	ctx context.Context,
) (CrawlReport, error) {
	state := "idle"

	if store, ok :=
		runner.store.(discoveryPauseStore); ok {
		paused, err :=
			store.DiscoveryPaused(ctx)
		if err != nil {
			runner.observe(
				"failed",
				origin.Origin{},
				"control_read_failed",
			)
			return CrawlReport{}, err
		}

		if paused {
			state = "paused"
		}
	}

	runner.observe(
		state,
		origin.Origin{},
		"",
	)

	return CrawlReport{}, nil
}

// Run processes due crawl sources until cancellation.
//
// Successful or claimed-work failures immediately lead to another claim
// attempt. That immediate continue after claimed-work failure is intentional so
// one source cannot park discovery on the durable source retry schedule. Idle
// polls and processor failures before a claim wait for PollInterval. Durable
// source backoff is applied only by lease completion.
func (runner *CrawlRunner) Run(
	ctx context.Context,
) error {
	if err := runner.validate(ctx, true); err != nil {
		return err
	}

	for {
		report, err := runner.RunOnce(ctx)
		if err != nil {
			if contextError :=
				ctx.Err(); contextError != nil {
				return contextError
			}

			if report.Worked {
				continue
			}

			if waitErr := runner.waiter.Wait(
				ctx,
				runner.config.PollInterval,
			); waitErr != nil {
				return waitErr
			}

			continue
		}

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

	if requireWaiter &&
		runner.waiter == nil {
		return errCrawlRunnerWaiterUnavailable
	}

	return nil
}

func validCrawlRunnerConfig(
	config CrawlRunnerConfig,
) bool {
	return config.DiscoveryInterval > 0 &&
		config.PollInterval > 0 &&
		config.LeaseDuration >= 0
}

func (config CrawlRunnerConfig) leaseDuration() time.Duration {
	if config.LeaseDuration > 0 {
		return config.LeaseDuration
	}

	return defaultCrawlSourceLeaseDuration
}

func discoveryLeaseRenewInterval(
	leaseDuration time.Duration,
) time.Duration {
	interval := leaseDuration / 2

	if interval <= 0 {
		return leaseDuration
	}

	return interval
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

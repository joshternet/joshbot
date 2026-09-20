package robots

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/joshternet/joshbot/internal/netguard"
	"github.com/joshternet/joshbot/internal/origin"
)

const maxCacheLifetime = 24 * time.Hour

// ErrDisallowed reports that the current robots policy denies the target.
var ErrDisallowed = errors.New("robots: target disallowed")

// ErrTemporary reports a failure while obtaining robots policy.
var ErrTemporary = errors.New("robots: policy temporarily unavailable")

var (
	errCheckerUnavailable = errors.New(
		"robots: checker unavailable",
	)
	errClockUnavailable = errors.New(
		"robots: clock unavailable",
	)
)

type cacheEntry struct {
	policy    Policy
	expiresAt time.Time
}

// Checker obtains, caches, evaluates, and schedules robots-aware requests.
type Checker struct {
	mu        sync.Mutex
	getter    hopGetter
	now       func() time.Time
	cache     map[origin.Origin]cacheEntry
	scheduler *originRequestScheduler
}

// NewChecker constructs a checker using guarded network access.
//
// The default constructor does not impose an operator minimum request delay.
// Production runtimes that have JOSHBOT_CRAWL_REQUEST_DELAY configured should
// use NewCheckerWithRequestDelay.
func NewChecker(
	resolver netguard.Resolver,
	dialer netguard.Dialer,
) *Checker {
	return NewCheckerWithRequestDelay(
		resolver,
		dialer,
		0,
	)
}

// NewCheckerWithRequestDelay constructs a checker using guarded network access
// and a minimum per-origin delay between outbound requests.
//
// An applicable robots Crawl-delay can increase this minimum but never reduce
// it.
//
// This constructor leaves Web Bot Auth disabled. Production crawler runtimes
// with a configured signing identity should use
// NewCheckerWithRequestDelayAndSigner.
func NewCheckerWithRequestDelay(
	resolver netguard.Resolver,
	dialer netguard.Dialer,
	requestDelay time.Duration,
) *Checker {
	return NewCheckerWithRequestDelayAndSigner(
		resolver,
		dialer,
		requestDelay,
		nil,
	)
}

// NewCheckerWithRequestDelayAndSigner constructs the shared JoshBot outbound
// HTTP boundary.
//
// Every robots fetch and robots-authorized target request uses the same guarded
// network access, stable User-Agent, per-origin scheduler, and optional request
// signer. Redirect hops are performed as new requests through this same
// boundary, so each hop receives a fresh signature for its actual authority.
func NewCheckerWithRequestDelayAndSigner(
	resolver netguard.Resolver,
	dialer netguard.Dialer,
	requestDelay time.Duration,
	signer RequestSigner,
) *Checker {
	return newCheckerWithRequestDelay(
		&guardedHTTP{
			resolver: resolver,
			dialer:   dialer,
			signer:   signer,
		},
		time.Now,
		requestDelay,
		timerRequestDelayWaiter{},
	)
}

func newChecker(
	getter hopGetter,
	now func() time.Time,
) *Checker {
	return newCheckerWithRequestDelay(
		getter,
		now,
		0,
		timerRequestDelayWaiter{},
	)
}

func newCheckerWithRequestDelay(
	getter hopGetter,
	now func() time.Time,
	requestDelay time.Duration,
	waiter requestDelayWaiter,
) *Checker {
	scheduler := newOriginRequestScheduler(
		now,
		waiter,
		requestDelay,
	)

	scheduledGetter := getter
	if getter != nil {
		scheduledGetter = scheduledHopGetter{
			getter:    getter,
			scheduler: scheduler,
		}
	}

	return &Checker{
		getter:    scheduledGetter,
		now:       now,
		cache:     make(map[origin.Origin]cacheEntry),
		scheduler: scheduler,
	}
}

// Allowed reports whether the current robots policy permits target.
//
// Policy acquisition failures return false with an error.
func (c *Checker) Allowed(
	ctx context.Context,
	target *url.URL,
) (bool, error) {
	if c == nil {
		return false, errCheckerUnavailable
	}

	if ctx == nil {
		return false, errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return false, err
	}

	if target == nil {
		return false, errInvalidHTTPTarget
	}

	initial, err := origin.Parse(target.String())
	if err != nil {
		return false, errInvalidHTTPTarget
	}

	if c.getter == nil {
		return false, errHTTPGetterUnavailable
	}

	if c.now == nil {
		return false, errClockUnavailable
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if entry, exists := c.cache[initial]; exists {
		if now.Before(entry.expiresAt) {
			return entry.policy.Allowed(target), nil
		}

		delete(c.cache, initial)
	}

	policy, err := obtainPolicy(ctx, initial, c.getter)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrTemporary, err)
	}

	crawlDelay, err := policy.CrawlDelay()
	if err != nil {
		return false, fmt.Errorf(
			"%w: %w",
			ErrTemporary,
			err,
		)
	}

	c.scheduler.setCrawlDelay(
		initial,
		crawlDelay,
	)

	c.cache[initial] = cacheEntry{
		policy:    policy,
		expiresAt: now.Add(maxCacheLifetime),
	}

	return policy.Allowed(target), nil
}

// Get requests target only when the current robots policy permits it.
//
// Robots acquisition and the protected request use the same guarded,
// per-origin scheduled HTTP implementation and JoshBot User-Agent.
func (c *Checker) Get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	allowed, err := c.Allowed(ctx, target)
	if err != nil {
		return nil, err
	}

	if !allowed {
		return nil, ErrDisallowed
	}

	response, err := c.getter.get(ctx, target)
	if err != nil {
		return nil, fmt.Errorf(
			"robots: fetch protected target: %w",
			err,
		)
	}

	if response == nil || response.Body == nil {
		return nil, errInvalidHTTPResponse
	}

	return response, nil
}

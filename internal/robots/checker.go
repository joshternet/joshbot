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

// maxCacheEntries bounds the robots policy cache. Tests may lower it.
var maxCacheEntries = 1024

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
	policy     Policy
	crawlDelay time.Duration
	expiresAt  time.Time
	lastAccess time.Time
}

type inflightPolicy struct {
	done   chan struct{}
	policy Policy
	err    error
}

// Checker obtains, caches, evaluates, and schedules robots-aware requests.
type Checker struct {
	mu            sync.Mutex
	getter        hopGetter
	now           func() time.Time
	cache         map[origin.Origin]cacheEntry
	inflight      map[origin.Origin]*inflightPolicy
	scheduler     *originRequestScheduler
	maxCrawlDelay time.Duration
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
	return newCheckerWithHTTPConfig(resolver, dialer, requestDelay, signer, DefaultHTTPConfig())
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
		maxCrawlDelay: DefaultMaxCrawlDelay,
		getter:        scheduledGetter,
		now:           now,
		cache:         make(map[origin.Origin]cacheEntry),
		inflight:      make(map[origin.Origin]*inflightPolicy),
		scheduler:     scheduler,
	}
}

// Allowed reports whether the current robots policy permits target.
//
// Policy acquisition failures return false with an error.
func (c *Checker) Allowed(
	ctx context.Context,
	target *url.URL,
) (bool, error) {
	initial, err := c.requestOrigin(ctx, target)
	if err != nil {
		return false, err
	}
	policy, err := c.policyFor(ctx, initial)
	if err != nil {
		return false, err
	}
	return policy.Allowed(target), nil
}

func (c *Checker) requestOrigin(ctx context.Context, target *url.URL) (origin.Origin, error) {
	if c == nil {
		return origin.Origin{}, errCheckerUnavailable
	}

	if ctx == nil {
		return origin.Origin{}, errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return origin.Origin{}, err
	}

	if target == nil {
		return origin.Origin{}, errInvalidHTTPTarget
	}

	initial, err := origin.Parse(target.String())
	if err != nil {
		return origin.Origin{}, errInvalidHTTPTarget
	}

	if c.getter == nil {
		return origin.Origin{}, errHTTPGetterUnavailable
	}

	if c.now == nil {
		return origin.Origin{}, errClockUnavailable
	}
	return initial, nil
}

func (c *Checker) policyFor(
	ctx context.Context,
	initial origin.Origin,
) (Policy, error) {
	schedule, err := c.scheduler.acquire(initial)
	if err != nil {
		return Policy{}, fmt.Errorf("%w: %w", ErrTemporary, err)
	}
	defer c.scheduler.release(schedule)
	c.mu.Lock()
	now := c.now()
	c.evictExpiredLocked(now)

	// Expired entries are removed by evictExpiredLocked above, so a hit here
	// is always still within its lifetime.
	if entry, exists := c.cache[initial]; exists && now.Before(entry.expiresAt) {
		entry.lastAccess = now
		c.cache[initial] = entry
		c.mu.Unlock()
		schedule.setCrawlDelay(entry.crawlDelay)
		return entry.policy, nil
	}

	if pending, exists := c.inflight[initial]; exists {
		c.mu.Unlock()
		select {
		case <-pending.done:
			return pending.policy, pending.err
		case <-ctx.Done():
			return Policy{}, ctx.Err()
		}
	}

	pending := &inflightPolicy{
		done: make(chan struct{}),
	}
	c.inflight[initial] = pending
	c.mu.Unlock()

	policy, err := obtainPolicy(ctx, initial, c.getter)
	if err != nil {
		err = fmt.Errorf("%w: %w", ErrTemporary, err)
		pending.err = err
		close(pending.done)

		c.mu.Lock()
		delete(c.inflight, initial)
		c.mu.Unlock()
		return Policy{}, err
	}

	crawlDelay, err := policy.crawlDelay(c.maxCrawlDelay)
	if err != nil {
		err = fmt.Errorf("%w: %w", ErrTemporary, err)
		pending.err = err
		close(pending.done)

		c.mu.Lock()
		delete(c.inflight, initial)
		c.mu.Unlock()
		return Policy{}, err
	}

	schedule.setCrawlDelay(crawlDelay)

	c.mu.Lock()
	delete(c.inflight, initial)
	now = c.now()
	c.evictExpiredLocked(now)
	c.evictOverflowLocked(now)
	c.cache[initial] = cacheEntry{
		policy:     policy,
		crawlDelay: crawlDelay,
		expiresAt:  now.Add(maxCacheLifetime),
		lastAccess: now,
	}
	pending.policy = policy
	close(pending.done)
	c.mu.Unlock()

	return policy, nil
}

func (c *Checker) evictExpiredLocked(now time.Time) {
	for key, entry := range c.cache {
		if !now.Before(entry.expiresAt) {
			delete(c.cache, key)
		}
	}
}

func (c *Checker) evictOverflowLocked(now time.Time) {
	for len(c.cache) >= maxCacheEntries {
		var (
			oldestKey    origin.Origin
			oldestAccess time.Time
			found        bool
		)

		for key, entry := range c.cache {
			if !found || entry.lastAccess.Before(oldestAccess) {
				oldestKey = key
				oldestAccess = entry.lastAccess
				found = true
			}
		}

		if !found {
			return
		}

		delete(c.cache, oldestKey)
		_ = now
	}
}

// Get requests target only when the current robots policy permits it.
//
// Robots acquisition and the protected request use the same guarded,
// per-origin scheduled HTTP implementation and JoshBot User-Agent.
func (c *Checker) Get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	initial, err := c.requestOrigin(ctx, target)
	if err != nil {
		return nil, err
	}
	// Preserve the schedule through policy lookup, waiting, and the request.
	// Eviction between these steps would lose the origin's learned delay.
	schedule, err := c.scheduler.acquire(initial)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTemporary, err)
	}
	defer c.scheduler.release(schedule)

	policy, err := c.policyFor(ctx, initial)
	if err != nil {
		return nil, err
	}
	if !policy.Allowed(target) {
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

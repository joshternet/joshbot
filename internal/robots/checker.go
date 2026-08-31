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

// Checker obtains, caches, and evaluates robots policies.
type Checker struct {
	mu     sync.Mutex
	getter hopGetter
	now    func() time.Time
	cache  map[origin.Origin]cacheEntry
}

// NewChecker constructs a checker using guarded network access.
func NewChecker(
	resolver netguard.Resolver,
	dialer netguard.Dialer,
) *Checker {
	return newChecker(
		&guardedHTTP{
			resolver: resolver,
			dialer:   dialer,
		},
		time.Now,
	)
}

func newChecker(
	getter hopGetter,
	now func() time.Time,
) *Checker {
	return &Checker{
		getter: getter,
		now:    now,
		cache:  make(map[origin.Origin]cacheEntry),
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
		return false, err
	}

	c.cache[initial] = cacheEntry{
		policy:    policy,
		expiresAt: now.Add(maxCacheLifetime),
	}

	return policy.Allowed(target), nil
}

// Get requests target only when the current robots policy permits it.
//
// Robots acquisition and the protected request use the same guarded HTTP
// implementation and JoshBot User-Agent.
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

package robots

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
)

const maxScheduleEntries = 1024

var errScheduleCapacity = errors.New("robots: request schedule capacity reached")

var errInvalidRequestDelay = errors.New(
	"robots: invalid minimum request delay",
)

type requestDelayWaiter interface {
	Wait(
		context.Context,
		time.Duration,
	) error
}

type timerRequestDelayWaiter struct{}

type originRequestScheduler struct {
	mu        sync.Mutex
	now       func() time.Time
	waiter    requestDelayWaiter
	minimum   time.Duration
	capacity  int
	schedules map[origin.Origin]*originRequestSchedule
}

type originRequestSchedule struct {
	mu         sync.Mutex
	crawlDelay time.Duration
	lastStart  time.Time
	lastAccess time.Time
	users      int
}

func newOriginRequestScheduler(
	now func() time.Time,
	waiter requestDelayWaiter,
	minimum time.Duration,
) *originRequestScheduler {
	return &originRequestScheduler{
		now:       now,
		waiter:    waiter,
		minimum:   minimum,
		capacity:  maxScheduleEntries,
		schedules: make(map[origin.Origin]*originRequestSchedule),
	}
}

// acquire pins an origin while policy lookup, a wait, or a request uses it.
// Only idle entries whose spacing has elapsed may be evicted.
func (scheduler *originRequestScheduler) acquire(source origin.Origin) (*originRequestSchedule, error) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	now := scheduler.now()
	var oldest *originRequestSchedule
	var oldestOrigin origin.Origin
	for key, entry := range scheduler.schedules {
		if entry.users != 0 {
			continue
		}
		entry.mu.Lock()
		delay := max(scheduler.minimum, entry.crawlDelay)
		eligible := !now.Before(entry.lastStart.Add(delay))
		entry.mu.Unlock()
		if !eligible {
			continue
		}
		if !now.Before(entry.lastAccess.Add(maxCacheLifetime)) {
			delete(scheduler.schedules, key)
			continue
		}
		if oldest == nil || entry.lastAccess.Before(oldest.lastAccess) ||
			(entry.lastAccess.Equal(oldest.lastAccess) && key.String() < oldestOrigin.String()) {
			oldest, oldestOrigin = entry, key
		}
	}
	schedule := scheduler.schedules[source]
	if schedule == nil {
		if len(scheduler.schedules) >= scheduler.capacity {
			if oldest == nil {
				return nil, errScheduleCapacity
			}
			delete(scheduler.schedules, oldestOrigin)
		}
		schedule = &originRequestSchedule{}
		scheduler.schedules[source] = schedule
	}
	schedule.users++
	schedule.lastAccess = now
	return schedule, nil
}

func (scheduler *originRequestScheduler) release(schedule *originRequestSchedule) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	schedule.users--
	schedule.lastAccess = scheduler.now()
}

func (schedule *originRequestSchedule) setCrawlDelay(delay time.Duration) {
	schedule.mu.Lock()
	defer schedule.mu.Unlock()
	schedule.crawlDelay = delay
}

func (scheduler *originRequestScheduler) acquireTarget(target *url.URL) (*originRequestSchedule, error) {
	if target == nil {
		return nil, errInvalidHTTPTarget
	}

	targetOrigin, err := origin.Parse(
		target.String(),
	)
	if err != nil {
		return nil, errInvalidHTTPTarget
	}

	if scheduler.minimum < 0 {
		return nil, errInvalidRequestDelay
	}

	return scheduler.acquire(targetOrigin)
}

func (scheduler *originRequestScheduler) wait(
	ctx context.Context,
	target *url.URL,
) error {
	schedule, err := scheduler.acquireTarget(target)
	if err != nil {
		return err
	}
	defer scheduler.release(schedule)

	for {
		schedule.mu.Lock()

		effectiveDelay := scheduler.minimum
		if schedule.crawlDelay > effectiveDelay {
			effectiveDelay = schedule.crawlDelay
		}

		var wait time.Duration
		now := scheduler.now()
		if !schedule.lastStart.IsZero() {
			earliestStart := schedule.lastStart.Add(
				effectiveDelay,
			)
			if earliestStart.After(now) {
				wait = earliestStart.Sub(now)
			}
		}

		if wait > 0 {
			// Release while sleeping so other origins are not blocked.
			schedule.mu.Unlock()
			if err := scheduler.waiter.Wait(ctx, wait); err != nil {
				return err
			}
			continue
		}

		schedule.lastStart = now
		schedule.mu.Unlock()
		return nil
	}
}

type scheduledHopGetter struct {
	getter    hopGetter
	scheduler *originRequestScheduler
}

func (getter scheduledHopGetter) get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	// Robots redirects also need to retain their own destination schedule
	// while awaiting response headers.
	schedule, err := getter.scheduler.acquireTarget(target)
	if err != nil {
		return nil, err
	}
	defer getter.scheduler.release(schedule)

	if err := getter.scheduler.wait(
		ctx,
		target,
	); err != nil {
		return nil, err
	}

	return getter.getter.get(
		ctx,
		target,
	)
}

func (timerRequestDelayWaiter) Wait(
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

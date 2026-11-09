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
	mu            sync.Mutex
	now           func() time.Time
	waiter        requestDelayWaiter
	minimum       time.Duration
	capacity      int
	schedules     map[origin.Origin]*originRequestSchedule
	neighborhoods map[string]*originRequestSchedule
}

type originRequestSchedule struct {
	mu         sync.Mutex
	crawlDelay time.Duration
	lastStart  time.Time
	lastAccess time.Time
	users      int
}

// pinnedSchedules holds the origin clock and, when the host has a
// neighborhood, the shared operator-minimum clock.
type pinnedSchedules struct {
	origin       *originRequestSchedule
	neighborhood *originRequestSchedule
}

func newOriginRequestScheduler(
	now func() time.Time,
	waiter requestDelayWaiter,
	minimum time.Duration,
) *originRequestScheduler {
	return &originRequestScheduler{
		now:           now,
		waiter:        waiter,
		minimum:       minimum,
		capacity:      maxScheduleEntries,
		schedules:     make(map[origin.Origin]*originRequestSchedule),
		neighborhoods: make(map[string]*originRequestSchedule),
	}
}

// acquire pins an origin while policy lookup, a wait, or a request uses it.
// Only idle entries whose spacing has elapsed may be evicted.
func (scheduler *originRequestScheduler) acquire(source origin.Origin) (*originRequestSchedule, error) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return pin(scheduler, scheduler.schedules, source, scheduler.now(), lessOrigin)
}

// lessOrigin reports whether left sorts before right when eviction breaks a tie.
func lessOrigin(left, right origin.Origin) bool {
	return left.String() < right.String()
}

// pin returns the schedule for key, creating it when absent.
//
// The caller must hold scheduler.mu. Only an idle entry whose spacing has
// elapsed can be evicted. Neighborhood entries leave crawlDelay at zero, so
// their spacing is the operator minimum.
func pin[Key comparable](
	scheduler *originRequestScheduler,
	entries map[Key]*originRequestSchedule,
	key Key,
	now time.Time,
	less func(left, right Key) bool,
) (*originRequestSchedule, error) {
	var oldest *originRequestSchedule
	var oldestKey Key
	for candidate, entry := range entries {
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
			delete(entries, candidate)
			continue
		}
		if oldest == nil || entry.lastAccess.Before(oldest.lastAccess) ||
			(entry.lastAccess.Equal(oldest.lastAccess) && less(candidate, oldestKey)) {
			oldest, oldestKey = entry, candidate
		}
	}
	schedule := entries[key]
	if schedule == nil {
		if len(entries) >= scheduler.capacity {
			if oldest == nil {
				return nil, errScheduleCapacity
			}
			delete(entries, oldestKey)
		}
		schedule = &originRequestSchedule{}
		entries[key] = schedule
	}
	schedule.users++
	schedule.lastAccess = now
	return schedule, nil
}

func (scheduler *originRequestScheduler) release(schedule *originRequestSchedule) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	scheduler.unpin(schedule, scheduler.now())
}

// unpin drops one pin. The caller must hold scheduler.mu.
func (scheduler *originRequestScheduler) unpin(schedule *originRequestSchedule, accessed time.Time) {
	schedule.users--
	schedule.lastAccess = accessed
}

func (scheduler *originRequestScheduler) releasePinned(pinned pinnedSchedules) {
	scheduler.release(pinned.origin)
	if pinned.neighborhood != nil {
		scheduler.release(pinned.neighborhood)
	}
}

func (schedule *originRequestSchedule) setCrawlDelay(delay time.Duration) {
	schedule.mu.Lock()
	defer schedule.mu.Unlock()
	schedule.crawlDelay = delay
}

func (scheduler *originRequestScheduler) acquireTarget(target *url.URL) (pinnedSchedules, error) {
	if target == nil {
		return pinnedSchedules{}, errInvalidHTTPTarget
	}

	targetOrigin, err := origin.Parse(target.String())
	if err != nil {
		return pinnedSchedules{}, errInvalidHTTPTarget
	}

	if scheduler.minimum < 0 {
		return pinnedSchedules{}, errInvalidRequestDelay
	}

	key, grouped := origin.NeighborhoodKey(targetOrigin.Hostname())
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	now := scheduler.now()
	originSchedule, err := pin(scheduler, scheduler.schedules, targetOrigin, now, lessOrigin)
	if err != nil {
		return pinnedSchedules{}, err
	}
	if !grouped {
		return pinnedSchedules{origin: originSchedule}, nil
	}

	group, err := pin(scheduler, scheduler.neighborhoods, key, now, func(left, right string) bool {
		return left < right
	})
	if err != nil {
		scheduler.unpin(originSchedule, now)
		return pinnedSchedules{}, err
	}
	return pinnedSchedules{origin: originSchedule, neighborhood: group}, nil
}

func (scheduler *originRequestScheduler) wait(
	ctx context.Context,
	target *url.URL,
) error {
	pinned, err := scheduler.acquireTarget(target)
	if err != nil {
		return err
	}
	defer scheduler.releasePinned(pinned)

	for {
		// The origin mutex is always acquired before the neighborhood mutex.
		pinned.origin.mu.Lock()
		if pinned.neighborhood != nil {
			pinned.neighborhood.mu.Lock()
		}

		originDelay := scheduler.minimum
		if pinned.origin.crawlDelay > originDelay {
			originDelay = pinned.origin.crawlDelay
		}
		now := scheduler.now()
		wait := remainingDelay(pinned.origin.lastStart, originDelay, now)
		if pinned.neighborhood != nil {
			groupWait := remainingDelay(pinned.neighborhood.lastStart, scheduler.minimum, now)
			if groupWait > wait {
				wait = groupWait
			}
		}

		if wait > 0 {
			if pinned.neighborhood != nil {
				pinned.neighborhood.mu.Unlock()
			}
			pinned.origin.mu.Unlock()
			if err := scheduler.waiter.Wait(ctx, wait); err != nil {
				return err
			}
			continue
		}

		pinned.origin.lastStart = now
		if pinned.neighborhood != nil {
			pinned.neighborhood.lastStart = now
			pinned.neighborhood.mu.Unlock()
		}
		pinned.origin.mu.Unlock()
		return nil
	}
}

// remainingDelay is how long lastStart still blocks a request spaced by delay.
func remainingDelay(lastStart time.Time, delay time.Duration, now time.Time) time.Duration {
	if lastStart.IsZero() {
		return 0
	}
	earliest := lastStart.Add(delay)
	if earliest.After(now) {
		return earliest.Sub(now)
	}
	return 0
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
	pinned, err := getter.scheduler.acquireTarget(target)
	if err != nil {
		return nil, err
	}
	defer getter.scheduler.releasePinned(pinned)

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

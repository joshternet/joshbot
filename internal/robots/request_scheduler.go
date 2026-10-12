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
	schedules map[origin.Origin]*originRequestSchedule
}

type originRequestSchedule struct {
	mu         sync.Mutex
	crawlDelay time.Duration
	lastStart  time.Time
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
		schedules: make(map[origin.Origin]*originRequestSchedule),
	}
}

func (scheduler *originRequestScheduler) scheduleFor(
	source origin.Origin,
) *originRequestSchedule {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()

	schedule := scheduler.schedules[source]
	if schedule == nil {
		schedule = &originRequestSchedule{}
		scheduler.schedules[source] = schedule
	}

	return schedule
}

func (scheduler *originRequestScheduler) setCrawlDelay(
	source origin.Origin,
	delay time.Duration,
) {
	schedule := scheduler.scheduleFor(source)

	schedule.mu.Lock()
	defer schedule.mu.Unlock()

	schedule.crawlDelay = delay
}

func (scheduler *originRequestScheduler) wait(
	ctx context.Context,
	target *url.URL,
) error {
	if target == nil {
		return errInvalidHTTPTarget
	}

	targetOrigin, err := origin.Parse(
		target.String(),
	)
	if err != nil {
		return errInvalidHTTPTarget
	}

	if scheduler.minimum < 0 {
		return errInvalidRequestDelay
	}

	schedule := scheduler.scheduleFor(
		targetOrigin,
	)

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

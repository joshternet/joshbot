package discovery

import (
	"context"
	"errors"
	"time"
)

var (
	errWaiterUnavailable = errors.New(
		"discovery: waiter unavailable",
	)
	errTimeoutFactoryUnavailable = errors.New(
		"discovery: timeout factory unavailable",
	)
)

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

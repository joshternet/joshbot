package robots

import (
	"context"
	"errors"
	"time"
)

// ErrFleetByteBudget reports that the shared UTC-day body-byte cap is spent.
// The request was not sent.
var ErrFleetByteBudget = errors.New(
	"robots: fleet byte budget exhausted",
)

// FleetBudget reserves a fleet-wide request slot and records body bytes.
//
// Acquire adds pendingBytes to today's total. A spent cap returns an error
// that implements FleetByteBudgetDelay. Otherwise the returned duration is how
// long the caller must wait before sending.
type FleetBudget interface {
	Acquire(
		context.Context,
		int64,
	) (time.Duration, error)
}

// FleetByteBudgetError is a spent daily byte cap with the wait until the next
// UTC midnight.
type FleetByteBudgetError struct {
	RetryAfter time.Duration
}

// Error returns the fleet byte-budget sentinel text.
func (e *FleetByteBudgetError) Error() string {
	return ErrFleetByteBudget.Error()
}

// Unwrap returns ErrFleetByteBudget.
func (e *FleetByteBudgetError) Unwrap() error {
	return ErrFleetByteBudget
}

// FleetByteBudgetDelay returns how long the process should wait before
// claiming more crawl work.
func (e *FleetByteBudgetError) FleetByteBudgetDelay() time.Duration {
	if e == nil {
		return 0
	}

	return e.RetryAfter
}

type fleetByteBudgetDelay interface {
	error
	FleetByteBudgetDelay() time.Duration
}

// FleetByteBudgetDelay reports the wait carried by a fleet byte-budget error.
//
// @param err - The error returned from a crawl request or processor loop.
// @returns The wait and true when err is a fleet byte-budget failure.
func FleetByteBudgetDelay(err error) (time.Duration, bool) {
	var budget fleetByteBudgetDelay
	if !errors.As(err, &budget) {
		return 0, false
	}

	return budget.FleetByteBudgetDelay(), true
}

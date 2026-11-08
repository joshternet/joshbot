package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/database"
)

var errInvalidFleetBudget = errors.New(
	"store: fleet budget is invalid",
)

// FleetByteBudgetError reports that today's UTC byte total is already at the
// configured cap. RetryAfter is the time until the next UTC midnight, measured
// by PostgreSQL at the moment the cap was observed.
type FleetByteBudgetError struct {
	RetryAfter time.Duration
}

// Error returns the sentinel text for a spent daily byte budget.
func (e *FleetByteBudgetError) Error() string {
	return "store: fleet byte budget exhausted"
}

// FleetByteBudgetDelay returns how long callers should wait before claiming
// more crawl work. A nil error waits nothing.
func (e *FleetByteBudgetError) FleetByteBudgetDelay() time.Duration {
	if e == nil {
		return 0
	}

	return e.RetryAfter
}

// FleetBudget is the single shared request and byte ceiling for worker and
// discovery processes.
//
// The caller owns the pool. Every process that crawls must use the same
// request rate and daily byte cap.
type FleetBudget struct {
	pool            database.Postgres
	requestInterval time.Duration
	maxBytesPerDay  int64
}

// NewFleetBudget constructs a fleet budget from a caller-owned pool.
//
// maxRequestsPerSecond is a positive integer. Spacing is one second divided by
// that integer and must stay positive. maxBytesPerDay is the positive UTC-day
// body-byte cap.
//
// @param pool - Open PostgreSQL pool. The caller closes it.
// @param maxRequestsPerSecond - Positive fleet request rate.
// @param maxBytesPerDay - Positive daily body-byte cap.
// @returns The shared budget, or an error when the pool or limits are unusable.
func NewFleetBudget(
	pool *pgxpool.Pool,
	maxRequestsPerSecond int,
	maxBytesPerDay int64,
) (*FleetBudget, error) {
	if pool == nil {
		return nil, errPoolUnavailable
	}

	return newFleetBudget(
		pool,
		maxRequestsPerSecond,
		maxBytesPerDay,
	)
}

func newFleetBudget(
	pool database.Postgres,
	maxRequestsPerSecond int,
	maxBytesPerDay int64,
) (*FleetBudget, error) {
	if pool == nil {
		return nil, errPoolUnavailable
	}

	if maxRequestsPerSecond <= 0 || maxBytesPerDay <= 0 {
		return nil, errInvalidFleetBudget
	}

	interval := time.Second / time.Duration(maxRequestsPerSecond)
	if interval <= 0 {
		return nil, errInvalidFleetBudget
	}

	return &FleetBudget{
		pool:            pool,
		requestInterval: interval,
		maxBytesPerDay:  maxBytesPerDay,
	}, nil
}

// Acquire adds pendingBytes to today's UTC total and either reserves the next
// request slot or reports that the cap is already reached.
//
// The row update commits before this method returns. The returned wait is
// PostgreSQL's granted_at minus now(), so the caller sleeps without holding
// the row lock. A spent cap still keeps the pending bytes and does not move
// the request slot. RetryAfter on the error is the time until the next UTC
// midnight from the same statement.
//
// @param ctx - Request context. Cancellation before the update returns ctx.Err.
// @param pendingBytes - Body bytes read since the previous successful add.
// @returns How long to wait before sending. Zero means send immediately.
// @throws *FleetByteBudgetError when today's total is at the cap.
// @throws an error when pendingBytes is negative or PostgreSQL rejects the update.
func (b *FleetBudget) Acquire(
	ctx context.Context,
	pendingBytes int64,
) (time.Duration, error) {
	if b == nil || b.pool == nil {
		return 0, errPoolUnavailable
	}

	if ctx == nil {
		return 0, errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return 0, err
	}

	if pendingBytes < 0 ||
		b.requestInterval <= 0 ||
		b.maxBytesPerDay <= 0 {
		return 0, errInvalidFleetBudget
	}

	var (
		exhausted         bool
		waitSeconds       float64
		retryAfterSeconds float64
	)

	err := b.pool.QueryRow(
		ctx,
		`
			UPDATE crawl_fleet_budget AS budget
			SET
				byte_window_started_at = CASE
					WHEN budget.byte_window_started_at < clock.window_start
					THEN clock.window_start
					ELSE budget.byte_window_started_at
				END,
				bytes_used = CASE
					WHEN budget.byte_window_started_at < clock.window_start
					THEN $1::bigint
					ELSE budget.bytes_used + $1::bigint
				END,
				next_request_at = CASE
					WHEN (
						CASE
							WHEN budget.byte_window_started_at < clock.window_start
							THEN $1::bigint
							ELSE budget.bytes_used + $1::bigint
						END
					) >= $2::bigint
					THEN budget.next_request_at
					ELSE GREATEST(budget.next_request_at, clock.db_now)
						+ $3::interval
				END
			FROM (
				SELECT
					(
						date_trunc('day', timezone('UTC', now()))
						AT TIME ZONE 'UTC'
					) AS window_start,
					now() AS db_now
			) AS clock
			WHERE budget.id = 1
			RETURNING
				bytes_used >= $2::bigint,
				CASE
					WHEN bytes_used >= $2::bigint THEN 0::double precision
					ELSE EXTRACT(
						EPOCH FROM (
							next_request_at - $3::interval - clock.db_now
						)
					)
				END,
				EXTRACT(
					EPOCH FROM (
						(clock.window_start + interval '1 day')
						- clock.db_now
					)
				)
		`,
		pendingBytes,
		b.maxBytesPerDay,
		b.requestInterval,
	).Scan(
		&exhausted,
		&waitSeconds,
		&retryAfterSeconds,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"store: acquire fleet budget: %w",
			err,
		)
	}

	if exhausted {
		return 0, &FleetByteBudgetError{
			RetryAfter: durationFromSeconds(retryAfterSeconds),
		}
	}

	return durationFromSeconds(waitSeconds), nil
}

// durationFromSeconds converts a PostgreSQL epoch interval into a duration.
// Non-finite or negative values wait nothing.
func durationFromSeconds(seconds float64) time.Duration {
	if math.IsNaN(seconds) ||
		math.IsInf(seconds, 0) ||
		seconds <= 0 {
		return 0
	}

	return time.Duration(seconds * float64(time.Second))
}

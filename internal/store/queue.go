package store

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
)

const maxQueueWorkerIDLength = 128

var (
	// ErrLeaseLost means the caller no longer has authority over queued work.
	ErrLeaseLost = errors.New("store: queue lease lost")

	errQueueUnavailable         = errors.New("store: queue is unavailable")
	errQueueClockUnavailable    = errors.New("store: queue clock is unavailable")
	errInvalidQueueConfig       = errors.New("store: queue configuration is invalid")
	errInvalidWorkerID          = errors.New("store: queue worker ID is invalid")
	errInvalidAvailableAt       = errors.New("store: queue availability time is invalid")
	errInvalidLease             = errors.New("store: queue lease is invalid")
	errInvalidRecheckAfter      = errors.New("store: recheck duration is invalid")
	errCompletionOriginMismatch = errors.New(
		"store: verification result origin does not match lease",
	)
)

// QueueConfig contains the operational timing policy for a queue.
type QueueConfig struct {
	LeaseDuration     time.Duration
	MinOriginInterval time.Duration
}

// Lease is temporary authority to process one queued origin.
//
// The expiration is a hard deadline for queue authority. Expiration does not
// terminate a worker process. Queue delivery is at-least-once.
type Lease struct {
	Origin     origin.Origin
	WorkerID   string
	Generation int64
	ClaimedAt  time.Time
	ExpiresAt  time.Time
}

// Queue coordinates declaration-verification work through PostgreSQL.
//
// The caller owns the supplied pool and remains responsible for closing it.
// Normal operation needs SELECT, INSERT, and UPDATE privileges. Schema
// migrations are intentionally separate.
type Queue struct {
	pool   *pgxpool.Pool
	config QueueConfig
	clock  queueClock
}

type queueClock interface {
	Now(
		context.Context,
		*pgxpool.Pool,
	) (time.Time, error)
}

type transactionQueueClock interface {
	NowTransaction(
		context.Context,
		pgx.Tx,
	) (time.Time, error)
}

type queueTimeQuerier interface {
	QueryRow(
		context.Context,
		string,
		...any,
	) pgx.Row
}

type databaseQueueClock struct{}

// NewQueue constructs a queue using a caller-owned PostgreSQL pool.
func NewQueue(
	pool *pgxpool.Pool,
	config QueueConfig,
) (*Queue, error) {
	return newQueue(
		pool,
		config,
		databaseQueueClock{},
	)
}

func newQueue(
	pool *pgxpool.Pool,
	config QueueConfig,
	clock queueClock,
) (*Queue, error) {
	if pool == nil {
		return nil, errPoolUnavailable
	}

	if !validQueueConfig(config) {
		return nil, errInvalidQueueConfig
	}

	if clock == nil {
		return nil, errQueueClockUnavailable
	}

	return &Queue{
		pool:   pool,
		config: config,
		clock:  clock,
	}, nil
}

// Schedule ensures source is queued no later than availableAt.
//
// Scheduling demand for an origin with an active unexpired lease is coalesced
// into that in-flight lease without changing its availability or authority.
func (q *Queue) Schedule(
	ctx context.Context,
	source origin.Origin,
	availableAt time.Time,
) error {
	if err := q.validate(ctx); err != nil {
		return err
	}

	if source.String() == "" {
		return errInvalidOrigin
	}

	if availableAt.IsZero() {
		return errInvalidAvailableAt
	}

	now, err := q.now(ctx)
	if err != nil {
		return err
	}

	_, err = q.pool.Exec(
		ctx,
		`
			INSERT INTO verification_queue (
				origin,
				available_at
			)
			VALUES ($1, $2)
			ON CONFLICT (origin) DO UPDATE
			SET available_at = CASE
				WHEN
					verification_queue.lease_owner IS NOT NULL
					AND verification_queue.lease_expires_at > $3
				THEN verification_queue.available_at
				ELSE LEAST(
					verification_queue.available_at,
					EXCLUDED.available_at
				)
			END
		`,
		source.String(),
		availableAt.UTC(),
		now,
	)
	if err != nil {
		return fmt.Errorf(
			"store: schedule queue work: %w",
			err,
		)
	}

	return nil
}

// Claim leases the first eligible origin visible to this claimant.
//
// Rows are selected by available_at and canonical origin. Locked rows are
// skipped so concurrent consumers can claim different work without waiting.
// A false found result with a nil error means no work is currently eligible.
func (q *Queue) Claim(
	ctx context.Context,
	workerID string,
) (Lease, bool, error) {
	if err := q.validate(ctx); err != nil {
		return Lease{}, false, err
	}

	if !validQueueWorkerID(workerID) {
		return Lease{}, false, errInvalidWorkerID
	}

	now, err := q.now(ctx)
	if err != nil {
		return Lease{}, false, err
	}

	expiresAt := now.Add(
		q.config.LeaseDuration,
	).UTC()
	politeBefore := now.Add(
		-q.config.MinOriginInterval,
	).UTC()

	var (
		lease Lease
		found bool
	)

	err = pgx.BeginFunc(
		ctx,
		q.pool,
		func(tx pgx.Tx) error {
			var storedOrigin string

			scanErr := tx.QueryRow(
				ctx,
				`
					WITH candidate AS (
						SELECT origin
						FROM verification_queue
						WHERE available_at <= $1
							AND (
								lease_expires_at IS NULL
								OR lease_expires_at <= $1
							)
							AND (
								last_claimed_at IS NULL
								OR last_claimed_at <= $4
							)
						ORDER BY
							available_at ASC,
							origin ASC
						FOR UPDATE SKIP LOCKED
						LIMIT 1
					)
					UPDATE verification_queue AS queued
					SET
						lease_generation =
							queued.lease_generation + 1,
						lease_owner = $2,
						lease_expires_at = $3,
						last_claimed_at = $1
					FROM candidate
					WHERE queued.origin = candidate.origin
					RETURNING
						queued.origin,
						queued.lease_owner,
						queued.lease_generation,
						queued.last_claimed_at,
						queued.lease_expires_at
				`,
				now,
				workerID,
				expiresAt,
				politeBefore,
			).Scan(
				&storedOrigin,
				&lease.WorkerID,
				&lease.Generation,
				&lease.ClaimedAt,
				&lease.ExpiresAt,
			)

			if errors.Is(scanErr, pgx.ErrNoRows) {
				return nil
			}

			if scanErr == nil {
				source, parseErr := origin.Parse(
					storedOrigin,
				)
				if parseErr != nil {
					return fmt.Errorf(
						"store: invalid queued origin: %w",
						parseErr,
					)
				}

				lease.Origin = source
				lease.ClaimedAt =
					lease.ClaimedAt.UTC()
				lease.ExpiresAt =
					lease.ExpiresAt.UTC()
				found = true
			}

			return scanErr
		},
	)
	if err != nil {
		return Lease{}, false, fmt.Errorf(
			"store: claim queue work: %w",
			err,
		)
	}

	return lease, found, nil
}

// Renew extends a lease that is still authoritative.
//
// Renewal keeps the same origin, worker, generation, and claim time. It never
// shortens the existing expiration and does not alter origin politeness state.
func (q *Queue) Renew(
	ctx context.Context,
	lease Lease,
) (Lease, error) {
	if err := q.validate(ctx); err != nil {
		return Lease{}, err
	}

	if !validQueueLease(lease) {
		return Lease{}, errInvalidLease
	}

	now, err := q.now(ctx)
	if err != nil {
		return Lease{}, err
	}

	candidateExpiresAt := now.Add(
		q.config.LeaseDuration,
	).UTC()

	renewed := Lease{
		Origin: lease.Origin,
	}

	err = q.pool.QueryRow(
		ctx,
		`
			UPDATE verification_queue
			SET lease_expires_at = GREATEST(
				lease_expires_at,
				$5
			)
			WHERE origin = $1
				AND lease_owner = $2
				AND lease_generation = $3
				AND lease_expires_at > $4
			RETURNING
				lease_owner,
				lease_generation,
				last_claimed_at,
				lease_expires_at
		`,
		lease.Origin.String(),
		lease.WorkerID,
		lease.Generation,
		now,
		candidateExpiresAt,
	).Scan(
		&renewed.WorkerID,
		&renewed.Generation,
		&renewed.ClaimedAt,
		&renewed.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Lease{}, ErrLeaseLost
	}

	if err != nil {
		return Lease{}, fmt.Errorf(
			"store: renew queue lease: %w",
			err,
		)
	}

	renewed.ClaimedAt = renewed.ClaimedAt.UTC()
	renewed.ExpiresAt = renewed.ExpiresAt.UTC()

	return renewed, nil
}

// Reschedule releases a valid lease and sets its next availability.
//
// The stored next availability is never earlier than the configured minimum
// interval after the lease's claim start.
func (q *Queue) Reschedule(
	ctx context.Context,
	lease Lease,
	availableAt time.Time,
) error {
	if err := q.validate(ctx); err != nil {
		return err
	}

	if !validQueueLease(lease) {
		return errInvalidLease
	}

	if availableAt.IsZero() {
		return errInvalidAvailableAt
	}

	now, err := q.now(ctx)
	if err != nil {
		return err
	}

	commandTag, err := q.pool.Exec(
		ctx,
		`
			UPDATE verification_queue
			SET
				available_at = GREATEST(
					$5,
					last_claimed_at + make_interval(
						secs => $6::double precision
					)
				),
				lease_owner = NULL,
				lease_expires_at = NULL
			WHERE origin = $1
				AND lease_owner = $2
				AND lease_generation = $3
				AND lease_expires_at > $4
		`,
		lease.Origin.String(),
		lease.WorkerID,
		lease.Generation,
		now,
		availableAt.UTC(),
		q.config.MinOriginInterval.Seconds(),
	)
	if err != nil {
		return fmt.Errorf(
			"store: reschedule queue work: %w",
			err,
		)
	}

	if commandTag.RowsAffected() != 1 {
		return ErrLeaseLost
	}

	return nil
}

// CompleteVerification atomically records a verification result, schedules
// the origin's next verification, and releases the active lease.
//
// The observation and scheduling timestamps use PostgreSQL shared time.
// A worker that no longer owns the current unexpired lease cannot record an
// observation.
func (q *Queue) CompleteVerification(
	ctx context.Context,
	lease Lease,
	result declaration.Result,
	recheckAfter time.Duration,
) error {
	if err := q.validate(ctx); err != nil {
		return err
	}

	if !validQueueLease(lease) {
		return errInvalidLease
	}

	if result.Origin.String() == "" {
		return errInvalidOrigin
	}

	if result.Origin != lease.Origin {
		return errCompletionOriginMismatch
	}

	if !validVerificationResult(result) {
		return errInvalidResult
	}

	if recheckAfter <= 0 {
		return errInvalidRecheckAfter
	}

	version, identity := storedDeclarationValues(result)

	err := pgx.BeginFunc(
		ctx,
		q.pool,
		func(tx pgx.Tx) error {
			completedAt, clockErr :=
				q.completionTime(ctx, tx)
			if clockErr != nil {
				return clockErr
			}

			requestedAvailableAt := completedAt.Add(
				recheckAfter,
			).UTC()

			commandTag, execErr := tx.Exec(
				ctx,
				`
					WITH completed_queue AS (
						UPDATE verification_queue
						SET
							available_at = GREATEST(
								$5,
								last_claimed_at +
									make_interval(
										secs =>
											$6::double precision
									)
							),
							lease_owner = NULL,
							lease_expires_at = NULL
						WHERE origin = $1
							AND lease_owner = $2
							AND lease_generation = $3
							AND lease_expires_at > $4
						RETURNING origin
					),
					recorded_origin AS (
						INSERT INTO origins (
							origin,
							first_observed_at
						)
						SELECT
							origin,
							$4
						FROM completed_queue
						ON CONFLICT (origin) DO UPDATE
						SET first_observed_at = LEAST(
							origins.first_observed_at,
							EXCLUDED.first_observed_at
						)
						RETURNING origin
					)
					INSERT INTO verification_observations (
						origin,
						observed_at,
						outcome,
						version,
						identity
					)
					SELECT
						origin,
						$4,
						$7,
						$8,
						$9
					FROM recorded_origin
				`,
				lease.Origin.String(),
				lease.WorkerID,
				lease.Generation,
				completedAt,
				requestedAvailableAt,
				q.config.MinOriginInterval.Seconds(),
				outcomeToText[result.Outcome],
				version,
				identity,
			)
			if execErr != nil {
				return execErr
			}

			if commandTag.RowsAffected() != 1 {
				return ErrLeaseLost
			}

			return nil
		},
	)
	if errors.Is(err, ErrLeaseLost) {
		return ErrLeaseLost
	}

	if err != nil {
		return fmt.Errorf(
			"store: complete verification: %w",
			err,
		)
	}

	return nil
}

func (q *Queue) validate(ctx context.Context) error {
	if q == nil {
		return errQueueUnavailable
	}

	if ctx == nil {
		return errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if q.pool == nil {
		return errPoolUnavailable
	}

	if !validQueueConfig(q.config) {
		return errInvalidQueueConfig
	}

	if q.clock == nil {
		return errQueueClockUnavailable
	}

	return nil
}

func (q *Queue) now(ctx context.Context) (time.Time, error) {
	now, err := q.clock.Now(ctx, q.pool)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"store: read queue clock: %w",
			err,
		)
	}

	return now.UTC(), nil
}

func (q *Queue) completionTime(
	ctx context.Context,
	tx pgx.Tx,
) (time.Time, error) {
	var (
		now time.Time
		err error
	)

	if clock, ok := q.clock.(transactionQueueClock); ok {
		now, err = clock.NowTransaction(ctx, tx)
	} else {
		now, err = q.clock.Now(ctx, q.pool)
	}

	if err != nil {
		return time.Time{}, fmt.Errorf(
			"store: read queue completion clock: %w",
			err,
		)
	}

	return now.UTC(), nil
}

func validQueueConfig(config QueueConfig) bool {
	return config.LeaseDuration > 0 &&
		config.MinOriginInterval > 0
}

func validQueueWorkerID(workerID string) bool {
	return workerID != "" &&
		utf8.RuneCountInString(workerID) <=
			maxQueueWorkerIDLength
}

func validQueueLease(lease Lease) bool {
	return lease.Origin.String() != "" &&
		validQueueWorkerID(lease.WorkerID) &&
		lease.Generation > 0 &&
		!lease.ClaimedAt.IsZero() &&
		!lease.ExpiresAt.IsZero() &&
		lease.ExpiresAt.After(lease.ClaimedAt)
}

func (databaseQueueClock) Now(
	ctx context.Context,
	pool *pgxpool.Pool,
) (time.Time, error) {
	return readQueueTime(ctx, pool)
}

func (databaseQueueClock) NowTransaction(
	ctx context.Context,
	tx pgx.Tx,
) (time.Time, error) {
	return readQueueTime(ctx, tx)
}

func readQueueTime(
	ctx context.Context,
	querier queueTimeQuerier,
) (time.Time, error) {
	var now time.Time
	err := querier.QueryRow(
		ctx,
		"SELECT clock_timestamp()",
	).Scan(&now)

	return now.UTC(), err
}

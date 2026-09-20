package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/database"
	"github.com/joshternet/joshbot/internal/origin"
)

type queueUnitClock struct {
	now time.Time
	err error
}

func (clock queueUnitClock) Now(
	context.Context,
	*pgxpool.Pool,
) (time.Time, error) {
	return clock.now, clock.err
}

func TestScheduleWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.September,
		18,
		21,
		0,
		0,
		0,
		time.UTC,
	)
	availableAt := now.Add(time.Hour)
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	t.Run(
		"validation",
		func(t *testing.T) {
			var queue *Queue

			err := queue.Schedule(
				ctx,
				source,
				availableAt,
			)

			if !errors.Is(
				err,
				errQueueUnavailable,
			) {
				t.Fatalf(
					"Schedule() error = %v, want %v",
					err,
					errQueueUnavailable,
				)
			}
		},
	)

	t.Run(
		"invalid origin",
		func(t *testing.T) {
			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				queueUnitClock{
					now: now,
				},
			)

			err := queue.Schedule(
				ctx,
				origin.Origin{},
				availableAt,
			)

			if !errors.Is(
				err,
				errInvalidOrigin,
			) {
				t.Fatalf(
					"Schedule() error = %v, want %v",
					err,
					errInvalidOrigin,
				)
			}
		},
	)

	t.Run(
		"invalid available time",
		func(t *testing.T) {
			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				queueUnitClock{
					now: now,
				},
			)

			err := queue.Schedule(
				ctx,
				source,
				time.Time{},
			)

			if !errors.Is(
				err,
				errInvalidAvailableAt,
			) {
				t.Fatalf(
					"Schedule() error = %v, want %v",
					err,
					errInvalidAvailableAt,
				)
			}
		},
	)

	t.Run(
		"clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test queue clock failure",
			)

			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				queueUnitClock{
					err: testErr,
				},
			)

			err := queue.Schedule(
				ctx,
				source,
				availableAt,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read queue clock",
				) {
				t.Fatalf(
					"Schedule() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test queue schedule failure",
			)

			database := &discoveryUnitDatabase{
				execResults: []discoveryUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				database,
				queueUnitClock{
					now: now,
				},
			)

			err := queue.Schedule(
				ctx,
				source,
				availableAt,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: schedule queue work",
				) {
				t.Fatalf(
					"Schedule() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"success",
		func(t *testing.T) {
			database := &discoveryUnitDatabase{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				database,
				queueUnitClock{
					now: now,
				},
			)

			err := queue.Schedule(
				ctx,
				source,
				availableAt,
			)
			if err != nil {
				t.Fatalf(
					"Schedule() error = %v",
					err,
				)
			}
		},
	)
}

func TestVerificationPausedWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.September,
		18,
		21,
		30,
		0,
		0,
		time.UTC,
	)

	t.Run(
		"validation",
		func(t *testing.T) {
			var queue *Queue

			paused, err :=
				queue.VerificationPaused(ctx)

			if paused {
				t.Fatal(
					"VerificationPaused() = true, want false",
				)
			}

			if !errors.Is(
				err,
				errQueueUnavailable,
			) {
				t.Fatalf(
					"VerificationPaused() error = %v, want %v",
					err,
					errQueueUnavailable,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test queue control failure",
			)

			database := &discoveryUnitDatabase{
				rowResults: []discoveryUnitRow{
					{
						err: testErr,
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				database,
				queueUnitClock{
					now: now,
				},
			)

			paused, err :=
				queue.VerificationPaused(ctx)

			if paused {
				t.Fatal(
					"VerificationPaused() = true, want false",
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read queue control",
				) {
				t.Fatalf(
					"VerificationPaused() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"not paused",
		func(t *testing.T) {
			database := &discoveryUnitDatabase{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							false,
						},
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				database,
				queueUnitClock{
					now: now,
				},
			)

			paused, err :=
				queue.VerificationPaused(ctx)
			if err != nil {
				t.Fatalf(
					"VerificationPaused() error = %v",
					err,
				)
			}

			if paused {
				t.Fatal(
					"VerificationPaused() = true, want false",
				)
			}
		},
	)

	t.Run(
		"paused",
		func(t *testing.T) {
			database := &discoveryUnitDatabase{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							true,
						},
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				database,
				queueUnitClock{
					now: now,
				},
			)

			paused, err :=
				queue.VerificationPaused(ctx)
			if err != nil {
				t.Fatalf(
					"VerificationPaused() error = %v",
					err,
				)
			}

			if !paused {
				t.Fatal(
					"VerificationPaused() = false, want true",
				)
			}
		},
	)
}

func queueSimpleDatabaseStore(
	storeDatabase database.Postgres,
	clock queueClock,
) *Queue {
	return &Queue{
		pool: storeDatabase,
		config: QueueConfig{
			LeaseDuration:     10 * time.Minute,
			MinOriginInterval: time.Hour,
		},
		clock: clock,
	}
}

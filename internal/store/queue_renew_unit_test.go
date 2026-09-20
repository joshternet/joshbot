package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestRenewWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.September,
		18,
		21,
		45,
		0,
		0,
		time.UTC,
	)
	lease := queueUnitLease(
		t,
		now.Add(-5*time.Minute),
		now.Add(5*time.Minute),
	)

	t.Run(
		"validation",
		func(t *testing.T) {
			var queue *Queue

			renewed, err := queue.Renew(
				ctx,
				lease,
			)

			if renewed != (Lease{}) {
				t.Fatalf(
					"Renew() lease = %#v, want zero",
					renewed,
				)
			}

			if !errors.Is(
				err,
				errQueueUnavailable,
			) {
				t.Fatalf(
					"Renew() error = %v, want %v",
					err,
					errQueueUnavailable,
				)
			}
		},
	)

	t.Run(
		"invalid lease",
		func(t *testing.T) {
			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				queueUnitClock{
					now: now,
				},
			)

			renewed, err := queue.Renew(
				ctx,
				Lease{},
			)

			if renewed != (Lease{}) {
				t.Fatalf(
					"Renew() lease = %#v, want zero",
					renewed,
				)
			}

			if !errors.Is(
				err,
				errInvalidLease,
			) {
				t.Fatalf(
					"Renew() error = %v, want %v",
					err,
					errInvalidLease,
				)
			}
		},
	)

	t.Run(
		"clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test renew clock failure",
			)

			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				queueUnitClock{
					err: testErr,
				},
			)

			renewed, err := queue.Renew(
				ctx,
				lease,
			)

			if renewed != (Lease{}) {
				t.Fatalf(
					"Renew() lease = %#v, want zero",
					renewed,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read queue clock",
				) {
				t.Fatalf(
					"Renew() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"lease lost",
		func(t *testing.T) {
			database := &discoveryUnitDatabase{
				rowResults: []discoveryUnitRow{
					{
						err: pgx.ErrNoRows,
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				database,
				queueUnitClock{
					now: now,
				},
			)

			renewed, err := queue.Renew(
				ctx,
				lease,
			)

			if renewed != (Lease{}) {
				t.Fatalf(
					"Renew() lease = %#v, want zero",
					renewed,
				)
			}

			if !errors.Is(
				err,
				ErrLeaseLost,
			) {
				t.Fatalf(
					"Renew() error = %v, want %v",
					err,
					ErrLeaseLost,
				)
			}
		},
	)

	t.Run(
		"database failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test renew database failure",
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

			renewed, err := queue.Renew(
				ctx,
				lease,
			)

			if renewed != (Lease{}) {
				t.Fatalf(
					"Renew() lease = %#v, want zero",
					renewed,
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: renew queue lease",
				) {
				t.Fatalf(
					"Renew() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"success",
		func(t *testing.T) {
			claimedAt := time.Date(
				2026,
				time.September,
				18,
				21,
				40,
				0,
				123,
				time.FixedZone(
					"test",
					-7*60*60,
				),
			)
			expiresAt := claimedAt.Add(
				20 * time.Minute,
			)

			database := &discoveryUnitDatabase{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							"worker-1",
							int64(7),
							claimedAt,
							expiresAt,
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

			renewed, err := queue.Renew(
				ctx,
				lease,
			)
			if err != nil {
				t.Fatalf(
					"Renew() error = %v",
					err,
				)
			}

			want := Lease{
				Origin:     lease.Origin,
				WorkerID:   "worker-1",
				Generation: 7,
				ClaimedAt:  claimedAt.UTC(),
				ExpiresAt:  expiresAt.UTC(),
			}

			if renewed != want {
				t.Fatalf(
					"Renew() lease = %#v, want %#v",
					renewed,
					want,
				)
			}
		},
	)
}

func queueUnitLease(
	t *testing.T,
	claimedAt time.Time,
	expiresAt time.Time,
) Lease {
	t.Helper()

	return Lease{
		Origin: mustStoreOrigin(
			t,
			"https://example.com",
		),
		WorkerID:   "worker-1",
		Generation: 7,
		ClaimedAt:  claimedAt,
		ExpiresAt:  expiresAt,
	}
}

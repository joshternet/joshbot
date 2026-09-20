package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestQueueClaimWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.September,
		18,
		22,
		15,
		0,
		0,
		time.UTC,
	)
	workerID := "worker-1"

	t.Run(
		"validation",
		func(t *testing.T) {
			var queue *Queue

			lease, found, err := queue.Claim(
				ctx,
				workerID,
			)

			if lease != (Lease{}) {
				t.Fatalf(
					"Claim() lease = %#v, want zero",
					lease,
				)
			}

			if found {
				t.Fatal(
					"Claim() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				errQueueUnavailable,
			) {
				t.Fatalf(
					"Claim() error = %v, want %v",
					err,
					errQueueUnavailable,
				)
			}
		},
	)

	t.Run(
		"invalid worker",
		func(t *testing.T) {
			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				queueUnitClock{
					now: now,
				},
			)

			lease, found, err := queue.Claim(
				ctx,
				"",
			)

			if lease != (Lease{}) {
				t.Fatalf(
					"Claim() lease = %#v, want zero",
					lease,
				)
			}

			if found {
				t.Fatal(
					"Claim() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				errInvalidWorkerID,
			) {
				t.Fatalf(
					"Claim() error = %v, want %v",
					err,
					errInvalidWorkerID,
				)
			}
		},
	)

	t.Run(
		"clock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test queue claim clock failure",
			)

			queue := queueSimpleDatabaseStore(
				&discoveryUnitDatabase{},
				queueUnitClock{
					err: testErr,
				},
			)

			lease, found, err := queue.Claim(
				ctx,
				workerID,
			)

			if lease != (Lease{}) {
				t.Fatalf(
					"Claim() lease = %#v, want zero",
					lease,
				)
			}

			if found {
				t.Fatal(
					"Claim() found = true, want false",
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
					"Claim() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"begin failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test queue claim begin failure",
			)

			database := &discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{},
				beginErr:              testErr,
			}

			queue := queueSimpleDatabaseStore(
				database,
				queueUnitClock{
					now: now,
				},
			)

			lease, found, err := queue.Claim(
				ctx,
				workerID,
			)

			if lease != (Lease{}) {
				t.Fatalf(
					"Claim() lease = %#v, want zero",
					lease,
				)
			}

			if found {
				t.Fatal(
					"Claim() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: claim queue work",
				) {
				t.Fatalf(
					"Claim() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"pause read failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test queue pause read failure",
			)

			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						err: testErr,
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				discoveryPolicyDatabase(tx),
				queueUnitClock{
					now: now,
				},
			)

			lease, found, err := queue.Claim(
				ctx,
				workerID,
			)

			if lease != (Lease{}) {
				t.Fatalf(
					"Claim() lease = %#v, want zero",
					lease,
				)
			}

			if found {
				t.Fatal(
					"Claim() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: read queue control",
				) ||
				!strings.Contains(
					err.Error(),
					"store: claim queue work",
				) {
				t.Fatalf(
					"Claim() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"paused",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							true,
						},
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				discoveryPolicyDatabase(tx),
				queueUnitClock{
					now: now,
				},
			)

			lease, found, err := queue.Claim(
				ctx,
				workerID,
			)
			if err != nil {
				t.Fatalf(
					"Claim() error = %v",
					err,
				)
			}

			if lease != (Lease{}) {
				t.Fatalf(
					"Claim() lease = %#v, want zero",
					lease,
				)
			}

			if found {
				t.Fatal(
					"Claim() found = true, want false",
				)
			}
		},
	)

	t.Run(
		"no work",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							false,
						},
					},
					{
						err: pgx.ErrNoRows,
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				discoveryPolicyDatabase(tx),
				queueUnitClock{
					now: now,
				},
			)

			lease, found, err := queue.Claim(
				ctx,
				workerID,
			)
			if err != nil {
				t.Fatalf(
					"Claim() error = %v",
					err,
				)
			}

			if lease != (Lease{}) {
				t.Fatalf(
					"Claim() lease = %#v, want zero",
					lease,
				)
			}

			if found {
				t.Fatal(
					"Claim() found = true, want false",
				)
			}
		},
	)

	t.Run(
		"claim read failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test queue claim read failure",
			)

			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							false,
						},
					},
					{
						err: testErr,
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				discoveryPolicyDatabase(tx),
				queueUnitClock{
					now: now,
				},
			)

			lease, found, err := queue.Claim(
				ctx,
				workerID,
			)

			if lease != (Lease{}) {
				t.Fatalf(
					"Claim() lease = %#v, want zero",
					lease,
				)
			}

			if found {
				t.Fatal(
					"Claim() found = true, want false",
				)
			}

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: claim queue work",
				) {
				t.Fatalf(
					"Claim() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"invalid stored origin",
		func(t *testing.T) {
			claimedAt := now
			expiresAt := now.Add(
				10 * time.Minute,
			)

			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							false,
						},
					},
					{
						values: []any{
							"ftp://example.com",
							workerID,
							int64(1),
							claimedAt,
							expiresAt,
						},
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				discoveryPolicyDatabase(tx),
				queueUnitClock{
					now: now,
				},
			)

			lease, found, err := queue.Claim(
				ctx,
				workerID,
			)

			if lease != (Lease{}) {
				t.Fatalf(
					"Claim() lease = %#v, want zero",
					lease,
				)
			}

			if found {
				t.Fatal(
					"Claim() found = true, want false",
				)
			}

			if err == nil ||
				!strings.Contains(
					err.Error(),
					"store: invalid queued origin",
				) ||
				!strings.Contains(
					err.Error(),
					"store: claim queue work",
				) {
				t.Fatalf(
					"Claim() error = %v",
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
				15,
				15,
				0,
				123,
				time.FixedZone(
					"test",
					-7*60*60,
				),
			)
			expiresAt := claimedAt.Add(
				10 * time.Minute,
			)

			tx := &discoveryPolicyUnitTx{
				rowResults: []discoveryUnitRow{
					{
						values: []any{
							false,
						},
					},
					{
						values: []any{
							"https://example.com",
							workerID,
							int64(7),
							claimedAt,
							expiresAt,
						},
					},
				},
			}

			queue := queueSimpleDatabaseStore(
				discoveryPolicyDatabase(tx),
				queueUnitClock{
					now: now,
				},
			)

			lease, found, err := queue.Claim(
				ctx,
				workerID,
			)
			if err != nil {
				t.Fatalf(
					"Claim() error = %v",
					err,
				)
			}

			if !found {
				t.Fatal(
					"Claim() found = false, want true",
				)
			}

			want := Lease{
				Origin: mustStoreOrigin(
					t,
					"https://example.com",
				),
				WorkerID:   workerID,
				Generation: 7,
				ClaimedAt:  claimedAt.UTC(),
				ExpiresAt:  expiresAt.UTC(),
			}

			if lease != want {
				t.Fatalf(
					"Claim() lease = %#v, want %#v",
					lease,
					want,
				)
			}
		},
	)
}

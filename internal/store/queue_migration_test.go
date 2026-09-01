package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestQueueMigrationCreatesSchemaInOrder(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	var tableCount int
	err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM information_schema.tables
			WHERE table_schema = current_schema()
				AND table_name IN (
					'origins',
					'verification_observations',
					'verification_queue'
				)
		`,
	).Scan(&tableCount)
	if err != nil {
		t.Fatalf("count migrated tables: %v", err)
	}

	if tableCount != 3 {
		t.Errorf(
			"migrated table count = %d, want 3",
			tableCount,
		)
	}
}

func TestQueueMigrationIsForwardOnly(t *testing.T) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	migration, err := os.ReadFile(
		"migrations/0002_verification_queue.sql",
	)
	if err != nil {
		t.Fatalf("read queue migration: %v", err)
	}

	_, err = pool.Exec(
		ctx,
		string(migration),
		pgx.QueryExecModeSimpleProtocol,
	)
	if err == nil {
		t.Error(
			"reapply queue migration error = nil, want non-nil",
		)
	}
}

func TestQueueSchemaRejectsInvalidStates(t *testing.T) {
	now := queueTestTime()

	tests := []struct {
		name           string
		origin         string
		generation     int64
		leaseOwner     any
		leaseExpiresAt any
		lastClaimedAt  any
	}{
		{
			name:       "empty origin",
			origin:     "",
			generation: 0,
		},
		{
			name:       "negative generation",
			origin:     "https://negative.example",
			generation: -1,
		},
		{
			name:          "owner without expiration",
			origin:        "https://owner-only.example",
			generation:    1,
			leaseOwner:    "worker-a",
			lastClaimedAt: now,
		},
		{
			name:           "expiration without owner",
			origin:         "https://expiration-only.example",
			generation:     1,
			leaseExpiresAt: now.Add(time.Minute),
			lastClaimedAt:  now,
		},
		{
			name:           "active lease without claim time",
			origin:         "https://missing-claim.example",
			generation:     1,
			leaseOwner:     "worker-a",
			leaseExpiresAt: now.Add(time.Minute),
		},
		{
			name:           "expiration equals claim time",
			origin:         "https://equal-times.example",
			generation:     1,
			leaseOwner:     "worker-a",
			leaseExpiresAt: now,
			lastClaimedAt:  now,
		},
		{
			name:           "empty owner",
			origin:         "https://empty-owner.example",
			generation:     1,
			leaseOwner:     "",
			leaseExpiresAt: now.Add(time.Minute),
			lastClaimedAt:  now,
		},
		{
			name:       "oversized owner",
			origin:     "https://large-owner.example",
			generation: 1,
			leaseOwner: strings.Repeat(
				"x",
				maxQueueWorkerIDLength+1,
			),
			leaseExpiresAt: now.Add(time.Minute),
			lastClaimedAt:  now,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := newStoreTestPool(t)

			_, err := pool.Exec(
				ctx,
				`
					INSERT INTO verification_queue (
						origin,
						available_at,
						lease_generation,
						lease_owner,
						lease_expires_at,
						last_claimed_at
					)
					VALUES ($1, $2, $3, $4, $5, $6)
				`,
				test.origin,
				now,
				test.generation,
				test.leaseOwner,
				test.leaseExpiresAt,
				test.lastClaimedAt,
			)
			if err == nil {
				t.Error(
					"invalid queue INSERT error = nil, want non-nil",
				)
			}
		})
	}
}

func TestQueueSchemaHasClaimIndexAndNoOriginForeignKey(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	var indexDefinition string
	err := pool.QueryRow(
		ctx,
		`
			SELECT indexdef
			FROM pg_indexes
			WHERE schemaname = current_schema()
				AND tablename = 'verification_queue'
				AND indexname =
					'verification_queue_claim_order_idx'
		`,
	).Scan(&indexDefinition)
	if err != nil {
		t.Fatalf("query claim index: %v", err)
	}

	if !strings.Contains(
		indexDefinition,
		"(available_at, origin)",
	) {
		t.Errorf(
			"claim index = %q, want available_at and origin",
			indexDefinition,
		)
	}

	var foreignKeyCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM pg_constraint
			WHERE conrelid =
					'verification_queue'::regclass
				AND contype = 'f'
		`,
	).Scan(&foreignKeyCount)
	if err != nil {
		t.Fatalf("count queue foreign keys: %v", err)
	}

	if foreignKeyCount != 0 {
		t.Errorf(
			"queue foreign key count = %d, want 0",
			foreignKeyCount,
		)
	}
}

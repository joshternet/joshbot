package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryLeaseOwnerSchemaSupportsOwnedAndLegacyLeases(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newSerialStoreTestPool(t)

	claimedAt := queueTestTime()
	expiresAt := claimedAt.Add(5 * time.Minute)

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_state (
				source_origin,
				lease_generation,
				lease_owner,
				last_claimed_at,
				lease_expires_at
			)
			VALUES
				(
					'https://owned.example',
					1,
					'discovery-a',
					$1,
					$2
				),
				(
					'https://legacy.example',
					1,
					NULL,
					$1,
					$2
				)
		`,
		claimedAt,
		expiresAt,
	)
	if err != nil {
		t.Fatalf(
			"insert discovery lease states: %v",
			err,
		)
	}

	var owner *string
	err = pool.QueryRow(
		ctx,
		`
			SELECT lease_owner
			FROM discovery_source_state
			WHERE source_origin = 'https://legacy.example'
		`,
	).Scan(&owner)
	if err != nil {
		t.Fatalf(
			"query legacy discovery lease owner: %v",
			err,
		)
	}

	if owner != nil {
		t.Errorf(
			"legacy lease owner = %q, want NULL",
			*owner,
		)
	}
}

func TestDiscoveryLeaseOwnerSchemaRejectsInvalidOwnedStates(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newSerialStoreTestPool(t)

	claimedAt := queueTestTime()
	expiresAt := claimedAt.Add(5 * time.Minute)

	tests := []struct {
		name      string
		origin    string
		owner     string
		expiresAt any
	}{
		{
			name:      "empty owner",
			origin:    "https://empty-owner.example",
			owner:     "",
			expiresAt: expiresAt,
		},
		{
			name:   "owner without expiration",
			origin: "https://owner-without-expiration.example",
			owner:  "discovery-a",
		},
		{
			name:   "oversized owner",
			origin: "https://oversized-owner.example",
			owner: strings.Repeat(
				"x",
				129,
			),
			expiresAt: expiresAt,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				_, err := pool.Exec(
					ctx,
					`
						INSERT INTO discovery_source_state (
							source_origin,
							lease_generation,
							lease_owner,
							last_claimed_at,
							lease_expires_at
						)
						VALUES ($1, 1, $2, $3, $4)
					`,
					test.origin,
					test.owner,
					claimedAt,
					test.expiresAt,
				)
				if err == nil {
					t.Fatal(
						"invalid discovery lease owner state accepted",
					)
				}
			},
		)
	}
}

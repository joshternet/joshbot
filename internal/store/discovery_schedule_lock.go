package store

import (
	"context"
	"fmt"
	"hash/fnv"

	"github.com/jackc/pgx/v5"
)

const discoveryScheduleLockDomain = "joshbot/discovery-source-schedule\x00"

// lockDiscoveryScheduleOrigin serializes scheduling decisions for one origin
// within the current transaction.
//
// The lock is deliberately separate from durable source state and from
// discovery_source_schedule itself. That lets callers serialize transitions
// before reading the state used to decide whether an origin should remain
// scheduled.
//
// The high bit keeps these keys in a namespace separate from the small,
// process-wide advisory lock constants used elsewhere by JoshBot.
func lockDiscoveryScheduleOrigin(
	ctx context.Context,
	tx pgx.Tx,
	rawOrigin string,
) error {
	hash := fnv.New64a()

	_, _ = hash.Write(
		[]byte(discoveryScheduleLockDomain),
	)
	_, _ = hash.Write(
		[]byte(rawOrigin),
	)

	key := int64(
		hash.Sum64() |
			(uint64(1) << 63),
	)

	if _, err := tx.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock($1)",
		key,
	); err != nil {
		return fmt.Errorf(
			"store: lock discovery schedule origin: %w",
			err,
		)
	}

	return nil
}

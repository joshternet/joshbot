package store

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/origin"
)

// Goal & Constraints: Fail-fast neighborhood claim coordination without
// weakening durable domain_inflight exclusivity. Transaction-scoped advisory
// locks coordinate acquire/renew/release; the unique domain_key row remains
// the durable lease. Acquisition uses try-lock before row locks; renew/release
// use blocking lock before any row mutation. No advisory across network I/O.

const (
	domainInflightLockDomain = "joshbot/domain-inflight-claim\x00"

	// domainInflightAcquireLockTimeout bounds unexpected waits on the durable
	// upsert during acquisition only. Renew/release do not install this timeout.
	domainInflightAcquireLockTimeout = "50ms"
)

// domainInflightKey returns the shared claim exclusivity key for an origin.
//
// Neighborhood hosts share their registrable domain or private public suffix.
// IP addresses and bare ICANN suffixes use the canonical origin string so they
// stay independent.
func domainInflightKey(source origin.Origin) string {
	if key, ok := origin.NeighborhoodKey(source.Hostname()); ok {
		return key
	}

	return source.String()
}

/**
 * neighborhoodAdvisoryLockKey derives a namespaced transaction advisory key.
 *
 * The schema name is included so isolated test schemas (and any multi-schema
 * deployment) do not contend on database-wide advisory keys for the same
 * neighborhood string.
 *
 * @param domainKey - Neighborhood or origin exclusivity string.
 * @param schema - current_schema() value for this transaction.
 * @returns A 64-bit key with the high bit set for namespace separation.
 */
func neighborhoodAdvisoryLockKey(domainKey string, schema string) int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(domainInflightLockDomain))
	_, _ = hash.Write([]byte(schema))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(domainKey))
	return int64(hash.Sum64() | (uint64(1) << 63))
}

/**
 * currentSchemaName reads the transaction search_path schema.
 *
 * @param ctx - Request context.
 * @param tx - Open transaction.
 * @returns The current schema name.
 */
func currentSchemaName(
	ctx context.Context,
	tx pgx.Tx,
) (string, error) {
	var schema string
	if err := tx.QueryRow(
		ctx,
		"SELECT current_schema()",
	).Scan(&schema); err != nil {
		return "", fmt.Errorf(
			"store: read current schema: %w",
			err,
		)
	}
	return schema, nil
}

/**
 * tryNeighborhoodAdvisoryLock attempts a nonblocking transaction-scoped lock.
 *
 * @param ctx - Request context.
 * @param tx - Open transaction.
 * @param domainKey - Neighborhood or origin exclusivity key.
 * @returns true when this transaction holds the advisory lock.
 */
func tryNeighborhoodAdvisoryLock(
	ctx context.Context,
	tx pgx.Tx,
	domainKey string,
) (bool, error) {
	schema, err := currentSchemaName(ctx, tx)
	if err != nil {
		return false, err
	}
	var acquired bool
	err = tx.QueryRow(
		ctx,
		"SELECT pg_try_advisory_xact_lock($1)",
		neighborhoodAdvisoryLockKey(domainKey, schema),
	).Scan(&acquired)
	if err != nil {
		return false, fmt.Errorf(
			"store: try neighborhood advisory lock: %w",
			err,
		)
	}
	return acquired, nil
}

/**
 * lockNeighborhoodAdvisory waits for the transaction-scoped neighborhood lock.
 *
 * Call before any domain_inflight or queue/source row mutation on renew/release.
 *
 * @param ctx - Request context.
 * @param tx - Open transaction.
 * @param domainKey - Neighborhood or origin exclusivity key.
 * @returns An error when the lock request fails.
 */
func lockNeighborhoodAdvisory(
	ctx context.Context,
	tx pgx.Tx,
	domainKey string,
) error {
	schema, err := currentSchemaName(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock($1)",
		neighborhoodAdvisoryLockKey(domainKey, schema),
	); err != nil {
		return fmt.Errorf(
			"store: lock neighborhood advisory: %w",
			err,
		)
	}
	return nil
}

/**
 * durableDomainLeaseActive reports whether a committed unexpired lease exists.
 *
 * Optimization against a committed snapshot only. Recheck after advisory.
 *
 * @param ctx - Request context.
 * @param tx - Open transaction.
 * @param domainKey - Neighborhood or origin exclusivity key.
 * @param now - Comparison timestamp.
 * @returns true when an unexpired durable lease row is visible.
 */
func durableDomainLeaseActive(
	ctx context.Context,
	tx pgx.Tx,
	domainKey string,
	now time.Time,
) (bool, error) {
	var active bool
	err := tx.QueryRow(
		ctx,
		`
			SELECT EXISTS (
				SELECT 1
				FROM domain_inflight_leases
				WHERE domain_key = $1
					AND lease_expires_at > $2
			)
		`,
		domainKey,
		now.UTC(),
	).Scan(&active)
	if err != nil {
		return false, fmt.Errorf(
			"store: read durable domain lease: %w",
			err,
		)
	}
	return active, nil
}

/**
 * reserveNeighborhoodForAcquire tries the fail-fast advisory protocol.
 *
 * Does not mutate durable rows. Call before FOR UPDATE on the candidate and
 * before writeDomainInflightLease.
 *
 * @param ctx - Request context.
 * @param tx - Open transaction.
 * @param domainKey - Neighborhood or origin exclusivity key.
 * @param now - Authoritative comparison time for this attempt.
 * @returns true when this transaction may proceed to lock the candidate row.
 */
func reserveNeighborhoodForAcquire(
	ctx context.Context,
	tx pgx.Tx,
	domainKey string,
	now time.Time,
) (bool, error) {
	active, err := durableDomainLeaseActive(ctx, tx, domainKey, now)
	if err != nil {
		return false, err
	}
	if active {
		return false, nil
	}

	locked, err := tryNeighborhoodAdvisoryLock(ctx, tx, domainKey)
	if err != nil {
		return false, err
	}
	if !locked {
		return false, nil
	}

	active, err = durableDomainLeaseActive(ctx, tx, domainKey, now)
	if err != nil {
		return false, err
	}
	if active {
		return false, nil
	}

	return true, nil
}

/**
 * writeDomainInflightLease upserts the durable neighborhood lease.
 *
 * Caller must already hold the neighborhood advisory via
 * reserveNeighborhoodForAcquire or lockNeighborhoodAdvisory.
 *
 * @param ctx - Request context.
 * @param tx - Open transaction.
 * @param domainKey - Neighborhood or origin exclusivity key.
 * @param holderOrigin - Canonical origin holding the slot.
 * @param leaseOwner - Worker or discovery process identity.
 * @param leaseGeneration - Origin lease generation fence.
 * @param claimedAt - Claim clock used for the origin lease.
 * @param expiresAt - Domain lease expiry matching the origin lease.
 * @returns true when this caller holds the durable domain slot.
 */
func writeDomainInflightLease(
	ctx context.Context,
	tx pgx.Tx,
	domainKey string,
	holderOrigin origin.Origin,
	leaseOwner string,
	leaseGeneration int64,
	claimedAt time.Time,
	expiresAt time.Time,
) (bool, error) {
	if _, err := tx.Exec(
		ctx,
		"SELECT set_config('lock_timeout', $1, true)",
		domainInflightAcquireLockTimeout,
	); err != nil {
		return false, fmt.Errorf(
			"store: set acquire lock_timeout: %w",
			err,
		)
	}
	// is_local=true lasts for the rest of the transaction until changed;
	// always restore before returning so later statements (lease bind,
	// crawl_run cleanup) are not under the acquisition timeout.
	resetLockTimeout := func() error {
		if _, err := tx.Exec(
			ctx,
			"SELECT set_config('lock_timeout', $1, true)",
			"0",
		); err != nil {
			return fmt.Errorf(
				"store: reset acquire lock_timeout: %w",
				err,
			)
		}
		return nil
	}
	resetDone := false
	defer func() {
		if !resetDone {
			_ = resetLockTimeout()
		}
	}()

	var storedKey string
	err := tx.QueryRow(
		ctx,
		`
			INSERT INTO domain_inflight_leases (
				domain_key,
				holder_origin,
				lease_owner,
				lease_generation,
				claimed_at,
				lease_expires_at
			) VALUES (
				$1,
				$2,
				$3,
				$4,
				$5,
				$6
			)
			ON CONFLICT (domain_key) DO UPDATE SET
				holder_origin = EXCLUDED.holder_origin,
				lease_owner = EXCLUDED.lease_owner,
				lease_generation = EXCLUDED.lease_generation,
				claimed_at = EXCLUDED.claimed_at,
				lease_expires_at = EXCLUDED.lease_expires_at
			WHERE domain_inflight_leases.lease_expires_at <=
				EXCLUDED.claimed_at
			RETURNING domain_key
		`,
		domainKey,
		holderOrigin.String(),
		leaseOwner,
		leaseGeneration,
		claimedAt.UTC(),
		expiresAt.UTC(),
	).Scan(&storedKey)
	if errors.Is(err, pgx.ErrNoRows) {
		if resetErr := resetLockTimeout(); resetErr != nil {
			return false, resetErr
		}
		resetDone = true
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf(
			"store: acquire domain inflight lease: %w",
			err,
		)
	}

	if resetErr := resetLockTimeout(); resetErr != nil {
		return false, resetErr
	}
	resetDone = true
	return true, nil
}

// acquireDomainInflight reserves the neighborhood and writes the durable lease.
//
// Prefer the split reserveNeighborhoodForAcquire → candidate FOR UPDATE →
// writeDomainInflightLease path in claim loops so queue row locks are not taken
// before the advisory. This helper remains for call sites that already hold no
// conflicting row locks.
//
// @param ctx - Request context.
// @param tx - Open transaction that also mutates the origin lease.
// @param domainKey - Neighborhood or origin exclusivity key.
// @param holderOrigin - Canonical origin holding the slot.
// @param leaseOwner - Worker or discovery process identity.
// @param leaseGeneration - Origin lease generation fence.
// @param claimedAt - Claim clock used for the origin lease.
// @param expiresAt - Domain lease expiry matching the origin lease.
// @returns true when this caller holds the domain slot.
func acquireDomainInflight(
	ctx context.Context,
	tx pgx.Tx,
	domainKey string,
	holderOrigin origin.Origin,
	leaseOwner string,
	leaseGeneration int64,
	claimedAt time.Time,
	expiresAt time.Time,
) (bool, error) {
	reserved, err := reserveNeighborhoodForAcquire(
		ctx,
		tx,
		domainKey,
		claimedAt,
	)
	if err != nil || !reserved {
		return reserved, err
	}

	return writeDomainInflightLease(
		ctx,
		tx,
		domainKey,
		holderOrigin,
		leaseOwner,
		leaseGeneration,
		claimedAt,
		expiresAt,
	)
}

// extendDomainInflight extends an authoritative domain lease expiry.
//
// Callers must already hold the blocking neighborhood advisory for domainKey.
//
// @param ctx - Request context.
// @param tx - Open transaction that also renews the origin lease.
// @param domainKey - Neighborhood or origin exclusivity key.
// @param holderOrigin - Canonical origin holding the slot.
// @param leaseGeneration - Origin lease generation fence.
// @param expiresAt - Candidate expiry; never shortens the stored value.
// @returns error when the domain row is missing or fenced out.
func extendDomainInflight(
	ctx context.Context,
	tx pgx.Tx,
	domainKey string,
	holderOrigin origin.Origin,
	leaseGeneration int64,
	expiresAt time.Time,
) error {
	commandTag, err := tx.Exec(
		ctx,
		`
			UPDATE domain_inflight_leases
			SET lease_expires_at = GREATEST(
				lease_expires_at,
				$4
			)
			WHERE domain_key = $1
				AND holder_origin = $2
				AND lease_generation = $3
		`,
		domainKey,
		holderOrigin.String(),
		leaseGeneration,
		expiresAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf(
			"store: extend domain inflight lease: %w",
			err,
		)
	}

	if commandTag.RowsAffected() != 1 {
		return errDomainInflightLost
	}

	return nil
}

// releaseDomainInflight frees the domain slot for an authoritative holder.
//
// Callers must already hold the blocking neighborhood advisory for domainKey.
//
// @param ctx - Request context.
// @param tx - Open transaction that also clears the origin lease.
// @param domainKey - Neighborhood or origin exclusivity key.
// @param holderOrigin - Canonical origin holding the slot.
// @param leaseGeneration - Origin lease generation fence.
// @returns error when the delete fails; missing rows are not an error so a
//
//	stale release after expire does not block origin lease clearing.
func releaseDomainInflight(
	ctx context.Context,
	tx pgx.Tx,
	domainKey string,
	holderOrigin origin.Origin,
	leaseGeneration int64,
) error {
	_, err := tx.Exec(
		ctx,
		`
			DELETE FROM domain_inflight_leases
			WHERE domain_key = $1
				AND holder_origin = $2
				AND lease_generation = $3
		`,
		domainKey,
		holderOrigin.String(),
		leaseGeneration,
	)
	if err != nil {
		return fmt.Errorf(
			"store: release domain inflight lease: %w",
			err,
		)
	}

	return nil
}

var errDomainInflightLost = errors.New(
	"store: domain inflight lease lost",
)

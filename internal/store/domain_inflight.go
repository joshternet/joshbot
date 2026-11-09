package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/origin"
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

// acquireDomainInflight takes the domain slot when free or expired.
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
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf(
			"store: acquire domain inflight lease: %w",
			err,
		)
	}

	return true, nil
}

// extendDomainInflight extends an authoritative domain lease expiry.
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

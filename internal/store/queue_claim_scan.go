package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/origin"
)

// ClaimWithStats claims one eligible origin and returns diagnostic stats.
//
// Protocol: nonlocking ordered discovery, per-candidate savepoint, try
// neighborhood advisory, FOR UPDATE SKIP LOCKED with full eligibility
// revalidation, then durable domain upsert and lease bind.
//
// @param ctx - Request context.
// @param workerID - Claiming worker identity.
// @returns The lease when successful, claim stats, and any hard error.
func (q *Queue) ClaimWithStats(
	ctx context.Context,
	workerID string,
) (Lease, ClaimStats, error) {
	stats := ClaimStats{Outcome: ClaimOutcomeNoWork}

	if err := q.validate(ctx); err != nil {
		stats.Outcome = ClaimOutcomeError
		return Lease{}, stats, err
	}

	if !validQueueWorkerID(workerID) {
		stats.Outcome = ClaimOutcomeError
		return Lease{}, stats, errInvalidWorkerID
	}

	var lease Lease

	err := pgx.BeginFunc(
		ctx,
		q.pool,
		func(tx pgx.Tx) error {
			var paused bool
			if err := tx.QueryRow(
				ctx,
				"SELECT verification_paused FROM crawl_control WHERE singleton",
			).Scan(&paused); err != nil {
				return fmt.Errorf(
					"store: read queue control: %w",
					err,
				)
			}
			if paused {
				stats.Outcome = ClaimOutcomeNoWork
				return nil
			}

			skippedOrigins := make([]string, 0)
			skippedDomains := make(map[string]struct{})
			var (
				contRank   int
				contAt     time.Time
				contOrigin string
				haveCont   bool
			)

			for {
				if stats.CandidatesExamined >= claimMaxCandidatesExamined {
					stats.Outcome = ClaimOutcomeDeferred
					return nil
				}

				now, err := q.claimTime(ctx, tx)
				if err != nil {
					return err
				}
				politeBefore := now.Add(
					-q.config.MinOriginInterval,
				).UTC()

				candidates, err := q.listClaimCandidates(
					ctx,
					tx,
					now,
					politeBefore,
					skippedOrigins,
					haveCont,
					contRank,
					contAt,
					contOrigin,
				)
				if err != nil {
					return err
				}
				if len(candidates) == 0 {
					if stats.BusySkips > 0 ||
						stats.LockedSkips > 0 ||
						stats.IneligibleSkips > 0 ||
						stats.LockTimeouts > 0 {
						stats.Outcome = ClaimOutcomeDeferred
					} else {
						stats.Outcome = ClaimOutcomeNoWork
					}
					return nil
				}

				for _, candidate := range candidates {
					stats.CandidatesExamined++
					if stats.CandidatesExamined > claimMaxCandidatesExamined {
						stats.Outcome = ClaimOutcomeDeferred
						return nil
					}

					contRank = candidate.modeRank
					contAt = candidate.availableAt
					contOrigin = candidate.origin
					haveCont = true

					domainKey := candidate.domainKey
					if _, busy := skippedDomains[domainKey]; busy {
						skippedOrigins = append(
							skippedOrigins,
							candidate.origin,
						)
						stats.BusySkips++
						continue
					}

					if _, err := tx.Exec(
						ctx,
						"SAVEPOINT claim_attempt",
					); err != nil {
						return fmt.Errorf(
							"store: claim savepoint: %w",
							err,
						)
					}

					claimed, skipKind, attemptErr := q.attemptClaimCandidate(
						ctx,
						tx,
						workerID,
						candidate,
						now,
					)
					if attemptErr != nil {
						_, _ = tx.Exec(
							ctx,
							"ROLLBACK TO SAVEPOINT claim_attempt",
						)
						if isLockTimeoutError(attemptErr) {
							stats.LockTimeouts++
							skippedDomains[domainKey] = struct{}{}
							skippedOrigins = append(
								skippedOrigins,
								candidate.origin,
							)
							continue
						}
						return attemptErr
					}

					if claimed != nil {
						if _, err := tx.Exec(
							ctx,
							"RELEASE SAVEPOINT claim_attempt",
						); err != nil {
							return fmt.Errorf(
								"store: release claim savepoint: %w",
								err,
							)
						}
						lease = *claimed
						stats.Outcome = ClaimOutcomeSuccess
						return nil
					}

					_, _ = tx.Exec(
						ctx,
						"ROLLBACK TO SAVEPOINT claim_attempt",
					)
					skippedOrigins = append(
						skippedOrigins,
						candidate.origin,
					)
					switch skipKind {
					case claimSkipBusy:
						skippedDomains[domainKey] = struct{}{}
						stats.BusySkips++
					case claimSkipLocked:
						stats.LockedSkips++
					case claimSkipIneligible:
						stats.IneligibleSkips++
					}
				}
			}
		},
	)
	if err != nil {
		stats.Outcome = ClaimOutcomeError
		return Lease{}, stats, fmt.Errorf(
			"store: claim queue work: %w",
			err,
		)
	}

	return lease, stats, nil
}

type claimSkipKind int

const (
	claimSkipNone claimSkipKind = iota
	claimSkipBusy
	claimSkipLocked
	claimSkipIneligible
)

type claimCandidate struct {
	origin       string
	modeRank     int
	availableAt  time.Time
	generation   int64
	domainKey    string
	parsedOrigin origin.Origin
}

func (q *Queue) listClaimCandidates(
	ctx context.Context,
	tx pgx.Tx,
	now time.Time,
	politeBefore time.Time,
	skippedOrigins []string,
	haveCont bool,
	contRank int,
	contAt time.Time,
	contOrigin string,
) ([]claimCandidate, error) {
	const listSQL = `
			SELECT
				origin,
				lease_generation,
				available_at,
				CASE
					WHEN mode = 'recurring' THEN 0
					WHEN mode = 'probe' THEN 1
					ELSE 2
				END AS mode_rank
			FROM verification_queue
			WHERE available_at <= $1
				AND (
					mode = 'recurring'
					OR NOT EXISTS (
						SELECT 1 FROM discovery_source_state
						WHERE source_origin = verification_queue.origin
							AND crawl_blocked
					)
				)
				AND (
					next_attempt_at IS NULL
					OR next_attempt_at <= $1
				)
				AND (
					lease_expires_at IS NULL
					OR lease_expires_at <= $1
				)
				AND (
					last_claimed_at IS NULL
					OR last_claimed_at <= $2
				)
				AND NOT (
					origin = ANY ($3::text[])
				)
`

	var (
		rows pgx.Rows
		err  error
	)
	if !haveCont {
		rows, err = tx.Query(
			ctx,
			listSQL+`
			ORDER BY
				CASE
					WHEN mode = 'recurring' THEN 0
					WHEN mode = 'probe' THEN 1
					ELSE 2
				END ASC,
				available_at ASC NULLS LAST,
				origin ASC
			LIMIT $4
			`,
			now,
			politeBefore,
			skippedOrigins,
			claimCandidateBatchSize,
		)
	} else {
		rows, err = tx.Query(
			ctx,
			listSQL+`
				AND (
					CASE
						WHEN mode = 'recurring' THEN 0
						WHEN mode = 'probe' THEN 1
						ELSE 2
					END,
					available_at,
					origin
				) > ($4::int, $5::timestamptz, $6::text)
			ORDER BY
				CASE
					WHEN mode = 'recurring' THEN 0
					WHEN mode = 'probe' THEN 1
					ELSE 2
				END ASC,
				available_at ASC NULLS LAST,
				origin ASC
			LIMIT $7
			`,
			now,
			politeBefore,
			skippedOrigins,
			contRank,
			contAt,
			contOrigin,
			claimCandidateBatchSize,
		)
	}
	if err != nil {
		return nil, fmt.Errorf(
			"store: list claim candidates: %w",
			err,
		)
	}
	defer rows.Close()

	candidates := make([]claimCandidate, 0, claimCandidateBatchSize)
	for rows.Next() {
		var candidate claimCandidate
		if err := rows.Scan(
			&candidate.origin,
			&candidate.generation,
			&candidate.availableAt,
			&candidate.modeRank,
		); err != nil {
			return nil, fmt.Errorf(
				"store: scan claim candidate: %w",
				err,
			)
		}
		source, parseErr := origin.Parse(candidate.origin)
		if parseErr != nil {
			return nil, fmt.Errorf(
				"store: invalid queued origin: %w",
				parseErr,
			)
		}
		candidate.parsedOrigin = source
		candidate.domainKey = domainInflightKey(source)
		candidate.availableAt = candidate.availableAt.UTC()
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"store: iterate claim candidates: %w",
			err,
		)
	}
	return candidates, nil
}

func (q *Queue) attemptClaimCandidate(
	ctx context.Context,
	tx pgx.Tx,
	workerID string,
	candidate claimCandidate,
	scanNow time.Time,
) (*Lease, claimSkipKind, error) {
	now, err := q.claimTime(ctx, tx)
	if err != nil {
		return nil, claimSkipNone, err
	}
	if now.IsZero() {
		now = scanNow
	}
	now = now.UTC()
	expiresAt := now.Add(q.config.LeaseDuration).UTC()
	politeBefore := now.Add(-q.config.MinOriginInterval).UTC()

	reserved, err := reserveNeighborhoodForAcquire(
		ctx,
		tx,
		candidate.domainKey,
		now,
	)
	if err != nil {
		return nil, claimSkipNone, err
	}
	if !reserved {
		return nil, claimSkipBusy, nil
	}

	var (
		lockedOrigin string
		currentGen   int64
	)
	lockErr := tx.QueryRow(
		ctx,
		`
			SELECT
				origin,
				lease_generation
			FROM verification_queue
			WHERE origin = $1
				AND available_at <= $2
				AND (
					mode = 'recurring'
					OR NOT EXISTS (
						SELECT 1 FROM discovery_source_state
						WHERE source_origin = verification_queue.origin
							AND crawl_blocked
					)
				)
				AND (
					next_attempt_at IS NULL
					OR next_attempt_at <= $2
				)
				AND (
					lease_expires_at IS NULL
					OR lease_expires_at <= $2
				)
				AND (
					last_claimed_at IS NULL
					OR last_claimed_at <= $3
				)
			FOR UPDATE SKIP LOCKED
		`,
		candidate.origin,
		now,
		politeBefore,
	).Scan(&lockedOrigin, &currentGen)
	if errors.Is(lockErr, pgx.ErrNoRows) {
		var stillExists bool
		if err := tx.QueryRow(
			ctx,
			`
				SELECT EXISTS (
					SELECT 1
					FROM verification_queue
					WHERE origin = $1
						AND available_at <= $2
						AND (
							mode = 'recurring'
							OR NOT EXISTS (
								SELECT 1 FROM discovery_source_state
								WHERE source_origin = verification_queue.origin
									AND crawl_blocked
							)
						)
						AND (
							next_attempt_at IS NULL
							OR next_attempt_at <= $2
						)
						AND (
							lease_expires_at IS NULL
							OR lease_expires_at <= $2
						)
						AND (
							last_claimed_at IS NULL
							OR last_claimed_at <= $3
						)
				)
			`,
			candidate.origin,
			now,
			politeBefore,
		).Scan(&stillExists); err != nil {
			return nil, claimSkipNone, err
		}
		if stillExists {
			return nil, claimSkipLocked, nil
		}
		return nil, claimSkipIneligible, nil
	}
	if lockErr != nil {
		return nil, claimSkipNone, lockErr
	}

	nextGeneration := currentGen + 1
	written, err := writeDomainInflightLease(
		ctx,
		tx,
		candidate.domainKey,
		candidate.parsedOrigin,
		workerID,
		nextGeneration,
		now,
		expiresAt,
	)
	if err != nil {
		return nil, claimSkipNone, err
	}
	if !written {
		return nil, claimSkipBusy, nil
	}

	var lease Lease
	if err := tx.QueryRow(
		ctx,
		`
			UPDATE verification_queue
			SET
				lease_generation = $2,
				lease_owner = $3,
				lease_expires_at = $4,
				last_claimed_at = $5
			WHERE origin = $1
			RETURNING
				lease_owner,
				lease_generation,
				last_claimed_at,
				lease_expires_at
		`,
		lockedOrigin,
		nextGeneration,
		workerID,
		expiresAt,
		now,
	).Scan(
		&lease.WorkerID,
		&lease.Generation,
		&lease.ClaimedAt,
		&lease.ExpiresAt,
	); err != nil {
		return nil, claimSkipNone, err
	}

	lease.Origin = candidate.parsedOrigin
	lease.ClaimedAt = lease.ClaimedAt.UTC()
	lease.ExpiresAt = lease.ExpiresAt.UTC()
	return &lease, claimSkipNone, nil
}

func isLockTimeoutError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// 55P03 = lock_not_available
		return pgErr.Code == "55P03"
	}
	return false
}

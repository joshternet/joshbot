package store

import (
	"context"
	"fmt"
)

// AbandonVerification releases an authoritative verification lease without
// recording a verification result or changing the queued work's schedule.
//
// Abandonment is used when verification work has finished but its result could
// not be committed. The exact origin, worker, generation, and unexpired lease
// must still be authoritative. A stale worker therefore cannot release a lease
// that has expired or been reclaimed by another worker.
//
// The existing availability, retry state, failure history, claim time, and
// queue mode are preserved. Normal queue politeness still determines when the
// origin may be claimed again.
func (q *Queue) AbandonVerification(
	ctx context.Context,
	lease Lease,
) error {
	if err := q.validate(ctx); err != nil {
		return err
	}

	if !validQueueLease(lease) {
		return errInvalidLease
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
	)
	if err != nil {
		return fmt.Errorf(
			"store: abandon verification lease: %w",
			err,
		)
	}

	if commandTag.RowsAffected() != 1 {
		return ErrLeaseLost
	}

	return nil
}

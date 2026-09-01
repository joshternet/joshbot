package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/origin"
)

// MaxObservationHistory is the largest history page accepted by Observations.
const MaxObservationHistory = 100

// Observations returns at most limit observations for source.
//
// Results are ordered by observation time descending and generated ID
// descending so equal timestamps have deterministic ordering. Limit must be
// between 1 and MaxObservationHistory, inclusive.
func (s *Store) Observations(
	ctx context.Context,
	source origin.Origin,
	limit int,
) ([]Observation, error) {
	if err := s.validate(ctx); err != nil {
		return nil, err
	}

	if source.String() == "" {
		return nil, errInvalidOrigin
	}

	if limit < 1 || limit > MaxObservationHistory {
		return nil, errInvalidHistoryLimit
	}

	rows, err := s.pool.Query(
		ctx,
		`
			SELECT
				observed_at,
				outcome,
				version,
				identity
			FROM verification_observations
			WHERE origin = $1
			ORDER BY observed_at DESC, id DESC
			LIMIT $2
		`,
		source.String(),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"store: query observation history: %w",
			err,
		)
	}

	return pgx.CollectRows(
		rows,
		scanStoredObservation,
	)
}

func scanStoredObservation(
	row pgx.CollectableRow,
) (Observation, error) {
	var (
		observedAt  time.Time
		outcomeText string
		version     *int
		identity    *string
	)

	err := row.Scan(
		&observedAt,
		&outcomeText,
		&version,
		&identity,
	)

	return observationFromStored(
		observedAt,
		outcomeText,
		version,
		identity,
	), err
}

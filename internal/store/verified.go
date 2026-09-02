package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
)

var errInvalidStoredVerifiedOrigin = errors.New(
	"store: stored verified origin is invalid",
)

// VerifiedOrigin is the public semantic state of one verified origin.
//
// It deliberately excludes observation timestamps, outcomes, history, and
// queue state.
type VerifiedOrigin struct {
	Origin      origin.Origin
	Declaration declaration.Declaration
}

// VerifiedOrigins returns every origin whose latest authoritative observation
// is valid.
//
// Results are ordered by canonical origin. Temporary unavailable and robots
// denial observations do not replace authoritative declaration state.
func (s *Store) VerifiedOrigins(
	ctx context.Context,
) ([]VerifiedOrigin, error) {
	if err := s.validate(ctx); err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(
		ctx,
		`
			SELECT
				effective.origin,
				effective.version,
				effective.identity
			FROM (
				SELECT DISTINCT ON (origin)
					origin,
					outcome,
					version,
					identity
				FROM verification_observations
				WHERE outcome IN (
					'valid',
					'absent',
					'invalid',
					'unsupported_version',
					'cross_origin_redirect'
				)
				ORDER BY
					origin ASC,
					observed_at DESC,
					id DESC
			) AS effective
			WHERE effective.outcome = 'valid'
			ORDER BY effective.origin ASC
		`,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"store: query verified origins: %w",
			err,
		)
	}

	verified, err := pgx.CollectRows(
		rows,
		scanVerifiedOrigin,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"store: collect verified origins: %w",
			err,
		)
	}

	return verified, nil
}

func scanVerifiedOrigin(
	row pgx.CollectableRow,
) (VerifiedOrigin, error) {
	var (
		storedOrigin   string
		version        int
		storedIdentity string
	)

	if err := row.Scan(
		&storedOrigin,
		&version,
		&storedIdentity,
	); err != nil {
		return VerifiedOrigin{}, fmt.Errorf(
			"store: scan verified origin: %w",
			err,
		)
	}

	source, err := origin.Parse(storedOrigin)
	if err != nil {
		return VerifiedOrigin{}, fmt.Errorf(
			"%w: origin",
			errInvalidStoredVerifiedOrigin,
		)
	}

	identity, known := identityFromText[storedIdentity]
	if version != 1 || !known {
		return VerifiedOrigin{}, fmt.Errorf(
			"%w: declaration",
			errInvalidStoredVerifiedOrigin,
		)
	}

	return VerifiedOrigin{
		Origin: source,
		Declaration: declaration.Declaration{
			Version:  version,
			Identity: identity,
		},
	}, nil
}

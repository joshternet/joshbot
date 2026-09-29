package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
)

var errInvalidStoredRegistryOrigin = errors.New(
	"store: stored registry origin is invalid",
)

// RegistryOrigin is the durable public registry state for an origin that has
// participated at least once.
//
// CurrentDeclaration is populated only when the latest authoritative
// declaration state is valid. LatestDeclarationCheckOutcome records the most
// recent check, including temporary outcomes that do not replace authoritative
// declaration state.
type RegistryOrigin struct {
	Origin                        origin.Origin
	FirstParticipatedAt           time.Time
	InitialDeclaration            declaration.Declaration
	LatestDeclarationCheckAt      time.Time
	LatestDeclarationCheckOutcome declaration.Outcome
	CurrentDeclaration            *declaration.Declaration
}

// RegistryOrigins returns every origin that has participated at least once.
//
// Results are ordered by canonical origin. Durable participation metadata is
// read from origins. CurrentDeclaration reflects the latest authoritative
// declaration state, so temporary unavailable and robots-denied checks do not
// erase a still-valid current declaration.
func (s *Store) RegistryOrigins(
	ctx context.Context,
) ([]RegistryOrigin, error) {
	if err := s.validate(ctx); err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(
		ctx,
		`
			SELECT
				stored_origin.origin,
				stored_origin.first_participated_at,
				stored_origin.initial_declaration_version,
				stored_origin.initial_declaration_identity,
				stored_origin.latest_declaration_check_at,
				stored_origin.latest_declaration_check_outcome,
				effective.outcome,
				effective.version,
				effective.identity
			FROM origins AS stored_origin
			LEFT JOIN LATERAL (
				SELECT
					observation.outcome,
					observation.version,
					observation.identity
				FROM verification_observations AS observation
				WHERE observation.origin = stored_origin.origin
					AND observation.outcome IN (
						'valid',
						'absent',
						'invalid',
						'unsupported_version',
						'cross_origin_redirect'
					)
				ORDER BY
					observation.observed_at DESC,
					observation.id DESC
				LIMIT 1
			) AS effective ON TRUE
			WHERE stored_origin.first_participated_at IS NOT NULL
			ORDER BY stored_origin.origin ASC
		`,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"store: query registry origins: %w",
			err,
		)
	}

	registry, err := pgx.CollectRows(
		rows,
		scanRegistryOrigin,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"store: collect registry origins: %w",
			err,
		)
	}

	return registry, nil
}

func scanRegistryOrigin(
	row pgx.CollectableRow,
) (RegistryOrigin, error) {
	var (
		storedOrigin                      string
		firstParticipatedAt               time.Time
		initialVersion                    int
		initialIdentityText               string
		latestDeclarationCheckAt          time.Time
		latestDeclarationCheckOutcomeText string
		effectiveOutcomeText              *string
		effectiveVersion                  *int
		effectiveIdentityText             *string
	)

	if err := row.Scan(
		&storedOrigin,
		&firstParticipatedAt,
		&initialVersion,
		&initialIdentityText,
		&latestDeclarationCheckAt,
		&latestDeclarationCheckOutcomeText,
		&effectiveOutcomeText,
		&effectiveVersion,
		&effectiveIdentityText,
	); err != nil {
		return RegistryOrigin{}, fmt.Errorf(
			"store: scan registry origin: %w",
			err,
		)
	}

	source, err := origin.Parse(storedOrigin)
	if err != nil {
		return RegistryOrigin{}, fmt.Errorf(
			"%w: origin",
			errInvalidStoredRegistryOrigin,
		)
	}

	initialIdentity, known := identityFromText[initialIdentityText]
	if initialVersion != 1 || !known {
		return RegistryOrigin{}, fmt.Errorf(
			"%w: initial declaration",
			errInvalidStoredRegistryOrigin,
		)
	}

	latestOutcome, known := outcomeFromText[latestDeclarationCheckOutcomeText]
	if !known {
		return RegistryOrigin{}, fmt.Errorf(
			"%w: latest declaration check outcome",
			errInvalidStoredRegistryOrigin,
		)
	}

	if firstParticipatedAt.After(
		latestDeclarationCheckAt,
	) {
		return RegistryOrigin{}, fmt.Errorf(
			"%w: participation times",
			errInvalidStoredRegistryOrigin,
		)
	}

	if effectiveOutcomeText == nil {
		return RegistryOrigin{}, fmt.Errorf(
			"%w: authoritative declaration state",
			errInvalidStoredRegistryOrigin,
		)
	}

	effectiveOutcome, known := outcomeFromText[*effectiveOutcomeText]
	if !known ||
		effectiveOutcome == declaration.OutcomeUnavailable ||
		effectiveOutcome == declaration.OutcomeRobotsDenied {
		return RegistryOrigin{}, fmt.Errorf(
			"%w: authoritative declaration outcome",
			errInvalidStoredRegistryOrigin,
		)
	}

	var currentDeclaration *declaration.Declaration

	if effectiveOutcome == declaration.OutcomeValid {
		if effectiveVersion == nil ||
			effectiveIdentityText == nil {
			return RegistryOrigin{}, fmt.Errorf(
				"%w: current declaration",
				errInvalidStoredRegistryOrigin,
			)
		}

		effectiveIdentity, known := identityFromText[*effectiveIdentityText]
		if *effectiveVersion != 1 || !known {
			return RegistryOrigin{}, fmt.Errorf(
				"%w: current declaration",
				errInvalidStoredRegistryOrigin,
			)
		}

		currentDeclaration = &declaration.Declaration{
			Version:  *effectiveVersion,
			Identity: effectiveIdentity,
		}
	} else if effectiveVersion != nil ||
		effectiveIdentityText != nil {
		return RegistryOrigin{}, fmt.Errorf(
			"%w: non-valid authoritative declaration",
			errInvalidStoredRegistryOrigin,
		)
	}

	return RegistryOrigin{
		Origin:              source,
		FirstParticipatedAt: firstParticipatedAt.UTC(),
		InitialDeclaration: declaration.Declaration{
			Version:  initialVersion,
			Identity: initialIdentity,
		},
		LatestDeclarationCheckAt:      latestDeclarationCheckAt.UTC(),
		LatestDeclarationCheckOutcome: latestOutcome,
		CurrentDeclaration:            currentDeclaration,
	}, nil
}

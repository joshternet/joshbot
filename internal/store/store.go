// Package store persists declaration verification observations.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
)

// EffectiveState classifies current authoritative knowledge.
type EffectiveState uint8

const (
	// StateUnknown means no authoritative observation exists.
	StateUnknown EffectiveState = iota

	// StateVerified means the latest authoritative observation is valid.
	StateVerified

	// StateNotVerified means the latest authoritative observation is not
	// a valid supported declaration.
	StateNotVerified
)

var (
	errStoreUnavailable    = errors.New("store: unavailable")
	errPoolUnavailable     = errors.New("store: PostgreSQL pool is unavailable")
	errInvalidContext      = errors.New("store: context is nil")
	errInvalidOrigin       = errors.New("store: origin is invalid")
	errInvalidObservedAt   = errors.New("store: observation time is invalid")
	errInvalidResult       = errors.New("store: verification result is invalid")
	errInvalidHistoryLimit = errors.New("store: history limit is invalid")
)

// Observation is one semantic declaration-verification observation.
//
// Declaration is populated only when Outcome is declaration.OutcomeValid.
type Observation struct {
	Outcome     declaration.Outcome
	ObservedAt  time.Time
	Declaration declaration.Declaration
}

// Effective describes the latest authoritative declaration state.
type Effective struct {
	State       EffectiveState
	Observation Observation
}

// OriginState contains the latest operational knowledge for one origin.
type OriginState struct {
	Origin          origin.Origin
	FirstObservedAt time.Time
	Latest          Observation
	Effective       Effective
}

// Store persists declaration verification observations.
//
// The caller owns the supplied pool and remains responsible for closing it.
// Normal operation needs SELECT, INSERT, and UPDATE privileges. Schema
// migrations are intentionally separate and require their own DDL privileges.
type Store struct {
	pool *pgxpool.Pool
}

var outcomeToText = map[declaration.Outcome]string{
	declaration.OutcomeValid:               "valid",
	declaration.OutcomeAbsent:              "absent",
	declaration.OutcomeInvalid:             "invalid",
	declaration.OutcomeUnsupportedVersion:  "unsupported_version",
	declaration.OutcomeUnavailable:         "unavailable",
	declaration.OutcomeRobotsDenied:        "robots_denied",
	declaration.OutcomeCrossOriginRedirect: "cross_origin_redirect",
}

var outcomeFromText = map[string]declaration.Outcome{
	"valid":                 declaration.OutcomeValid,
	"absent":                declaration.OutcomeAbsent,
	"invalid":               declaration.OutcomeInvalid,
	"unsupported_version":   declaration.OutcomeUnsupportedVersion,
	"unavailable":           declaration.OutcomeUnavailable,
	"robots_denied":         declaration.OutcomeRobotsDenied,
	"cross_origin_redirect": declaration.OutcomeCrossOriginRedirect,
}

var identityToText = map[declaration.Identity]string{
	declaration.IdentityUndeclared: "undeclared",
	declaration.IdentityAffirmed:   "affirmed",
	declaration.IdentityDeclined:   "declined",
}

var identityFromText = map[string]declaration.Identity{
	"undeclared": declaration.IdentityUndeclared,
	"affirmed":   declaration.IdentityAffirmed,
	"declined":   declaration.IdentityDeclined,
}

// New constructs a store using a caller-owned PostgreSQL pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{
		pool: pool,
	}
}

// RecordVerification atomically records one completed verification.
func (s *Store) RecordVerification(
	ctx context.Context,
	observedAt time.Time,
	result declaration.Result,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}

	if result.Origin.String() == "" {
		return errInvalidOrigin
	}

	if observedAt.IsZero() {
		return errInvalidObservedAt
	}

	if !validVerificationResult(result) {
		return errInvalidResult
	}

	version, identity := storedDeclarationValues(result)

	err := pgx.BeginFunc(
		ctx,
		s.pool,
		func(tx pgx.Tx) error {
			_, execErr := tx.Exec(
				ctx,
				`
					WITH recorded_origin AS (
						INSERT INTO origins (
							origin,
							first_observed_at
						)
						VALUES ($1, $2)
						ON CONFLICT (origin) DO UPDATE
						SET first_observed_at = LEAST(
							origins.first_observed_at,
							EXCLUDED.first_observed_at
						)
						RETURNING origin
					)
					INSERT INTO verification_observations (
						origin,
						observed_at,
						outcome,
						version,
						identity
					)
					SELECT
						origin,
						$2,
						$3,
						$4,
						$5
					FROM recorded_origin
				`,
				result.Origin.String(),
				observedAt.UTC(),
				outcomeToText[result.Outcome],
				version,
				identity,
			)

			return execErr
		},
	)
	if err != nil {
		return fmt.Errorf(
			"store: record verification: %w",
			err,
		)
	}

	return nil
}

// OriginState returns the latest stored state for source.
//
// A false found result means the origin has no stored observations. An origin
// with observations but no authoritative observation returns found true and
// StateUnknown.
func (s *Store) OriginState(
	ctx context.Context,
	source origin.Origin,
) (OriginState, bool, error) {
	if err := s.validate(ctx); err != nil {
		return OriginState{}, false, err
	}

	if source.String() == "" {
		return OriginState{}, false, errInvalidOrigin
	}

	var (
		firstObservedAt      time.Time
		latestObservedAt     time.Time
		latestOutcomeText    string
		latestVersion        *int
		latestIdentity       *string
		effectiveObservedAt  *time.Time
		effectiveOutcomeText *string
		effectiveVersion     *int
		effectiveIdentity    *string
	)

	err := s.pool.QueryRow(
		ctx,
		`
			SELECT
				o.first_observed_at,
				latest.observed_at,
				latest.outcome,
				latest.version,
				latest.identity,
				effective.observed_at,
				effective.outcome,
				effective.version,
				effective.identity
			FROM origins AS o
			JOIN LATERAL (
				SELECT
					observed_at,
					outcome,
					version,
					identity
				FROM verification_observations
				WHERE origin = o.origin
				ORDER BY observed_at DESC, id DESC
				LIMIT 1
			) AS latest ON TRUE
			LEFT JOIN LATERAL (
				SELECT
					observed_at,
					outcome,
					version,
					identity
				FROM verification_observations
				WHERE origin = o.origin
					AND outcome IN (
						'valid',
						'absent',
						'invalid',
						'unsupported_version',
						'cross_origin_redirect'
					)
				ORDER BY observed_at DESC, id DESC
				LIMIT 1
			) AS effective ON TRUE
			WHERE o.origin = $1
		`,
		source.String(),
	).Scan(
		&firstObservedAt,
		&latestObservedAt,
		&latestOutcomeText,
		&latestVersion,
		&latestIdentity,
		&effectiveObservedAt,
		&effectiveOutcomeText,
		&effectiveVersion,
		&effectiveIdentity,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return OriginState{}, false, nil
	}

	if err != nil {
		return OriginState{}, false, fmt.Errorf(
			"store: query origin state: %w",
			err,
		)
	}

	effective := Effective{
		State: StateUnknown,
	}
	if effectiveOutcomeText != nil {
		effectiveOutcome := outcomeFromText[*effectiveOutcomeText]
		effective = Effective{
			State: effectiveStateForOutcome(effectiveOutcome),
			Observation: observationFromStored(
				*effectiveObservedAt,
				*effectiveOutcomeText,
				effectiveVersion,
				effectiveIdentity,
			),
		}
	}

	return OriginState{
		Origin:          source,
		FirstObservedAt: firstObservedAt.UTC(),
		Latest: observationFromStored(
			latestObservedAt,
			latestOutcomeText,
			latestVersion,
			latestIdentity,
		),
		Effective: effective,
	}, true, nil
}

func (s *Store) validate(ctx context.Context) error {
	if s == nil {
		return errStoreUnavailable
	}

	if ctx == nil {
		return errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if s.pool == nil {
		return errPoolUnavailable
	}

	return nil
}

func validVerificationResult(
	result declaration.Result,
) bool {
	if _, known := outcomeToText[result.Outcome]; !known {
		return false
	}

	if result.Outcome == declaration.OutcomeValid {
		return result.Declaration.Version == 1 &&
			validIdentity(result.Declaration.Identity)
	}

	return result.Declaration == (declaration.Declaration{})
}

func validIdentity(identity declaration.Identity) bool {
	switch identity {
	case declaration.IdentityUndeclared,
		declaration.IdentityAffirmed,
		declaration.IdentityDeclined:
		return true
	default:
		return false
	}
}

func storedDeclarationValues(
	result declaration.Result,
) (any, any) {
	if result.Outcome != declaration.OutcomeValid {
		return nil, nil
	}

	return result.Declaration.Version,
		identityToText[result.Declaration.Identity]
}

func observationFromStored(
	observedAt time.Time,
	outcomeText string,
	version *int,
	identityText *string,
) Observation {
	observation := Observation{
		Outcome:    outcomeFromText[outcomeText],
		ObservedAt: observedAt.UTC(),
	}

	if version != nil {
		observation.Declaration.Version = *version
	}

	if identityText != nil {
		observation.Declaration.Identity =
			identityFromText[*identityText]
	}

	return observation
}

func effectiveStateForOutcome(
	outcome declaration.Outcome,
) EffectiveState {
	if outcome == declaration.OutcomeValid {
		return StateVerified
	}

	return StateNotVerified
}

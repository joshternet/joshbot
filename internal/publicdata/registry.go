package publicdata

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/store"
)

var (
	// ErrInvalidRegistryOrigin means BuildRegistry received impossible
	// semantic input.
	ErrInvalidRegistryOrigin = errors.New(
		"publicdata: registry origin is invalid",
	)

	publicOutcomeTexts = map[declaration.Outcome]string{
		declaration.OutcomeValid:               "valid",
		declaration.OutcomeAbsent:              "absent",
		declaration.OutcomeInvalid:             "invalid",
		declaration.OutcomeUnsupportedVersion:  "unsupported_version",
		declaration.OutcomeUnavailable:         "unavailable",
		declaration.OutcomeRobotsDenied:        "robots_denied",
		declaration.OutcomeCrossOriginRedirect: "cross_origin_redirect",
	}
)

type historyRegistryDocument struct {
	FormatVersion int                    `json:"format_version"`
	Nodes         []historyRegistryEntry `json:"nodes"`
}

type historyRegistryEntry struct {
	Origin                        string               `json:"origin"`
	Path                          string               `json:"path"`
	FirstParticipatedAt           time.Time            `json:"first_participated_at"`
	InitialDeclaration            declarationDocument  `json:"initial_declaration"`
	LatestDeclarationCheckAt      time.Time            `json:"latest_declaration_check_at"`
	LatestDeclarationCheckOutcome string               `json:"latest_declaration_check_outcome"`
	Declaration                   *declarationDocument `json:"declaration,omitempty"`
}

type historyNodeDocument struct {
	FormatVersion                 int                  `json:"format_version"`
	Origin                        string               `json:"origin"`
	FirstParticipatedAt           time.Time            `json:"first_participated_at"`
	InitialDeclaration            declarationDocument  `json:"initial_declaration"`
	LatestDeclarationCheckAt      time.Time            `json:"latest_declaration_check_at"`
	LatestDeclarationCheckOutcome string               `json:"latest_declaration_check_outcome"`
	Declaration                   *declarationDocument `json:"declaration,omitempty"`
}

// BuildRegistry creates a deterministic logical public registry snapshot from
// durable participation state.
//
// BuildRegistry is pure: it performs no database, filesystem, network, clock,
// random, or environment operations.
func BuildRegistry(
	participants []store.RegistryOrigin,
) ([]File, error) {
	ordered := append(
		[]store.RegistryOrigin(nil),
		participants...,
	)
	seenOrigins := make(
		map[string]struct{},
		len(ordered),
	)

	for _, participant := range ordered {
		canonical := participant.Origin.String()

		if !validRegistryOrigin(participant) {
			return nil, fmt.Errorf(
				"%w",
				ErrInvalidRegistryOrigin,
			)
		}

		if _, exists := seenOrigins[canonical]; exists {
			return nil, fmt.Errorf(
				"%w: %s",
				ErrDuplicateOrigin,
				canonical,
			)
		}

		seenOrigins[canonical] = struct{}{}
	}

	sort.Slice(
		ordered,
		func(left, right int) bool {
			return ordered[left].Origin.String() <
				ordered[right].Origin.String()
		},
	)

	registry := historyRegistryDocument{
		FormatVersion: FormatVersion,
		Nodes: make(
			[]historyRegistryEntry,
			0,
			len(ordered),
		),
	}
	files := make(
		[]File,
		0,
		len(ordered)+1,
	)

	for _, participant := range ordered {
		canonical := participant.Origin.String()
		nodePath := pathForOrigin(canonical)

		initialDeclaration := projectDeclaration(
			participant.InitialDeclaration,
		)
		currentDeclaration := projectOptionalDeclaration(
			participant.CurrentDeclaration,
		)
		latestOutcome := publicOutcomeTexts[participant.LatestDeclarationCheckOutcome]

		firstParticipatedAt :=
			participant.FirstParticipatedAt.UTC()
		latestCheckAt :=
			participant.LatestDeclarationCheckAt.UTC()

		registry.Nodes = append(
			registry.Nodes,
			historyRegistryEntry{
				Origin:                        canonical,
				Path:                          nodePath,
				FirstParticipatedAt:           firstParticipatedAt,
				InitialDeclaration:            initialDeclaration,
				LatestDeclarationCheckAt:      latestCheckAt,
				LatestDeclarationCheckOutcome: latestOutcome,
				Declaration:                   currentDeclaration,
			},
		)

		files = append(
			files,
			File{
				Path: nodePath,
				Data: marshalPublicJSON(
					historyNodeDocument{
						FormatVersion:                 FormatVersion,
						Origin:                        canonical,
						FirstParticipatedAt:           firstParticipatedAt,
						InitialDeclaration:            initialDeclaration,
						LatestDeclarationCheckAt:      latestCheckAt,
						LatestDeclarationCheckOutcome: latestOutcome,
						Declaration:                   currentDeclaration,
					},
				),
			},
		)
	}

	files = append(
		files,
		File{
			Path: "registry.json",
			Data: marshalPublicJSON(
				registry,
			),
		},
	)

	sort.Slice(
		files,
		func(left, right int) bool {
			return files[left].Path <
				files[right].Path
		},
	)

	return files, nil
}

func validRegistryOrigin(
	participant store.RegistryOrigin,
) bool {
	if participant.Origin.String() == "" ||
		participant.FirstParticipatedAt.IsZero() ||
		participant.LatestDeclarationCheckAt.IsZero() ||
		participant.FirstParticipatedAt.After(
			participant.LatestDeclarationCheckAt,
		) ||
		!validRegistryDeclaration(
			participant.InitialDeclaration,
		) {
		return false
	}

	if participant.CurrentDeclaration != nil &&
		!validRegistryDeclaration(
			*participant.CurrentDeclaration,
		) {
		return false
	}

	switch participant.LatestDeclarationCheckOutcome {
	case declaration.OutcomeValid:
		return participant.CurrentDeclaration != nil

	case declaration.OutcomeAbsent,
		declaration.OutcomeInvalid,
		declaration.OutcomeUnsupportedVersion,
		declaration.OutcomeCrossOriginRedirect:
		return participant.CurrentDeclaration == nil

	case declaration.OutcomeUnavailable,
		declaration.OutcomeRobotsDenied:
		return true

	default:
		return false
	}
}

func validRegistryDeclaration(
	source declaration.Declaration,
) bool {
	return source.Version == 1 &&
		validPublicIdentity(source.Identity)
}

func projectOptionalDeclaration(
	source *declaration.Declaration,
) *declarationDocument {
	if source == nil {
		return nil
	}

	projected := projectDeclaration(*source)

	return &projected
}

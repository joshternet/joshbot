// Package publicdata builds JoshBot's deterministic public registry projection.
package publicdata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/store"
)

// FormatVersion is the JoshBot public registry artifact format version.
const FormatVersion = 1

var (
	// ErrInvalidVerifiedOrigin means Build received impossible semantic input.
	ErrInvalidVerifiedOrigin = errors.New(
		"publicdata: verified origin is invalid",
	)

	// ErrDuplicateOrigin means Build received the same canonical origin twice.
	ErrDuplicateOrigin = errors.New(
		"publicdata: duplicate origin",
	)
)

// File is one logical file in a deterministic public snapshot.
type File struct {
	Path string
	Data []byte
}

type registryDocument struct {
	FormatVersion int             `json:"format_version"`
	Nodes         []registryEntry `json:"nodes"`
}

type registryEntry struct {
	Origin      string              `json:"origin"`
	Path        string              `json:"path"`
	Declaration declarationDocument `json:"declaration"`
}

type nodeDocument struct {
	FormatVersion int                 `json:"format_version"`
	Origin        string              `json:"origin"`
	Declaration   declarationDocument `json:"declaration"`
}

type declarationDocument struct {
	Version int   `json:"version"`
	Josh    *bool `json:"josh,omitempty"`
}

// Build creates a deterministic logical public registry snapshot.
//
// Build is pure: it performs no database, filesystem, network, clock, random,
// or environment operations.
func Build(
	verified []store.VerifiedOrigin,
) ([]File, error) {
	ordered := append(
		[]store.VerifiedOrigin(nil),
		verified...,
	)
	seenOrigins := make(
		map[string]struct{},
		len(ordered),
	)

	for _, participant := range ordered {
		canonical := participant.Origin.String()
		if canonical == "" ||
			participant.Declaration.Version != 1 ||
			!validPublicIdentity(
				participant.Declaration.Identity,
			) {
			return nil, fmt.Errorf(
				"%w",
				ErrInvalidVerifiedOrigin,
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

	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].Origin.String() <
			ordered[right].Origin.String()
	})

	registry := registryDocument{
		FormatVersion: FormatVersion,
		Nodes: make(
			[]registryEntry,
			0,
			len(ordered),
		),
	}
	files := make([]File, 0, len(ordered)+1)

	for _, participant := range ordered {
		canonical := participant.Origin.String()
		nodePath := pathForOrigin(canonical)
		publicDeclaration := projectDeclaration(
			participant.Declaration,
		)

		registry.Nodes = append(
			registry.Nodes,
			registryEntry{
				Origin:      canonical,
				Path:        nodePath,
				Declaration: publicDeclaration,
			},
		)
		files = append(
			files,
			File{
				Path: nodePath,
				Data: marshalPublicJSON(
					nodeDocument{
						FormatVersion: FormatVersion,
						Origin:        canonical,
						Declaration:   publicDeclaration,
					},
				),
			},
		)
	}

	files = append(
		files,
		File{
			Path: "registry.json",
			Data: marshalPublicJSON(registry),
		},
	)

	sort.Slice(files, func(left, right int) bool {
		return files[left].Path < files[right].Path
	})

	return files, nil
}

func validPublicIdentity(
	identity declaration.Identity,
) bool {
	switch identity {
	case declaration.IdentityUndeclared,
		declaration.IdentityAffirmed,
		declaration.IdentityDeclined:
		return true
	default:
		return false
	}
}

func projectDeclaration(
	source declaration.Declaration,
) declarationDocument {
	projected := declarationDocument{
		Version: source.Version,
	}

	switch source.Identity {
	case declaration.IdentityAffirmed:
		value := true
		projected.Josh = &value
	case declaration.IdentityDeclined:
		value := false
		projected.Josh = &value
	}

	return projected
}

func pathForOrigin(canonical string) string {
	digest := sha256.Sum256([]byte(canonical))
	encoded := hex.EncodeToString(digest[:])

	return "nodes/" +
		encoded[:2] +
		"/" +
		encoded +
		".json"
}

func marshalPublicJSON(value any) []byte {
	data, _ := json.MarshalIndent(value, "", "  ")

	return append(data, '\n')
}

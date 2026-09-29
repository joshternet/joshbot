// Package publicdata builds JoshBot's deterministic public registry projection.
package publicdata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/joshternet/joshbot/internal/declaration"
)

// FormatVersion is the JoshBot public registry artifact format version.
const FormatVersion = 1

var (
	// ErrDuplicateOrigin means a registry projection contains the same
	// canonical origin more than once.
	ErrDuplicateOrigin = errors.New(
		"publicdata: duplicate origin",
	)
)

// File is one logical file in a deterministic public snapshot.
type File struct {
	Path string
	Data []byte
}

type declarationDocument struct {
	Version int   `json:"version"`
	Josh    *bool `json:"josh,omitempty"`
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

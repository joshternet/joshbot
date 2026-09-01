// Package declaration validates RFC-JOSH-0002 declarations.
package declaration

import (
	"bytes"
	json "encoding/json/v2"
	"unicode/utf8"

	"encoding/json/jsontext"
)

// MaxBodySize is JoshBot's maximum accepted declaration size.
//
// One KiB is 1024 bytes. This is a JoshBot resource-safety policy rather
// than an RFC-JOSH-0002 protocol limit.
const MaxBodySize = 64 * 1024

// ParseStatus classifies declaration parsing results.
type ParseStatus uint8

const (
	// ParseInvalid reports malformed JSON or invalid declaration semantics.
	ParseInvalid ParseStatus = iota

	// ParseValid reports a valid RFC-JOSH-0002 version 1 declaration.
	ParseValid

	// ParseUnsupportedVersion reports a valid integer version other than 1.
	ParseUnsupportedVersion
)

// Identity is the Josh identity state declared by a valid resource.
type Identity uint8

const (
	// IdentityUndeclared means the declaration omits the josh member.
	IdentityUndeclared Identity = iota

	// IdentityAffirmed means the declaration contains josh: true.
	IdentityAffirmed

	// IdentityDeclined means the declaration contains josh: false.
	IdentityDeclined
)

// Declaration is the semantic content of a valid declaration.
type Declaration struct {
	Version  int
	Identity Identity
}

// ParseResult contains the semantic result of declaration parsing.
//
// Declaration is populated only when Status is ParseValid.
type ParseResult struct {
	Status      ParseStatus
	Declaration Declaration
}

type wireDeclaration struct {
	Version jsontext.Value `json:"version,case:strict"`
	Josh    jsontext.Value `json:"josh,case:strict"`
}

// Parse validates an RFC-JOSH-0002 declaration.
//
// Unknown members are ignored. Duplicate names and invalid UTF-8 are rejected
// throughout the complete JSON document. JoshBot deliberately rejects an
// initial UTF-8 BOM rather than using RFC 8259's optional BOM tolerance.
func Parse(data []byte) ParseResult {
	if len(data) > MaxBodySize || !utf8.Valid(data) {
		return ParseResult{Status: ParseInvalid}
	}

	var wire wireDeclaration
	if err := json.Unmarshal(
		data,
		&wire,
		jsontext.AllowDuplicateNames(false),
		jsontext.AllowInvalidUTF8(false),
		json.MatchCaseInsensitiveNames(false),
	); err != nil {
		return ParseResult{Status: ParseInvalid}
	}

	version := bytes.TrimSpace(wire.Version)
	if !isJSONInteger(version) {
		return ParseResult{Status: ParseInvalid}
	}

	if !bytes.Equal(version, []byte("1")) {
		return ParseResult{
			Status: ParseUnsupportedVersion,
		}
	}

	identity := IdentityUndeclared
	josh := bytes.TrimSpace(wire.Josh)
	if len(josh) > 0 {
		switch {
		case bytes.Equal(josh, []byte("true")):
			identity = IdentityAffirmed
		case bytes.Equal(josh, []byte("false")):
			identity = IdentityDeclined
		default:
			return ParseResult{Status: ParseInvalid}
		}
	}

	return ParseResult{
		Status: ParseValid,
		Declaration: Declaration{
			Version:  1,
			Identity: identity,
		},
	}
}

func isJSONInteger(value []byte) bool {
	if len(value) == 0 {
		return false
	}

	if value[0] == '-' {
		value = value[1:]
	}

	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}

	return len(value) > 0
}

// Package operatortoken validates private operator bearer-token strength.
package operatortoken

import "strings"

const (
	// MinLength is the shortest accepted operator bearer token.
	MinLength = 32

	// MaxLength is the longest accepted operator bearer token.
	MaxLength = 4096
)

// Valid reports whether token meets operator bearer-token strength rules:
// length in [MinLength, MaxLength], no surrounding whitespace, printable
// ASCII only ('!' through '~'), and at least eight distinct characters.
func Valid(token string) bool {
	if len(token) < MinLength || len(token) > MaxLength ||
		strings.TrimSpace(token) != token ||
		strings.ContainsAny(token, "\r\n") {
		return false
	}

	unique := make(map[rune]struct{})
	for _, character := range token {
		if character < '!' || character > '~' {
			return false
		}
		unique[character] = struct{}{}
	}

	return len(unique) >= 8
}

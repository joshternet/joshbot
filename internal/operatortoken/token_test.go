package operatortoken

import (
	"strings"
	"testing"
)

func TestValidAcceptsStrongToken(t *testing.T) {
	token := "0123456789abcdef0123456789abcdef"
	if !Valid(token) {
		t.Fatalf("Valid(%q) = false", token)
	}
}

func TestValidRejectsWeakTokens(t *testing.T) {
	tests := []string{
		"",
		"short",
		strings.Repeat(" ", MinLength),
		strings.Repeat("x", MinLength),
		"0123456789abcdef\n0123456789abcdef",
		"0123456789abcdef\r0123456789abcdef",
		"0123456789abcdef0123456789abcde\x7f",
		" 0123456789abcdef0123456789abcde",
		"0123456789abcdef0123456789abcde ",
		strings.Repeat("01234567", (MaxLength/8)+1),
	}

	for _, token := range tests {
		if Valid(token) {
			t.Errorf("Valid(%q) = true", token)
		}
	}
}

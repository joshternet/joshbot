package personalsite

import (
	"fmt"
	"strings"
)

// ScoringMode controls whether personal-site scores affect admission.
type ScoringMode string

const (
	// ScoringModeOff disables qualification and admission ordering.
	ScoringModeOff ScoringMode = "off"
	// ScoringModeShadow qualifies and persists without changing admission order.
	ScoringModeShadow ScoringMode = "shadow"
	// ScoringModeOrder qualifies and orders automatic admission by score.
	ScoringModeOrder ScoringMode = "order"
)

// ParseScoringMode maps an environment value to ScoringMode.
//
// Empty values default to off.
//
// @param raw - Environment string.
// @returns Parsed mode.
// @throws error when raw is not a known mode.
func ParseScoringMode(raw string) (ScoringMode, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "", string(ScoringModeOff):
		return ScoringModeOff, nil
	case string(ScoringModeShadow):
		return ScoringModeShadow, nil
	case string(ScoringModeOrder):
		return ScoringModeOrder, nil
	default:
		return "", fmt.Errorf(
			"personalsite: unknown scoring mode %q",
			raw,
		)
	}
}

// Active reports whether qualification should run.
//
// @returns true for shadow and order modes.
func (mode ScoringMode) Active() bool {
	switch mode {
	case ScoringModeShadow, ScoringModeOrder:
		return true
	case ScoringModeOff, "":
		return false
	default:
		return false
	}
}

// OrdersAdmission reports whether PendingAutomaticCandidates should rank by
// score.
//
// @returns true only for order mode.
func (mode ScoringMode) OrdersAdmission() bool {
	return mode == ScoringModeOrder
}

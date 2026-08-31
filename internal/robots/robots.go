// Package robots interprets robots exclusion policies for JoshBot.
package robots

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// ProductToken is JoshBot's stable RFC 9309 product token.
const ProductToken = "Joshternet-Joshbot"

type rule struct {
	pattern     string
	allow       bool
	specificity int
}

type group struct {
	userAgents []string
	rules      []rule
	hasRules   bool
}

// Policy is the robots exclusion policy applicable to JoshBot.
type Policy struct {
	rules []rule
}

// Parse interprets robots exclusion rules applicable to JoshBot.
func Parse(data []byte) Policy {
	groups := parseGroups(data)

	var exactRules []rule
	var wildcardRules []rule
	exactMatched := false

	for _, candidate := range groups {
		exact := false
		wildcard := false

		for _, userAgent := range candidate.userAgents {
			switch {
			case strings.EqualFold(userAgent, ProductToken):
				exact = true
			case userAgent == "*":
				wildcard = true
			}
		}

		if exact {
			exactMatched = true
			exactRules = append(exactRules, candidate.rules...)
		} else if wildcard {
			wildcardRules = append(
				wildcardRules,
				candidate.rules...,
			)
		}
	}

	if exactMatched {
		return Policy{rules: exactRules}
	}

	return Policy{rules: wildcardRules}
}

func parseGroups(data []byte) []group {
	var groups []group
	var current *group

	for _, line := range strings.Split(string(data), "\n") {
		field, value, ok := parseRecord(line)
		if !ok {
			continue
		}

		switch {
		case strings.EqualFold(field, "user-agent"):
			if validProductToken(value) {
				if current == nil || current.hasRules {
					groups = append(groups, group{})
					current = &groups[len(groups)-1]
				}

				current.userAgents = append(
					current.userAgents,
					value,
				)
			}
		case strings.EqualFold(field, "allow") ||
			strings.EqualFold(field, "disallow"):
			if current != nil {
				current.hasRules = true

				parsedRule, valid := makeRule(
					strings.EqualFold(field, "allow"),
					value,
				)
				if valid {
					current.rules = append(
						current.rules,
						parsedRule,
					)
				}
			}
		}
	}

	return groups
}

func makeRule(allow bool, pattern string) (rule, bool) {
	if pattern == "" {
		return rule{}, false
	}

	// RFC 9309's published ABNF requires a leading slash, but its own
	// documented *.gif$ example requires a leading wildcard. Erratum 7995
	// remains reported, so JoshBot accepts both forms.
	if pattern[0] != '/' && pattern[0] != '*' {
		return rule{}, false
	}

	normalized, ok := normalizeMatchValue(pattern, false)
	if !ok {
		return rule{}, false
	}

	return rule{
		pattern:     normalized,
		allow:       allow,
		specificity: len(normalized),
	}, true
}

func parseRecord(line string) (string, string, bool) {
	line = strings.TrimSuffix(line, "\r")
	if !utf8.ValidString(line) || hasControlCharacter(line) {
		return "", "", false
	}

	line, _, _ = strings.Cut(line, "#")
	field, value, found := strings.Cut(line, ":")
	if !found {
		return "", "", false
	}

	field = strings.Trim(field, " \t")
	value = strings.Trim(value, " \t")

	return field, value, true
}

func hasControlCharacter(value string) bool {
	return strings.IndexFunc(value, func(character rune) bool {
		return character < ' ' && character != '\t' ||
			character == '\x7f'
	}) >= 0
}

func validProductToken(token string) bool {
	if token == "*" {
		return true
	}

	for _, character := range token {
		if (character < 'A' || character > 'Z') &&
			(character < 'a' || character > 'z') &&
			character != '-' &&
			character != '_' {
			return false
		}
	}

	return token != ""
}

// Allowed reports whether target is allowed by the policy.
//
// Invalid or unsupported targets fail closed.
func (p Policy) Allowed(target *url.URL) bool {
	targetValue, ok := targetMatchValue(target)
	if !ok {
		return false
	}

	if targetValue == "/robots.txt" {
		return true
	}

	allowed := true
	bestSpecificity := -1

	for _, candidate := range p.rules {
		if !patternMatches(candidate.pattern, targetValue) {
			continue
		}

		if candidate.specificity > bestSpecificity ||
			candidate.specificity == bestSpecificity &&
				candidate.allow {
			bestSpecificity = candidate.specificity
			allowed = candidate.allow
		}
	}

	return allowed
}

func targetMatchValue(target *url.URL) (string, bool) {
	if target == nil ||
		(target.Scheme != "http" && target.Scheme != "https") ||
		target.Host == "" ||
		target.User != nil {
		return "", false
	}

	value := target.EscapedPath()
	if value == "" {
		value = "/"
	}

	if target.ForceQuery || target.RawQuery != "" {
		value += "?" + target.RawQuery
	}

	return normalizeMatchValue(value, true)
}

func normalizeMatchValue(
	value string,
	encodeSpecial bool,
) (string, bool) {
	var normalized strings.Builder
	normalized.Grow(len(value))

	for index := 0; index < len(value); {
		character := value[index]

		switch {
		case character == '%':
			if index+2 >= len(value) ||
				!isHex(value[index+1]) ||
				!isHex(value[index+2]) {
				return "", false
			}

			octet := hexValue(value[index+1])<<4 |
				hexValue(value[index+2])
			if isUnreserved(octet) {
				normalized.WriteByte(octet)
			} else {
				writePercentEncoded(&normalized, octet)
			}

			index += 3
		case character >= utf8.RuneSelf:
			decoded, size := utf8.DecodeRuneInString(value[index:])
			if decoded == utf8.RuneError && size == 1 {
				return "", false
			}

			for _, octet := range []byte(value[index : index+size]) {
				writePercentEncoded(&normalized, octet)
			}

			index += size
		case character < ' ' || character == '\x7f':
			return "", false
		case encodeSpecial &&
			(character == '*' || character == '$'):
			writePercentEncoded(&normalized, character)
			index++
		default:
			normalized.WriteByte(character)
			index++
		}
	}

	return normalized.String(), true
}

func patternMatches(pattern string, target string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	if anchored {
		pattern = strings.TrimSuffix(pattern, "$")
	}

	patternIndex := 0
	targetIndex := 0
	starIndex := -1
	starTargetIndex := 0

	for targetIndex < len(target) {
		switch {
		case patternIndex < len(pattern) &&
			pattern[patternIndex] == '*':
			starIndex = patternIndex
			patternIndex++
			starTargetIndex = targetIndex
		case patternIndex < len(pattern) &&
			pattern[patternIndex] == target[targetIndex]:
			patternIndex++
			targetIndex++
		case starIndex >= 0:
			patternIndex = starIndex + 1
			starTargetIndex++
			targetIndex = starTargetIndex
		default:
			return false
		}

		if !anchored && patternIndex == len(pattern) {
			return true
		}
	}

	for patternIndex < len(pattern) &&
		pattern[patternIndex] == '*' {
		patternIndex++
	}

	return patternIndex == len(pattern)
}

func isHex(character byte) bool {
	return character >= '0' && character <= '9' ||
		character >= 'A' && character <= 'F' ||
		character >= 'a' && character <= 'f'
}

func hexValue(character byte) byte {
	switch {
	case character >= '0' && character <= '9':
		return character - '0'
	case character >= 'A' && character <= 'F':
		return character - 'A' + 10
	default:
		return character - 'a' + 10
	}
}

func isUnreserved(character byte) bool {
	return character >= 'A' && character <= 'Z' ||
		character >= 'a' && character <= 'z' ||
		character >= '0' && character <= '9' ||
		character == '-' ||
		character == '.' ||
		character == '_' ||
		character == '~'
}

func writePercentEncoded(
	destination *strings.Builder,
	character byte,
) {
	const hexadecimal = "0123456789ABCDEF"

	destination.WriteByte('%')
	destination.WriteByte(hexadecimal[character>>4])
	destination.WriteByte(hexadecimal[character&0x0f])
}

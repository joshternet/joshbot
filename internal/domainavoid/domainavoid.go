// Goal & Constraints: Operator domain-avoid rules match hostnames by exact
// suffix, public-suffix family, hostname glob strings, or explicit RE2 regex.
// Inputs are lowercase durable patterns without commas. Invalid patterns fail
// closed at write time. Matching never panics on a stored pattern.
//
// Acceptance: example.com and blogspot.* keep prior semantics; cbd-hash* and
// re:^cbd-hash match spam hosts; add/remove use the same normalized pattern.
package domainavoid

import (
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// RegexPrefix marks an RE2 pattern matched against the lowercase hostname.
	RegexPrefix = "re:"

	maxDomainPatternBytes = 253
	maxRegexPatternBytes  = 512
)

// Normalize trims and lowercases a rule. Trailing dots are removed for domain,
// family, and glob forms only so regex bodies keep escaped dots intact.
func Normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(value, RegexPrefix) {
		return value
	}
	return strings.TrimSuffix(value, ".")
}

// Valid reports whether one durable avoid pattern is acceptable.
func Valid(pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || strings.Contains(pattern, ",") {
		return false
	}
	if strings.ToLower(pattern) != pattern {
		return false
	}

	switch {
	case strings.HasPrefix(pattern, RegexPrefix):
		return validRegexPattern(pattern)
	case isFamilyPattern(pattern):
		return validFamilyPattern(pattern)
	case strings.Contains(pattern, ".*"):
		// `.*` is reserved for public-suffix families such as blogspot.*.
		return false
	case strings.Contains(pattern, "*"):
		return validGlobPattern(pattern)
	default:
		return validDomainPattern(pattern)
	}
}

// Matches reports whether hostname is excluded by pattern.
// Hostname matching is case-insensitive; invalid patterns never match.
func Matches(hostname, pattern string) bool {
	hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	pattern = strings.TrimSpace(pattern)
	if hostname == "" || pattern == "" || !Valid(pattern) {
		return false
	}

	switch {
	case strings.HasPrefix(pattern, RegexPrefix):
		// Valid already verified the RE2 body compiles.
		return regexp.MustCompile(pattern[len(RegexPrefix):]).
			MatchString(hostname)
	case isFamilyPattern(pattern):
		family := strings.TrimSuffix(pattern, ".*")
		return strings.HasPrefix(hostname, family+".") ||
			strings.Contains(hostname, "."+family+".")
	case strings.Contains(pattern, "*"):
		matched, err := path.Match(pattern, hostname)
		return err == nil && matched
	default:
		return hostname == pattern || strings.HasSuffix(hostname, "."+pattern)
	}
}

// ValidList validates a comma-separated emergency exclusion list.
func ValidList(value string) bool {
	for _, pattern := range strings.Split(value, ",") {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if !Valid(pattern) {
			return false
		}
	}
	return true
}

// IsRegex reports whether pattern is an explicit RE2 avoid rule.
func IsRegex(pattern string) bool {
	return strings.HasPrefix(strings.TrimSpace(pattern), RegexPrefix)
}

// NonRegexPatterns returns domain, family, and glob rules for SQL evaluation.
// Explicit re: rules stay on the Go Matches path only.
func NonRegexPatterns(patterns []string) []string {
	filtered := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || IsRegex(pattern) {
			continue
		}
		filtered = append(filtered, pattern)
	}
	return filtered
}

func isFamilyPattern(pattern string) bool {
	if !strings.HasSuffix(pattern, ".*") {
		return false
	}
	family := strings.TrimSuffix(pattern, ".*")
	return family != "" && !strings.Contains(family, "*")
}

func validRegexPattern(pattern string) bool {
	body := pattern[len(RegexPrefix):]
	if body == "" ||
		len(pattern) > maxRegexPatternBytes ||
		!utf8.ValidString(pattern) ||
		strings.ContainsRune(pattern, 0) {
		return false
	}
	_, err := regexp.Compile(body)
	return err == nil
}

func validFamilyPattern(pattern string) bool {
	family := strings.TrimSuffix(pattern, ".*")
	return validDomainPattern(family)
}

func validGlobPattern(pattern string) bool {
	if pattern == "*" ||
		len(pattern) > maxDomainPatternBytes ||
		strings.HasPrefix(pattern, ".") ||
		strings.HasSuffix(pattern, ".") ||
		strings.ContainsAny(pattern, `/:@?#_%\`) {
		return false
	}
	for _, part := range strings.Split(pattern, "*") {
		if part == "" {
			continue
		}
		for _, label := range strings.Split(part, ".") {
			if label == "" {
				continue
			}
			if !validDNSLabel(label) {
				return false
			}
		}
	}
	_, err := path.Match(pattern, "example.com")
	return err == nil
}

func validDomainPattern(pattern string) bool {
	if pattern == "" ||
		len(pattern) > maxDomainPatternBytes ||
		strings.HasPrefix(pattern, ".") ||
		strings.HasSuffix(pattern, ".") ||
		strings.ContainsAny(pattern, `/:@?#*_%\`) {
		return false
	}
	for _, label := range strings.Split(pattern, ".") {
		if !validDNSLabel(label) {
			return false
		}
	}
	return true
}

func validDNSLabel(label string) bool {
	if len(label) < 1 || len(label) > 63 {
		return false
	}
	if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
		return false
	}
	for i := 0; i < len(label); i++ {
		character := label[i]
		isAlphaNum := (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9')
		if !isAlphaNum && character != '-' {
			return false
		}
	}
	return true
}

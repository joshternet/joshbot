package domainavoid

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{" Example.COM. ", "example.com"},
		{"re:^CBD-Hash", "re:^cbd-hash"},
		{"re:foo\\.bar", "re:foo\\.bar"},
		{"cbd-hash*", "cbd-hash*"},
	}

	for _, test := range tests {
		if got := Normalize(test.in); got != test.want {
			t.Errorf("Normalize(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

func TestValidAndMatches(t *testing.T) {
	t.Parallel()

	valid := []string{
		"example.com",
		"blogspot.*",
		"cbd-hash*",
		"*bloginder.com",
		"*cbd-hash*",
		"a..b*",
		"re:^cbd-hash",
		`re:^cbd-hash[0-9]+\.`,
	}
	for _, pattern := range valid {
		if !Valid(pattern) {
			t.Errorf("Valid(%q) = false", pattern)
		}
	}

	invalid := []string{
		"",
		"*",
		"Example.com",
		".example.com",
		"example.com.",
		"https://example.com",
		"foo,bar",
		"blogspot.*.example",
		"foo_bar.example",
		"re:",
		"re:[",
		"re:foo,bar",
		"bad?.example",
		"a*.-b",
		"a*.b-",
		"bad!.com",
		"example..com",
		"-bad.com",
		"bad-.com",
		strings.Repeat("a", 64) + ".com",
		"cbd*" + strings.Repeat("a", 64),
		strings.Repeat("a", maxDomainPatternBytes+1),
	}
	for _, pattern := range invalid {
		if Valid(pattern) {
			t.Errorf("Valid(%q) = true", pattern)
		}
	}

	matches := []struct {
		hostname string
		pattern  string
	}{
		{"www.example.com", "example.com"},
		{"example.com", "example.com"},
		{"writer.blogspot.com", "blogspot.*"},
		{"foo.blogspot.co.uk", "blogspot.*"},
		{"cbd-hash27174.ampedpages.com", "cbd-hash*"},
		{"cbd-hash27174.ampedpages.com", "re:^cbd-hash"},
		{"cbd-hash81104.ampedpages.com", `re:^cbd-hash[0-9]+\.`},
		{"mirror.bloginder.com", "*bloginder.com"},
	}
	for _, test := range matches {
		if !Matches(test.hostname, test.pattern) {
			t.Errorf("Matches(%q, %q) = false", test.hostname, test.pattern)
		}
	}

	nonMatches := []struct {
		hostname string
		pattern  string
	}{
		{"example.org", "example.com"},
		{"notblogspot.com", "blogspot.*"},
		{"xcbd-hash.example.com", "cbd-hash*"},
		{"xcbd-hash.example.com", "re:^cbd-hash"},
		{"safe.example.com", "re:["},
	}
	for _, test := range nonMatches {
		if Matches(test.hostname, test.pattern) {
			t.Errorf("Matches(%q, %q) = true", test.hostname, test.pattern)
		}
	}
}

func TestValidList(t *testing.T) {
	t.Parallel()

	if !ValidList("example.com, blogspot.*, cbd-hash*, re:^cbd-hash") {
		t.Fatal("ValidList() = false")
	}
	if !ValidList("example.com,, blogspot.*") {
		t.Fatal("ValidList(empty segment) = false")
	}
	if ValidList("example.com, re:[") {
		t.Fatal("ValidList(invalid regex) = true")
	}
}

func TestNonRegexPatterns(t *testing.T) {
	t.Parallel()

	got := NonRegexPatterns([]string{
		"example.com",
		"re:^cbd-hash",
		"cbd-hash*",
		"",
		"blogspot.*",
	})
	want := []string{"example.com", "cbd-hash*", "blogspot.*"}
	if len(got) != len(want) {
		t.Fatalf("NonRegexPatterns() = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("NonRegexPatterns() = %#v, want %#v", got, want)
		}
	}
}

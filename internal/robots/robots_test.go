package robots

import (
	"net/url"
	"testing"
)

func TestPolicyDisallowsJoshBotPath(t *testing.T) {
	policy := Parse([]byte(
		"User-agent: Joshternet-Joshbot\n" +
			"Disallow: /private\n",
	))

	tests := []struct {
		name   string
		rawURL string
		want   bool
	}{
		{
			name:   "disallowed prefix",
			rawURL: "https://example.com/private/page",
			want:   false,
		},
		{
			name:   "unmatched path",
			rawURL: "https://example.com",
			want:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := mustTarget(t, test.rawURL)

			if got := policy.Allowed(target); got != test.want {
				t.Errorf(
					"Policy.Allowed(%q) = %t, want %t",
					test.rawURL,
					got,
					test.want,
				)
			}
		})
	}
}

func TestPolicySelectsJoshBotGroups(t *testing.T) {
	const mergedExactGroups = `
User-agent: Joshternet-Joshbot
Disallow: /one

User-agent: OtherBot
Disallow: /other

User-agent: JOSHTERNET-JOSHBOT
Disallow: /two
`

	const mergedWildcardGroups = `
User-agent: *
Disallow: /one

User-agent: OtherBot
Disallow: /other

User-agent: *
Disallow: /two
`

	tests := []struct {
		name   string
		robots string
		rawURL string
		want   bool
	}{
		{
			name: "exact token match is case insensitive",
			robots: `
User-agent: JOSHTERNET-JOSHBOT
Disallow: /case
`,
			rawURL: "https://example.com/case/page",
			want:   false,
		},
		{
			name: "consecutive user agents share a group",
			robots: `
User-agent: OtherBot
User-agent: Joshternet-Joshbot
Disallow: /shared
`,
			rawURL: "https://example.com/shared/page",
			want:   false,
		},
		{
			name:   "first exact group is merged",
			robots: mergedExactGroups,
			rawURL: "https://example.com/one/page",
			want:   false,
		},
		{
			name:   "second exact group is merged",
			robots: mergedExactGroups,
			rawURL: "https://example.com/two/page",
			want:   false,
		},
		{
			name: "wildcard is fallback",
			robots: `
User-agent: *
Disallow: /everybody
`,
			rawURL: "https://example.com/everybody/page",
			want:   false,
		},
		{
			name:   "first wildcard group is merged",
			robots: mergedWildcardGroups,
			rawURL: "https://example.com/one/page",
			want:   false,
		},
		{
			name:   "second wildcard group is merged",
			robots: mergedWildcardGroups,
			rawURL: "https://example.com/two/page",
			want:   false,
		},
		{
			name: "exact group excludes wildcard rules",
			robots: `
User-agent: *
Disallow: /everybody

User-agent: Joshternet-Joshbot
Disallow: /private
`,
			rawURL: "https://example.com/everybody/page",
			want:   true,
		},
		{
			name: "empty exact group excludes wildcard rules",
			robots: `
User-agent: *
Disallow: /

User-agent: Joshternet-Joshbot
`,
			rawURL: "https://example.com/private/page",
			want:   true,
		},
		{
			name: "unrelated user agent is ignored",
			robots: `
User-agent: OtherBot
Disallow: /other
`,
			rawURL: "https://example.com/other/page",
			want:   true,
		},
		{
			name:   "no groups allows access",
			robots: "Sitemap: https://example.com/sitemap.xml\n",
			rawURL: "https://example.com/page",
			want:   true,
		},
		{
			name: "comma-separated extension is not adopted",
			robots: `
User-agent: OtherBot, Joshternet-Joshbot
Disallow: /comma
`,
			rawURL: "https://example.com/comma/page",
			want:   true,
		},
		{
			name: "empty user-agent is invalid",
			robots: `
User-agent:
Disallow: /empty
`,
			rawURL: "https://example.com/empty/page",
			want:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := Parse([]byte(test.robots))
			target := mustTarget(t, test.rawURL)

			if got := policy.Allowed(target); got != test.want {
				t.Errorf(
					"Policy.Allowed(%q) = %t, want %t",
					test.rawURL,
					got,
					test.want,
				)
			}
		})
	}
}

func TestParseUsesParseableRecords(t *testing.T) {
	invalidUTF8 := []byte(
		"User-agent: Joshternet-Joshbot\n" +
			"Disallow: /before\n",
	)
	invalidUTF8 = append(invalidUTF8, 0xff)
	invalidUTF8 = append(
		invalidUTF8,
		[]byte("\nDisallow: /after\n")...,
	)

	tests := []struct {
		name   string
		robots []byte
		rawURL string
		want   bool
	}{
		{
			name: "field case whitespace comments and CRLF",
			robots: []byte(
				" # full-line comment\r\n" +
					" \tUsEr-AgEnT\t:\t" +
					"Joshternet-Joshbot\t# bot\r\n" +
					" \tDiSaLlOw\t:\t/private\t" +
					"# private content\r\n",
			),
			rawURL: "https://example.com/private/page",
			want:   false,
		},
		{
			name: "unknown records do not terminate group",
			robots: []byte(
				"User-agent: Joshternet-Joshbot\n" +
					"Disallow: /before\n" +
					"Sitemap: https://example.com/sitemap.xml\n" +
					"Crawl-delay: 10\n" +
					"Host: example.com\n" +
					"Something-Else: value\n" +
					"Disallow: /after\n",
			),
			rawURL: "https://example.com/after/page",
			want:   false,
		},
		{
			name: "rule before first group is ignored",
			robots: []byte(
				"Disallow: /ignore-this\n" +
					"User-agent: Joshternet-Joshbot\n" +
					"Disallow: /real-rule\n",
			),
			rawURL: "https://example.com/ignore-this/page",
			want:   true,
		},
		{
			name: "valid grouped rule remains effective",
			robots: []byte(
				"Disallow: /ignore-this\n" +
					"User-agent: Joshternet-Joshbot\n" +
					"Disallow: /real-rule\n",
			),
			rawURL: "https://example.com/real-rule/page",
			want:   false,
		},
		{
			name: "malformed line preserves earlier rule",
			robots: []byte(
				"User-agent: Joshternet-Joshbot\n" +
					"Disallow: /before\n" +
					"this line has no colon\n" +
					"Disallow: /after\n",
			),
			rawURL: "https://example.com/before/page",
			want:   false,
		},
		{
			name: "malformed line preserves later rule",
			robots: []byte(
				"User-agent: Joshternet-Joshbot\n" +
					"Disallow: /before\n" +
					"this line has no colon\n" +
					"Disallow: /after\n",
			),
			rawURL: "https://example.com/after/page",
			want:   false,
		},
		{
			name: "empty directives have no matching rule",
			robots: []byte(
				"User-agent: Joshternet-Joshbot\n" +
					"Disallow:\n" +
					"Allow:\n",
			),
			rawURL: "https://example.com/private/page",
			want:   true,
		},
		{
			name:   "invalid UTF-8 preserves earlier rule",
			robots: invalidUTF8,
			rawURL: "https://example.com/before/page",
			want:   false,
		},
		{
			name:   "invalid UTF-8 preserves later rule",
			robots: invalidUTF8,
			rawURL: "https://example.com/after/page",
			want:   false,
		},
		{
			name: "control byte line is ignored",
			robots: []byte(
				"User-agent: Joshternet-Joshbot\n" +
					"Disallow: /ignored\x00path\n" +
					"Disallow: /effective\n",
			),
			rawURL: "https://example.com/effective/page",
			want:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := Parse(test.robots)
			target := mustTarget(t, test.rawURL)

			if got := policy.Allowed(target); got != test.want {
				t.Errorf(
					"Policy.Allowed(%q) = %t, want %t",
					test.rawURL,
					got,
					test.want,
				)
			}
		})
	}
}

func mustTarget(t *testing.T, rawURL string) *url.URL {
	t.Helper()

	target, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf(
			"url.Parse(%q) error = %v, want nil",
			rawURL,
			err,
		)
	}

	return target
}

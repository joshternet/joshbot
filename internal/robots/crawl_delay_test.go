package robots

import (
	"errors"
	"net/url"
	"testing"
	"time"
)

func TestPolicyCrawlDelaySelection(t *testing.T) {
	tests := []struct {
		name   string
		robots string
		want   time.Duration
	}{
		{
			name: "missing crawl delay",
			robots: `
User-agent: Joshternet-Joshbot
Disallow: /private
`,
			want: 0,
		},
		{
			name: "wildcard crawl delay is fallback",
			robots: `
User-agent: *
Crawl-delay: 4
`,
			want: 4 * time.Second,
		},
		{
			name: "JoshBot crawl delay overrides wildcard",
			robots: `
User-agent: *
Crawl-delay: 10

User-agent: Joshternet-Joshbot
Crawl-delay: 2
`,
			want: 2 * time.Second,
		},
		{
			name: "JoshBot group without delay excludes wildcard delay",
			robots: `
User-agent: *
Crawl-delay: 10

User-agent: Joshternet-Joshbot
Disallow: /private
`,
			want: 0,
		},
		{
			name: "malformed wildcard does not leak into exact group",
			robots: `
User-agent: *
Crawl-delay: garbage

User-agent: Joshternet-Joshbot
Disallow: /private
`,
			want: 0,
		},
		{
			name: "fractional crawl delay",
			robots: `
User-agent: Joshternet-Joshbot
Crawl-delay: 1.5
`,
			want: 1500 * time.Millisecond,
		},
		{
			name: "field case whitespace comments and CRLF",
			robots: " \tUsEr-AgEnT\t:\tJoshternet-Joshbot\t# bot\r\n" +
				" \tCrAwL-DeLaY\t:\t1.25\t# seconds\r\n",
			want: 1250 * time.Millisecond,
		},
		{
			name: "zero crawl delay",
			robots: `
User-agent: Joshternet-Joshbot
Crawl-delay: 0
`,
			want: 0,
		},
		{
			name: "maximum duration",
			robots: `
User-agent: Joshternet-Joshbot
Crawl-delay: 9223372036.854775807
`,
			want: time.Duration(1<<63 - 1),
		},
		{
			name: "crawl delay before first group is ignored",
			robots: `
Crawl-delay: 99
User-agent: Joshternet-Joshbot
Crawl-delay: 2
`,
			want: 2 * time.Second,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := Parse([]byte(test.robots))

			got, err := policy.CrawlDelay()
			if err != nil {
				t.Fatalf(
					"Policy.CrawlDelay() error = %v, want nil",
					err,
				)
			}

			if got != test.want {
				t.Errorf(
					"Policy.CrawlDelay() = %s, want %s",
					got,
					test.want,
				)
			}
		})
	}
}

func TestPolicyCrawlDelayRejectsMalformedValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "negative", value: "-1"},
		{name: "explicit plus sign", value: "+1"},
		{name: "duration unit", value: "5s"},
		{name: "exponent", value: "1e3"},
		{name: "not a number", value: "NaN"},
		{name: "infinity", value: "Inf"},
		{name: "multiple decimal points", value: "1.2.3"},
		{name: "space inside number", value: "1 .5"},
		{name: "missing integer part", value: ".5"},
		{name: "missing fractional part", value: "5."},
		{name: "sub nanosecond precision", value: "0.1234567891"},
		{
			name:  "unsigned integer overflow",
			value: "999999999999999999999999999999999999",
		},
		{name: "duration seconds overflow", value: "10000000000"},
		{
			name:  "duration fractional overflow",
			value: "9223372036.854775808",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := Parse([]byte(
				"User-agent: Joshternet-Joshbot\n" +
					"Crawl-delay: " + test.value + "\n",
			))

			got, err := policy.CrawlDelay()
			if !errors.Is(err, ErrInvalidCrawlDelay) {
				t.Fatalf(
					"Policy.CrawlDelay() error = %v, want %v",
					err,
					ErrInvalidCrawlDelay,
				)
			}

			if got != 0 {
				t.Errorf(
					"Policy.CrawlDelay() = %s on error, want 0",
					got,
				)
			}
		})
	}
}

func TestPolicyCrawlDelayRepeatedDirectives(t *testing.T) {
	tests := []struct {
		name    string
		robots  string
		want    time.Duration
		wantErr bool
	}{
		{
			name: "largest repeated directive wins",
			robots: `
User-agent: Joshternet-Joshbot
Crawl-delay: 2
Crawl-delay: 7
Crawl-delay: 3
`,
			want: 7 * time.Second,
		},
		{
			name: "largest delay across repeated exact groups wins",
			robots: `
User-agent: Joshternet-Joshbot
Crawl-delay: 3

User-agent: OtherBot
Crawl-delay: 20

User-agent: JOSHTERNET-JOSHBOT
Crawl-delay: 6
`,
			want: 6 * time.Second,
		},
		{
			name: "largest delay across repeated wildcard groups wins",
			robots: `
User-agent: *
Crawl-delay: 2

User-agent: OtherBot
Crawl-delay: 20

User-agent: *
Crawl-delay: 5
`,
			want: 5 * time.Second,
		},
		{
			name: "malformed applicable directive returns error",
			robots: `
User-agent: Joshternet-Joshbot
Crawl-delay: 2
Crawl-delay: garbage
Crawl-delay: 7
`,
			wantErr: true,
		},
		{
			name: "malformed exact delay does not fall back to wildcard",
			robots: `
User-agent: *
Crawl-delay: 10

User-agent: Joshternet-Joshbot
Crawl-delay: garbage
`,
			wantErr: true,
		},
		{
			name: "malformed wildcard is ignored when exact group applies",
			robots: `
User-agent: *
Crawl-delay: garbage

User-agent: Joshternet-Joshbot
Crawl-delay: 2
`,
			want: 2 * time.Second,
		},
		{
			name: "malformed unrelated group is ignored",
			robots: `
User-agent: OtherBot
Crawl-delay: garbage

User-agent: Joshternet-Joshbot
Crawl-delay: 2
`,
			want: 2 * time.Second,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := Parse([]byte(test.robots))

			got, err := policy.CrawlDelay()
			if test.wantErr {
				if !errors.Is(err, ErrInvalidCrawlDelay) {
					t.Fatalf(
						"Policy.CrawlDelay() error = %v, want %v",
						err,
						ErrInvalidCrawlDelay,
					)
				}
				if got != 0 {
					t.Errorf(
						"Policy.CrawlDelay() = %s on error, want 0",
						got,
					)
				}
				return
			}

			if err != nil {
				t.Fatalf(
					"Policy.CrawlDelay() error = %v, want nil",
					err,
				)
			}

			if got != test.want {
				t.Errorf(
					"Policy.CrawlDelay() = %s, want %s",
					got,
					test.want,
				)
			}
		})
	}
}

func TestPolicyCrawlDelayDoesNotChangeAllowDisallow(t *testing.T) {
	policy := Parse([]byte(`
User-agent: Joshternet-Joshbot
Crawl-delay: 2
Disallow: /private
Allow: /private/public
`))

	delay, err := policy.CrawlDelay()
	if err != nil {
		t.Fatalf("Policy.CrawlDelay() error = %v, want nil", err)
	}

	if delay != 2*time.Second {
		t.Fatalf("Policy.CrawlDelay() = %s, want 2s", delay)
	}

	tests := []struct {
		name   string
		rawURL string
		want   bool
	}{
		{
			name:   "disallow remains effective",
			rawURL: "https://example.com/private/secret",
			want:   false,
		},
		{
			name:   "more specific allow remains effective",
			rawURL: "https://example.com/private/public/page",
			want:   true,
		},
		{
			name:   "unmatched path remains allowed",
			rawURL: "https://example.com/other",
			want:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, err := url.Parse(test.rawURL)
			if err != nil {
				t.Fatalf(
					"url.Parse(%q) error = %v",
					test.rawURL,
					err,
				)
			}

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

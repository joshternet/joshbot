package robots_test

import (
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/robots"
)

func TestRobotsPolicyIntegrationCrawlDelayContract(t *testing.T) {
	tests := []struct {
		name    string
		policy  string
		want    time.Duration
		wantErr error
	}{
		{
			name: "largest exact delay wins",
			policy: `
User-agent: *
Crawl-delay: 20

User-agent: Joshternet-Joshbot
Crawl-delay: 1.25
Crawl-delay: 7
`,
			want: 7 * time.Second,
		},
		{
			name: "fractional nanoseconds",
			policy: `
User-agent: Joshternet-Joshbot
Crawl-delay: 0.123456789
`,
			want: 123456789 * time.Nanosecond,
		},
		{
			name: "maximum duration",
			policy: `
User-agent: Joshternet-Joshbot
Crawl-delay: 9223372036.854775807
`,
			want: time.Duration(1<<63 - 1),
		},
		{
			name: "invalid decimal syntax",
			policy: `
User-agent: Joshternet-Joshbot
Crawl-delay: 1.2.3
`,
			wantErr: robots.ErrInvalidCrawlDelay,
		},
		{
			name: "sub nanosecond precision",
			policy: `
User-agent: Joshternet-Joshbot
Crawl-delay: 0.1234567891
`,
			wantErr: robots.ErrInvalidCrawlDelay,
		},
		{
			name: "duration overflow",
			policy: `
User-agent: Joshternet-Joshbot
Crawl-delay: 9223372036.854775808
`,
			wantErr: robots.ErrInvalidCrawlDelay,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := robots.Parse([]byte(test.policy))

			got, err := policy.CrawlDelay()
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf(
						"CrawlDelay() error = %v, want %v",
						err,
						test.wantErr,
					)
				}

				if got != 0 {
					t.Errorf(
						"CrawlDelay() = %v on error, want 0",
						got,
					)
				}

				return
			}

			if err != nil {
				t.Fatalf(
					"CrawlDelay() error = %v",
					err,
				)
			}

			if got != test.want {
				t.Errorf(
					"CrawlDelay() = %v, want %v",
					got,
					test.want,
				)
			}
		})
	}
}

func TestRobotsPolicyIntegrationMatchesNormalizedTargets(t *testing.T) {
	policy := robots.Parse([]byte(`
User-agent: Joshternet-Joshbot
Disallow: /private
Allow: /private/public
Disallow: /foo/%62%61%7A$
Disallow: /encoded/%2fvalue$
Disallow: /unicode/%e3%83%84$
Disallow: *.gif$
Disallow: /literal-%2A$
Disallow: /price-%24$
Disallow: /search?secret=1$
`))

	tests := []struct {
		name   string
		rawURL string
		want   bool
	}{
		{
			name:   "longer allow wins",
			rawURL: "https://example.com/private/public/page",
			want:   true,
		},
		{
			name:   "disallow prefix",
			rawURL: "https://example.com/private/secret",
			want:   false,
		},
		{
			name:   "encoded unreserved normalizes to literal",
			rawURL: "https://example.com/foo/baz",
			want:   false,
		},
		{
			name:   "reserved percent encoding stays encoded",
			rawURL: "https://example.com/encoded/%2Fvalue",
			want:   false,
		},
		{
			name:   "reserved percent encoding differs from separator",
			rawURL: "https://example.com/encoded//value",
			want:   true,
		},
		{
			name:   "unicode normalizes to UTF-8 percent encoding",
			rawURL: "https://example.com/unicode/ツ",
			want:   false,
		},
		{
			name:   "leading wildcard and anchor",
			rawURL: "https://example.com/images/picture.gif",
			want:   false,
		},
		{
			name:   "anchor includes query",
			rawURL: "https://example.com/images/picture.gif?size=1",
			want:   true,
		},
		{
			name:   "encoded star is literal",
			rawURL: "https://example.com/literal-*",
			want:   false,
		},
		{
			name:   "encoded dollar is literal",
			rawURL: "https://example.com/price-$",
			want:   false,
		},
		{
			name:   "query participates in matching",
			rawURL: "https://example.com/search?secret=1",
			want:   false,
		},
		{
			name:   "different query remains allowed",
			rawURL: "https://example.com/search?secret=2",
			want:   true,
		},
		{
			name:   "robots file is always allowed",
			rawURL: "https://example.com/robots.txt#ignored",
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
					"Allowed(%q) = %v, want %v",
					test.rawURL,
					got,
					test.want,
				)
			}
		})
	}
}

func TestRobotsPolicyIntegrationSelectsApplicableGroupAndFailsClosed(t *testing.T) {
	policy := robots.Parse([]byte(`
User-agent: *
Disallow: /wildcard
Crawl-delay: 9

User-agent: OtherBot
Disallow: /other

User-agent: JOSHTERNET-JOSHBOT
Disallow: /exact
Crawl-delay: 2
`))

	delay, err := policy.CrawlDelay()
	if err != nil {
		t.Fatalf(
			"CrawlDelay() error = %v",
			err,
		)
	}

	if delay != 2*time.Second {
		t.Errorf(
			"CrawlDelay() = %v, want 2s",
			delay,
		)
	}

	tests := []struct {
		name   string
		target *url.URL
		want   bool
	}{
		{
			name: "exact rule applies",
			target: &url.URL{
				Scheme: "https",
				Host:   "example.com",
				Path:   "/exact/page",
			},
			want: false,
		},
		{
			name: "wildcard rule excluded by exact group",
			target: &url.URL{
				Scheme: "https",
				Host:   "example.com",
				Path:   "/wildcard/page",
			},
			want: true,
		},
		{
			name:   "nil target",
			target: nil,
			want:   false,
		},
		{
			name: "unsupported scheme",
			target: &url.URL{
				Scheme: "ftp",
				Host:   "example.com",
				Path:   "/exact",
			},
			want: false,
		},
		{
			name: "credentials",
			target: &url.URL{
				Scheme: "https",
				Host:   "example.com",
				User:   url.User("private"),
				Path:   "/exact",
			},
			want: false,
		},
		{
			name: "malformed percent query",
			target: &url.URL{
				Scheme:   "https",
				Host:     "example.com",
				Path:     "/search",
				RawQuery: "value=%ZZ",
			},
			want: false,
		},
		{
			name: "control character query",
			target: &url.URL{
				Scheme:   "https",
				Host:     "example.com",
				Path:     "/search",
				RawQuery: "value=\x00",
			},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := policy.Allowed(test.target); got != test.want {
				t.Errorf(
					"Allowed(%#v) = %v, want %v",
					test.target,
					got,
					test.want,
				)
			}
		})
	}
}

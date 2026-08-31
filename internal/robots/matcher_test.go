package robots

import (
	"net/url"
	"testing"
)

func TestPolicyMatchesTargetURI(t *testing.T) {
	tests := []struct {
		name   string
		rules  string
		rawURL string
		want   bool
	}{
		{
			name:   "prefix disallow",
			rules:  "Disallow: /private\n",
			rawURL: "https://example.com/private/page",
			want:   false,
		},
		{
			name: "longer allow wins",
			rules: "Disallow: /example/\n" +
				"Allow: /example/public/\n",
			rawURL: "https://example.com/example/public/page",
			want:   true,
		},
		{
			name: "longer disallow wins",
			rules: "Allow: /example/\n" +
				"Disallow: /example/private/\n",
			rawURL: "https://example.com/example/private/page",
			want:   false,
		},
		{
			name: "equal specificity prefers allow",
			rules: "Disallow: /same\n" +
				"Allow: /same\n",
			rawURL: "https://example.com/same/page",
			want:   true,
		},
		{
			name:   "path matching is case sensitive",
			rules:  "Disallow: /Private\n",
			rawURL: "https://example.com/private/page",
			want:   true,
		},
		{
			name:   "wildcard matches zero octets",
			rules:  "Disallow: /foo/*/private\n",
			rawURL: "https://example.com/foo//private",
			want:   false,
		},
		{
			name:   "wildcard matches multiple components",
			rules:  "Disallow: /foo/*/private\n",
			rawURL: "https://example.com/foo/one/two/private",
			want:   false,
		},
		{
			name:   "trailing wildcard matches zero octets",
			rules:  "Disallow: /fish*\n",
			rawURL: "https://example.com/fish",
			want:   false,
		},
		{
			name:   "terminal dollar matches exact target",
			rules:  "Disallow: /exact$\n",
			rawURL: "https://example.com/exact",
			want:   false,
		},
		{
			name:   "terminal dollar rejects longer target",
			rules:  "Disallow: /exact$\n",
			rawURL: "https://example.com/exact/more",
			want:   true,
		},
		{
			name:   "leading wildcard follows RFC example",
			rules:  "Disallow: *.gif$\n",
			rawURL: "https://example.com/images/picture.gif",
			want:   false,
		},
		{
			name:   "leading wildcard terminal excludes query",
			rules:  "Disallow: *.gif$\n",
			rawURL: "https://example.com/images/picture.gif?size=1",
			want:   true,
		},
		{
			name: "longer wildcard rule wins",
			rules: "Allow: /page\n" +
				"Disallow: /*.html\n",
			rawURL: "https://example.com/page.html",
			want:   false,
		},
		{
			name:   "query participates in matching",
			rules:  "Disallow: /search?secret=1\n",
			rawURL: "https://example.com/search?secret=1",
			want:   false,
		},
		{
			name:   "different query does not match",
			rules:  "Disallow: /search?secret=1$\n",
			rawURL: "https://example.com/search?secret=2",
			want:   true,
		},
		{
			name:   "fragment does not participate",
			rules:  "Disallow: /fragment$\n",
			rawURL: "https://example.com/fragment#ignored",
			want:   false,
		},
		{
			name:   "literal Unicode rule matches URI",
			rules:  "Disallow: /ツ\n",
			rawURL: "https://example.com/ツ",
			want:   false,
		},
		{
			name:   "encoded Unicode rule matches URI",
			rules:  "Disallow: /%E3%83%84\n",
			rawURL: "https://example.com/ツ",
			want:   false,
		},
		{
			name:   "lowercase percent encoding is normalized",
			rules:  "Disallow: /%e3%83%84\n",
			rawURL: "https://example.com/%E3%83%84",
			want:   false,
		},
		{
			name:   "encoded unreserved rule matches literal URI",
			rules:  "Disallow: /foo/%62%61%7A\n",
			rawURL: "https://example.com/foo/baz",
			want:   false,
		},
		{
			name:   "encoded unreserved URI matches literal rule",
			rules:  "Disallow: /foo/baz\n",
			rawURL: "https://example.com/foo/%62%61%7A",
			want:   false,
		},
		{
			name:   "encoded reserved octet matches encoded URI",
			rules:  "Disallow: /a%2Fb\n",
			rawURL: "https://example.com/a%2Fb",
			want:   false,
		},
		{
			name:   "encoded slash differs from path separator",
			rules:  "Disallow: /a%2Fb\n",
			rawURL: "https://example.com/a/b",
			want:   true,
		},
		{
			name:   "encoded star matches literal star",
			rules:  "Disallow: /file-%2A.html\n",
			rawURL: "https://example.com/file-*.html",
			want:   false,
		},
		{
			name:   "encoded dollar matches literal dollar",
			rules:  "Disallow: /price-%24\n",
			rawURL: "https://example.com/price-$",
			want:   false,
		},
		{
			name:   "robots file is implicitly allowed",
			rules:  "Disallow: /\n",
			rawURL: "https://example.com/robots.txt#ignored",
			want:   true,
		},
		{
			name:   "no matching rule allows access",
			rules:  "Disallow: /private\n",
			rawURL: "https://example.com/public",
			want:   true,
		},
		{
			name:   "pattern without leading slash or wildcard is ignored",
			rules:  "Disallow: private\n",
			rawURL: "https://example.com/private",
			want:   true,
		},
		{
			name:   "malformed percent pattern is ignored",
			rules:  "Disallow: /bad%ZZ\n",
			rawURL: "https://example.com/bad%25ZZ",
			want:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := Parse([]byte(
				"User-agent: Joshternet-Joshbot\n" +
					test.rules,
			))
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

func TestPolicyRejectsInvalidTarget(t *testing.T) {
	policy := Parse([]byte(
		"User-agent: Joshternet-Joshbot\n",
	))

	tests := []struct {
		name   string
		target *url.URL
	}{
		{
			name:   "nil URL",
			target: nil,
		},
		{
			name: "relative URL",
			target: &url.URL{
				Path: "/relative",
			},
		},
		{
			name: "missing host",
			target: &url.URL{
				Scheme: "https",
				Path:   "/missing-host",
			},
		},
		{
			name: "unsupported scheme",
			target: &url.URL{
				Scheme: "ftp",
				Host:   "example.com",
				Path:   "/file",
			},
		},
		{
			name: "credentials",
			target: &url.URL{
				Scheme: "https",
				Host:   "example.com",
				User:   url.User("user"),
				Path:   "/private",
			},
		},
		{
			name: "malformed percent encoding",
			target: &url.URL{
				Scheme:   "https",
				Host:     "example.com",
				Path:     "/search",
				RawQuery: "value=%ZZ",
			},
		},
		{
			name: "invalid UTF-8",
			target: &url.URL{
				Scheme: "https",
				Host:   "example.com",
				Path:   "/search",
				RawQuery: "value=" +
					string([]byte{0xff}),
			},
		},
		{
			name: "control character",
			target: &url.URL{
				Scheme:   "https",
				Host:     "example.com",
				Path:     "/search",
				RawQuery: "value=\x00",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if policy.Allowed(test.target) {
				t.Fatal("Policy.Allowed() = true, want false")
			}
		})
	}
}

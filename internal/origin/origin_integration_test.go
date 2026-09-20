package origin_test

import (
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/origin"
)

func TestOriginIntegrationCanonicalizesSupportedWebOrigins(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		want     string
		hostname string
		port     uint16
	}{
		{
			name:     "https domain",
			raw:      "HTTPS://Example.COM:443/path?query=1#fragment",
			want:     "https://example.com",
			hostname: "example.com",
			port:     443,
		},
		{
			name:     "http non-default port",
			raw:      "http://example.com:8080/path",
			want:     "http://example.com:8080",
			hostname: "example.com",
			port:     8080,
		},
		{
			name:     "internationalized domain",
			raw:      "https://bücher.example/path",
			want:     "https://xn--bcher-kva.example",
			hostname: "xn--bcher-kva.example",
			port:     443,
		},
		{
			name:     "IPv4",
			raw:      "https://192.0.2.10/path",
			want:     "https://192.0.2.10",
			hostname: "192.0.2.10",
			port:     443,
		},
		{
			name:     "IPv6",
			raw:      "https://[2001:0db8:0:0:0:0:0:1]:8443/path",
			want:     "https://[2001:db8::1]:8443",
			hostname: "2001:db8::1",
			port:     8443,
		},
		{
			name:     "maximum port",
			raw:      "https://example.com:65535/path",
			want:     "https://example.com:65535",
			hostname: "example.com",
			port:     65535,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := origin.Parse(test.raw)
			if err != nil {
				t.Fatalf(
					"origin.Parse(%q) error = %v",
					test.raw,
					err,
				)
			}

			if got.String() != test.want {
				t.Errorf(
					"String() = %q, want %q",
					got.String(),
					test.want,
				)
			}

			if got.Hostname() != test.hostname {
				t.Errorf(
					"Hostname() = %q, want %q",
					got.Hostname(),
					test.hostname,
				)
			}

			if got.Port() != test.port {
				t.Errorf(
					"Port() = %d, want %d",
					got.Port(),
					test.port,
				)
			}

			reparsed, err := origin.Parse(
				got.String(),
			)
			if err != nil {
				t.Fatalf(
					"reparse canonical origin %q: %v",
					got.String(),
					err,
				)
			}

			if reparsed != got {
				t.Errorf(
					"reparsed origin = %#v, want %#v",
					reparsed,
					got,
				)
			}
		})
	}
}

func TestOriginIntegrationRejectsUnsupportedOrAmbiguousOrigins(
	t *testing.T,
) {
	const credentialSecret = "do-not-leak-this"

	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "malformed URL",
			raw:  "https://example.com/%zz",
		},
		{
			name: "unsupported scheme",
			raw:  "ftp://example.com/file",
		},
		{
			name: "credentials",
			raw: "https://josh:" +
				credentialSecret +
				"@example.com/",
		},
		{
			name: "missing host",
			raw:  "https:///path",
		},
		{
			name: "IPv6 zone identifier",
			raw:  "https://[fe80::1%25eth0]/",
		},
		{
			name: "invalid punycode",
			raw:  "https://xn--example-.com/",
		},
		{
			name: "nonstandard numeric IPv4",
			raw:  "https://127.1/path",
		},
		{
			name: "nonnumeric port",
			raw:  "https://example.com:notaport",
		},
		{
			name: "port above maximum",
			raw:  "https://example.com:65536",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := origin.Parse(test.raw)
			if err == nil {
				t.Fatalf(
					"origin.Parse(%q) = %#v, nil error",
					test.raw,
					got,
				)
			}

			if got != (origin.Origin{}) {
				t.Errorf(
					"origin.Parse(%q) = %#v on error, want zero origin",
					test.raw,
					got,
				)
			}

			if strings.Contains(
				err.Error(),
				credentialSecret,
			) {
				t.Errorf(
					"origin.Parse(%q) error exposed credential: %v",
					test.name,
					err,
				)
			}
		})
	}
}

func TestOriginIntegrationZeroValueIsNotAnOrigin(
	t *testing.T,
) {
	var zero origin.Origin

	if zero.String() != "" {
		t.Errorf(
			"zero String() = %q, want empty",
			zero.String(),
		)
	}

	if zero.Hostname() != "" {
		t.Errorf(
			"zero Hostname() = %q, want empty",
			zero.Hostname(),
		)
	}

	if zero.Port() != 0 {
		t.Errorf(
			"zero Port() = %d, want 0",
			zero.Port(),
		)
	}
}

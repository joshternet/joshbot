package origin

import (
	"strings"
	"testing"
)

func TestParseNormalizesWebOrigin(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "path is omitted",
			input: "https://example.com/path",
			want:  "https://example.com",
		},
		{
			name:  "path query and fragment are omitted",
			input: "https://example.com/a?q=1#fragment",
			want:  "https://example.com",
		},
		{
			name:  "hostname ASCII case is canonicalized",
			input: "https://Example.COM/path",
			want:  "https://example.com",
		},
		{
			name:  "scheme case is canonicalized",
			input: "HTTPS://example.com/path",
			want:  "https://example.com",
		},
		{
			name:  "default HTTPS port is omitted",
			input: "https://example.com:443/path",
			want:  "https://example.com",
		},
		{
			name:  "default HTTP port is omitted",
			input: "http://example.com:80/path",
			want:  "http://example.com",
		},
		{
			name:  "numeric default port is normalized",
			input: "https://example.com:00443/path",
			want:  "https://example.com",
		},
		{
			name:  "numeric non-default port is normalized",
			input: "https://example.com:08443/path",
			want:  "https://example.com:8443",
		},
		{
			name:  "maximum port is preserved",
			input: "https://example.com:65535/path",
			want:  "https://example.com:65535",
		},
		{
			name:  "IPv4 literal is preserved",
			input: "https://192.0.2.10/path",
			want:  "https://192.0.2.10",
		},
		{
			name:  "IPv6 is compressed",
			input: "https://[2001:0db8:0000:0000:0000:0000:0000:0001]/path",
			want:  "https://[2001:db8::1]",
		},
		{
			name:  "IPv6 non-default port is preserved",
			input: "https://[2001:db8::1]:8443/path",
			want:  "https://[2001:db8::1]:8443",
		},
		{
			name:  "internationalized hostname has ASCII identity",
			input: "https://bücher.example/path",
			want:  "https://xn--bcher-kva.example",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(test.input)
			if err != nil {
				t.Fatalf("Parse() error = %v, want nil", err)
			}

			if gotString := got.String(); gotString != test.want {
				t.Errorf("Parse().String() = %q, want %q", gotString, test.want)
			}
		})
	}
}

func TestParseEquivalentOriginsCompareEqual(t *testing.T) {
	want, err := Parse("https://example.com")
	if err != nil {
		t.Fatalf("Parse() reference error = %v, want nil", err)
	}

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "scheme and host case with path",
			input: "HTTPS://EXAMPLE.COM/a",
		},
		{
			name:  "default port with path and query",
			input: "https://example.com:443/b?q=1",
		},
		{
			name:  "fragment",
			input: "https://example.com/#x",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(test.input)
			if err != nil {
				t.Fatalf("Parse() error = %v, want nil", err)
			}

			if got != want {
				t.Errorf("Parse() = %q, want origin equal to %q", got, want)
			}
		})
	}
}

func TestParseDistinctOriginsCompareUnequal(t *testing.T) {
	tests := []struct {
		name       string
		leftInput  string
		rightInput string
	}{
		{
			name:       "scheme differs",
			leftInput:  "http://example.com",
			rightInput: "https://example.com",
		},
		{
			name:       "host differs",
			leftInput:  "https://example.com",
			rightInput: "https://www.example.com",
		},
		{
			name:       "non-default port differs",
			leftInput:  "https://example.com",
			rightInput: "https://example.com:8443",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left, err := Parse(test.leftInput)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v, want nil", test.leftInput, err)
			}

			right, err := Parse(test.rightInput)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v, want nil", test.rightInput, err)
			}

			if left == right {
				t.Errorf(
					"Parse(%q) = Parse(%q) = %q, want unequal origins",
					test.leftInput,
					test.rightInput,
					left,
				)
			}
		})
	}
}

func TestOriginStringReparsesToSameOrigin(t *testing.T) {
	original, err := Parse("HTTPS://BÜCHER.example:443/path?q=1#fragment")
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil", err)
	}

	serialization := original.String()

	reparsed, err := Parse(serialization)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v, want nil", serialization, err)
	}

	if reparsed != original {
		t.Errorf("reparsed origin = %q, want %q", reparsed, original)
	}

	if reparsed.String() != serialization {
		t.Errorf(
			"reparsed serialization = %q, want %q",
			reparsed.String(),
			serialization,
		)
	}
}

func TestParseRejectsInvalidOrigin(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "relative URL",
			input: "example.com",
		},
		{
			name:  "relative path",
			input: "/example",
		},
		{
			name:  "network-path reference",
			input: "//example.com/path",
		},
		{
			name:  "scheme without authority",
			input: "https:",
		},
		{
			name:  "FTP scheme",
			input: "ftp://example.com",
		},
		{
			name:  "file scheme",
			input: "file:///tmp/example",
		},
		{
			name:  "data scheme",
			input: "data:text/plain,Josh",
		},
		{
			name:  "mailto scheme",
			input: "mailto:josh@example.com",
		},
		{
			name:  "WebSocket scheme",
			input: "ws://example.com",
		},
		{
			name:  "secure WebSocket scheme",
			input: "wss://example.com",
		},
		{
			name:  "missing host",
			input: "https:///path",
		},
		{
			name:  "nonnumeric port",
			input: "https://example.com:notaport",
		},
		{
			name:  "port above maximum",
			input: "https://example.com:65536",
		},
		{
			name:  "port out of range",
			input: "https://example.com:99999",
		},
		{
			name:  "malformed IPv6",
			input: "https://[2001:db8::1/path",
		},
		{
			name:  "IPv6 zone identifier",
			input: "https://[fe80::1%25eth0]/",
		},
		{
			name:  "invalid Punycode",
			input: "https://xn--example-.com/",
		},
		{
			name:  "nonstandard IPv4",
			input: "https://127.1/path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(test.input)
			if err == nil {
				t.Fatal("Parse() error = nil, want non-nil")
			}
		})
	}
}

func TestParseRejectsCredentialsWithoutLeakingValues(t *testing.T) {
	const sensitiveValue = "do-not-leak-this"

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "username only",
			input: "https://" + sensitiveValue + "@example.com/",
		},
		{
			name:  "username and password",
			input: "https://josh:" + sensitiveValue + "@example.com/",
		},
		{
			name:  "malformed URL",
			input: "https://josh:" + sensitiveValue + "@example.com:notaport/",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(test.input)
			if err == nil {
				t.Fatal("Parse() error = nil, want non-nil")
			}

			if strings.Contains(err.Error(), sensitiveValue) {
				t.Error("Parse() error exposed supplied credential value")
			}
		})
	}
}

func FuzzParseRoundTrip(f *testing.F) {
	seeds := []string{
		"https://example.com",
		"http://Example.COM:80/path",
		"https://example.com:08443/a?q=1#fragment",
		"https://example.com:65535/path",
		"https://192.0.2.10/path",
		"https://[2001:0db8:0000:0000:0000:0000:0000:0001]:8443/path",
		"https://bücher.example/path",
		"",
		"example.com",
		"//example.com/path",
		"ftp://example.com",
		"ws://example.com",
		"https:///path",
		"https://example.com:notaport",
		"https://example.com:65536",
		"https://[2001:db8::1/path",
		"https://[fe80::1%25eth0]/",
		"https://xn--example-.com/",
		"https://127.1/path",
		"https://josh:do-not-leak-this@example.com/",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawURL string) {
		original, err := Parse(rawURL)
		if err != nil {
			return
		}

		serialization := original.String()

		reparsed, err := Parse(serialization)
		if err != nil {
			t.Fatalf(
				"canonical serialization %q failed to reparse: %v",
				serialization,
				err,
			)
		}

		if reparsed != original {
			t.Errorf(
				"reparsed origin = %q, want %q",
				reparsed,
				original,
			)
		}

		if reparsed.String() != serialization {
			t.Errorf(
				"reparsed serialization = %q, want %q",
				reparsed.String(),
				serialization,
			)
		}
	})
}

package origin

import "testing"

func TestOriginExposesCanonicalNetworkDestination(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantHostname string
		wantPort     uint16
	}{
		{
			name:         "default HTTPS port",
			input:        "https://Example.COM/path",
			wantHostname: "example.com",
			wantPort:     443,
		},
		{
			name:         "default HTTP port",
			input:        "http://example.com/path",
			wantHostname: "example.com",
			wantPort:     80,
		},
		{
			name:         "non-default port",
			input:        "https://example.com:08443/path",
			wantHostname: "example.com",
			wantPort:     8443,
		},
		{
			name:         "zero port",
			input:        "https://example.com:0/path",
			wantHostname: "example.com",
			wantPort:     0,
		},
		{
			name:         "IPv4 literal",
			input:        "https://8.8.8.8/path",
			wantHostname: "8.8.8.8",
			wantPort:     443,
		},
		{
			name:         "IPv6 literal without brackets",
			input:        "https://[2001:4860:4860::8888]:8443/path",
			wantHostname: "2001:4860:4860::8888",
			wantPort:     8443,
		},
		{
			name:         "IDNA hostname",
			input:        "https://bücher.example/path",
			wantHostname: "xn--bcher-kva.example",
			wantPort:     443,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(test.input)
			if err != nil {
				t.Fatalf("Parse() error = %v, want nil", err)
			}

			if hostname := got.Hostname(); hostname != test.wantHostname {
				t.Errorf(
					"Origin.Hostname() = %q, want %q",
					hostname,
					test.wantHostname,
				)
			}

			if port := got.Port(); port != test.wantPort {
				t.Errorf(
					"Origin.Port() = %d, want %d",
					port,
					test.wantPort,
				)
			}
		})
	}
}

func TestZeroOriginHasNoNetworkDestination(t *testing.T) {
	var zero Origin

	if hostname := zero.Hostname(); hostname != "" {
		t.Errorf("Origin.Hostname() = %q, want empty", hostname)
	}

	if port := zero.Port(); port != 0 {
		t.Errorf("Origin.Port() = %d, want 0", port)
	}
}

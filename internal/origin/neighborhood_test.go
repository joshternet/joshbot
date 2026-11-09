package origin

import "testing"

func TestNeighborhoodKey(t *testing.T) {
	tests := []struct {
		host string
		key  string
		ok   bool
	}{
		{host: "www.example.com", key: "example.com", ok: true},
		{host: "blog.example.com", key: "example.com", ok: true},
		{host: "other.example", key: "other.example", ok: true},
		{host: "books.amazon.co.uk", key: "amazon.co.uk", ok: true},
		{host: "www.amazon.co.uk", key: "amazon.co.uk", ok: true},
		{host: "other.co.uk", key: "other.co.uk", ok: true},
		{host: "alice.blogspot.com", key: "blogspot.com", ok: true},
		{host: "bob.blogspot.com", key: "blogspot.com", ok: true},
		{host: "blogspot.com", key: "blogspot.com", ok: true},
		{host: "alice.blogspot.co.uk", key: "blogspot.co.uk", ok: true},
		{host: "192.0.2.1", ok: false},
		{host: "2001:db8::1", ok: false},
		{host: "com", ok: false},
	}

	for _, test := range tests {
		t.Run(test.host, func(t *testing.T) {
			key, ok := NeighborhoodKey(test.host)
			if ok != test.ok || key != test.key {
				t.Fatalf(
					"NeighborhoodKey(%q) = %q, %t; want %q, %t",
					test.host,
					key,
					ok,
					test.key,
					test.ok,
				)
			}
		})
	}
}

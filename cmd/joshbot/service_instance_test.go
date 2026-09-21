package main

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadServiceInstanceID(t *testing.T) {
	t.Parallel()

	configured, err := loadServiceInstanceID(
		func(name string) string {
			if name == serviceInstanceIDEnvironment {
				return "  worker-slot  "
			}

			return "ignored"
		},
		"fallback",
	)
	if err != nil {
		t.Fatalf("loadServiceInstanceID() error = %v", err)
	}
	if configured != "worker-slot" {
		t.Fatalf("configured instance ID = %q", configured)
	}

	fallback, err := loadServiceInstanceID(
		func(string) string { return "   " },
		"  discovery  ",
	)
	if err != nil {
		t.Fatalf("fallback loadServiceInstanceID() error = %v", err)
	}
	if fallback != "discovery" {
		t.Fatalf("fallback instance ID = %q", fallback)
	}

	longest := strings.Repeat("a", maxServiceInstanceIDLength)
	bounded, err := loadServiceInstanceID(
		func(name string) string {
			if name == serviceInstanceIDEnvironment {
				return longest
			}

			return ""
		},
		"fallback",
	)
	if err != nil || bounded != longest {
		t.Fatalf("bounded instance ID = %q, %v", bounded, err)
	}

	for _, invalid := range []string{
		strings.Repeat("b", maxServiceInstanceIDLength+1),
		string([]byte{0xff}),
	} {
		_, err := loadServiceInstanceID(
			func(name string) string {
				if name == serviceInstanceIDEnvironment {
					return invalid
				}

				return ""
			},
			"fallback",
		)
		if !errors.Is(err, errInvalidRuntimeConfiguration) {
			t.Fatalf("loadServiceInstanceID(%q) error = %v", invalid, err)
		}
	}

	_, err = loadServiceInstanceID(
		func(string) string { return "" },
		string([]byte{0xff}),
	)
	if !errors.Is(err, errInvalidRuntimeConfiguration) {
		t.Fatalf("invalid fallback error = %v", err)
	}

	if discoveryServiceInstanceFallback(func(string) string {
		return ""
	}) != defaultDiscoveryInstanceID {
		t.Fatal("empty hostname fallback is not discovery")
	}
	if discoveryServiceInstanceFallback(func(name string) string {
		if name == hostnameEnvironment {
			return "   "
		}

		return ""
	}) != defaultDiscoveryInstanceID {
		t.Fatal("blank hostname fallback is not discovery")
	}
	if discoveryServiceInstanceFallback(func(name string) string {
		if name == hostnameEnvironment {
			return "container-host"
		}

		return ""
	}) != "container-host" {
		t.Fatal("hostname fallback was not preserved")
	}
}

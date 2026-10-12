package main

import (
	"encoding/hex"
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
		strings.NewReader(strings.Repeat("a", generatedServiceInstanceIDBytes)),
	)
	if err != nil {
		t.Fatalf("loadServiceInstanceID() error = %v", err)
	}
	if configured != "worker-slot" {
		t.Fatalf("configured instance ID = %q", configured)
	}

	generated, err := loadServiceInstanceID(
		func(string) string { return "   " },
		strings.NewReader(strings.Repeat("b", generatedServiceInstanceIDBytes)),
	)
	if err != nil {
		t.Fatalf("generated loadServiceInstanceID() error = %v", err)
	}
	wantGenerated := "service-" + hex.EncodeToString(
		[]byte(strings.Repeat("b", generatedServiceInstanceIDBytes)),
	)
	if generated != wantGenerated {
		t.Fatalf("generated instance ID = %q, want %q", generated, wantGenerated)
	}

	longest := strings.Repeat("a", maxServiceInstanceIDLength)
	bounded, err := loadServiceInstanceID(
		func(name string) string {
			if name == serviceInstanceIDEnvironment {
				return longest
			}

			return ""
		},
		strings.NewReader(strings.Repeat("c", generatedServiceInstanceIDBytes)),
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
			strings.NewReader(strings.Repeat("d", generatedServiceInstanceIDBytes)),
		)
		if !errors.Is(err, errInvalidRuntimeConfiguration) {
			t.Fatalf("loadServiceInstanceID(%q) error = %v", invalid, err)
		}
	}

	_, err = loadServiceInstanceID(
		func(string) string { return "" },
		nil,
	)
	if !errors.Is(err, errServiceInstanceIDGeneration) {
		t.Fatalf("nil entropy error = %v", err)
	}

	_, err = loadServiceInstanceID(
		func(string) string { return "" },
		strings.NewReader("short"),
	)
	if !errors.Is(err, errServiceInstanceIDGeneration) {
		t.Fatalf("short entropy error = %v", err)
	}
}

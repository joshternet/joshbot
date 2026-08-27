package testutil

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckGoldenUpdatesWhenEnvironmentEnabled(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "fixture.golden")

	if err := os.WriteFile(fixture, []byte("expected\n"), 0o600); err != nil {
		t.Fatalf("write golden fixture: %v", err)
	}

	t.Setenv("JOSHBOT_UPDATE_GOLDEN", "1")

	actual := []byte("actual\n")
	if err := CheckGolden(fixture, actual); err != nil {
		t.Fatalf("CheckGolden() error = %v, want nil", err)
	}

	got, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read updated golden fixture: %v", err)
	}

	if !bytes.Equal(got, actual) {
		t.Fatalf("updated fixture = %q, want %q", got, actual)
	}
}

func TestCheckGoldenRejectsFalseLookingUpdateValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{
			name:  "zero",
			value: "0",
		},
		{
			name:  "true",
			value: "true",
		},
		{
			name:  "yes",
			value: "yes",
		},
		{
			name:  "one with trailing space",
			value: "1 ",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := filepath.Join(t.TempDir(), "fixture.golden")
			expected := []byte("expected\n")
			actual := []byte("actual\n")

			if err := os.WriteFile(fixture, expected, 0o600); err != nil {
				t.Fatalf("write golden fixture: %v", err)
			}

			t.Setenv("JOSHBOT_UPDATE_GOLDEN", test.value)

			if err := CheckGolden(fixture, actual); err == nil {
				t.Fatal("CheckGolden() error = nil, want mismatch error")
			}

			got, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatalf("read golden fixture: %v", err)
			}

			if !bytes.Equal(got, expected) {
				t.Fatalf(
					"fixture with JOSHBOT_UPDATE_GOLDEN=%q = %q, want %q",
					test.value,
					got,
					expected,
				)
			}
		})
	}
}

func TestCheckGoldenExactMatchWhenEnvironmentDisabled(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "fixture.golden")
	expected := []byte("expected\n")

	if err := os.WriteFile(fixture, expected, 0o600); err != nil {
		t.Fatalf("write golden fixture: %v", err)
	}

	t.Setenv("JOSHBOT_UPDATE_GOLDEN", "")

	if err := CheckGolden(fixture, expected); err != nil {
		t.Fatalf("CheckGolden() error = %v, want nil", err)
	}
}

func TestCheckGoldenMissingFixtureDoesNotCreateIt(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "missing.golden")

	t.Setenv("JOSHBOT_UPDATE_GOLDEN", "")

	err := CheckGolden(fixture, []byte("actual\n"))
	if err == nil {
		t.Fatal("CheckGolden() error = nil, want missing-fixture error")
	}

	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("CheckGolden() error = %v, want it to wrap os.ErrNotExist", err)
	}

	if !strings.Contains(err.Error(), "read golden fixture") {
		t.Errorf(
			"CheckGolden() error = %q, want it to contain %q",
			err,
			"read golden fixture",
		)
	}

	_, statErr := os.Stat(fixture)
	if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat() error = %v, want os.ErrNotExist", statErr)
	}
}

func TestCheckGoldenDoesNotUpdateWhenEnvironmentDisabled(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "fixture.golden")
	expected := []byte("expected\n")
	actual := []byte("actual\n")

	if err := os.WriteFile(fixture, expected, 0o600); err != nil {
		t.Fatalf("write golden fixture: %v", err)
	}

	t.Setenv("JOSHBOT_UPDATE_GOLDEN", "")

	err := CheckGolden(fixture, actual)
	if err == nil {
		t.Fatal("CheckGolden() error = nil, want mismatch error")
	}

	for _, want := range []string{
		"golden mismatch",
		fixture,
		string(expected),
		string(actual),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("CheckGolden() error = %q, want it to contain %q", err, want)
		}
	}

	got, readErr := os.ReadFile(fixture)
	if readErr != nil {
		t.Fatalf("read golden fixture: %v", readErr)
	}

	if !bytes.Equal(got, expected) {
		t.Fatalf("fixture after normal check = %q, want %q", got, expected)
	}
}

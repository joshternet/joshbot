package joshbot

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareGoldenMismatchDoesNotRewriteFixture(t *testing.T) {
	goldenPath := filepath.Join("testdata", "golden", "mismatch.golden")
	actual := []byte("actual\n")

	before, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture before comparison: %v", err)
	}

	err = compareGolden(goldenPath, actual)
	if err == nil {
		t.Fatal("compareGolden() error = nil, want mismatch error")
	}

	for _, want := range []string{goldenPath, "expected", "actual"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("compareGolden() error = %q, want it to contain %q", err, want)
		}
	}

	after, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture after comparison: %v", err)
	}

	if !bytes.Equal(after, before) {
		t.Fatalf(
			"golden fixture changed during comparison:\nbefore:\n%s\nafter:\n%s",
			before,
			after,
		)
	}
}

func TestCheckGoldenUsingFlagExactMatch(t *testing.T) {
	goldenPath := filepath.Join("testdata", "golden", "mismatch.golden")
	actual := []byte("expected\n")

	if err := checkGoldenUsingFlag(goldenPath, actual); err != nil {
		t.Fatalf("checkGoldenUsingFlag() error = %v, want nil", err)
	}
}

func TestCompareGoldenMissingFixtureDoesNotCreateIt(t *testing.T) {
	goldenPath := filepath.Join(t.TempDir(), "missing.golden")

	err := compareGolden(goldenPath, []byte("actual\n"))
	if err == nil {
		t.Fatal("compareGolden() error = nil, want missing-fixture error")
	}

	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("compareGolden() error = %v, want it to wrap os.ErrNotExist", err)
	}

	for _, want := range []string{"read golden fixture", goldenPath} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("compareGolden() error = %q, want it to contain %q", err, want)
		}
	}

	if _, statErr := os.Stat(goldenPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(%q) error = %v, want os.ErrNotExist", goldenPath, statErr)
	}
}

func TestUpdateGoldenReplacesExistingFixture(t *testing.T) {
	goldenPath := filepath.Join(t.TempDir(), "example.golden")

	if err := os.WriteFile(goldenPath, []byte("expected\n"), 0o600); err != nil {
		t.Fatalf("create golden fixture: %v", err)
	}

	want := []byte("actual\n")
	if err := updateGolden(goldenPath, want); err != nil {
		t.Fatalf("updateGolden() error = %v, want nil", err)
	}

	got, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read updated golden fixture: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("updated golden fixture = %q, want %q", got, want)
	}
}

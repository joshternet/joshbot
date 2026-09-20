package testutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoldenIntegrationExplicitUpdateAndVerification(
	t *testing.T,
) {
	path := filepath.Join(
		t.TempDir(),
		"fixture.golden",
	)

	actual := []byte(
		"JoshBot integration golden\n",
	)

	t.Setenv(
		updateGoldenEnvironmentVariable,
		"1",
	)

	if err := CheckGolden(
		path,
		actual,
	); err != nil {
		t.Fatalf(
			"CheckGolden(update) error = %v",
			err,
		)
	}

	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf(
			"read updated golden: %v",
			err,
		)
	}

	if string(stored) != string(actual) {
		t.Errorf(
			"updated golden = %q, want %q",
			stored,
			actual,
		)
	}

	t.Setenv(
		updateGoldenEnvironmentVariable,
		"0",
	)

	if err := CheckGolden(
		path,
		actual,
	); err != nil {
		t.Fatalf(
			"CheckGolden(verify) error = %v",
			err,
		)
	}
}

func TestGoldenIntegrationReportsMismatch(
	t *testing.T,
) {
	path := filepath.Join(
		t.TempDir(),
		"fixture.golden",
	)

	if err := os.WriteFile(
		path,
		[]byte("expected\n"),
		0o600,
	); err != nil {
		t.Fatalf(
			"write golden fixture: %v",
			err,
		)
	}

	t.Setenv(
		updateGoldenEnvironmentVariable,
		"0",
	)

	err := CheckGolden(
		path,
		[]byte("actual\n"),
	)
	if err == nil {
		t.Fatal(
			"CheckGolden() error = nil, want mismatch",
		)
	}

	for _, fragment := range []string{
		"golden mismatch",
		"expected:",
		"expected",
		"actual:",
		"actual",
	} {
		if !strings.Contains(
			err.Error(),
			fragment,
		) {
			t.Errorf(
				"mismatch error = %q, missing %q",
				err.Error(),
				fragment,
			)
		}
	}
}

func TestGoldenIntegrationReportsMissingFixture(
	t *testing.T,
) {
	path := filepath.Join(
		t.TempDir(),
		"missing.golden",
	)

	t.Setenv(
		updateGoldenEnvironmentVariable,
		"0",
	)

	err := CheckGolden(
		path,
		[]byte("actual\n"),
	)
	if err == nil {
		t.Fatal(
			"CheckGolden() error = nil, want read failure",
		)
	}

	if !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Errorf(
			"CheckGolden() error = %v, want os.ErrNotExist",
			err,
		)
	}

	if !strings.Contains(
		err.Error(),
		"read golden fixture",
	) {
		t.Errorf(
			"CheckGolden() error = %q, want read context",
			err.Error(),
		)
	}
}

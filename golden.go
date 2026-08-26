package joshbot

import (
	"bytes"
	"fmt"
	"os"
)

func checkGolden(goldenPath string, actual []byte, update bool) error {
	if update {
		return updateGolden(goldenPath, actual)
	}

	return compareGolden(goldenPath, actual)
}

func compareGolden(goldenPath string, actual []byte) error {
	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		return fmt.Errorf("read golden fixture %q: %w", goldenPath, err)
	}

	if bytes.Equal(expected, actual) {
		return nil
	}

	return fmt.Errorf(
		"golden mismatch for %q:\nexpected:\n%s\nactual:\n%s",
		goldenPath,
		expected,
		actual,
	)
}

func updateGolden(goldenPath string, actual []byte) error {
	return os.WriteFile(goldenPath, actual, 0o600)
}

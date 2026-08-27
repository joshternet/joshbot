package testutil

import (
	"bytes"
	"fmt"
	"os"
)

const updateGoldenEnvironmentVariable = "JOSHBOT_UPDATE_GOLDEN"

// CheckGolden checks or explicitly updates a golden fixture.
func CheckGolden(path string, actual []byte) error {
	if os.Getenv(updateGoldenEnvironmentVariable) == "1" {
		return os.WriteFile(path, actual, 0o600)
	}

	expected, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read golden fixture %q: %w", path, err)
	}

	if bytes.Equal(expected, actual) {
		return nil
	}

	return fmt.Errorf(
		"golden mismatch for %q:\nexpected:\n%s\nactual:\n%s\n",
		path,
		expected,
		actual,
	)
}

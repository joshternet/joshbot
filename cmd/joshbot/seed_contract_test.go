package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunRejectsUnavailableCrawlSeedOperations(
	t *testing.T,
) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithOperations(
		context.Background(),
		[]string{"seed", "list"},
		&stdout,
		&stderr,
		&fakeCommandOperations{},
	)

	if exitCode != exitFailure {
		t.Errorf(
			"exit code = %d, want %d",
			exitCode,
			exitFailure,
		)
	}

	if stdout.Len() != 0 {
		t.Errorf(
			"stdout = %q, want empty",
			stdout.String(),
		)
	}

	if !strings.Contains(
		stderr.String(),
		errOperationsUnavailable.Error(),
	) {
		t.Errorf(
			"stderr = %q, want unavailable operations",
			stderr.String(),
		)
	}
}

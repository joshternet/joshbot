package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func TestCLIIntegrationTopLevelRunPrintsHelp(
	t *testing.T,
) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(
		context.Background(),
		[]string{"help"},
		&stdout,
		&stderr,
	)

	if code != exitSuccess {
		t.Fatalf(
			"run(help) exit = %d, want %d; stderr = %q",
			code,
			exitSuccess,
			stderr.String(),
		)
	}

	if stdout.String() != helpText {
		t.Errorf(
			"run(help) stdout = %q, want helpText",
			stdout.String(),
		)
	}

	if stderr.Len() != 0 {
		t.Errorf(
			"run(help) stderr = %q, want empty",
			stderr.String(),
		)
	}
}

func TestCLIIntegrationReportsRealRuntimeFailures(
	t *testing.T,
) {
	operations := newRuntimeOperations(
		io.Discard,
	)

	operations.getenv = func(
		string,
	) string {
		return ""
	}

	operations.random = bytes.NewReader(
		make(
			[]byte,
			generatedWorkerIDBytes,
		),
	)

	tests := []struct {
		name    string
		command string
		args    []string
	}{
		{
			name:    "health",
			command: "health",
			args:    []string{"health"},
		},
		{
			name:    "migrate",
			command: "migrate",
			args:    []string{"migrate"},
		},
		{
			name:    "schedule",
			command: "schedule",
			args: []string{
				"schedule",
				"https://example.com",
			},
		},
		{
			name:    "seed add",
			command: "seed",
			args: []string{
				"seed",
				"add",
				"https://example.com",
			},
		},
		{
			name:    "seed remove",
			command: "seed",
			args: []string{
				"seed",
				"remove",
				"https://example.com",
			},
		},
		{
			name:    "seed list",
			command: "seed",
			args: []string{
				"seed",
				"list",
			},
		},
		{
			name:    "source block",
			command: "source",
			args: []string{
				"source",
				"block",
				"https://example.com",
			},
		},
		{
			name:    "source allow",
			command: "source",
			args: []string{
				"source",
				"allow",
				"https://example.com",
			},
		},
		{
			name:    "source list",
			command: "source",
			args: []string{
				"source",
				"list",
			},
		},
		{
			name:    "worker",
			command: "worker",
			args:    []string{"worker"},
		},
		{
			name:    "discover once",
			command: "discover",
			args: []string{
				"discover",
				"--once",
			},
		},
		{
			name:    "report",
			command: "report",
			args:    []string{"report"},
		},
		{
			name:    "control",
			command: "control",
			args:    []string{"control"},
		},
		{
			name:    "export",
			command: "export",
			args: []string{
				"export",
				"--output",
				t.TempDir(),
			},
		},
		{
			name:    "publish",
			command: "publish",
			args: []string{
				"publish",
				"--input",
				t.TempDir(),
			},
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				var stdout bytes.Buffer
				var stderr bytes.Buffer

				code := runWithOperations(
					context.Background(),
					test.args,
					&stdout,
					&stderr,
					operations,
				)

				if code != exitFailure {
					t.Fatalf(
						"%v exit = %d, want %d; stdout = %q; stderr = %q",
						test.args,
						code,
						exitFailure,
						stdout.String(),
						stderr.String(),
					)
				}

				if stdout.Len() != 0 {
					t.Errorf(
						"%v stdout = %q, want empty",
						test.args,
						stdout.String(),
					)
				}

				wantPrefix :=
					"joshbot " +
						test.command +
						":"

				if !strings.Contains(
					stderr.String(),
					wantPrefix,
				) {
					t.Errorf(
						"%v stderr = %q, want %q",
						test.args,
						stderr.String(),
						wantPrefix,
					)
				}

				if strings.Contains(
					stderr.String(),
					helpText,
				) {
					t.Errorf(
						"%v stderr contains usage help for runtime failure",
						test.args,
					)
				}
			},
		)
	}
}

func TestCLIIntegrationRejectsUnavailableOperations(
	t *testing.T,
) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runWithOperations(
		context.Background(),
		[]string{"health"},
		&stdout,
		&stderr,
		nil,
	)

	if code != exitFailure {
		t.Fatalf(
			"exit = %d, want %d",
			code,
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
			"stderr = %q, want operations unavailable",
			stderr.String(),
		)
	}
}

package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/origin"
)

func TestRunHelp(t *testing.T) {
	for _, command := range []string{
		"help",
		"-h",
		"--help",
	} {
		t.Run(command, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			code := runWithOperations(
				context.Background(),
				[]string{command},
				&stdout,
				&stderr,
				&fakeCommandOperations{},
			)

			if code != exitSuccess {
				t.Errorf(
					"exit code = %d, want %d",
					code,
					exitSuccess,
				)
			}

			if stdout.String() != helpText {
				t.Errorf(
					"stdout = %q, want help text",
					stdout.String(),
				)
			}

			if stderr.Len() != 0 {
				t.Errorf(
					"stderr = %q, want empty",
					stderr.String(),
				)
			}

			for _, required := range []string{
				"health",
				"migrate",
				"schedule",
				"worker",
				"export",
				"help",
			} {
				if !strings.Contains(
					stdout.String(),
					required,
				) {
					t.Errorf(
						"help does not contain %q",
						required,
					)
				}
			}
		})
	}
}

func TestRunRejectsInvalidUsage(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "missing command",
		},
		{
			name: "help arguments",
			args: []string{"help", "extra"},
		},
		{
			name: "health arguments",
			args: []string{"health", "extra"},
		},
		{
			name: "migrate arguments",
			args: []string{"migrate", "extra"},
		},
		{
			name: "schedule missing origin",
			args: []string{"schedule"},
		},
		{
			name: "schedule extra origin",
			args: []string{
				"schedule",
				"https://example.com",
				"https://example.net",
			},
		},
		{
			name: "schedule invalid origin",
			args: []string{
				"schedule",
				"ftp://example.com",
			},
		},
		{
			name: "worker arguments",
			args: []string{"worker", "extra"},
		},
		{
			name: "export missing arguments",
			args: []string{"export"},
		},
		{
			name: "export wrong flag",
			args: []string{
				"export",
				"--directory",
				"snapshot",
			},
		},
		{
			name: "export empty output",
			args: []string{
				"export",
				"--output",
				"",
			},
		},
		{
			name: "export extra argument",
			args: []string{
				"export",
				"--output",
				"snapshot",
				"extra",
			},
		},
		{
			name: "unknown command",
			args: []string{"unknown"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			code := runWithOperations(
				context.Background(),
				test.args,
				&stdout,
				&stderr,
				&fakeCommandOperations{},
			)

			if code != exitUsage {
				t.Errorf(
					"exit code = %d, want %d",
					code,
					exitUsage,
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
				"Usage:",
			) {
				t.Errorf(
					"stderr = %q, want usage",
					stderr.String(),
				)
			}
		})
	}
}

func TestRunRejectsUnavailableOperations(
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
		t.Errorf(
			"exit code = %d, want %d",
			code,
			exitFailure,
		)
	}

	if !strings.Contains(
		stderr.String(),
		errOperationsUnavailable.Error(),
	) {
		t.Errorf(
			"stderr = %q, want unavailable error",
			stderr.String(),
		)
	}
}

func TestRunDispatchesCommands(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCall   string
		wantOutput string
	}{
		{
			name:       "health",
			args:       []string{"health"},
			wantCall:   "health",
			wantOutput: "healthy\n",
		},
		{
			name:       "migrate",
			args:       []string{"migrate"},
			wantCall:   "migrate",
			wantOutput: "migrations complete\n",
		},
		{
			name: "schedule",
			args: []string{
				"schedule",
				"https://EXAMPLE.com/path",
			},
			wantCall: "schedule",
			wantOutput: "scheduled " +
				"https://example.com\n",
		},
		{
			name:     "worker",
			args:     []string{"worker"},
			wantCall: "worker",
		},
		{
			name: "export",
			args: []string{
				"export",
				"--output",
				"snapshot",
			},
			wantCall:   "export",
			wantOutput: "exported snapshot\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations := &fakeCommandOperations{}
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			code := runWithOperations(
				context.Background(),
				test.args,
				&stdout,
				&stderr,
				operations,
			)

			if code != exitSuccess {
				t.Errorf(
					"exit code = %d, want %d",
					code,
					exitSuccess,
				)
			}

			if stdout.String() != test.wantOutput {
				t.Errorf(
					"stdout = %q, want %q",
					stdout.String(),
					test.wantOutput,
				)
			}

			if stderr.Len() != 0 {
				t.Errorf(
					"stderr = %q, want empty",
					stderr.String(),
				)
			}

			if len(operations.calls) != 1 ||
				operations.calls[0] != test.wantCall {
				t.Errorf(
					"operation calls = %#v, want [%q]",
					operations.calls,
					test.wantCall,
				)
			}

			if test.wantCall == "schedule" &&
				operations.scheduled.String() !=
					"https://example.com" {
				t.Errorf(
					"scheduled origin = %q, want canonical origin",
					operations.scheduled.String(),
				)
			}

			if test.wantCall == "export" &&
				operations.outputRoot != "snapshot" {
				t.Errorf(
					"output root = %q, want snapshot",
					operations.outputRoot,
				)
			}
		})
	}
}

func TestRunReturnsOperationFailures(t *testing.T) {
	operationFailure := errors.New(
		"test operation failure",
	)

	tests := []struct {
		name      string
		args      []string
		operation string
	}{
		{
			name:      "health",
			args:      []string{"health"},
			operation: "health",
		},
		{
			name:      "migrate",
			args:      []string{"migrate"},
			operation: "migrate",
		},
		{
			name: "schedule",
			args: []string{
				"schedule",
				"https://example.com",
			},
			operation: "schedule",
		},
		{
			name:      "worker",
			args:      []string{"worker"},
			operation: "worker",
		},
		{
			name: "export",
			args: []string{
				"export",
				"--output",
				"snapshot",
			},
			operation: "export",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations := &fakeCommandOperations{
				errors: map[string]error{
					test.operation: operationFailure,
				},
			}
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
				t.Errorf(
					"exit code = %d, want %d",
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
				operationFailure.Error(),
			) {
				t.Errorf(
					"stderr = %q, want operation failure",
					stderr.String(),
				)
			}
		})
	}
}

func TestMainUsesCommandRunner(t *testing.T) {
	originalArguments := commandArguments
	originalOutput := commandOutput
	originalErrors := commandErrors
	originalExit := exitProcess

	t.Cleanup(func() {
		commandArguments = originalArguments
		commandOutput = originalOutput
		commandErrors = originalErrors
		exitProcess = originalExit
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := -1

	commandArguments = []string{"help"}
	commandOutput = &stdout
	commandErrors = &stderr
	exitProcess = func(code int) {
		exitCode = code
	}

	main()

	if exitCode != exitSuccess {
		t.Errorf(
			"exit code = %d, want %d",
			exitCode,
			exitSuccess,
		)
	}

	if stdout.String() != helpText {
		t.Errorf(
			"stdout = %q, want help text",
			stdout.String(),
		)
	}

	if stderr.Len() != 0 {
		t.Errorf(
			"stderr = %q, want empty",
			stderr.String(),
		)
	}
}

type fakeCommandOperations struct {
	calls      []string
	errors     map[string]error
	scheduled  origin.Origin
	outputRoot string
}

func (operations *fakeCommandOperations) result(
	name string,
) error {
	operations.calls = append(
		operations.calls,
		name,
	)

	return operations.errors[name]
}

func (operations *fakeCommandOperations) health(
	context.Context,
) error {
	return operations.result("health")
}

func (operations *fakeCommandOperations) migrate(
	context.Context,
) error {
	return operations.result("migrate")
}

func (operations *fakeCommandOperations) schedule(
	_ context.Context,
	source origin.Origin,
) error {
	operations.scheduled = source

	return operations.result("schedule")
}

func (operations *fakeCommandOperations) worker(
	context.Context,
) error {
	return operations.result("worker")
}

func (operations *fakeCommandOperations) export(
	_ context.Context,
	root string,
) error {
	operations.outputRoot = root

	return operations.result("export")
}

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestAppRuntimeIntegrationOptionalOperationBoundaries(
	t *testing.T,
) {
	base := &fakeCommandOperations{}
	unavailable := struct {
		commandOperations
	}{
		commandOperations: base,
	}

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "seed",
			args: []string{
				"seed",
				"list",
			},
		},
		{
			name: "source",
			args: []string{
				"source",
				"list",
			},
		},
		{
			name: "control",
			args: []string{
				"control",
			},
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
				unavailable,
			)

			if code != exitFailure {
				t.Errorf(
					"runWithOperations(%v) exit = %d, want %d",
					test.args,
					code,
					exitFailure,
				)
			}

			if stdout.Len() != 0 {
				t.Errorf(
					"runWithOperations(%v) stdout = %q, want empty",
					test.args,
					stdout.String(),
				)
			}

			if !strings.Contains(
				stderr.String(),
				errOperationsUnavailable.Error(),
			) {
				t.Errorf(
					"runWithOperations(%v) stderr = %q, want unavailable operations error",
					test.args,
					stderr.String(),
				)
			}
		})
	}
}

func TestAppRuntimeIntegrationSuccessfulServiceCommands(
	t *testing.T,
) {
	tests := []struct {
		name     string
		args     []string
		wantCall string
	}{
		{
			name: "report",
			args: []string{
				"report",
			},
			wantCall: "report",
		},
		{
			name: "control",
			args: []string{
				"control",
			},
			wantCall: "control",
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
				t.Fatalf(
					"runWithOperations(%v) exit = %d, want %d; stderr = %q",
					test.args,
					code,
					exitSuccess,
					stderr.String(),
				)
			}

			if stdout.Len() != 0 {
				t.Errorf(
					"runWithOperations(%v) stdout = %q, want empty",
					test.args,
					stdout.String(),
				)
			}

			if stderr.Len() != 0 {
				t.Errorf(
					"runWithOperations(%v) stderr = %q, want empty",
					test.args,
					stderr.String(),
				)
			}

			if len(operations.calls) != 1 ||
				operations.calls[0] != test.wantCall {
				t.Errorf(
					"runWithOperations(%v) calls = %#v, want [%q]",
					test.args,
					operations.calls,
					test.wantCall,
				)
			}
		})
	}
}

func TestAppRuntimeIntegrationPublishResults(
	t *testing.T,
) {
	t.Run("changed", func(t *testing.T) {
		operations := &fakeCommandOperations{}
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		code := runWithOperations(
			context.Background(),
			[]string{
				"publish",
				"--input",
				"snapshot",
			},
			&stdout,
			&stderr,
			operations,
		)

		if code != exitSuccess {
			t.Fatalf(
				"publish changed exit = %d, want %d; stderr = %q",
				code,
				exitSuccess,
				stderr.String(),
			)
		}

		if stdout.String() !=
			"published fake-publish-commit\n" {
			t.Errorf(
				"publish changed stdout = %q, want published commit",
				stdout.String(),
			)
		}

		if stderr.Len() != 0 {
			t.Errorf(
				"publish changed stderr = %q, want empty",
				stderr.String(),
			)
		}
	})

	t.Run("unchanged", func(t *testing.T) {
		operations :=
			&fakePublishCommandOperations{}
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		code := runWithOperations(
			context.Background(),
			[]string{
				"publish",
				"--input",
				"snapshot",
			},
			&stdout,
			&stderr,
			operations,
		)

		if code != exitSuccess {
			t.Fatalf(
				"publish unchanged exit = %d, want %d; stderr = %q",
				code,
				exitSuccess,
				stderr.String(),
			)
		}

		if stdout.String() !=
			"registry unchanged\n" {
			t.Errorf(
				"publish unchanged stdout = %q, want unchanged message",
				stdout.String(),
			)
		}

		if stderr.Len() != 0 {
			t.Errorf(
				"publish unchanged stderr = %q, want empty",
				stderr.String(),
			)
		}
	})
}

func TestAppRuntimeIntegrationDispatchesConformance(
	t *testing.T,
) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runWithOperations(
		context.Background(),
		[]string{
			"conformance",
		},
		&stdout,
		&stderr,
		&fakeCommandOperations{},
	)

	if code == exitSuccess {
		t.Errorf(
			"conformance without arguments exit = %d, want non-success",
			code,
		)
	}

	if stderr.Len() == 0 {
		t.Error(
			"conformance without arguments stderr is empty, want usage error",
		)
	}
}

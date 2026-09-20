package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/store"
)

func TestCLIIntegrationExercisesRuntimeAndPostgreSQL(
	t *testing.T,
) {
	ctx := context.Background()

	operations, pool := newCLIIntegrationEnvironment(t)

	baseGetenv := operations.getenv
	operations.getenv = func(name string) string {
		switch name {
		case crawlMaxDepthEnvironment:
			return "0"
		case crawlMaxPagesEnvironment:
			return "1"
		case crawlMaxPageBytesEnvironment:
			return "1048576"
		case crawlRequestDelayEnvironment:
			return "0s"
		case crawlRedirectLimitEnvironment:
			return "5"
		case automaticCrawlEnabledEnvironment:
			return "false"
		default:
			return baseGetenv(name)
		}
	}

	assertCLIIntegrationCommand(
		t,
		ctx,
		operations,
		[]string{"help"},
		exitSuccess,
		helpText,
		"",
	)

	assertCLIIntegrationCommand(
		t,
		ctx,
		operations,
		[]string{"health"},
		exitSuccess,
		"healthy\n",
		"",
	)

	assertCLIIntegrationCommand(
		t,
		ctx,
		operations,
		[]string{"migrate"},
		exitSuccess,
		"migrations complete\n",
		"",
	)

	assertCLIIntegrationCommand(
		t,
		ctx,
		operations,
		[]string{"discover", "--once"},
		exitSuccess,
		"discovery attempt complete\n",
		"",
	)

	assertCLIIntegrationCommand(
		t,
		ctx,
		operations,
		[]string{
			"schedule",
			"https://EXAMPLE.com/path",
		},
		exitSuccess,
		"scheduled https://example.com\n",
		"",
	)

	source, err := origin.Parse(
		"https://example.com",
	)
	if err != nil {
		t.Fatalf(
			"origin.Parse() error = %v, want nil",
			err,
		)
	}

	databaseStore := store.New(pool)

	if err := databaseStore.RecordVerification(
		ctx,
		time.Unix(1_800_000_000, 0).UTC(),
		declaration.Result{
			Outcome: declaration.OutcomeValid,
			Origin:  source,
			Declaration: declaration.Declaration{
				Version:  1,
				Identity: declaration.IdentityAffirmed,
			},
		},
	); err != nil {
		t.Fatalf(
			"RecordVerification() error = %v, want nil",
			err,
		)
	}

	outputRoot := filepath.Join(
		t.TempDir(),
		"registry",
	)

	assertCLIIntegrationCommand(
		t,
		ctx,
		operations,
		[]string{
			"export",
			"--output",
			outputRoot,
		},
		exitSuccess,
		"exported "+outputRoot+"\n",
		"",
	)

	exported, err := os.ReadDir(outputRoot)
	if err != nil {
		t.Fatalf(
			"ReadDir(%q) error = %v, want nil",
			outputRoot,
			err,
		)
	}

	if len(exported) == 0 {
		t.Fatalf(
			"exported registry %q is empty",
			outputRoot,
		)
	}

	assertCLIIntegrationCommand(
		t,
		ctx,
		operations,
		[]string{
			"seed",
			"add",
			"https://seed.example/start",
		},
		exitSuccess,
		"seed added https://seed.example\n",
		"",
	)

	assertCLIIntegrationCommand(
		t,
		ctx,
		operations,
		[]string{
			"seed",
			"list",
		},
		exitSuccess,
		"https://seed.example\n",
		"",
	)

	assertCLIIntegrationCommand(
		t,
		ctx,
		operations,
		[]string{
			"source",
			"block",
			"https://seed.example/path",
		},
		exitSuccess,
		"source blocked https://seed.example\n",
		"",
	)

	code, stdout, stderr := runCLIIntegrationCommand(
		ctx,
		operations,
		"source",
		"list",
	)
	if code != exitSuccess {
		t.Fatalf(
			"source list exit = %d, want %d; stderr = %q",
			code,
			exitSuccess,
			stderr,
		)
	}

	if !strings.Contains(
		stdout,
		"https://seed.example\tcurated=true",
	) {
		t.Errorf(
			"source list stdout = %q, want curated seed",
			stdout,
		)
	}

	if !strings.Contains(
		stdout,
		"\tblocked=true\t",
	) {
		t.Errorf(
			"source list stdout = %q, want blocked source",
			stdout,
		)
	}

	assertCLIIntegrationCommand(
		t,
		ctx,
		operations,
		[]string{
			"source",
			"allow",
			"https://seed.example",
		},
		exitSuccess,
		"source allowed https://seed.example\n",
		"",
	)

	assertCLIIntegrationCommand(
		t,
		ctx,
		operations,
		[]string{
			"seed",
			"remove",
			"https://seed.example",
		},
		exitSuccess,
		"seed removed https://seed.example\n",
		"",
	)

	assertCLIIntegrationCommand(
		t,
		ctx,
		operations,
		[]string{
			"seed",
			"list",
		},
		exitSuccess,
		"",
		"",
	)

	workerContext, cancelWorker :=
		context.WithCancel(ctx)
	cancelWorker()

	assertCLIIntegrationCommand(
		t,
		workerContext,
		operations,
		[]string{"worker"},
		exitSuccess,
		"",
		"",
	)
}

func TestCLIIntegrationUsageContract(
	t *testing.T,
) {
	ctx := context.Background()

	operations, _ := newCLIIntegrationEnvironment(t)

	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{
			name:       "missing command",
			args:       nil,
			wantStderr: "a command is required",
		},
		{
			name:       "help arguments",
			args:       []string{"help", "extra"},
			wantStderr: "help does not accept arguments",
		},
		{
			name:       "health arguments",
			args:       []string{"health", "extra"},
			wantStderr: "health does not accept arguments",
		},
		{
			name:       "migrate arguments",
			args:       []string{"migrate", "extra"},
			wantStderr: "migrate does not accept arguments",
		},
		{
			name:       "schedule missing origin",
			args:       []string{"schedule"},
			wantStderr: "schedule requires exactly one origin",
		},
		{
			name: "schedule invalid origin",
			args: []string{
				"schedule",
				"not-an-origin",
			},
			wantStderr: "schedule requires a valid HTTP or HTTPS origin",
		},
		{
			name:       "seed missing operation",
			args:       []string{"seed"},
			wantStderr: "seed requires add, remove, or list",
		},
		{
			name: "seed add missing origin",
			args: []string{
				"seed",
				"add",
			},
			wantStderr: "seed add requires exactly one HTTP or HTTPS URL",
		},
		{
			name: "seed add invalid origin",
			args: []string{
				"seed",
				"add",
				"not-an-origin",
			},
			wantStderr: "seed add requires a valid HTTP or HTTPS URL",
		},
		{
			name: "seed remove missing origin",
			args: []string{
				"seed",
				"remove",
			},
			wantStderr: "seed remove requires exactly one HTTP or HTTPS URL",
		},
		{
			name: "seed remove invalid origin",
			args: []string{
				"seed",
				"remove",
				"not-an-origin",
			},
			wantStderr: "seed remove requires a valid HTTP or HTTPS URL",
		},
		{
			name: "seed list arguments",
			args: []string{
				"seed",
				"list",
				"extra",
			},
			wantStderr: "seed list does not accept arguments",
		},
		{
			name: "seed unknown operation",
			args: []string{
				"seed",
				"unknown",
			},
			wantStderr: "seed requires add, remove, or list",
		},
		{
			name:       "source missing operation",
			args:       []string{"source"},
			wantStderr: "source requires block, allow, or list",
		},
		{
			name: "source block missing origin",
			args: []string{
				"source",
				"block",
			},
			wantStderr: "source block and allow require exactly one HTTP or HTTPS URL",
		},
		{
			name: "source block invalid origin",
			args: []string{
				"source",
				"block",
				"not-an-origin",
			},
			wantStderr: "source block and allow require a valid HTTP or HTTPS URL",
		},
		{
			name: "source list arguments",
			args: []string{
				"source",
				"list",
				"extra",
			},
			wantStderr: "source list does not accept arguments",
		},
		{
			name: "source unknown operation",
			args: []string{
				"source",
				"unknown",
			},
			wantStderr: "source requires block, allow, or list",
		},
		{
			name:       "worker arguments",
			args:       []string{"worker", "extra"},
			wantStderr: "worker does not accept arguments",
		},
		{
			name: "discover invalid arguments",
			args: []string{
				"discover",
				"--wrong",
			},
			wantStderr: "discover accepts only the optional --once flag",
		},
		{
			name:       "report arguments",
			args:       []string{"report", "extra"},
			wantStderr: "report does not accept arguments",
		},
		{
			name:       "control arguments",
			args:       []string{"control", "extra"},
			wantStderr: "control does not accept arguments",
		},
		{
			name: "export invalid arguments",
			args: []string{
				"export",
				"--wrong",
				"path",
			},
			wantStderr: "export requires --output <directory>",
		},
		{
			name: "publish invalid arguments",
			args: []string{
				"publish",
				"--wrong",
				"path",
			},
			wantStderr: "publish requires --input <directory>",
		},
		{
			name:       "unknown command",
			args:       []string{"definitely-not-a-command"},
			wantStderr: `unknown command "definitely-not-a-command"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, _, stderr := runCLIIntegrationCommand(
				ctx,
				operations,
				test.args...,
			)

			if code != exitUsage {
				t.Fatalf(
					"exit = %d, want %d; stderr = %q",
					code,
					exitUsage,
					stderr,
				)
			}

			if !strings.Contains(
				stderr,
				test.wantStderr,
			) {
				t.Fatalf(
					"stderr = %q, want substring %q",
					stderr,
					test.wantStderr,
				)
			}
		})
	}
}

func runCLIIntegrationCommand(
	ctx context.Context,
	operations runtimeOperations,
	args ...string,
) (
	int,
	string,
	string,
) {
	var stdout strings.Builder
	var stderr strings.Builder

	code := runWithOperations(
		ctx,
		args,
		&stdout,
		&stderr,
		operations,
	)

	return code, stdout.String(), stderr.String()
}

func assertCLIIntegrationCommand(
	t *testing.T,
	ctx context.Context,
	operations runtimeOperations,
	args []string,
	wantCode int,
	wantStdout string,
	wantStderr string,
) {
	t.Helper()

	code, stdout, stderr := runCLIIntegrationCommand(
		ctx,
		operations,
		args...,
	)

	if code != wantCode {
		t.Fatalf(
			"%v exit = %d, want %d; stdout = %q; stderr = %q",
			args,
			code,
			wantCode,
			stdout,
			stderr,
		)
	}

	if stdout != wantStdout {
		t.Errorf(
			"%v stdout = %q, want %q",
			args,
			stdout,
			wantStdout,
		)
	}

	if stderr != wantStderr {
		t.Errorf(
			"%v stderr = %q, want %q",
			args,
			stderr,
			wantStderr,
		)
	}
}

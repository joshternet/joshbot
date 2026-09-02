package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/joshternet/joshbot/internal/origin"
)

const (
	exitSuccess = 0
	exitFailure = 1
	exitUsage   = 2
)

const helpText = `JoshBot declaration verification runtime

Usage:
  joshbot health
  joshbot migrate
  joshbot schedule <origin>
  joshbot worker
  joshbot export --output <directory>
  joshbot help

Commands:
  health    Check PostgreSQL connectivity
  migrate   Apply pending database migrations
  schedule  Schedule an origin for verification
  worker    Process queued verification work
  export    Write a deterministic public registry snapshot
  help      Show this help
`

var errOperationsUnavailable = errors.New(
	"command operations are unavailable",
)

type commandOperations interface {
	health(context.Context) error
	migrate(context.Context) error
	schedule(context.Context, origin.Origin) error
	worker(context.Context) error
	export(context.Context, string) error
}

func run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	return runWithOperations(
		ctx,
		args,
		stdout,
		stderr,
		newRuntimeOperations(stderr),
	)
}

func runWithOperations(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	operations commandOperations,
) int {
	if operations == nil {
		return reportCommandFailure(
			stderr,
			"command",
			errOperationsUnavailable,
		)
	}

	if len(args) == 0 {
		return reportUsage(
			stderr,
			"a command is required",
		)
	}

	command := args[0]
	commandArgs := args[1:]

	switch command {
	case "help", "-h", "--help":
		if len(commandArgs) != 0 {
			return reportUsage(
				stderr,
				"help does not accept arguments",
			)
		}

		_, _ = fmt.Fprint(stdout, helpText)

		return exitSuccess

	case "health":
		if len(commandArgs) != 0 {
			return reportUsage(
				stderr,
				"health does not accept arguments",
			)
		}

		if err := operations.health(ctx); err != nil {
			return reportCommandFailure(
				stderr,
				command,
				err,
			)
		}

		_, _ = fmt.Fprintln(stdout, "healthy")

		return exitSuccess

	case "migrate":
		if len(commandArgs) != 0 {
			return reportUsage(
				stderr,
				"migrate does not accept arguments",
			)
		}

		if err := operations.migrate(ctx); err != nil {
			return reportCommandFailure(
				stderr,
				command,
				err,
			)
		}

		_, _ = fmt.Fprintln(
			stdout,
			"migrations complete",
		)

		return exitSuccess

	case "schedule":
		if len(commandArgs) != 1 {
			return reportUsage(
				stderr,
				"schedule requires exactly one origin",
			)
		}

		source, err := origin.Parse(commandArgs[0])
		if err != nil {
			return reportUsage(
				stderr,
				"schedule requires a valid HTTP or HTTPS origin",
			)
		}

		if err := operations.schedule(
			ctx,
			source,
		); err != nil {
			return reportCommandFailure(
				stderr,
				command,
				err,
			)
		}

		_, _ = fmt.Fprintf(
			stdout,
			"scheduled %s\n",
			source.String(),
		)

		return exitSuccess

	case "worker":
		if len(commandArgs) != 0 {
			return reportUsage(
				stderr,
				"worker does not accept arguments",
			)
		}

		if err := operations.worker(ctx); err != nil {
			return reportCommandFailure(
				stderr,
				command,
				err,
			)
		}

		return exitSuccess

	case "export":
		if len(commandArgs) != 2 ||
			commandArgs[0] != "--output" ||
			commandArgs[1] == "" {
			return reportUsage(
				stderr,
				"export requires --output <directory>",
			)
		}

		if err := operations.export(
			ctx,
			commandArgs[1],
		); err != nil {
			return reportCommandFailure(
				stderr,
				command,
				err,
			)
		}

		_, _ = fmt.Fprintf(
			stdout,
			"exported %s\n",
			commandArgs[1],
		)

		return exitSuccess

	default:
		return reportUsage(
			stderr,
			fmt.Sprintf(
				"unknown command %q",
				command,
			),
		)
	}
}

func reportCommandFailure(
	stderr io.Writer,
	command string,
	err error,
) int {
	_, _ = fmt.Fprintf(
		stderr,
		"joshbot %s: %v\n",
		command,
		err,
	)

	return exitFailure
}

func reportUsage(
	stderr io.Writer,
	message string,
) int {
	_, _ = fmt.Fprintf(
		stderr,
		"joshbot: %s\n\n%s",
		message,
		helpText,
	)

	return exitUsage
}

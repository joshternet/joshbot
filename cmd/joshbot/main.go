package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
)

var (
	commandArguments           = os.Args[1:]
	commandOutput    io.Writer = os.Stdout
	commandErrors    io.Writer = os.Stderr
	exitProcess                = os.Exit
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	code := run(
		ctx,
		commandArguments,
		commandOutput,
		commandErrors,
	)

	exitProcess(code)
}

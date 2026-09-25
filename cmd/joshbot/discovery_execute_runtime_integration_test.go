package main

import (
	"context"
	"errors"
	"testing"
)

func TestExecuteDiscoveryRuntimeIntegrationDependencies(
	t *testing.T,
) {
	t.Run("missing runner", func(t *testing.T) {
		err := executeDiscovery(
			context.Background(),
			false,
			nil,
			testDiscoveryLogger(),
		)

		if !errors.Is(
			err,
			errDiscoveryRunnerUnavailable,
		) {
			t.Fatalf(
				"executeDiscovery() error = %v, want unavailable runner",
				err,
			)
		}
	})

	t.Run("missing logger", func(t *testing.T) {
		err := executeDiscovery(
			context.Background(),
			false,
			&fakeDiscoveryRunner{},
			nil,
		)

		if !errors.Is(
			err,
			errDiscoveryLoggerUnavailable,
		) {
			t.Fatalf(
				"executeDiscovery() error = %v, want unavailable logger",
				err,
			)
		}
	})
}

func TestExecuteDiscoveryRuntimeIntegrationLongRunning(
	t *testing.T,
) {
	t.Run("clean stop", func(t *testing.T) {
		runner := &fakeDiscoveryRunner{}

		err := executeDiscovery(
			context.Background(),
			false,
			runner,
			testDiscoveryLogger(),
		)
		if err != nil {
			t.Fatalf(
				"executeDiscovery() error = %v, want nil",
				err,
			)
		}

		if runner.runCount != 1 {
			t.Errorf(
				"Run() count = %d, want 1",
				runner.runCount,
			)
		}

		if runner.runOnceCount != 0 {
			t.Errorf(
				"RunOnce() count = %d, want 0",
				runner.runOnceCount,
			)
		}
	})

	t.Run("parent cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		cancel()

		runner := &fakeDiscoveryRunner{
			runErr: context.Canceled,
		}

		err := executeDiscovery(
			ctx,
			false,
			runner,
			testDiscoveryLogger(),
		)
		if err != nil {
			t.Fatalf(
				"executeDiscovery() error = %v, want nil",
				err,
			)
		}

		if runner.runCount != 1 {
			t.Errorf(
				"Run() count = %d, want 1",
				runner.runCount,
			)
		}
	})

	t.Run("runtime failure", func(t *testing.T) {
		runErr := errors.New(
			"integration discovery runtime failure",
		)
		runner := &fakeDiscoveryRunner{
			runErr: runErr,
		}

		err := executeDiscovery(
			context.Background(),
			false,
			runner,
			testDiscoveryLogger(),
		)

		if !errors.Is(err, runErr) {
			t.Fatalf(
				"executeDiscovery() error = %v, want %v",
				err,
				runErr,
			)
		}

		if runner.runCount != 1 {
			t.Errorf(
				"Run() count = %d, want 1",
				runner.runCount,
			)
		}
	})
}

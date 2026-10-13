package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/githubpublish"
)

func TestRegistryPublishIntegrationBackoffCapsAtMaximum(t *testing.T) {
	root := t.TempDir()
	name, err := automaticSnapshotNameFor(
		time.Now().UTC(),
		bytes.NewReader(bytes.Repeat([]byte{0xAA}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeRegistryPointer(root, name); err != nil {
		t.Fatal(err)
	}

	calls := 0
	var delays []time.Duration

	if err := runRegistryPublish(
		context.Background(),
		time.Hour,
		root,
		func(context.Context, string) (githubpublish.Result, error) {
			calls++
			return githubpublish.Result{}, errors.New("integration publish failure")
		},
		func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			if calls >= 8 {
				return errors.New("stop")
			}
			return nil
		},
		slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	); err != nil {
		t.Fatal(err)
	}

	if calls != 8 {
		t.Fatalf("publish calls = %d, want 8", calls)
	}

	for _, delay := range delays[len(delays)-2:] {
		if delay != registryPublishMaxBackoff {
			t.Fatalf(
				"capped delay = %v, want %v (delays = %v)",
				delay, registryPublishMaxBackoff, delays,
			)
		}
	}
}

func TestRegistryPublishIntegration(t *testing.T) {
	t.Run("interval", TestRegistryPublishInterval)
	t.Run("export root", TestRegistryExportRoot)
	t.Run("snapshot names", TestAutomaticSnapshotNames)
	t.Run("pointer", TestRegistryPointerRoundTrip)
	t.Run("cleanup", TestRemoveOldAutomaticSnapshots)
	t.Run("usable snapshot", TestRegistrySnapshotUsable)
	t.Run("export loop", TestRegistryExportLoopRefreshesAndStops)
	t.Run("export failures", TestRegistryExportLoopReportsFailures)
	t.Run("cleanup failure", TestRegistryExportLoopStopsAfterCleanupFailure)
	t.Run("interval cancellation", TestRegistryExportStopsAfterIntervalCancellation)
	t.Run("startup cleanup failure", TestRegistryExportStopsWhenCleanupFails)
	t.Run("partial cleanup", TestRegistryExportRemovesPartialBeforeRetry)
	t.Run("restart after failed cleanup", TestRegistryExportFailedCleanupPreventsRestartGrowth)
	t.Run("invalid pointer", TestRegistryExportRejectsInvalidStartupPointer)
	t.Run("new export root", TestRegistryExportCreatesInitiallyMissingRoot)
	t.Run("failed publication cleanup", TestRegistryExportPointerFailureRemovesSnapshot)
	t.Run("publish loop", TestRegistryPublishLoopPublishesANewSnapshot)
	t.Run("unsafe snapshots", TestRegistryPublishLoopSkipsUnsafeSnapshots)
	t.Run("commands", TestRegistryIntervalCommands)
	t.Run("sleep", TestSleepRegistryInterval)
	t.Run("export configuration", TestExportRegistryRejectsConfiguration)
	t.Run("publish configuration", TestPublishRegistryRejectsConfiguration)
	t.Run("publish current snapshot", TestPublishRegistryPublishesTheCurrentSnapshot)
}

func TestExportRegistryStopsAfterOneSnapshot(t *testing.T) {
	operations, _ := newCLIIntegrationEnvironment(t)
	root := t.TempDir()
	baseGetenv := operations.getenv
	var logs bytes.Buffer
	operations.logger = slog.New(
		slog.NewTextHandler(&logs, nil),
	)
	operations.getenv = func(name string) string {
		switch name {
		case registryPublishIntervalEnvironment:
			return "1h"
		case registryExportRootEnvironment:
			return root
		default:
			return baseGetenv(name)
		}
	}

	if err := operations.migrate(
		context.Background(),
	); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	defer cancel()
	if err := operations.exportRegistry(ctx); err != nil {
		t.Fatal(err)
	}

	name, err := readRegistryPointer(root)
	if err != nil {
		t.Fatalf(
			"read pointer: %v\nlogs:\n%s",
			err,
			logs.String(),
		)
	}
	info, err := os.Lstat(filepath.Join(root, name))
	if err != nil || !info.IsDir() {
		t.Fatalf("exported snapshot error = %v", err)
	}
}

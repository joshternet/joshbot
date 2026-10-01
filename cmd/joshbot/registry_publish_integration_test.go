package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegistryPublishIntegration(t *testing.T) {
	t.Run("interval", TestRegistryPublishInterval)
	t.Run("export root", TestRegistryExportRoot)
	t.Run("snapshot names", TestAutomaticSnapshotNames)
	t.Run("pointer", TestRegistryPointerRoundTrip)
	t.Run("cleanup", TestRemoveOldAutomaticSnapshots)
	t.Run("usable snapshot", TestRegistrySnapshotUsable)
	t.Run("export loop", TestRegistryExportLoopRefreshesAndStops)
	t.Run("export failures", TestRegistryExportLoopReportsFailures)
	t.Run("cleanup failure", TestRegistryExportLoopContinuesAfterCleanupFailure)
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

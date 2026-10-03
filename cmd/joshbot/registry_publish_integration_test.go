package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/githubpublish"
)

func TestRegistryPublishIntegrationInterval(t *testing.T) {
	interval, err := loadRegistryPublishInterval(
		func(string) string { return "" },
	)
	if err != nil || interval != 15*time.Minute {
		t.Fatalf("default interval = %v, %v", interval, err)
	}

	interval, err = loadRegistryPublishInterval(
		func(string) string { return "15m" },
	)
	if err != nil || interval != 15*time.Minute {
		t.Fatalf("parsed interval = %v, %v", interval, err)
	}

	for _, value := range []string{"0s", "-1s", "soon"} {
		if _, err := loadRegistryPublishInterval(
			func(string) string { return value },
		); !errors.Is(err, errInvalidRegistryInterval) {
			t.Fatalf("interval %q error = %v", value, err)
		}
	}
}

func TestRegistryPublishIntegrationLoopUpdatesTheRegistry(t *testing.T) {
	root := t.TempDir()
	exports := 0
	publishes := 0
	var delays []time.Duration

	err := runRegistryPublish(
		context.Background(),
		15*time.Minute,
		root,
		func(_ context.Context, path string) error {
			exports++
			if exports == 1 {
				return errors.New("database unavailable")
			}
			return os.Mkdir(path, 0o755)
		},
		func(context.Context, string) (githubpublish.Result, error) {
			publishes++
			if publishes == 1 {
				return githubpublish.Result{}, errors.New(
					"github unavailable",
				)
			}
			return githubpublish.Result{
				Changed:   true,
				CommitSHA: "abc123",
			}, nil
		},
		time.Now,
		func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			if publishes == 2 {
				return errors.New("stop")
			}
			return nil
		},
		strings.NewReader(strings.Repeat("a", 64)),
		slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if exports != 3 {
		t.Fatalf("exports = %d, want 3", exports)
	}
	if publishes != 2 {
		t.Fatalf("publishes = %d, want 2", publishes)
	}
	if len(delays) != 3 ||
		delays[0] != registryPublishRetryInterval ||
		delays[1] != registryPublishRetryInterval ||
		delays[2] != 15*time.Minute {
		t.Fatalf("delays = %v", delays)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("snapshots left behind: %d", len(entries))
	}
}

func TestRegistryPublishIntegrationLoopLeavesAnUnchangedRegistry(t *testing.T) {
	root := t.TempDir()
	published := false

	err := runRegistryPublish(
		context.Background(),
		time.Minute,
		root,
		func(_ context.Context, path string) error {
			return os.Mkdir(path, 0o755)
		},
		func(context.Context, string) (githubpublish.Result, error) {
			published = true
			return githubpublish.Result{
				Changed:   false,
				CommitSHA: "current",
			}, nil
		},
		time.Now,
		func(_ context.Context, delay time.Duration) error {
			if !published {
				t.Fatal("slept before publishing")
			}
			if delay != time.Minute {
				t.Fatalf("delay = %v, want 1m", delay)
			}
			return errors.New("stop")
		},
		strings.NewReader(strings.Repeat("b", 64)),
		slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !published {
		t.Fatal("unchanged registry was not checked")
	}
	if _, err := os.Stat(filepath.Join(root, "leftover")); !os.IsNotExist(err) {
		t.Fatal("unexpected snapshot state")
	}
}

type registryPublishIntegrationCommandOperations struct {
	*fakeCommandOperations
	err error
}

func (operations *registryPublishIntegrationCommandOperations) publishRegistry(
	context.Context,
) error {
	return operations.err
}

func TestRegistryPublishIntegrationRejectsBadConfiguration(t *testing.T) {
	if _, err := loadRegistryPublishInterval(nil); !errors.Is(
		err,
		errInvalidRegistryInterval,
	) {
		t.Fatalf("nil interval environment error = %v", err)
	}

	if _, err := loadRegistryExportRoot(nil); !errors.Is(
		err,
		errInvalidRegistryExportRoot,
	) {
		t.Fatalf("nil export root environment error = %v", err)
	}

	root, err := loadRegistryExportRoot(func(string) string { return "" })
	if err != nil || root != defaultRegistryExportRoot {
		t.Fatalf("default root = %q, %v", root, err)
	}

	root, err = loadRegistryExportRoot(func(string) string {
		return "/var/joshbot/exports"
	})
	if err != nil || root != "/var/joshbot/exports" {
		t.Fatalf("custom root = %q, %v", root, err)
	}

	for _, value := range []string{
		"exports",
		"/",
		"/tmp/../exports",
		"/tmp/exports/",
	} {
		if _, err := loadRegistryExportRoot(
			func(string) string { return value },
		); !errors.Is(err, errInvalidRegistryExportRoot) {
			t.Fatalf("root %q error = %v", value, err)
		}
	}

	if validRegistryExportRoot("") {
		t.Fatal("empty export root was valid")
	}
}

func TestPublishRegistryIntegrationStopsBeforeTheFirstCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	missingRandom := runtimeOperations{
		getenv: func(string) string { return "" },
		logger: logger,
	}
	if err := missingRandom.publishRegistry(ctx); err != nil {
		t.Fatal(err)
	}

	suppliedRandom := missingRandom
	suppliedRandom.random = strings.NewReader(strings.Repeat("a", 16))
	if err := suppliedRandom.publishRegistry(ctx); err != nil {
		t.Fatal(err)
	}

	invalidInterval := runtimeOperations{
		getenv: func(string) string { return "soon" },
		logger: logger,
	}
	if err := invalidInterval.publishRegistry(ctx); !errors.Is(
		err,
		errInvalidRegistryInterval,
	) {
		t.Fatalf("interval error = %v", err)
	}

	invalidRoot := runtimeOperations{
		getenv: func(name string) string {
			if name == registryExportRootEnvironment {
				return "exports"
			}
			return ""
		},
		logger: logger,
	}
	if err := invalidRoot.publishRegistry(ctx); !errors.Is(
		err,
		errInvalidRegistryExportRoot,
	) {
		t.Fatalf("root error = %v", err)
	}

	missingLogger := runtimeOperations{
		getenv: func(string) string { return "" },
	}
	if err := missingLogger.publishRegistry(ctx); !errors.Is(
		err,
		errRegistryLoggerUnavailable,
	) {
		t.Fatalf("logger error = %v", err)
	}
}

func TestRegistryPublishIntegrationLoopStopsOnEveryFailurePath(t *testing.T) {
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	stop := func(context.Context, time.Duration) error {
		return errors.New("stop")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runRegistryPublish(
		ctx,
		time.Minute,
		root,
		func(context.Context, string) error { return nil },
		func(context.Context, string) (githubpublish.Result, error) {
			return githubpublish.Result{}, nil
		},
		time.Now,
		stop,
		strings.NewReader(strings.Repeat("a", 16)),
		logger,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := registrySnapshotName(time.Now(), nil); !errors.Is(
		err,
		errRegistryEntropyUnavailable,
	) {
		t.Fatalf("nil entropy error = %v", err)
	}
	if _, err := registrySnapshotName(
		time.Now(),
		strings.NewReader("short"),
	); !errors.Is(err, errRegistryEntropyUnavailable) {
		t.Fatalf("short entropy error = %v", err)
	}

	nameWaits := 0
	if err := runRegistryPublish(
		context.Background(),
		time.Minute,
		root,
		func(context.Context, string) error {
			t.Fatal("export ran without a snapshot name")
			return nil
		},
		func(context.Context, string) (githubpublish.Result, error) {
			return githubpublish.Result{}, nil
		},
		time.Now,
		func(context.Context, time.Duration) error {
			nameWaits++
			if nameWaits == 1 {
				return nil
			}
			return errors.New("stop")
		},
		strings.NewReader("short"),
		logger,
	); err != nil {
		t.Fatal(err)
	}

	exportCanceled, cancelExport := context.WithCancel(context.Background())
	if err := runRegistryPublish(
		exportCanceled,
		time.Minute,
		root,
		func(context.Context, string) error {
			cancelExport()
			return errors.New("export canceled")
		},
		func(context.Context, string) (githubpublish.Result, error) {
			t.Fatal("published after a canceled export")
			return githubpublish.Result{}, nil
		},
		time.Now,
		stop,
		strings.NewReader(strings.Repeat("c", 16)),
		logger,
	); err != nil {
		t.Fatal(err)
	}

	if err := runRegistryPublish(
		context.Background(),
		time.Minute,
		root,
		func(context.Context, string) error {
			return errors.New("database unavailable")
		},
		func(context.Context, string) (githubpublish.Result, error) {
			t.Fatal("published after an export failure")
			return githubpublish.Result{}, nil
		},
		time.Now,
		stop,
		strings.NewReader(strings.Repeat("d", 16)),
		logger,
	); err != nil {
		t.Fatal(err)
	}

	publishCanceled, cancelPublish := context.WithCancel(context.Background())
	if err := runRegistryPublish(
		publishCanceled,
		time.Minute,
		root,
		func(_ context.Context, path string) error {
			return os.Mkdir(path, 0o755)
		},
		func(context.Context, string) (githubpublish.Result, error) {
			cancelPublish()
			return githubpublish.Result{}, errors.New("publish canceled")
		},
		time.Now,
		stop,
		strings.NewReader(strings.Repeat("e", 16)),
		logger,
	); err != nil {
		t.Fatal(err)
	}

	if err := runRegistryPublish(
		context.Background(),
		time.Minute,
		root,
		func(_ context.Context, path string) error {
			return os.Mkdir(path, 0o755)
		},
		func(context.Context, string) (githubpublish.Result, error) {
			return githubpublish.Result{}, errors.New("github unavailable")
		},
		time.Now,
		stop,
		strings.NewReader(strings.Repeat("f", 16)),
		logger,
	); err != nil {
		t.Fatal(err)
	}

	removeRegistrySnapshot(root, "not-a-registry-snapshot")
	removeRegistrySnapshot(root, registrySnapshotPrefix+"nested/name")
}

func TestSleepRegistryIntervalIntegration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepRegistryInterval(ctx, time.Hour); err == nil {
		t.Fatal("canceled sleep returned nil")
	}
	if err := sleepRegistryInterval(
		context.Background(),
		time.Millisecond,
	); err != nil {
		t.Fatal(err)
	}
}

func TestPublishRegistryCommandIntegration(t *testing.T) {
	operations := &registryPublishIntegrationCommandOperations{
		fakeCommandOperations: &fakeCommandOperations{},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runWithOperations(
		context.Background(),
		[]string{"publish-registry"},
		&stdout,
		&stderr,
		operations,
	)
	if code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}

	code = runWithOperations(
		context.Background(),
		[]string{"publish-registry", "extra"},
		&stdout,
		&stderr,
		operations,
	)
	if code != exitUsage {
		t.Fatalf("extra argument exit = %d, want %d", code, exitUsage)
	}

	operations.err = errors.New("registry unavailable")
	code = runWithOperations(
		context.Background(),
		[]string{"publish-registry"},
		&stdout,
		&stderr,
		operations,
	)
	if code != exitFailure {
		t.Fatalf("failed publish exit = %d, want %d", code, exitFailure)
	}

	code = runWithOperations(
		context.Background(),
		[]string{"publish-registry"},
		&stdout,
		&stderr,
		&fakeCommandOperations{},
	)
	if code != exitFailure {
		t.Fatalf("unavailable publish exit = %d, want %d", code, exitFailure)
	}
}

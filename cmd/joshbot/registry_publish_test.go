package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/githubpublish"
	"github.com/joshternet/joshbot/internal/publicdata"
)

type registryIntervalOperations struct {
	*fakeCommandOperations
	exportErr  error
	publishErr error
}

func (operations *registryIntervalOperations) exportRegistry(
	context.Context,
) error {
	return operations.exportErr
}

func (operations *registryIntervalOperations) publishRegistry(
	context.Context,
) error {
	return operations.publishErr
}

type registryEntropy struct {
	reads int
	fail  int
	err   error
}

func (reader *registryEntropy) Read(
	target []byte,
) (int, error) {
	reader.reads++
	if reader.reads == reader.fail {
		return 0, reader.err
	}

	for index := range target {
		target[index] = byte(reader.reads)
	}

	return len(target), nil
}

type fixedRegistryPublisher struct {
	cancel context.CancelFunc
	result githubpublish.Result
}

func (publisher fixedRegistryPublisher) Publish(
	context.Context,
	[]publicdata.File,
) (githubpublish.Result, error) {
	if publisher.cancel != nil {
		publisher.cancel()
	}

	return publisher.result, nil
}

func TestRegistryPublishInterval(t *testing.T) {
	interval, err := loadRegistryPublishInterval(
		func(string) string { return "" },
	)
	if err != nil || interval != defaultRegistryPublishInterval {
		t.Fatalf(
			"default interval = %v, %v",
			interval,
			err,
		)
	}

	if _, err := loadRegistryPublishInterval(
		nil,
	); !errors.Is(err, errInvalidRegistryInterval) {
		t.Fatalf("nil environment error = %v", err)
	}

	interval, err = loadRegistryPublishInterval(
		func(string) string { return "15m" },
	)
	if err != nil || interval != 15*time.Minute {
		t.Fatalf(
			"parsed interval = %v, %v",
			interval,
			err,
		)
	}

	for _, value := range []string{"0s", "-1s", "soon"} {
		if _, err := loadRegistryPublishInterval(
			func(string) string { return value },
		); !errors.Is(err, errInvalidRegistryInterval) {
			t.Fatalf("interval %q error = %v", value, err)
		}
	}
}

func TestRegistryExportRoot(t *testing.T) {
	root, err := loadRegistryExportRoot(
		func(string) string { return "" },
	)
	if err != nil || root != defaultRegistryExportRoot {
		t.Fatalf("default root = %q, %v", root, err)
	}

	if _, err := loadRegistryExportRoot(nil); !errors.Is(
		err,
		errInvalidRegistryExportRoot,
	) {
		t.Fatalf("nil environment error = %v", err)
	}

	root, err = loadRegistryExportRoot(
		func(string) string {
			return "/var/joshbot/exports"
		},
	)
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
}

func TestAutomaticSnapshotNames(t *testing.T) {
	now := time.Date(
		2026, time.September, 30, 18, 0, 0, 0, time.UTC,
	)
	name, err := automaticSnapshotNameFor(
		now,
		strings.NewReader(strings.Repeat("a", 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !validAutomaticSnapshotName(name) {
		t.Fatalf("snapshot name %q is not valid", name)
	}

	if _, err := automaticSnapshotNameFor(
		now,
		nil,
	); !errors.Is(err, errRegistryEntropyUnavailable) {
		t.Fatalf("nil entropy error = %v", err)
	}

	if _, err := automaticSnapshotNameFor(
		now,
		&registryEntropy{
			fail: 1,
			err:  errors.New("entropy unavailable"),
		},
	); !errors.Is(err, errRegistryEntropyUnavailable) {
		t.Fatalf("read entropy error = %v", err)
	}
}

func TestRegistryPointerRoundTrip(t *testing.T) {
	root := t.TempDir()
	name, err := automaticSnapshotNameFor(
		time.Now().UTC(),
		strings.NewReader(strings.Repeat("b", 16)),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := writeRegistryPointer(root, name); err != nil {
		t.Fatal(err)
	}
	got, err := readRegistryPointer(root)
	if err != nil || got != name {
		t.Fatalf("pointer = %q, %v", got, err)
	}

	if err := writeRegistryPointer(
		root,
		"../elsewhere",
	); !errors.Is(err, errInvalidRegistryPointer) {
		t.Fatalf("unsafe pointer error = %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(root, registryCurrentName),
		[]byte(name),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegistryPointer(root); !errors.Is(
		err,
		errInvalidRegistryPointer,
	) {
		t.Fatalf("unterminated pointer error = %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(root, registryCurrentName),
		[]byte(name+"\nextra\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegistryPointer(root); !errors.Is(
		err,
		errInvalidRegistryPointer,
	) {
		t.Fatalf("multiline pointer error = %v", err)
	}

	blocked := t.TempDir()
	if err := os.Mkdir(
		filepath.Join(blocked, registryCurrentName),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	if err := writeRegistryPointer(blocked, name); err == nil {
		t.Fatal("rename onto a directory succeeded")
	}
}

func TestRemoveOldAutomaticSnapshots(t *testing.T) {
	root := t.TempDir()
	current := automaticSnapshotPrefix +
		"20260930T000000Z-" + strings.Repeat("a", 32)
	previous := automaticSnapshotPrefix +
		"20260930T000001Z-" + strings.Repeat("b", 32)
	old := automaticSnapshotPrefix +
		"20260930T000002Z-" + strings.Repeat("c", 32)
	for _, name := range []string{current, previous, old} {
		if err := os.Mkdir(
			filepath.Join(root, name),
			0o755,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(root, registryCurrentName),
		[]byte("keep\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	if err := removeOldAutomaticSnapshots(
		root,
		current,
		previous,
	); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		current,
		previous,
		registryCurrentName,
	} {
		if _, err := os.Lstat(
			filepath.Join(root, name),
		); err != nil {
			t.Fatalf("%s was removed: %v", name, err)
		}
	}
	if _, err := os.Lstat(
		filepath.Join(root, old),
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old snapshot error = %v", err)
	}

	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeOldAutomaticSnapshots(
		file,
		current,
		previous,
	); err == nil {
		t.Fatal("reading a file as a directory succeeded")
	}

	leftover := filepath.Join(
		root,
		automaticSnapshotPrefix+"leftover",
	)
	if err := os.Mkdir(leftover, 0o755); err != nil {
		t.Fatal(err)
	}
	original := removeSnapshotDirectory
	removeSnapshotDirectory = func(string) error {
		return errors.New("snapshot removal failed")
	}
	t.Cleanup(func() {
		removeSnapshotDirectory = original
	})
	if err := removeOldAutomaticSnapshots(
		root,
		current,
		previous,
	); err == nil {
		t.Fatal("snapshot removal failure was ignored")
	}
	if _, err := os.Lstat(leftover); err != nil {
		t.Fatalf("failed removal deleted the snapshot: %v", err)
	}
}

func TestRegistrySnapshotUsable(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := usableAutomaticSnapshot(directory); err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := usableAutomaticSnapshot(file); !errors.Is(
		err,
		errInvalidRegistryPointer,
	) {
		t.Fatalf("file snapshot error = %v", err)
	}

	link := filepath.Join(root, "link")
	if err := os.Symlink(directory, link); err != nil {
		t.Fatal(err)
	}
	if err := usableAutomaticSnapshot(link); !errors.Is(
		err,
		errInvalidRegistryPointer,
	) {
		t.Fatalf("symlink snapshot error = %v", err)
	}

	if err := usableAutomaticSnapshot(
		filepath.Join(root, "missing"),
	); err == nil {
		t.Fatal("missing snapshot was usable")
	}
}

func TestRegistryExportLoopRefreshesAndStops(t *testing.T) {
	root := t.TempDir()
	logger := discardRegistryLogger()
	now := time.Date(
		2026, time.September, 30, 18, 15, 0, 0, time.UTC,
	)
	var exported []string
	sleeps := 0

	err := runRegistryExport(
		context.Background(),
		time.Millisecond,
		root,
		func(_ context.Context, path string) error {
			exported = append(exported, path)
			return os.Mkdir(path, 0o755)
		},
		func() time.Time { return now },
		func(context.Context, time.Duration) error {
			sleeps++
			if sleeps == 3 {
				return errors.New("stop")
			}
			return nil
		},
		removeOldAutomaticSnapshots,
		&registryEntropy{},
		logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported) != 3 {
		t.Fatalf("exports = %d, want 3", len(exported))
	}

	pointer, err := readRegistryPointer(root)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Join(root, pointer) != exported[2] {
		t.Fatalf(
			"pointer %q does not name the latest snapshot",
			pointer,
		)
	}
	if _, err := os.Lstat(exported[0]); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf(
			"first snapshot error = %v, want removed",
			err,
		)
	}
	if _, err := os.Lstat(exported[1]); err != nil {
		t.Fatalf("previous snapshot was removed: %v", err)
	}
}

func TestRegistryExportLoopReportsFailures(t *testing.T) {
	root := t.TempDir()
	logger := discardRegistryLogger()

	if err := runRegistryExport(
		context.Background(),
		time.Millisecond,
		root,
		func(context.Context, string) error {
			return nil
		},
		time.Now,
		func(context.Context, time.Duration) error {
			return errors.New("stop")
		},
		removeOldAutomaticSnapshots,
		&registryEntropy{
			fail: 1,
			err:  errors.New("entropy unavailable"),
		},
		logger,
	); err != nil {
		t.Fatal(err)
	}

	canceled, cancelExport := context.WithCancel(
		context.Background(),
	)
	cancelExport()
	if err := runRegistryExport(
		canceled,
		time.Millisecond,
		root,
		func(context.Context, string) error {
			t.Fatal("export ran after cancellation")
			return nil
		},
		time.Now,
		func(context.Context, time.Duration) error {
			t.Fatal("sleep ran after cancellation")
			return nil
		},
		removeOldAutomaticSnapshots,
		strings.NewReader(strings.Repeat("a", 16)),
		logger,
	); err != nil {
		t.Fatal(err)
	}

	exportCanceled, cancelDuringExport := context.WithCancel(
		context.Background(),
	)
	if err := runRegistryExport(
		exportCanceled,
		time.Millisecond,
		root,
		func(context.Context, string) error {
			cancelDuringExport()
			return errors.New("export canceled")
		},
		time.Now,
		func(context.Context, time.Duration) error {
			t.Fatal("sleep ran after a canceled export")
			return nil
		},
		removeOldAutomaticSnapshots,
		strings.NewReader(strings.Repeat("a", 16)),
		logger,
	); err != nil {
		t.Fatal(err)
	}

	attempts := 0
	if err := runRegistryExport(
		context.Background(),
		time.Millisecond,
		root,
		func(context.Context, string) error {
			attempts++
			if attempts == 1 {
				return errors.New("database unavailable")
			}
			return os.RemoveAll(root)
		},
		time.Now,
		func(context.Context, time.Duration) error {
			if attempts >= 2 {
				return errors.New("stop")
			}
			return nil
		},
		removeOldAutomaticSnapshots,
		strings.NewReader(strings.Repeat("abcd", 8)),
		logger,
	); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryExportLoopContinuesAfterCleanupFailure(t *testing.T) {
	root := t.TempDir()
	err := runRegistryExport(
		context.Background(),
		time.Millisecond,
		root,
		func(_ context.Context, path string) error {
			return os.Mkdir(path, 0o755)
		},
		time.Now,
		func(context.Context, time.Duration) error {
			return errors.New("stop")
		},
		func(string, string, string) error {
			return errors.New("cleanup failed")
		},
		strings.NewReader(strings.Repeat("a", 16)),
		discardRegistryLogger(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := readRegistryPointer(root); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryPublishLoopPublishesANewSnapshot(t *testing.T) {
	root := t.TempDir()
	first, err := automaticSnapshotNameFor(
		time.Now().UTC(),
		strings.NewReader(strings.Repeat("e", 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := automaticSnapshotNameFor(
		time.Now().UTC(),
		strings.NewReader(strings.Repeat("f", 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{first, second} {
		if err := os.Mkdir(
			filepath.Join(root, name),
			0o755,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeRegistryPointer(root, first); err != nil {
		t.Fatal(err)
	}

	calls := 0
	err = runRegistryPublish(
		context.Background(),
		15*time.Minute,
		root,
		func(context.Context, string) (githubpublish.Result, error) {
			calls++
			switch calls {
			case 1:
				return githubpublish.Result{}, errors.New(
					"github unavailable",
				)
			case 2:
				return githubpublish.Result{
					Changed:   true,
					CommitSHA: "abc123",
				}, nil
			default:
				return githubpublish.Result{}, nil
			}
		},
		func(context.Context, time.Duration) error {
			if calls == 2 {
				if err := writeRegistryPointer(
					root,
					second,
				); err != nil {
					t.Fatal(err)
				}
			}
			if calls == 3 {
				return errors.New("stop")
			}
			return nil
		},
		discardRegistryLogger(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("publish calls = %d, want 3", calls)
	}
}

func TestRegistryPublishLoopSkipsUnsafeSnapshots(t *testing.T) {
	root := t.TempDir()
	logger := discardRegistryLogger()

	if err := runRegistryPublish(
		context.Background(),
		time.Minute,
		root,
		func(context.Context, string) (githubpublish.Result, error) {
			t.Fatal("published without a snapshot pointer")
			return githubpublish.Result{}, nil
		},
		func(context.Context, time.Duration) error {
			return errors.New("stop")
		},
		logger,
	); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(root, registryCurrentName),
		[]byte("not-a-snapshot\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := runRegistryPublish(
		context.Background(),
		time.Minute,
		root,
		func(context.Context, string) (githubpublish.Result, error) {
			t.Fatal("published an invalid pointer")
			return githubpublish.Result{}, nil
		},
		func(context.Context, time.Duration) error {
			return errors.New("stop")
		},
		logger,
	); err != nil {
		t.Fatal(err)
	}

	name, err := automaticSnapshotNameFor(
		time.Now().UTC(),
		strings.NewReader(strings.Repeat("a", 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, name),
		[]byte("x"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := writeRegistryPointer(root, name); err != nil {
		t.Fatal(err)
	}
	if err := runRegistryPublish(
		context.Background(),
		time.Minute,
		root,
		func(context.Context, string) (githubpublish.Result, error) {
			t.Fatal("published a file snapshot")
			return githubpublish.Result{}, nil
		},
		func(context.Context, time.Duration) error {
			return errors.New("stop")
		},
		logger,
	); err != nil {
		t.Fatal(err)
	}

	missing, err := automaticSnapshotNameFor(
		time.Now().UTC(),
		strings.NewReader(strings.Repeat("b", 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRegistryPointer(root, missing); err != nil {
		t.Fatal(err)
	}
	if err := runRegistryPublish(
		context.Background(),
		time.Minute,
		root,
		func(context.Context, string) (githubpublish.Result, error) {
			t.Fatal("published a missing snapshot")
			return githubpublish.Result{}, nil
		},
		func(context.Context, time.Duration) error {
			return errors.New("stop")
		},
		logger,
	); err != nil {
		t.Fatal(err)
	}

	canceled, cancel := context.WithCancel(
		context.Background(),
	)
	if err := writeRegistryPointer(root, name); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runRegistryPublish(
		canceled,
		time.Minute,
		root,
		func(context.Context, string) (githubpublish.Result, error) {
			cancel()
			return githubpublish.Result{}, errors.New(
				"publish canceled",
			)
		},
		func(context.Context, time.Duration) error {
			t.Fatal("sleep ran after a canceled publish")
			return nil
		},
		logger,
	); err != nil {
		t.Fatal(err)
	}

	if err := runRegistryPublish(
		canceled,
		time.Minute,
		root,
		func(context.Context, string) (githubpublish.Result, error) {
			t.Fatal("publish ran after cancellation")
			return githubpublish.Result{}, nil
		},
		func(context.Context, time.Duration) error {
			t.Fatal("sleep ran after cancellation")
			return nil
		},
		logger,
	); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryIntervalCommands(t *testing.T) {
	operations := &registryIntervalOperations{
		fakeCommandOperations: &fakeCommandOperations{},
	}
	assertRegistryCommand(
		t,
		operations,
		[]string{"export-registry"},
		exitSuccess,
	)
	assertRegistryCommand(
		t,
		operations,
		[]string{"publish-registry"},
		exitSuccess,
	)
	assertRegistryCommand(
		t,
		operations,
		[]string{"export-registry", "extra"},
		exitUsage,
	)
	assertRegistryCommand(
		t,
		operations,
		[]string{"publish-registry", "extra"},
		exitUsage,
	)

	operations.exportErr = errors.New("export failed")
	operations.publishErr = errors.New("publish failed")
	assertRegistryCommand(
		t,
		operations,
		[]string{"export-registry"},
		exitFailure,
	)
	assertRegistryCommand(
		t,
		operations,
		[]string{"publish-registry"},
		exitFailure,
	)

	assertRegistryCommand(
		t,
		&fakeCommandOperations{},
		[]string{"export-registry"},
		exitFailure,
	)
	assertRegistryCommand(
		t,
		&fakeCommandOperations{},
		[]string{"publish-registry"},
		exitFailure,
	)
}

func assertRegistryCommand(
	t *testing.T,
	operations commandOperations,
	args []string,
	want int,
) {
	t.Helper()

	if code := runWithOperations(
		context.Background(),
		args,
		io.Discard,
		io.Discard,
		operations,
	); code != want {
		t.Fatalf(
			"run %v exit = %d, want %d",
			args,
			code,
			want,
		)
	}
}

func TestSleepRegistryInterval(t *testing.T) {
	if err := sleepRegistryInterval(
		context.Background(),
		time.Millisecond,
	); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepRegistryInterval(
		ctx,
		time.Hour,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled sleep error = %v", err)
	}
}

func TestExportRegistryRejectsConfiguration(t *testing.T) {
	operations := newRuntimeOperations(&bytes.Buffer{})
	operations.getenv = func(string) string { return "0s" }
	if err := operations.exportRegistry(
		context.Background(),
	); !errors.Is(err, errInvalidRegistryInterval) {
		t.Fatalf("interval error = %v", err)
	}

	operations.getenv = func(name string) string {
		if name == registryExportRootEnvironment {
			return "relative"
		}
		return ""
	}
	if err := operations.exportRegistry(
		context.Background(),
	); !errors.Is(err, errInvalidRegistryExportRoot) {
		t.Fatalf("root error = %v", err)
	}

	operations.logger = nil
	operations.getenv = func(name string) string {
		if name == registryExportRootEnvironment {
			return t.TempDir()
		}
		return ""
	}
	if err := operations.exportRegistry(
		context.Background(),
	); !errors.Is(err, errRegistryLoggerUnavailable) {
		t.Fatalf("logger error = %v", err)
	}

	operations = newRuntimeOperations(&bytes.Buffer{})
	canceled, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()
	operations.random = nil
	operations.getenv = func(name string) string {
		switch name {
		case registryPublishIntervalEnvironment:
			return "1h"
		case registryExportRootEnvironment:
			return t.TempDir()
		default:
			return ""
		}
	}
	if err := operations.exportRegistry(canceled); err != nil {
		t.Fatal(err)
	}
}

func TestPublishRegistryRejectsConfiguration(t *testing.T) {
	operations := newRuntimeOperations(&bytes.Buffer{})
	operations.getenv = func(string) string { return "-1s" }
	if err := operations.publishRegistry(
		context.Background(),
	); !errors.Is(err, errInvalidRegistryInterval) {
		t.Fatalf("interval error = %v", err)
	}

	operations.getenv = func(name string) string {
		if name == registryExportRootEnvironment {
			return "/"
		}
		return ""
	}
	if err := operations.publishRegistry(
		context.Background(),
	); !errors.Is(err, errInvalidRegistryExportRoot) {
		t.Fatalf("root error = %v", err)
	}

	operations.logger = nil
	operations.getenv = func(name string) string {
		if name == registryExportRootEnvironment {
			return t.TempDir()
		}
		return ""
	}
	if err := operations.publishRegistry(
		context.Background(),
	); !errors.Is(err, errRegistryLoggerUnavailable) {
		t.Fatalf("logger error = %v", err)
	}
}

func TestPublishRegistryPublishesTheCurrentSnapshot(t *testing.T) {
	operations := newRuntimeOperations(&bytes.Buffer{})
	root := t.TempDir()
	name, err := automaticSnapshotNameFor(
		time.Now().UTC(),
		strings.NewReader(strings.Repeat("a", 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(
		filepath.Join(root, name),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	if err := writeRegistryPointer(root, name); err != nil {
		t.Fatal(err)
	}

	token := filepath.Join(root, "token")
	if err := os.WriteFile(
		token,
		[]byte("registry-token\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	operations.getenv = func(key string) string {
		switch key {
		case registryPublishIntervalEnvironment:
			return "15m"
		case registryExportRootEnvironment:
			return root
		case publishOwnerEnvironment:
			return "joshternet"
		case publishRepositoryEnvironment:
			return "index-data"
		case publishBranchEnvironment:
			return "main"
		case githubTokenFileEnvironment:
			return token
		default:
			return ""
		}
	}
	operations.readRegistry = func(
		context.Context,
		string,
	) ([]publicdata.File, error) {
		return []publicdata.File{{
			Path: "registry.json",
			Data: []byte("{}\n"),
		}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	operations.newPublisher = func(
		githubpublish.Config,
		string,
	) (registryPublisher, error) {
		return fixedRegistryPublisher{
			cancel: cancel,
			result: githubpublish.Result{
				Changed:   true,
				CommitSHA: "abc123",
			},
		}, nil
	}

	if err := operations.publishRegistry(ctx); err != nil {
		t.Fatal(err)
	}
}

func discardRegistryLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/joshternet/joshbot/internal/githubpublish"
)

const (
	registryPublishIntervalEnvironment = "JOSHBOT_REGISTRY_PUBLISH_INTERVAL"

	registryExportRootEnvironment = "JOSHBOT_REGISTRY_EXPORT_ROOT"

	defaultRegistryPublishInterval = 15 * time.Minute

	defaultRegistryExportRoot = "/exports"

	registryCurrentName = "registry-current"

	automaticSnapshotPrefix = ".joshbot-automatic-"

	registryPublishFollowInterval = 5 * time.Second
)

var (
	errInvalidRegistryInterval = errors.New(
		"registry publish interval is invalid",
	)

	errInvalidRegistryExportRoot = errors.New(
		"registry export root is invalid",
	)

	errRegistryLoggerUnavailable = errors.New(
		"registry logger is unavailable",
	)

	errRegistryEntropyUnavailable = errors.New(
		"registry snapshot entropy is unavailable",
	)

	errInvalidRegistryPointer = errors.New(
		"registry snapshot pointer is invalid",
	)

	automaticSnapshotName = regexp.MustCompile(
		`^\.joshbot-automatic-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{32}$`,
	)
)

func loadRegistryPublishInterval(
	getenv environmentGetter,
) (time.Duration, error) {
	if getenv == nil {
		return 0, errInvalidRegistryInterval
	}

	value := getenv(registryPublishIntervalEnvironment)
	if value == "" {
		return defaultRegistryPublishInterval, nil
	}

	interval, err := time.ParseDuration(value)
	if err != nil || interval <= 0 {
		return 0, errInvalidRegistryInterval
	}

	return interval, nil
}

func loadRegistryExportRoot(
	getenv environmentGetter,
) (string, error) {
	if getenv == nil {
		return "", errInvalidRegistryExportRoot
	}

	root := getenv(registryExportRootEnvironment)
	if root == "" {
		root = defaultRegistryExportRoot
	}

	if !validRegistryExportRoot(root) {
		return "", errInvalidRegistryExportRoot
	}

	return root, nil
}

func validRegistryExportRoot(root string) bool {
	clean := filepath.Clean(root)
	if root == "" ||
		clean != root ||
		!filepath.IsAbs(root) ||
		clean == string(os.PathSeparator) ||
		strings.Contains(root, "..") {
		return false
	}

	return true
}

func (operations runtimeOperations) exportRegistry(
	ctx context.Context,
) error {
	interval, err := loadRegistryPublishInterval(
		operations.getenv,
	)
	if err != nil {
		return err
	}

	root, err := loadRegistryExportRoot(
		operations.getenv,
	)
	if err != nil {
		return err
	}

	if operations.logger == nil {
		return errRegistryLoggerUnavailable
	}

	random := operations.random
	if random == nil {
		random = rand.Reader
	}

	return runRegistryExport(
		ctx,
		interval,
		root,
		operations.export,
		time.Now,
		sleepRegistryInterval,
		removeOldAutomaticSnapshots,
		random,
		operations.logger,
	)
}

func (operations runtimeOperations) publishRegistry(
	ctx context.Context,
) error {
	interval, err := loadRegistryPublishInterval(
		operations.getenv,
	)
	if err != nil {
		return err
	}

	root, err := loadRegistryExportRoot(
		operations.getenv,
	)
	if err != nil {
		return err
	}

	if operations.logger == nil {
		return errRegistryLoggerUnavailable
	}

	return runRegistryPublish(
		ctx,
		interval,
		root,
		operations.publish,
		sleepRegistryInterval,
		operations.logger,
	)
}

func runRegistryExport(
	ctx context.Context,
	interval time.Duration,
	root string,
	export func(context.Context, string) error,
	now func() time.Time,
	sleep func(context.Context, time.Duration) error,
	removeOld func(string, string, string) error,
	random io.Reader,
	logger *slog.Logger,
) error {
	logger.Info(
		"registry export started",
		"interval",
		interval,
	)

	var previous string

	for {
		if err := ctx.Err(); err != nil {
			logger.Info("registry export stopped")
			return nil
		}

		name, err := automaticSnapshotNameFor(
			now(),
			random,
		)
		if err != nil {
			logger.Error(
				"registry snapshot name failed",
				"error",
				err,
			)
		} else if err := export(
			ctx,
			filepath.Join(root, name),
		); err != nil {
			if ctx.Err() != nil {
				logger.Info("registry export stopped")
				return nil
			}

			logger.Error(
				"registry export failed",
				"error",
				err,
			)
		} else if err := writeRegistryPointer(
			root,
			name,
		); err != nil {
			logger.Error(
				"registry snapshot pointer failed",
				"error",
				err,
			)
		} else {
			if err := removeOld(
				root,
				name,
				previous,
			); err != nil {
				logger.Error(
					"registry snapshot cleanup failed",
					"error",
					err,
				)
			}

			logger.Info(
				"registry snapshot exported",
				"snapshot",
				name,
			)
			previous = name
		}

		if err := sleep(ctx, interval); err != nil {
			logger.Info("registry export stopped")
			return nil
		}
	}
}

func runRegistryPublish(
	ctx context.Context,
	interval time.Duration,
	root string,
	publish func(
		context.Context,
		string,
	) (githubpublish.Result, error),
	sleep func(context.Context, time.Duration) error,
	logger *slog.Logger,
) error {
	logger.Info(
		"registry publish started",
		"interval",
		interval,
	)

	var published string

	for {
		if err := ctx.Err(); err != nil {
			logger.Info("registry publish stopped")
			return nil
		}

		name, err := readRegistryPointer(root)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				logger.Error(
					"registry snapshot pointer failed",
					"error",
					err,
				)
			}
		} else if name != published {
			snapshot := filepath.Join(root, name)
			if err := usableAutomaticSnapshot(
				snapshot,
			); err != nil {
				logger.Error(
					"registry snapshot is not publishable",
					"snapshot",
					name,
					"error",
					err,
				)
			} else if result, err := publish(
				ctx,
				snapshot,
			); err != nil {
				if ctx.Err() != nil {
					logger.Info(
						"registry publish stopped",
					)
					return nil
				}

				logger.Error(
					"registry publish failed",
					"error",
					err,
				)
			} else {
				published = name
				if result.Changed {
					logger.Info(
						"registry published",
						"commit",
						result.CommitSHA,
					)
				} else {
					logger.Info(
						"registry unchanged",
					)
				}
			}
		}

		if err := sleep(
			ctx,
			registryPublishFollowInterval,
		); err != nil {
			logger.Info("registry publish stopped")
			return nil
		}
	}
}

func automaticSnapshotNameFor(
	now time.Time,
	random io.Reader,
) (string, error) {
	if random == nil {
		return "", errRegistryEntropyUnavailable
	}

	var entropy [16]byte
	if _, err := random.Read(entropy[:]); err != nil {
		return "", errRegistryEntropyUnavailable
	}

	return automaticSnapshotPrefix +
		now.UTC().Format("20060102T150405Z") +
		"-" +
		hex.EncodeToString(entropy[:]), nil
}

func writeRegistryPointer(
	root string,
	name string,
) error {
	if !validAutomaticSnapshotName(name) {
		return errInvalidRegistryPointer
	}

	pointer := filepath.Join(
		root,
		registryCurrentName,
	)
	temporary := pointer + ".tmp"
	if err := os.WriteFile(
		temporary,
		[]byte(name+"\n"),
		0o644,
	); err != nil {
		return err
	}

	if err := os.Rename(
		temporary,
		pointer,
	); err != nil {
		return err
	}

	return nil
}

func readRegistryPointer(
	root string,
) (string, error) {
	data, err := os.ReadFile(
		filepath.Join(root, registryCurrentName),
	)
	if err != nil {
		return "", err
	}

	name, found := strings.CutSuffix(
		string(data),
		"\n",
	)
	if !found ||
		strings.Contains(name, "\n") ||
		!validAutomaticSnapshotName(name) {
		return "", errInvalidRegistryPointer
	}

	return name, nil
}

func validAutomaticSnapshotName(name string) bool {
	return automaticSnapshotName.MatchString(name)
}

func usableAutomaticSnapshot(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 ||
		!info.IsDir() {
		return errInvalidRegistryPointer
	}

	return nil
}

func removeOldAutomaticSnapshots(
	root string,
	current string,
	previous string,
) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(
			name,
			automaticSnapshotPrefix,
		) {
			continue
		}

		if name == current || name == previous {
			continue
		}

		if err := removeSnapshotDirectory(
			filepath.Join(root, name),
		); err != nil {
			return err
		}
	}

	return nil
}

var removeSnapshotDirectory = os.RemoveAll

func sleepRegistryInterval(
	ctx context.Context,
	delay time.Duration,
) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

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
	"strings"
	"time"

	"github.com/joshternet/joshbot/internal/githubpublish"
)

const (
	registryPublishIntervalEnvironment = "JOSHBOT_REGISTRY_PUBLISH_INTERVAL"
	registryExportRootEnvironment      = "JOSHBOT_REGISTRY_EXPORT_ROOT"

	defaultRegistryPublishInterval = 15 * time.Minute
	defaultRegistryExportRoot      = "/exports"
	registryPublishRetryInterval   = 5 * time.Second

	registrySnapshotPrefix = ".joshbot-registry-"
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
	return root != "" &&
		clean == root &&
		filepath.IsAbs(root) &&
		clean != string(os.PathSeparator) &&
		!strings.Contains(root, "..")
}

func (operations runtimeOperations) publishRegistry(
	ctx context.Context,
) error {
	interval, err := loadRegistryPublishInterval(operations.getenv)
	if err != nil {
		return err
	}

	root, err := loadRegistryExportRoot(operations.getenv)
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

	return runRegistryPublish(
		ctx,
		interval,
		root,
		operations.export,
		operations.publish,
		time.Now,
		sleepRegistryInterval,
		random,
		operations.logger,
	)
}

func runRegistryPublish(
	ctx context.Context,
	interval time.Duration,
	root string,
	export func(context.Context, string) error,
	publish func(context.Context, string) (githubpublish.Result, error),
	now func() time.Time,
	sleep func(context.Context, time.Duration) error,
	random io.Reader,
	logger *slog.Logger,
) error {
	logger.Info("registry publish started", "interval", interval)

	for {
		if err := ctx.Err(); err != nil {
			logger.Info("registry publish stopped")
			return nil
		}

		name, err := registrySnapshotName(now(), random)
		if err != nil {
			logger.Error("registry snapshot name failed", "error", err)
			if err := sleep(ctx, registryPublishRetryInterval); err != nil {
				logger.Info("registry publish stopped")
				return nil
			}
			continue
		}

		snapshot := filepath.Join(root, name)
		if err := export(ctx, snapshot); err != nil {
			if ctx.Err() != nil {
				logger.Info("registry publish stopped")
				return nil
			}
			logger.Error("registry export failed", "error", err)
			if err := sleep(ctx, registryPublishRetryInterval); err != nil {
				logger.Info("registry publish stopped")
				return nil
			}
			continue
		}

		result, err := publish(ctx, snapshot)
		removeRegistrySnapshot(root, name)
		if err != nil {
			if ctx.Err() != nil {
				logger.Info("registry publish stopped")
				return nil
			}
			logger.Error("registry publish failed", "error", err)
			if err := sleep(ctx, registryPublishRetryInterval); err != nil {
				logger.Info("registry publish stopped")
				return nil
			}
			continue
		}

		if result.Changed {
			logger.Info(
				"registry published",
				"commit",
				result.CommitSHA,
			)
		} else {
			logger.Info("registry unchanged")
		}

		if err := sleep(ctx, interval); err != nil {
			logger.Info("registry publish stopped")
			return nil
		}
	}
}

func registrySnapshotName(
	now time.Time,
	random io.Reader,
) (string, error) {
	if random == nil {
		return "", errRegistryEntropyUnavailable
	}

	var entropy [16]byte
	if _, err := io.ReadFull(random, entropy[:]); err != nil {
		return "", errRegistryEntropyUnavailable
	}

	return registrySnapshotPrefix +
		now.UTC().Format("20060102T150405Z") +
		"-" +
		hex.EncodeToString(entropy[:]), nil
}

func removeRegistrySnapshot(root string, name string) {
	if !strings.HasPrefix(name, registrySnapshotPrefix) ||
		strings.Contains(name, "/") ||
		strings.Contains(name, string(os.PathSeparator)) {
		return
	}

	_ = os.RemoveAll(filepath.Join(root, name))
}

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

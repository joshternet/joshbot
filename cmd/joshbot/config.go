package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/joshternet/joshbot/internal/store"
	"github.com/joshternet/joshbot/internal/worker"
)

const (
	databaseURLEnvironment          = "JOSHBOT_DATABASE_URL"
	databasePasswordFileEnvironment = "JOSHBOT_DATABASE_PASSWORD_FILE"

	leaseDurationEnvironment     = "JOSHBOT_LEASE_DURATION"
	minOriginIntervalEnvironment = "JOSHBOT_MIN_ORIGIN_INTERVAL"
	pollIntervalEnvironment      = "JOSHBOT_POLL_INTERVAL"
	jobTimeoutEnvironment        = "JOSHBOT_JOB_TIMEOUT"
	completionGraceEnvironment   = "JOSHBOT_COMPLETION_GRACE"
	recheckIntervalEnvironment   = "JOSHBOT_RECHECK_INTERVAL"
	workerIDEnvironment          = "JOSHBOT_WORKER_ID"
	serviceInstanceIDEnvironment = "JOSHBOT_SERVICE_INSTANCE_ID"
	hostnameEnvironment          = "HOSTNAME"

	defaultLeaseDuration     = 5 * time.Minute
	defaultMinOriginInterval = time.Minute
	defaultPollInterval      = 30 * time.Second
	defaultJobTimeout        = 2 * time.Minute
	defaultCompletionGrace   = 30 * time.Second
	defaultRecheckInterval   = 24 * time.Hour

	generatedWorkerIDBytes     = 16
	maxRuntimeWorkerIDLength   = 128
	maxServiceInstanceIDLength = 128
	defaultDiscoveryInstanceID = "discovery"
)

var (
	errInvalidRuntimeConfiguration = errors.New(
		"runtime configuration is invalid",
	)
	errWorkerIDGeneration = errors.New(
		"worker ID generation failed",
	)
)

type environmentGetter func(string) string

type workerSettings struct {
	queue  store.QueueConfig
	worker worker.Config
}

func loadQueueConfig(
	getenv environmentGetter,
) (store.QueueConfig, error) {
	leaseDuration, err := positiveDurationSetting(
		getenv,
		leaseDurationEnvironment,
		defaultLeaseDuration,
	)
	if err != nil {
		return store.QueueConfig{}, err
	}

	minOriginInterval, err := positiveDurationSetting(
		getenv,
		minOriginIntervalEnvironment,
		defaultMinOriginInterval,
	)
	if err != nil {
		return store.QueueConfig{}, err
	}

	return store.QueueConfig{
		LeaseDuration:     leaseDuration,
		MinOriginInterval: minOriginInterval,
	}, nil
}

func loadWorkerSettings(
	getenv environmentGetter,
	random io.Reader,
) (workerSettings, error) {
	queueConfig, err := loadQueueConfig(getenv)
	if err != nil {
		return workerSettings{}, err
	}

	pollInterval, err := positiveDurationSetting(
		getenv,
		pollIntervalEnvironment,
		defaultPollInterval,
	)
	if err != nil {
		return workerSettings{}, err
	}

	jobTimeout, err := positiveDurationSetting(
		getenv,
		jobTimeoutEnvironment,
		defaultJobTimeout,
	)
	if err != nil {
		return workerSettings{}, err
	}

	completionGrace, err := positiveDurationSetting(
		getenv,
		completionGraceEnvironment,
		defaultCompletionGrace,
	)
	if err != nil {
		return workerSettings{}, err
	}

	recheckInterval, err := positiveDurationSetting(
		getenv,
		recheckIntervalEnvironment,
		defaultRecheckInterval,
	)
	if err != nil {
		return workerSettings{}, err
	}

	if completionGrace >= queueConfig.LeaseDuration ||
		jobTimeout >=
			queueConfig.LeaseDuration-completionGrace {
		return workerSettings{}, fmt.Errorf(
			"%w: job timeout and completion grace "+
				"must fit inside lease duration",
			errInvalidRuntimeConfiguration,
		)
	}

	workerID, err := runtimeWorkerID(
		getenv(workerIDEnvironment),
		random,
	)
	if err != nil {
		return workerSettings{}, err
	}

	return workerSettings{
		queue: queueConfig,
		worker: worker.Config{
			WorkerID:        workerID,
			PollInterval:    pollInterval,
			JobTimeout:      jobTimeout,
			CompletionGrace: completionGrace,
			RecheckInterval: recheckInterval,
		},
	}, nil
}

func positiveDurationSetting(
	getenv environmentGetter,
	name string,
	defaultValue time.Duration,
) (time.Duration, error) {
	value := getenv(name)
	if value == "" {
		return defaultValue, nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf(
			"%w: %s must be a positive duration",
			errInvalidRuntimeConfiguration,
			name,
		)
	}

	return duration, nil
}

func runtimeWorkerID(
	configured string,
	random io.Reader,
) (string, error) {
	if configured != "" {
		if !validRuntimeWorkerID(configured) {
			return "", fmt.Errorf(
				"%w: %s is invalid",
				errInvalidRuntimeConfiguration,
				workerIDEnvironment,
			)
		}

		return configured, nil
	}

	if random == nil {
		return "", errWorkerIDGeneration
	}

	randomBytes := make(
		[]byte,
		generatedWorkerIDBytes,
	)
	if _, err := io.ReadFull(
		random,
		randomBytes,
	); err != nil {
		return "", errWorkerIDGeneration
	}

	return "worker-" +
		hex.EncodeToString(randomBytes), nil
}

func validRuntimeWorkerID(workerID string) bool {
	return workerID != "" &&
		utf8.ValidString(workerID) &&
		utf8.RuneCountInString(workerID) <=
			maxRuntimeWorkerIDLength
}

// loadServiceInstanceID resolves the logical heartbeat slot for one process.
// A configured JOSHBOT_SERVICE_INSTANCE_ID wins. Otherwise fallback is used.
// The result is trimmed, valid UTF-8, and at most 128 bytes, matching the
// crawl_service_heartbeats instance_id limit.
func loadServiceInstanceID(
	getenv environmentGetter,
	fallback string,
) (string, error) {
	candidate := strings.TrimSpace(
		getenv(serviceInstanceIDEnvironment),
	)
	if candidate == "" {
		candidate = strings.TrimSpace(fallback)
	}
	if !validServiceInstanceID(candidate) {
		return "", fmt.Errorf(
			"%w: %s is invalid",
			errInvalidRuntimeConfiguration,
			serviceInstanceIDEnvironment,
		)
	}

	return candidate, nil
}

func discoveryServiceInstanceFallback(
	getenv environmentGetter,
) string {
	hostname := strings.TrimSpace(
		getenv(hostnameEnvironment),
	)
	if hostname == "" {
		return defaultDiscoveryInstanceID
	}

	return hostname
}

func validServiceInstanceID(instanceID string) bool {
	return instanceID != "" &&
		utf8.ValidString(instanceID) &&
		len(instanceID) <= maxServiceInstanceIDLength
}

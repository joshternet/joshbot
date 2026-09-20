package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/robots"
	"github.com/joshternet/joshbot/internal/store"
)

const (
	discoveryIntervalEnvironment    = "JOSHBOT_DISCOVERY_INTERVAL"
	discoveryPollEnvironment        = "JOSHBOT_DISCOVERY_POLL_INTERVAL"
	discoveryPageTimeoutEnvironment = "JOSHBOT_DISCOVERY_PAGE_TIMEOUT"

	defaultDiscoveryInterval     = 168 * time.Hour
	defaultDiscoveryPollInterval = 30 * time.Second
	defaultDiscoveryPageTimeout  = 30 * time.Second

	crawlMaxDepthEnvironment = "JOSHBOT_CRAWL_MAX_DEPTH"

	crawlMaxPagesEnvironment = "JOSHBOT_CRAWL_MAX_PAGES"

	crawlMaxPageBytesEnvironment = "JOSHBOT_CRAWL_MAX_PAGE_BYTES"

	crawlRequestDelayEnvironment = "JOSHBOT_CRAWL_REQUEST_DELAY"

	crawlRedirectLimitEnvironment = "JOSHBOT_CRAWL_REDIRECT_LIMIT"

	automaticCrawlEnabledEnvironment = "JOSHBOT_AUTOMATIC_CRAWL_ENABLED"

	automaticCrawlMaxPendingProbesEnvironment    = "JOSHBOT_AUTOMATIC_CRAWL_MAX_PENDING_PROBES"
	automaticCrawlMaxPromotionsPerRunEnvironment = "JOSHBOT_AUTOMATIC_CRAWL_MAX_PROMOTIONS_PER_RUN"
	automaticCrawlExcludedHostsEnvironment       = "JOSHBOT_AUTOMATIC_CRAWL_EXCLUDED_HOSTS"
	crawlTelemetryRetentionEnvironment           = "JOSHBOT_CRAWL_TELEMETRY_RETENTION"

	defaultAutomaticCrawlMaxPendingProbes    = 1000
	defaultAutomaticCrawlMaxPromotionsPerRun = 100
	defaultCrawlTelemetryRetention           = 30 * 24 * time.Hour
	defaultAutomaticCrawlExcludedHosts       = ""
)

type crawlRuntimeSettings struct {
	runner             discovery.CrawlRunnerConfig
	crawl              discovery.CrawlConfig
	automatic          store.AutomaticCrawlConfig
	telemetryRetention time.Duration
	signer             robots.RequestSigner
}

func loadCrawlRuntimeSettings(
	getenv environmentGetter,
) (crawlRuntimeSettings, error) {
	discoveryInterval, err := positiveDurationSetting(
		getenv,
		discoveryIntervalEnvironment,
		defaultDiscoveryInterval,
	)
	if err != nil {
		return crawlRuntimeSettings{}, err
	}

	pollInterval, err := positiveDurationSetting(
		getenv,
		discoveryPollEnvironment,
		defaultDiscoveryPollInterval,
	)
	if err != nil {
		return crawlRuntimeSettings{}, err
	}

	pageTimeout, err := positiveDurationSetting(
		getenv,
		discoveryPageTimeoutEnvironment,
		defaultDiscoveryPageTimeout,
	)
	if err != nil {
		return crawlRuntimeSettings{}, err
	}

	maxDepth, err := requiredIntegerSetting(
		getenv,
		crawlMaxDepthEnvironment,
		0,
	)
	if err != nil {
		return crawlRuntimeSettings{}, err
	}

	maxPages, err := requiredIntegerSetting(
		getenv,
		crawlMaxPagesEnvironment,
		1,
	)
	if err != nil {
		return crawlRuntimeSettings{}, err
	}

	maxPageBytes, err := requiredIntegerSetting(
		getenv,
		crawlMaxPageBytesEnvironment,
		1,
	)
	if err != nil {
		return crawlRuntimeSettings{}, err
	}

	requestDelay, err :=
		requiredNonNegativeDurationSetting(
			getenv,
			crawlRequestDelayEnvironment,
		)
	if err != nil {
		return crawlRuntimeSettings{}, err
	}

	redirectLimit, err := requiredIntegerSetting(
		getenv,
		crawlRedirectLimitEnvironment,
		1,
	)
	if err != nil {
		return crawlRuntimeSettings{}, err
	}

	automaticEnabled, err := optionalBooleanSetting(
		getenv,
		automaticCrawlEnabledEnvironment,
	)
	if err != nil {
		return crawlRuntimeSettings{}, err
	}

	automaticConfig := store.AutomaticCrawlConfig{
		Enabled: automaticEnabled,
		ExcludedHostSuffixes: settingOrDefault(
			getenv,
			automaticCrawlExcludedHostsEnvironment,
			defaultAutomaticCrawlExcludedHosts,
		),
	}
	automaticConfig.MaxAutomaticPromotionsPerRun, err =
		optionalPositiveIntegerSetting(
			getenv,
			automaticCrawlMaxPromotionsPerRunEnvironment,
			defaultAutomaticCrawlMaxPromotionsPerRun,
		)
	if err != nil {
		return crawlRuntimeSettings{}, err
	}
	if automaticEnabled {
		automaticConfig.MaxPendingProbes, err =
			optionalPositiveIntegerSetting(
				getenv,
				automaticCrawlMaxPendingProbesEnvironment,
				defaultAutomaticCrawlMaxPendingProbes,
			)
		if err != nil {
			return crawlRuntimeSettings{}, err
		}
	}

	telemetryRetention, err := positiveDurationSetting(
		getenv,
		crawlTelemetryRetentionEnvironment,
		defaultCrawlTelemetryRetention,
	)
	if err != nil {
		return crawlRuntimeSettings{}, err
	}

	return crawlRuntimeSettings{
		runner: discovery.CrawlRunnerConfig{
			DiscoveryInterval: discoveryInterval,
			PollInterval:      pollInterval,
		},
		crawl: discovery.CrawlConfig{
			MaxDepth:                     maxDepth,
			MaxPages:                     maxPages,
			MaxPageBytes:                 maxPageBytes,
			RequestDelay:                 requestDelay,
			RedirectLimit:                redirectLimit,
			PageTimeout:                  pageTimeout,
			MaxAutomaticPromotionsPerRun: automaticConfig.MaxAutomaticPromotionsPerRun,
		},
		automatic:          automaticConfig,
		telemetryRetention: telemetryRetention,
	}, nil
}

func settingOrDefault(getenv environmentGetter, name, fallback string) string {
	if value := getenv(name); value != "" {
		return value
	}
	return fallback
}

func optionalPositiveIntegerSetting(
	getenv environmentGetter,
	name string,
	defaultValue int,
) (int, error) {
	value := getenv(name)
	if value == "" {
		return defaultValue, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf(
			"%w: %s must be a positive integer",
			errInvalidRuntimeConfiguration,
			name,
		)
	}

	return parsed, nil
}

func optionalBooleanSetting(
	getenv environmentGetter,
	name string,
) (bool, error) {
	value := getenv(name)
	if value == "" {
		return false, nil
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf(
			"%w: %s must be a boolean",
			errInvalidRuntimeConfiguration,
			name,
		)
	}

	return parsed, nil
}

func requiredIntegerSetting(
	getenv environmentGetter,
	name string,
	minimum int,
) (int, error) {
	value := getenv(name)
	parsed, err := strconv.Atoi(value)
	if value == "" || err != nil || parsed < minimum {
		return 0, fmt.Errorf(
			"%w: %s must be an integer greater than or equal to %d",
			errInvalidRuntimeConfiguration,
			name,
			minimum,
		)
	}

	return parsed, nil
}

func requiredNonNegativeDurationSetting(
	getenv environmentGetter,
	name string,
) (time.Duration, error) {
	value := getenv(name)
	duration, err := time.ParseDuration(value)
	if value == "" || err != nil || duration < 0 {
		return 0, fmt.Errorf(
			"%w: %s must be a non-negative duration",
			errInvalidRuntimeConfiguration,
			name,
		)
	}

	return duration, nil
}

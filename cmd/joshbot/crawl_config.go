package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
)

const (
	crawlMaxDepthEnvironment = "JOSHBOT_CRAWL_MAX_DEPTH"

	crawlMaxPagesEnvironment = "JOSHBOT_CRAWL_MAX_PAGES"

	crawlMaxPageBytesEnvironment = "JOSHBOT_CRAWL_MAX_PAGE_BYTES"

	crawlRequestDelayEnvironment = "JOSHBOT_CRAWL_REQUEST_DELAY"

	crawlRedirectLimitEnvironment = "JOSHBOT_CRAWL_REDIRECT_LIMIT"
)

type crawlRuntimeSettings struct {
	runner discovery.CrawlRunnerConfig
	crawl  discovery.CrawlConfig
}

func loadCrawlRuntimeSettings(
	getenv environmentGetter,
) (crawlRuntimeSettings, error) {
	legacy, err := loadDiscoveryConfig(getenv)
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

	return crawlRuntimeSettings{
		runner: discovery.CrawlRunnerConfig{
			DiscoveryInterval: legacy.DiscoveryInterval,
			PollInterval:      legacy.PollInterval,
		},
		crawl: discovery.CrawlConfig{
			MaxDepth:      maxDepth,
			MaxPages:      maxPages,
			MaxPageBytes:  maxPageBytes,
			RequestDelay:  requestDelay,
			RedirectLimit: redirectLimit,
			PageTimeout:   legacy.PageTimeout,
		},
	}, nil
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

package main

import "time"

const defaultCrawlRequestDelay = time.Second

func loadWorkerRequestDelay(
	getenv environmentGetter,
) (time.Duration, error) {
	if getenv(crawlRequestDelayEnvironment) == "" {
		return defaultCrawlRequestDelay, nil
	}

	return requiredNonNegativeDurationSetting(
		getenv,
		crawlRequestDelayEnvironment,
	)
}

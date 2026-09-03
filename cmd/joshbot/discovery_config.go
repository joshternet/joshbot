package main

import (
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
)

const (
	discoveryIntervalEnvironment    = "JOSHBOT_DISCOVERY_INTERVAL"
	discoveryPollEnvironment        = "JOSHBOT_DISCOVERY_POLL_INTERVAL"
	discoveryPageTimeoutEnvironment = "JOSHBOT_DISCOVERY_PAGE_TIMEOUT"

	defaultDiscoveryInterval     = 168 * time.Hour
	defaultDiscoveryPollInterval = 30 * time.Second
	defaultDiscoveryPageTimeout  = 30 * time.Second
)

func loadDiscoveryConfig(
	getenv environmentGetter,
) (discovery.Config, error) {
	discoveryInterval, err := positiveDurationSetting(
		getenv,
		discoveryIntervalEnvironment,
		defaultDiscoveryInterval,
	)
	if err != nil {
		return discovery.Config{}, err
	}

	pollInterval, err := positiveDurationSetting(
		getenv,
		discoveryPollEnvironment,
		defaultDiscoveryPollInterval,
	)
	if err != nil {
		return discovery.Config{}, err
	}

	pageTimeout, err := positiveDurationSetting(
		getenv,
		discoveryPageTimeoutEnvironment,
		defaultDiscoveryPageTimeout,
	)
	if err != nil {
		return discovery.Config{}, err
	}

	return discovery.Config{
		DiscoveryInterval: discoveryInterval,
		PollInterval:      pollInterval,
		PageTimeout:       pageTimeout,
	}, nil
}

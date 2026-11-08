package main

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/robots"
	"github.com/joshternet/joshbot/internal/store"
)

const (
	robotsMaxCrawlDelayEnvironment       = "JOSHBOT_ROBOTS_MAX_CRAWL_DELAY"
	httpDialTimeoutEnvironment           = "JOSHBOT_HTTP_DIAL_TIMEOUT"
	httpTLSHandshakeTimeoutEnvironment   = "JOSHBOT_HTTP_TLS_HANDSHAKE_TIMEOUT"
	httpResponseHeaderTimeoutEnvironment = "JOSHBOT_HTTP_RESPONSE_HEADER_TIMEOUT"
)

// Shared construction boundary for all outbound runtime clients.
var newRuntimeHTTPChecker = robots.NewCheckerWithHTTPConfig

// Shared fleet-budget boundary for worker and discovery crawl clients.
var newRuntimeFleetBudget = func(
	pool *pgxpool.Pool,
	maxRequestsPerSecond int,
	maxBytesPerDay int64,
) (robots.FleetBudget, error) {
	return store.NewFleetBudget(
		pool,
		maxRequestsPerSecond,
		maxBytesPerDay,
	)
}

func loadHTTPConfig(getenv environmentGetter) (robots.HTTPConfig, error) {
	config := robots.DefaultHTTPConfig()
	for _, setting := range []struct {
		name  string
		value *time.Duration
	}{
		{robotsMaxCrawlDelayEnvironment, &config.MaxCrawlDelay},
		{httpDialTimeoutEnvironment, &config.DialTimeout},
		{httpTLSHandshakeTimeoutEnvironment, &config.TLSHandshakeTimeout},
		{httpResponseHeaderTimeoutEnvironment, &config.ResponseHeaderTimeout},
	} {
		value, err := positiveDurationSetting(getenv, setting.name, *setting.value)
		if err != nil {
			return robots.HTTPConfig{}, err
		}
		*setting.value = value
	}
	return config, nil
}

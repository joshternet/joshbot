package main

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/store"
)

func TestCrawlRuntimeConfigurationUsesExplicitBudgets(
	t *testing.T,
) {
	settings, err := loadCrawlRuntimeSettings(
		validCrawlRuntimeEnvironment().get,
	)
	if err != nil {
		t.Fatalf(
			"loadCrawlRuntimeSettings() error = %v, want nil",
			err,
		)
	}

	want := crawlRuntimeSettings{
		telemetryRetention: defaultCrawlTelemetryRetention,
		automatic: store.AutomaticCrawlConfig{
			MaxAutomaticPromotionsPerRun: defaultAutomaticCrawlMaxPromotionsPerRun,
			ExcludedHostSuffixes:         defaultAutomaticCrawlExcludedHosts,
		},
		runner: discovery.CrawlRunnerConfig{
			DiscoveryInterval: defaultDiscoveryInterval,
			PollInterval:      defaultDiscoveryPollInterval,
		},
		crawl: discovery.CrawlConfig{
			MaxDepth:                     2,
			MaxPages:                     32,
			MaxPageBytes:                 1048576,
			RequestDelay:                 250 * time.Millisecond,
			RedirectLimit:                5,
			PageTimeout:                  defaultDiscoveryPageTimeout,
			MaxAutomaticPromotionsPerRun: defaultAutomaticCrawlMaxPromotionsPerRun,
		},
	}

	if settings != want {
		t.Errorf(
			"settings = %#v, want %#v",
			settings,
			want,
		)
	}
}

func TestCrawlRuntimeConfigurationOverridesEverySetting(
	t *testing.T,
) {
	environment := validCrawlRuntimeEnvironment()
	environment[discoveryIntervalEnvironment] = "336h"
	environment[discoveryPollEnvironment] = "10s"
	environment[discoveryPageTimeoutEnvironment] = "15s"
	environment[crawlMaxDepthEnvironment] = "4"
	environment[crawlMaxPagesEnvironment] = "128"
	environment[crawlMaxPageBytesEnvironment] = "2097152"
	environment[crawlRequestDelayEnvironment] = "1.5s"
	environment[crawlRedirectLimitEnvironment] = "8"
	environment[automaticCrawlEnabledEnvironment] = "true"
	environment[automaticCrawlMaxPendingProbesEnvironment] = "250"
	environment[automaticCrawlMaxPromotionsPerRunEnvironment] = "17"
	environment[automaticCrawlExcludedHostsEnvironment] = "hosted.example,social.example"
	environment[crawlTelemetryRetentionEnvironment] = "168h"

	settings, err := loadCrawlRuntimeSettings(
		environment.get,
	)
	if err != nil {
		t.Fatalf(
			"loadCrawlRuntimeSettings() error = %v, want nil",
			err,
		)
	}

	want := crawlRuntimeSettings{
		telemetryRetention: 7 * 24 * time.Hour,
		runner: discovery.CrawlRunnerConfig{
			DiscoveryInterval: 336 * time.Hour,
			PollInterval:      10 * time.Second,
		},
		crawl: discovery.CrawlConfig{
			MaxDepth:                     4,
			MaxPages:                     128,
			MaxPageBytes:                 2097152,
			RequestDelay:                 1500 * time.Millisecond,
			RedirectLimit:                8,
			PageTimeout:                  15 * time.Second,
			MaxAutomaticPromotionsPerRun: 17,
		},
		automatic: store.AutomaticCrawlConfig{
			Enabled:                      true,
			MaxPendingProbes:             250,
			MaxAutomaticPromotionsPerRun: 17,
			ExcludedHostSuffixes:         "hosted.example,social.example",
		},
	}

	if settings != want {
		t.Errorf(
			"settings = %#v, want %#v",
			settings,
			want,
		)
	}
}

func TestCrawlRuntimeConfigurationAllowsZeroDepthAndDelay(
	t *testing.T,
) {
	environment := validCrawlRuntimeEnvironment()
	environment[crawlMaxDepthEnvironment] = "0"
	environment[crawlRequestDelayEnvironment] = "0s"

	settings, err := loadCrawlRuntimeSettings(
		environment.get,
	)
	if err != nil {
		t.Fatalf(
			"loadCrawlRuntimeSettings() error = %v, want nil",
			err,
		)
	}

	if settings.crawl.MaxDepth != 0 {
		t.Errorf(
			"MaxDepth = %d, want 0",
			settings.crawl.MaxDepth,
		)
	}

	if settings.crawl.RequestDelay != 0 {
		t.Errorf(
			"RequestDelay = %v, want 0",
			settings.crawl.RequestDelay,
		)
	}
}

func TestCrawlRuntimeConfigurationRejectsInvalidValues(
	t *testing.T,
) {
	tests := []struct {
		name  string
		key   string
		value *string
	}{
		{
			name:  "invalid discovery poll interval",
			key:   discoveryPollEnvironment,
			value: stringPointer("0s"),
		},
		{
			name:  "invalid discovery page timeout",
			key:   discoveryPageTimeoutEnvironment,
			value: stringPointer("0s"),
		},
		{
			name: "missing maximum depth",
			key:  crawlMaxDepthEnvironment,
		},
		{
			name:  "invalid maximum depth",
			key:   crawlMaxDepthEnvironment,
			value: stringPointer("invalid"),
		},
		{
			name:  "negative maximum depth",
			key:   crawlMaxDepthEnvironment,
			value: stringPointer("-1"),
		},
		{
			name: "missing maximum pages",
			key:  crawlMaxPagesEnvironment,
		},
		{
			name:  "invalid maximum pages",
			key:   crawlMaxPagesEnvironment,
			value: stringPointer("1.5"),
		},
		{
			name:  "zero maximum pages",
			key:   crawlMaxPagesEnvironment,
			value: stringPointer("0"),
		},
		{
			name: "missing maximum page bytes",
			key:  crawlMaxPageBytesEnvironment,
		},
		{
			name:  "invalid maximum page bytes",
			key:   crawlMaxPageBytesEnvironment,
			value: stringPointer("one-megabyte"),
		},
		{
			name:  "zero maximum page bytes",
			key:   crawlMaxPageBytesEnvironment,
			value: stringPointer("0"),
		},
		{
			name: "missing request delay",
			key:  crawlRequestDelayEnvironment,
		},
		{
			name:  "invalid request delay",
			key:   crawlRequestDelayEnvironment,
			value: stringPointer("later"),
		},
		{
			name:  "negative request delay",
			key:   crawlRequestDelayEnvironment,
			value: stringPointer("-1ms"),
		},
		{
			name: "missing redirect limit",
			key:  crawlRedirectLimitEnvironment,
		},
		{
			name:  "invalid redirect limit",
			key:   crawlRedirectLimitEnvironment,
			value: stringPointer("many"),
		},
		{
			name:  "zero redirect limit",
			key:   crawlRedirectLimitEnvironment,
			value: stringPointer("0"),
		},
		{
			name:  "invalid telemetry retention",
			key:   crawlTelemetryRetentionEnvironment,
			value: stringPointer("forever"),
		},
		{
			name:  "zero telemetry retention",
			key:   crawlTelemetryRetentionEnvironment,
			value: stringPointer("0s"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environment :=
				validCrawlRuntimeEnvironment()

			if test.value == nil {
				delete(environment, test.key)
			} else {
				environment[test.key] =
					*test.value
			}

			settings, err :=
				loadCrawlRuntimeSettings(
					environment.get,
				)

			if !errors.Is(
				err,
				errInvalidRuntimeConfiguration,
			) {
				t.Errorf(
					"loadCrawlRuntimeSettings() error = %v, want invalid configuration",
					err,
				)
			}

			if settings !=
				(crawlRuntimeSettings{}) {
				t.Errorf(
					"settings = %#v, want zero",
					settings,
				)
			}
		})
	}
}

func TestCrawlRuntimeConfigurationRejectsInvalidAutomaticExpansion(
	t *testing.T,
) {
	tests := []mapEnvironment{
		{
			automaticCrawlEnabledEnvironment: "sometimes",
		},
		{
			automaticCrawlEnabledEnvironment:          "true",
			automaticCrawlMaxPendingProbesEnvironment: "0",
		},
		{
			automaticCrawlEnabledEnvironment:          "true",
			automaticCrawlMaxPendingProbesEnvironment: "many",
		},
		{
			automaticCrawlEnabledEnvironment:             "true",
			automaticCrawlMaxPromotionsPerRunEnvironment: "0",
		},
		{
			automaticCrawlEnabledEnvironment:             "true",
			automaticCrawlMaxPromotionsPerRunEnvironment: "many",
		},
	}

	for _, overrides := range tests {
		environment := validCrawlRuntimeEnvironment()
		for key, value := range overrides {
			environment[key] = value
		}

		settings, err := loadCrawlRuntimeSettings(environment.get)
		if !errors.Is(err, errInvalidRuntimeConfiguration) {
			t.Errorf(
				"configuration error = %v, want invalid configuration",
				err,
			)
		}

		if settings != (crawlRuntimeSettings{}) {
			t.Errorf("settings = %#v, want zero", settings)
		}
	}
}

func TestCrawlRuntimeConfigurationDefaultsAutomaticBackpressure(
	t *testing.T,
) {
	environment := validCrawlRuntimeEnvironment()
	environment[automaticCrawlEnabledEnvironment] = "true"

	settings, err := loadCrawlRuntimeSettings(environment.get)
	if err != nil {
		t.Fatalf("load crawl settings: %v", err)
	}

	if settings.automatic.MaxPendingProbes !=
		defaultAutomaticCrawlMaxPendingProbes {
		t.Errorf(
			"MaxPendingProbes = %d, want %d",
			settings.automatic.MaxPendingProbes,
			defaultAutomaticCrawlMaxPendingProbes,
		)
	}
	if settings.automatic.MaxAutomaticPromotionsPerRun !=
		defaultAutomaticCrawlMaxPromotionsPerRun {
		t.Errorf(
			"MaxAutomaticPromotionsPerRun = %d, want %d",
			settings.automatic.MaxAutomaticPromotionsPerRun,
			defaultAutomaticCrawlMaxPromotionsPerRun,
		)
	}
	if settings.crawl.MaxAutomaticPromotionsPerRun !=
		defaultAutomaticCrawlMaxPromotionsPerRun {
		t.Errorf(
			"crawl MaxAutomaticPromotionsPerRun = %d, want %d",
			settings.crawl.MaxAutomaticPromotionsPerRun,
			defaultAutomaticCrawlMaxPromotionsPerRun,
		)
	}
}

func TestCrawlRuntimeConfigurationPreservesSchedulingFailures(
	t *testing.T,
) {
	environment := validCrawlRuntimeEnvironment()
	environment[discoveryIntervalEnvironment] = "0s"

	settings, err := loadCrawlRuntimeSettings(
		environment.get,
	)

	if !errors.Is(
		err,
		errInvalidRuntimeConfiguration,
	) {
		t.Errorf(
			"loadCrawlRuntimeSettings() error = %v, want invalid configuration",
			err,
		)
	}

	if settings != (crawlRuntimeSettings{}) {
		t.Errorf(
			"settings = %#v, want zero",
			settings,
		)
	}
}

func TestRequiredIntegerSetting(t *testing.T) {
	environment := mapEnvironment{
		"VALUE": "7",
	}

	value, err := requiredIntegerSetting(
		environment.get,
		"VALUE",
		1,
	)
	if err != nil {
		t.Fatalf(
			"requiredIntegerSetting() error = %v, want nil",
			err,
		)
	}

	if value != 7 {
		t.Errorf(
			"value = %d, want 7",
			value,
		)
	}
}

func TestRequiredNonNegativeDurationSetting(
	t *testing.T,
) {
	environment := mapEnvironment{
		"VALUE": "125ms",
	}

	value, err :=
		requiredNonNegativeDurationSetting(
			environment.get,
			"VALUE",
		)
	if err != nil {
		t.Fatalf(
			"requiredNonNegativeDurationSetting() error = %v, want nil",
			err,
		)
	}

	if value != 125*time.Millisecond {
		t.Errorf(
			"value = %v, want %v",
			value,
			125*time.Millisecond,
		)
	}
}

func TestValidCrawlRuntimeEnvironmentIsIndependent(
	t *testing.T,
) {
	first := validCrawlRuntimeEnvironment()
	second := validCrawlRuntimeEnvironment()

	first[crawlMaxPagesEnvironment] = "1"

	if reflect.DeepEqual(first, second) {
		t.Error(
			"validCrawlRuntimeEnvironment() returned shared state",
		)
	}
}

func validCrawlRuntimeEnvironment() mapEnvironment {
	return mapEnvironment{
		crawlMaxDepthEnvironment:      "2",
		crawlMaxPagesEnvironment:      "32",
		crawlMaxPageBytesEnvironment:  "1048576",
		crawlRequestDelayEnvironment:  "250ms",
		crawlRedirectLimitEnvironment: "5",
		webBotAuthModeEnvironment:     webBotAuthModeUnsigned,
	}
}

func stringPointer(value string) *string {
	return &value
}

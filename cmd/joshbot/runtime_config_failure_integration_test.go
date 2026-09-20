package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type runtimeConfigFailureIntegrationEnvironment map[string]string

func (environment runtimeConfigFailureIntegrationEnvironment) get(name string) string {
	return environment[name]
}

type runtimeConfigFailureIntegrationReader struct {
	err error
}

func (reader runtimeConfigFailureIntegrationReader) Read([]byte) (int, error) {
	return 0, reader.err
}

func TestRuntimeConfigFailureIntegrationWorkerSettings(t *testing.T) {
	tests := []struct {
		name   string
		env    runtimeConfigFailureIntegrationEnvironment
		random io.Reader
		want   error
	}{
		{
			name: "invalid lease duration",
			env: runtimeConfigFailureIntegrationEnvironment{
				leaseDurationEnvironment: "0s",
			},
			random: strings.NewReader(strings.Repeat("x", generatedWorkerIDBytes)),
			want:   errInvalidRuntimeConfiguration,
		},
		{
			name: "invalid minimum origin interval",
			env: runtimeConfigFailureIntegrationEnvironment{
				minOriginIntervalEnvironment: "later",
			},
			random: strings.NewReader(strings.Repeat("x", generatedWorkerIDBytes)),
			want:   errInvalidRuntimeConfiguration,
		},
		{
			name: "invalid poll interval",
			env: runtimeConfigFailureIntegrationEnvironment{
				pollIntervalEnvironment: "0s",
			},
			random: strings.NewReader(strings.Repeat("x", generatedWorkerIDBytes)),
			want:   errInvalidRuntimeConfiguration,
		},
		{
			name: "invalid job timeout",
			env: runtimeConfigFailureIntegrationEnvironment{
				jobTimeoutEnvironment: "-1s",
			},
			random: strings.NewReader(strings.Repeat("x", generatedWorkerIDBytes)),
			want:   errInvalidRuntimeConfiguration,
		},
		{
			name: "invalid completion grace",
			env: runtimeConfigFailureIntegrationEnvironment{
				completionGraceEnvironment: "garbage",
			},
			random: strings.NewReader(strings.Repeat("x", generatedWorkerIDBytes)),
			want:   errInvalidRuntimeConfiguration,
		},
		{
			name: "invalid recheck interval",
			env: runtimeConfigFailureIntegrationEnvironment{
				recheckIntervalEnvironment: "0s",
			},
			random: strings.NewReader(strings.Repeat("x", generatedWorkerIDBytes)),
			want:   errInvalidRuntimeConfiguration,
		},
		{
			name: "completion budget exceeds lease",
			env: runtimeConfigFailureIntegrationEnvironment{
				leaseDurationEnvironment:   "1m",
				jobTimeoutEnvironment:      "45s",
				completionGraceEnvironment: "30s",
			},
			random: strings.NewReader(strings.Repeat("x", generatedWorkerIDBytes)),
			want:   errInvalidRuntimeConfiguration,
		},
		{
			name: "configured worker ID invalid",
			env: runtimeConfigFailureIntegrationEnvironment{
				workerIDEnvironment: strings.Repeat("x", maxRuntimeWorkerIDLength+1),
			},
			random: strings.NewReader(strings.Repeat("x", generatedWorkerIDBytes)),
			want:   errInvalidRuntimeConfiguration,
		},
		{
			name:   "missing random source",
			env:    runtimeConfigFailureIntegrationEnvironment{},
			random: nil,
			want:   errWorkerIDGeneration,
		},
		{
			name: "random source failure",
			env:  runtimeConfigFailureIntegrationEnvironment{},
			random: runtimeConfigFailureIntegrationReader{
				err: errors.New("integration random failure"),
			},
			want: errWorkerIDGeneration,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings, err := loadWorkerSettings(test.env.get, test.random)
			if !errors.Is(err, test.want) {
				t.Fatalf("loadWorkerSettings() error = %v, want %v", err, test.want)
			}
			if settings != (workerSettings{}) {
				t.Errorf("loadWorkerSettings() = %#v, want zero settings", settings)
			}
		})
	}
}

func TestRuntimeConfigFailureIntegrationCrawlSettings(t *testing.T) {
	base := runtimeConfigFailureIntegrationCrawlEnvironment()

	tests := []struct {
		name  string
		key   string
		value *string
	}{
		{name: "invalid discovery interval", key: discoveryIntervalEnvironment, value: runtimeConfigFailureIntegrationString("0s")},
		{name: "invalid discovery poll", key: discoveryPollEnvironment, value: runtimeConfigFailureIntegrationString("0s")},
		{name: "invalid page timeout", key: discoveryPageTimeoutEnvironment, value: runtimeConfigFailureIntegrationString("0s")},
		{name: "missing max depth", key: crawlMaxDepthEnvironment},
		{name: "invalid max pages", key: crawlMaxPagesEnvironment, value: runtimeConfigFailureIntegrationString("0")},
		{name: "invalid max bytes", key: crawlMaxPageBytesEnvironment, value: runtimeConfigFailureIntegrationString("nope")},
		{name: "negative request delay", key: crawlRequestDelayEnvironment, value: runtimeConfigFailureIntegrationString("-1ms")},
		{name: "invalid redirect limit", key: crawlRedirectLimitEnvironment, value: runtimeConfigFailureIntegrationString("0")},
		{name: "invalid automatic boolean", key: automaticCrawlEnabledEnvironment, value: runtimeConfigFailureIntegrationString("sometimes")},
		{name: "invalid promotions", key: automaticCrawlMaxPromotionsPerRunEnvironment, value: runtimeConfigFailureIntegrationString("0")},
		{name: "invalid telemetry retention", key: crawlTelemetryRetentionEnvironment, value: runtimeConfigFailureIntegrationString("0s")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environment := runtimeConfigFailureIntegrationClone(base)
			if test.value == nil {
				delete(environment, test.key)
			} else {
				environment[test.key] = *test.value
			}

			settings, err := loadCrawlRuntimeSettings(environment.get)
			if !errors.Is(err, errInvalidRuntimeConfiguration) {
				t.Fatalf("loadCrawlRuntimeSettings() error = %v, want %v", err, errInvalidRuntimeConfiguration)
			}
			if settings != (crawlRuntimeSettings{}) {
				t.Errorf("loadCrawlRuntimeSettings() = %#v, want zero settings", settings)
			}
		})
	}

	t.Run("invalid pending probes when automatic crawling enabled", func(t *testing.T) {
		environment := runtimeConfigFailureIntegrationClone(base)
		environment[automaticCrawlEnabledEnvironment] = "true"
		environment[automaticCrawlMaxPendingProbesEnvironment] = "0"

		settings, err := loadCrawlRuntimeSettings(environment.get)
		if !errors.Is(err, errInvalidRuntimeConfiguration) {
			t.Fatalf("loadCrawlRuntimeSettings() error = %v, want %v", err, errInvalidRuntimeConfiguration)
		}
		if settings != (crawlRuntimeSettings{}) {
			t.Errorf("loadCrawlRuntimeSettings() = %#v, want zero settings", settings)
		}
	})
}

func TestRuntimeConfigFailureIntegrationPublishSettings(t *testing.T) {
	for _, environment := range []runtimeConfigFailureIntegrationEnvironment{
		{},
		{publishOwnerEnvironment: "joshternet"},
		{
			publishOwnerEnvironment:      "joshternet",
			publishRepositoryEnvironment: "index-data",
		},
	} {
		settings, err := loadPublishSettings(environment.get)
		if !errors.Is(err, errInvalidPublishConfiguration) {
			t.Errorf("loadPublishSettings(%v) error = %v, want %v", environment, err, errInvalidPublishConfiguration)
		}
		if settings != (publishSettings{}) {
			t.Errorf("loadPublishSettings(%v) = %#v, want zero settings", environment, settings)
		}
	}

	missing := filepath.Join(t.TempDir(), "missing-token")
	if _, err := readGitHubTokenFile(missing); !errors.Is(err, errOpenGitHubTokenFile) {
		t.Errorf("readGitHubTokenFile(missing) error = %v, want %v", err, errOpenGitHubTokenFile)
	}

	readFailure := errors.New("integration GitHub token read failure")
	if _, err := readGitHubToken(runtimeConfigFailureIntegrationReader{err: readFailure}); !errors.Is(err, errReadGitHubTokenFile) {
		t.Errorf("readGitHubToken(read failure) error = %v, want %v", err, errReadGitHubTokenFile)
	}

	invalid := []struct {
		name string
		raw  string
		want error
	}{
		{name: "empty", raw: "\n", want: errEmptyGitHubToken},
		{name: "too large", raw: strings.Repeat("x", maxGitHubTokenSize+1), want: errGitHubTokenTooLarge},
		{name: "surrounding whitespace", raw: " token ", want: errInvalidGitHubToken},
		{name: "control character", raw: "token\x00value", want: errInvalidGitHubToken},
		{name: "invalid UTF-8", raw: string([]byte{0xff}), want: errInvalidGitHubToken},
	}

	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			token, err := readGitHubToken(strings.NewReader(test.raw))
			if !errors.Is(err, test.want) || token != "" {
				t.Errorf("readGitHubToken() = %q, %v, want empty, %v", token, err, test.want)
			}
		})
	}

	t.Run("load settings propagates token open failure", func(t *testing.T) {
		environment := runtimeConfigFailureIntegrationEnvironment{
			publishOwnerEnvironment:      "joshternet",
			publishRepositoryEnvironment: "index-data",
			githubTokenFileEnvironment:   missing,
		}
		settings, err := loadPublishSettings(environment.get)
		if !errors.Is(err, errOpenGitHubTokenFile) {
			t.Fatalf("loadPublishSettings() error = %v, want %v", err, errOpenGitHubTokenFile)
		}
		if settings != (publishSettings{}) {
			t.Errorf("loadPublishSettings() = %#v, want zero settings", settings)
		}
	})
}

func TestRuntimeConfigFailureIntegrationReportSettings(t *testing.T) {
	if settings, err := loadReportSettings(nil); !errors.Is(err, errInvalidReportConfiguration) || settings != (reportSettings{}) {
		t.Errorf("loadReportSettings(nil) = %#v, %v", settings, err)
	}

	tests := []runtimeConfigFailureIntegrationEnvironment{
		{},
		{
			reportTokenFileEnvironment:     "/secret/token",
			reportListenAddressEnvironment: "localhost",
		},
		{
			reportTokenFileEnvironment:     "/secret/token",
			reportListenAddressEnvironment: "localhost:0",
		},
		{
			reportTokenFileEnvironment:       "/secret/token",
			automaticCrawlEnabledEnvironment: "not-a-bool",
		},
		{
			reportTokenFileEnvironment:                "/secret/token",
			automaticCrawlEnabledEnvironment:          "true",
			automaticCrawlMaxPendingProbesEnvironment: "0",
		},
	}

	for index, environment := range tests {
		settings, err := loadReportSettings(environment.get)
		if err == nil || settings != (reportSettings{}) {
			t.Errorf("report case %d = %#v, %v, want zero settings and error", index, settings, err)
		}
	}

	missing := filepath.Join(t.TempDir(), "missing-report-token")
	if _, err := readReportTokenFile(missing); !errors.Is(err, errOpenReportTokenFile) {
		t.Errorf("readReportTokenFile(missing) error = %v, want %v", err, errOpenReportTokenFile)
	}

	if _, err := readReportToken(nil); !errors.Is(err, errReportTokenReaderUnavailable) {
		t.Errorf("readReportToken(nil) error = %v, want %v", err, errReportTokenReaderUnavailable)
	}

	readFailure := errors.New("integration report token read failure")
	if _, err := readReportToken(runtimeConfigFailureIntegrationReader{err: readFailure}); !errors.Is(err, errReadReportTokenFile) {
		t.Errorf("readReportToken(read failure) error = %v, want %v", err, errReadReportTokenFile)
	}

	invalid := []struct {
		name string
		raw  string
		want error
	}{
		{name: "empty", raw: "\r\n", want: errEmptyReportToken},
		{name: "too large", raw: strings.Repeat("x", maxReportTokenSize+1), want: errReportTokenTooLarge},
		{name: "multiline", raw: "first\nsecond", want: errInvalidReportToken},
	}

	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			token, err := readReportToken(strings.NewReader(test.raw))
			if !errors.Is(err, test.want) || token != "" {
				t.Errorf("readReportToken() = %q, %v, want empty, %v", token, err, test.want)
			}
		})
	}
}

func TestRuntimeConfigFailureIntegrationWebBotAuthSettings(t *testing.T) {
	if identity, err := loadWebBotAuthIdentity(nil); !errors.Is(err, errInvalidWebBotAuthConfiguration) || identity != nil {
		t.Errorf("loadWebBotAuthIdentity(nil) = %#v, %v", identity, err)
	}

	if err := validateWebBotAuthIdentity(runtimeConfigFailureIntegrationEnvironment{}.get); err != nil {
		t.Errorf("validateWebBotAuthIdentity(unset) error = %v, want nil", err)
	}

	whitespace := runtimeConfigFailureIntegrationEnvironment{
		webBotAuthPrivateKeyFileEnvironment: " /secret/key ",
	}
	if identity, err := loadWebBotAuthIdentity(whitespace.get); !errors.Is(err, errInvalidWebBotAuthConfiguration) || identity != nil {
		t.Errorf("loadWebBotAuthIdentity(whitespace) = %#v, %v", identity, err)
	}

	missing := filepath.Join(t.TempDir(), "missing-private-key.pem")
	if identity, err := readWebBotAuthIdentityFile(missing); !errors.Is(err, errOpenWebBotAuthPrivateKeyFile) || identity != nil {
		t.Errorf("readWebBotAuthIdentityFile(missing) = %#v, %v", identity, err)
	}

	invalidFile := filepath.Join(t.TempDir(), "invalid-private-key.pem")
	if err := os.WriteFile(invalidFile, []byte("not a private key"), 0o600); err != nil {
		t.Fatalf("write invalid private key: %v", err)
	}
	if identity, err := readWebBotAuthIdentityFile(invalidFile); !errors.Is(err, errInvalidWebBotAuthPrivateKey) || identity != nil {
		t.Errorf("readWebBotAuthIdentityFile(invalid) = %#v, %v", identity, err)
	}
}

func runtimeConfigFailureIntegrationCrawlEnvironment() runtimeConfigFailureIntegrationEnvironment {
	return runtimeConfigFailureIntegrationEnvironment{
		crawlMaxDepthEnvironment:      "2",
		crawlMaxPagesEnvironment:      "32",
		crawlMaxPageBytesEnvironment:  "1048576",
		crawlRequestDelayEnvironment:  "250ms",
		crawlRedirectLimitEnvironment: "5",
	}
}

func runtimeConfigFailureIntegrationClone(
	source runtimeConfigFailureIntegrationEnvironment,
) runtimeConfigFailureIntegrationEnvironment {
	clone := make(runtimeConfigFailureIntegrationEnvironment, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func runtimeConfigFailureIntegrationString(value string) *string {
	return new(value)
}

package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeConfigurationIntegrationLoadsOperationalSettingsAndWebBotAuth(
	t *testing.T,
) {
	keyPath :=
		writeRuntimeConfigurationIntegrationKey(t)

	environment := map[string]string{
		leaseDurationEnvironment:                     "10m",
		minOriginIntervalEnvironment:                 "2s",
		pollIntervalEnvironment:                      "3s",
		jobTimeoutEnvironment:                        "30s",
		completionGraceEnvironment:                   "5s",
		recheckIntervalEnvironment:                   "12h",
		workerIDEnvironment:                          "integration-worker",
		discoveryIntervalEnvironment:                 "24h",
		discoveryPollEnvironment:                     "4s",
		discoveryPageTimeoutEnvironment:              "7s",
		crawlMaxDepthEnvironment:                     "3",
		crawlMaxPagesEnvironment:                     "16",
		crawlMaxPageBytesEnvironment:                 "524288",
		crawlRequestDelayEnvironment:                 "125ms",
		crawlRedirectLimitEnvironment:                "4",
		automaticCrawlEnabledEnvironment:             "true",
		automaticCrawlMaxPendingProbesEnvironment:    "75",
		automaticCrawlMaxPromotionsPerRunEnvironment: "11",
		automaticCrawlExcludedHostsEnvironment:       "hosted.example,social.example",
		crawlTelemetryRetentionEnvironment:           "72h",
		webBotAuthPrivateKeyFileEnvironment:          keyPath,
	}

	getenv := func(name string) string {
		return environment[name]
	}

	workerSettings, err :=
		loadWorkerSettings(
			getenv,
			nil,
		)
	if err != nil {
		t.Fatalf(
			"loadWorkerSettings() error = %v",
			err,
		)
	}

	if workerSettings.worker.WorkerID !=
		"integration-worker" {
		t.Errorf(
			"WorkerID = %q, want integration-worker",
			workerSettings.worker.WorkerID,
		)
	}

	if workerSettings.queue.LeaseDuration !=
		10*time.Minute {
		t.Errorf(
			"LeaseDuration = %v, want 10m",
			workerSettings.queue.LeaseDuration,
		)
	}

	if workerSettings.worker.JobTimeout !=
		30*time.Second ||
		workerSettings.worker.CompletionGrace !=
			5*time.Second {
		t.Errorf(
			"worker timeout settings = %v/%v, want 30s/5s",
			workerSettings.worker.JobTimeout,
			workerSettings.worker.CompletionGrace,
		)
	}

	crawlSettings, err :=
		loadCrawlRuntimeSettings(getenv)
	if err != nil {
		t.Fatalf(
			"loadCrawlRuntimeSettings() error = %v",
			err,
		)
	}

	if crawlSettings.runner.DiscoveryInterval !=
		24*time.Hour ||
		crawlSettings.runner.PollInterval !=
			4*time.Second {
		t.Errorf(
			"runner settings = %v/%v, want 24h/4s",
			crawlSettings.runner.DiscoveryInterval,
			crawlSettings.runner.PollInterval,
		)
	}

	if crawlSettings.crawl.MaxDepth != 3 ||
		crawlSettings.crawl.MaxPages != 16 ||
		crawlSettings.crawl.RequestDelay !=
			125*time.Millisecond ||
		crawlSettings.crawl.PageTimeout !=
			7*time.Second {
		t.Errorf(
			"crawl settings = %#v",
			crawlSettings.crawl,
		)
	}

	if !crawlSettings.automatic.Enabled ||
		crawlSettings.automatic.MaxPendingProbes !=
			75 ||
		crawlSettings.automatic.
			MaxAutomaticPromotionsPerRun != 11 ||
		crawlSettings.automatic.
			ExcludedHostSuffixes !=
			"hosted.example,social.example" {
		t.Errorf(
			"automatic settings = %#v",
			crawlSettings.automatic,
		)
	}

	if crawlSettings.telemetryRetention !=
		72*time.Hour {
		t.Errorf(
			"telemetry retention = %v, want 72h",
			crawlSettings.telemetryRetention,
		)
	}

	identity, err :=
		loadWebBotAuthIdentity(getenv)
	if err != nil {
		t.Fatalf(
			"loadWebBotAuthIdentity() error = %v",
			err,
		)
	}

	if identity == nil ||
		identity.KeyID() == "" {
		t.Fatal(
			"loaded Web Bot Auth identity is missing",
		)
	}

	signer, err := identity.Signer()
	if err != nil {
		t.Fatalf(
			"Identity.Signer() error = %v",
			err,
		)
	}

	request, err := http.NewRequest(
		http.MethodGet,
		"https://example.com/page",
		nil,
	)
	if err != nil {
		t.Fatalf(
			"http.NewRequest() error = %v",
			err,
		)
	}

	if err := signer.Sign(request); err != nil {
		t.Fatalf(
			"Signer.Sign() error = %v",
			err,
		)
	}

	for _, header := range []string{
		"Signature-Agent",
		"Signature-Input",
		"Signature",
	} {
		if request.Header.Get(header) == "" {
			t.Errorf(
				"%s header is empty",
				header,
			)
		}
	}
}

func writeRuntimeConfigurationIntegrationKey(
	t *testing.T,
) string {
	t.Helper()

	seed := make(
		[]byte,
		ed25519.SeedSize,
	)

	for index := range seed {
		seed[index] = byte(255 - index)
	}

	privateKey :=
		ed25519.NewKeyFromSeed(seed)

	der, err :=
		x509.MarshalPKCS8PrivateKey(
			privateKey,
		)
	if err != nil {
		t.Fatalf(
			"MarshalPKCS8PrivateKey() error = %v",
			err,
		)
	}

	data := pem.EncodeToMemory(
		&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: der,
		},
	)
	if data == nil {
		t.Fatal(
			"pem.EncodeToMemory() returned nil",
		)
	}

	path := filepath.Join(
		t.TempDir(),
		"web-bot-auth-private-key.pem",
	)

	if err := os.WriteFile(
		path,
		data,
		0o600,
	); err != nil {
		t.Fatalf(
			"write Web Bot Auth key: %v",
			err,
		)
	}

	return path
}

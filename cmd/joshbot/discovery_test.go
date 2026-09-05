package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
)

func (operations *fakeCommandOperations) discover(
	_ context.Context,
	once bool,
) error {
	if once {
		return operations.result("discover-once")
	}

	return operations.result("discover")
}

func TestRunHelpIncludesDiscovery(t *testing.T) {
	if !strings.Contains(helpText, "discover") {
		t.Fatal(
			"help text does not contain discover",
		)
	}
}

func TestRunDispatchesDiscovery(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCall   string
		wantOutput string
	}{
		{
			name:     "long running",
			args:     []string{"discover"},
			wantCall: "discover",
		},
		{
			name:       "one attempt",
			args:       []string{"discover", "--once"},
			wantCall:   "discover-once",
			wantOutput: "discovery attempt complete\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations := &fakeCommandOperations{}
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			code := runWithOperations(
				context.Background(),
				test.args,
				&stdout,
				&stderr,
				operations,
			)

			if code != exitSuccess {
				t.Errorf(
					"exit code = %d, want %d",
					code,
					exitSuccess,
				)
			}

			if stdout.String() != test.wantOutput {
				t.Errorf(
					"stdout = %q, want %q",
					stdout.String(),
					test.wantOutput,
				)
			}

			if stderr.Len() != 0 {
				t.Errorf(
					"stderr = %q, want empty",
					stderr.String(),
				)
			}

			if len(operations.calls) != 1 ||
				operations.calls[0] != test.wantCall {
				t.Errorf(
					"calls = %#v, want [%q]",
					operations.calls,
					test.wantCall,
				)
			}
		})
	}
}

func TestRunRejectsInvalidDiscoveryArguments(
	t *testing.T,
) {
	tests := [][]string{
		{"discover", "--unexpected"},
		{"discover", "--once", "extra"},
		{"discover", "extra"},
	}

	for _, args := range tests {
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		code := runWithOperations(
			context.Background(),
			args,
			&stdout,
			&stderr,
			&fakeCommandOperations{},
		)

		if code != exitUsage {
			t.Errorf(
				"runWithOperations(%q) code = %d, want %d",
				args,
				code,
				exitUsage,
			)
		}

		if stdout.Len() != 0 {
			t.Errorf(
				"runWithOperations(%q) stdout = %q, want empty",
				args,
				stdout.String(),
			)
		}

		if !strings.Contains(
			stderr.String(),
			"optional --once flag",
		) {
			t.Errorf(
				"runWithOperations(%q) stderr = %q, want discovery usage",
				args,
				stderr.String(),
			)
		}
	}
}

func TestRunReturnsDiscoveryFailures(t *testing.T) {
	operationErr := errors.New(
		"discovery operation failed",
	)

	tests := []struct {
		name      string
		args      []string
		operation string
	}{
		{
			name:      "long running",
			args:      []string{"discover"},
			operation: "discover",
		},
		{
			name: "one attempt",
			args: []string{
				"discover",
				"--once",
			},
			operation: "discover-once",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations := &fakeCommandOperations{
				errors: map[string]error{
					test.operation: operationErr,
				},
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			code := runWithOperations(
				context.Background(),
				test.args,
				&stdout,
				&stderr,
				operations,
			)

			if code != exitFailure {
				t.Errorf(
					"exit code = %d, want %d",
					code,
					exitFailure,
				)
			}

			if stdout.Len() != 0 {
				t.Errorf(
					"stdout = %q, want empty",
					stdout.String(),
				)
			}

			if !strings.Contains(
				stderr.String(),
				operationErr.Error(),
			) {
				t.Errorf(
					"stderr = %q, want operation error",
					stderr.String(),
				)
			}
		})
	}
}

func TestDiscoveryConfigurationDefaults(
	t *testing.T,
) {
	config, err := loadDiscoveryConfig(
		mapEnvironment{}.get,
	)
	if err != nil {
		t.Fatalf(
			"loadDiscoveryConfig() error = %v, want nil",
			err,
		)
	}

	want := discovery.Config{
		DiscoveryInterval: defaultDiscoveryInterval,
		PollInterval:      defaultDiscoveryPollInterval,
		PageTimeout:       defaultDiscoveryPageTimeout,
	}
	if config != want {
		t.Errorf(
			"config = %#v, want %#v",
			config,
			want,
		)
	}
}

func TestDiscoveryConfigurationOverrides(
	t *testing.T,
) {
	config, err := loadDiscoveryConfig(
		mapEnvironment{
			discoveryIntervalEnvironment:    "336h",
			discoveryPollEnvironment:        "10s",
			discoveryPageTimeoutEnvironment: "15s",
		}.get,
	)
	if err != nil {
		t.Fatalf(
			"loadDiscoveryConfig() error = %v, want nil",
			err,
		)
	}

	want := discovery.Config{
		DiscoveryInterval: 336 * time.Hour,
		PollInterval:      10 * time.Second,
		PageTimeout:       15 * time.Second,
	}
	if config != want {
		t.Errorf(
			"config = %#v, want %#v",
			config,
			want,
		)
	}
}

func TestDiscoveryConfigurationRejectsInvalidValues(
	t *testing.T,
) {
	tests := []struct {
		name        string
		environment mapEnvironment
	}{
		{
			name: "invalid discovery interval",
			environment: mapEnvironment{
				discoveryIntervalEnvironment: "7d",
			},
		},
		{
			name: "zero discovery interval",
			environment: mapEnvironment{
				discoveryIntervalEnvironment: "0s",
			},
		},
		{
			name: "invalid poll interval",
			environment: mapEnvironment{
				discoveryPollEnvironment: "invalid",
			},
		},
		{
			name: "negative poll interval",
			environment: mapEnvironment{
				discoveryPollEnvironment: "-1s",
			},
		},
		{
			name: "invalid page timeout",
			environment: mapEnvironment{
				discoveryPageTimeoutEnvironment: "invalid",
			},
		},
		{
			name: "zero page timeout",
			environment: mapEnvironment{
				discoveryPageTimeoutEnvironment: "0s",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := loadDiscoveryConfig(
				test.environment.get,
			)
			if !errors.Is(
				err,
				errInvalidRuntimeConfiguration,
			) {
				t.Errorf(
					"loadDiscoveryConfig() error = %v, want invalid configuration",
					err,
				)
			}

			if config != (discovery.Config{}) {
				t.Errorf(
					"config = %#v, want zero",
					config,
				)
			}
		})
	}
}

func TestExecuteDiscoveryOneShot(t *testing.T) {
	runner := &fakeDiscoveryRunner{
		report: discovery.CrawlReport{
			Worked: true,
		},
	}

	err := executeDiscovery(
		context.Background(),
		true,
		runner,
		testDiscoveryLogger(),
	)
	if err != nil {
		t.Fatalf(
			"executeDiscovery() error = %v, want nil",
			err,
		)
	}

	if runner.runOnceCount != 1 {
		t.Errorf(
			"RunOnce() count = %d, want 1",
			runner.runOnceCount,
		)
	}

	if runner.runCount != 0 {
		t.Errorf(
			"Run() count = %d, want 0",
			runner.runCount,
		)
	}
}

func TestExecuteDiscoveryLongRunning(t *testing.T) {
	runner := &fakeDiscoveryRunner{}

	err := executeDiscovery(
		context.Background(),
		false,
		runner,
		testDiscoveryLogger(),
	)
	if err != nil {
		t.Fatalf(
			"executeDiscovery() error = %v, want nil",
			err,
		)
	}

	if runner.runCount != 1 {
		t.Errorf(
			"Run() count = %d, want 1",
			runner.runCount,
		)
	}

	if runner.runOnceCount != 0 {
		t.Errorf(
			"RunOnce() count = %d, want 0",
			runner.runOnceCount,
		)
	}
}

func TestExecuteDiscoveryReturnsOneShotFailure(
	t *testing.T,
) {
	runErr := errors.New("one-shot failure")
	runner := &fakeDiscoveryRunner{
		runOnceErr: runErr,
	}

	err := executeDiscovery(
		context.Background(),
		true,
		runner,
		testDiscoveryLogger(),
	)
	if !errors.Is(err, runErr) {
		t.Errorf(
			"executeDiscovery() error = %v, want %v",
			err,
			runErr,
		)
	}
}

func TestExecuteDiscoveryReturnsLongRunningFailure(
	t *testing.T,
) {
	runErr := errors.New("long-running failure")
	runner := &fakeDiscoveryRunner{
		runErr: runErr,
	}

	err := executeDiscovery(
		context.Background(),
		false,
		runner,
		testDiscoveryLogger(),
	)
	if !errors.Is(err, runErr) {
		t.Errorf(
			"executeDiscovery() error = %v, want %v",
			err,
			runErr,
		)
	}
}

func TestExecuteDiscoveryTreatsParentCancellationAsShutdown(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	runner := &fakeDiscoveryRunner{
		runErr: context.Canceled,
	}

	err := executeDiscovery(
		ctx,
		false,
		runner,
		testDiscoveryLogger(),
	)
	if err != nil {
		t.Errorf(
			"executeDiscovery() error = %v, want nil",
			err,
		)
	}
}

func TestExecuteDiscoveryValidatesDependencies(
	t *testing.T,
) {
	err := executeDiscovery(
		context.Background(),
		false,
		nil,
		testDiscoveryLogger(),
	)
	if !errors.Is(
		err,
		errDiscoveryRunnerUnavailable,
	) {
		t.Errorf(
			"nil runner error = %v, want unavailable runner",
			err,
		)
	}

	err = executeDiscovery(
		context.Background(),
		false,
		&fakeDiscoveryRunner{},
		nil,
	)
	if !errors.Is(
		err,
		errDiscoveryLoggerUnavailable,
	) {
		t.Errorf(
			"nil logger error = %v, want unavailable logger",
			err,
		)
	}
}

func TestNewDiscoveryRuntimeRejectsNilPool(
	t *testing.T,
) {
	runner, err := newDiscoveryRuntime(
		nil,
		testCrawlRuntimeSettings(t),
	)
	if err == nil {
		t.Fatal(
			"newDiscoveryRuntime() error = nil, want non-nil",
		)
	}

	if runner != nil {
		t.Errorf(
			"newDiscoveryRuntime() runner = %#v, want nil",
			runner,
		)
	}
}

func TestDiscoveryRuntimeOneShotWithNoSource(
	t *testing.T,
) {
	ctx := context.Background()
	operations, _ := newCLIIntegrationEnvironment(t)
	operations.getenv = withCrawlRuntimeEnvironment(
		operations.getenv,
	)

	if err := operations.migrate(ctx); err != nil {
		t.Fatalf(
			"migrate() error = %v, want nil",
			err,
		)
	}

	if err := operations.discover(
		ctx,
		true,
	); err != nil {
		t.Fatalf(
			"discover() error = %v, want nil",
			err,
		)
	}
}

func TestDiscoveryRuntimeRejectsInvalidConfiguration(
	t *testing.T,
) {
	operations, _ := newTestRuntimeOperations(t)
	operations.getenv = mapEnvironment{
		discoveryIntervalEnvironment: "0s",
	}.get

	err := operations.discover(
		context.Background(),
		true,
	)
	if !errors.Is(
		err,
		errInvalidRuntimeConfiguration,
	) {
		t.Errorf(
			"discover() error = %v, want invalid configuration",
			err,
		)
	}
}

func TestDiscoveryRuntimeRejectsUnavailablePool(
	t *testing.T,
) {
	operations, _ := newTestRuntimeOperations(t)
	operations.getenv = withCrawlRuntimeEnvironment(
		operations.getenv,
	)

	err := operations.discover(
		context.Background(),
		true,
	)
	if err == nil {
		t.Fatal(
			"discover() error = nil, want non-nil",
		)
	}

	if !strings.Contains(
		err.Error(),
		"construct discovery store",
	) {
		t.Errorf(
			"discover() error = %v, want discovery-store error",
			err,
		)
	}
}

func TestRuntimeCandidateSinkRecordsCandidates(
	t *testing.T,
) {
	source, err := origin.Parse(
		"https://source.example",
	)
	if err != nil {
		t.Fatalf(
			"Parse(source) error = %v, want nil",
			err,
		)
	}

	candidateOrigin, err := origin.Parse(
		"https://candidate.example",
	)
	if err != nil {
		t.Fatalf(
			"Parse(candidate) error = %v, want nil",
			err,
		)
	}

	candidates := []discovery.Candidate{
		{
			Origin: candidateOrigin,
			Kind:   discovery.KindLink,
		},
	}

	candidateStore := &fakeDiscoveryCandidateStore{
		result: discovery.RecordResult{
			Accepted: 1,
		},
	}

	err = (runtimeCandidateSink{
		store: candidateStore,
	}).RecordCandidates(
		context.Background(),
		source,
		candidates,
	)
	if err != nil {
		t.Fatalf(
			"RecordCandidates() error = %v, want nil",
			err,
		)
	}

	if candidateStore.source != source {
		t.Errorf(
			"recorded source = %v, want %v",
			candidateStore.source,
			source,
		)
	}

	if len(candidateStore.candidates) != 1 ||
		candidateStore.candidates[0] != candidates[0] {
		t.Errorf(
			"recorded candidates = %#v, want %#v",
			candidateStore.candidates,
			candidates,
		)
	}
}

func TestRuntimeCandidateSinkReturnsStoreFailure(
	t *testing.T,
) {
	recordErr := errors.New(
		"record discovery failure",
	)
	candidateStore := &fakeDiscoveryCandidateStore{
		err: recordErr,
	}

	err := (runtimeCandidateSink{
		store: candidateStore,
	}).RecordCandidates(
		context.Background(),
		origin.Origin{},
		nil,
	)
	if !errors.Is(err, recordErr) {
		t.Errorf(
			"RecordCandidates() error = %v, want %v",
			err,
			recordErr,
		)
	}
}

type fakeDiscoveryRunner struct {
	report       discovery.CrawlReport
	runOnceErr   error
	runErr       error
	runOnceCount int
	runCount     int
}

func (runner *fakeDiscoveryRunner) RunOnce(
	context.Context,
) (discovery.CrawlReport, error) {
	runner.runOnceCount++

	return runner.report, runner.runOnceErr
}

func (runner *fakeDiscoveryRunner) Run(
	context.Context,
) error {
	runner.runCount++

	return runner.runErr
}

type fakeDiscoveryCandidateStore struct {
	source     origin.Origin
	candidates []discovery.Candidate
	result     discovery.RecordResult
	err        error
}

func (candidateStore *fakeDiscoveryCandidateStore) RecordDiscovery(
	_ context.Context,
	source origin.Origin,
	candidates []discovery.Candidate,
) (discovery.RecordResult, error) {
	candidateStore.source = source
	candidateStore.candidates = append(
		[]discovery.Candidate(nil),
		candidates...,
	)

	return candidateStore.result, candidateStore.err
}

func testCrawlRuntimeSettings(
	t *testing.T,
) crawlRuntimeSettings {
	t.Helper()

	settings, err := loadCrawlRuntimeSettings(
		validCrawlRuntimeEnvironment().get,
	)
	if err != nil {
		t.Fatalf(
			"loadCrawlRuntimeSettings() error = %v, want nil",
			err,
		)
	}

	return settings
}

func withCrawlRuntimeEnvironment(
	getenv environmentGetter,
) environmentGetter {
	environment := validCrawlRuntimeEnvironment()

	return func(name string) string {
		if value, ok := environment[name]; ok {
			return value
		}

		return getenv(name)
	}
}

func testDiscoveryLogger() *slog.Logger {
	return slog.New(
		slog.NewTextHandler(
			io.Discard,
			nil,
		),
	)
}

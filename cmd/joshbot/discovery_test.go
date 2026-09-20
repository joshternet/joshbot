package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
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
		context.Background(),
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
	operations.newDiscoveryRunner = newDiscoveryRuntime

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

func TestDiscoveryRuntimeHeartbeatFailures(t *testing.T) {
	testFailure := errors.New("heartbeat failure")
	tests := []struct {
		name    string
		factory func(*pgxpool.Pool) (heartbeatStore, error)
	}{
		{
			name: "construction",
			factory: func(*pgxpool.Pool) (heartbeatStore, error) {
				return nil, testFailure
			},
		},
		{
			name: "retention",
			factory: func(*pgxpool.Pool) (heartbeatStore, error) {
				return &fakeHeartbeatStore{purgeErr: testFailure}, nil
			},
		},
		{
			name: "initial heartbeat",
			factory: func(*pgxpool.Pool) (heartbeatStore, error) {
				return &fakeHeartbeatStore{writeErr: testFailure}, nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations, _ := newTestRuntimeOperations(t)
			operations.getenv = withCrawlRuntimeEnvironment(operations.getenv)
			operations.newHeartbeatStore = test.factory
			if err := operations.discover(context.Background(), true); !errors.Is(err, testFailure) {
				t.Errorf("discover() error = %v", err)
			}
		})
	}
}

func TestDiscoveryRuntimeRecordsHeartbeatLifecycle(t *testing.T) {
	t.Setenv("HOSTNAME", "")
	for _, test := range []struct {
		name      string
		runErr    error
		wantState string
	}{
		{name: "stopping", wantState: "stopping"},
		{name: "failed", runErr: errors.New("discovery failed"), wantState: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			operations, _ := newTestRuntimeOperations(t)
			operations.getenv = withCrawlRuntimeEnvironment(operations.getenv)
			storage := &fakeHeartbeatStore{}
			runner := &fakeDiscoveryRunner{runOnceErr: test.runErr}
			operations.newHeartbeatStore = func(*pgxpool.Pool) (heartbeatStore, error) {
				return storage, nil
			}
			operations.newDiscoveryRunner = func(
				context.Context,
				*pgxpool.Pool,
				crawlRuntimeSettings,
			) (discoveryRunner, error) {
				return runner, nil
			}
			err := operations.discover(context.Background(), true)
			if (err != nil) != (test.runErr != nil) {
				t.Errorf("discover() error = %v", err)
			}
			if runner.observer == nil {
				t.Error("lifecycle observer was not installed")
			}
			storage.mu.Lock()
			defer storage.mu.Unlock()
			if len(storage.writes) != 2 ||
				storage.writes[len(storage.writes)-1].State != test.wantState {
				t.Errorf("heartbeat writes = %#v", storage.writes)
			}
		})
	}
}

func TestDiscoveryRuntimeSkipsHeartbeatAfterCancellation(t *testing.T) {
	operations, _ := newTestRuntimeOperations(t)
	operations.getenv = withCrawlRuntimeEnvironment(operations.getenv)
	factoryCalled := false
	operations.newHeartbeatStore = func(*pgxpool.Pool) (heartbeatStore, error) {
		factoryCalled = true
		return &fakeHeartbeatStore{}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := operations.discover(ctx, true); err != nil {
		t.Errorf("discover() error = %v", err)
	}
	if factoryCalled {
		t.Error("heartbeat factory called after cancellation")
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

	if err := (runtimeCandidateSink{store: candidateStore}).RecordCandidatesForRun(
		context.Background(),
		9,
		source,
		candidates,
	); err != nil {
		t.Fatalf("RecordCandidatesForRun() fallback error = %v", err)
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
	observer     discovery.LifecycleObserver
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

func (runner *fakeDiscoveryRunner) SetLifecycleObserver(observer discovery.LifecycleObserver) {
	runner.observer = observer
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

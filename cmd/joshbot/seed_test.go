package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/githubpublish"
	"github.com/joshternet/joshbot/internal/origin"
)

func TestRunHelpIncludesCrawlSeedCommands(t *testing.T) {
	for _, expected := range []string{
		"joshbot seed add <origin>",
		"joshbot seed remove <origin>",
		"joshbot seed list",
		"curated crawl seed",
	} {
		if !strings.Contains(helpText, expected) {
			t.Errorf(
				"help text does not contain %q",
				expected,
			)
		}
	}
}

func TestRunDispatchesCrawlSeedCommands(t *testing.T) {
	listedSeeds := []origin.Origin{
		mustCommandSeedOrigin(
			t,
			"https://alpha.example",
		),
		mustCommandSeedOrigin(
			t,
			"https://zulu.example:8443",
		),
	}

	tests := []struct {
		name         string
		args         []string
		listed       []origin.Origin
		wantCall     string
		wantOrigin   string
		wantOutput   string
		wantExitCode int
	}{
		{
			name: "add normalizes URL to origin",
			args: []string{
				"seed",
				"add",
				"https://EXAMPLE.com/some/path?q=x#fragment",
			},
			wantCall:     "seed-add",
			wantOrigin:   "https://example.com",
			wantOutput:   "seed added https://example.com\n",
			wantExitCode: exitSuccess,
		},
		{
			name: "remove normalizes URL to origin",
			args: []string{
				"seed",
				"remove",
				"http://EXAMPLE.com:8080/directory",
			},
			wantCall:     "seed-remove",
			wantOrigin:   "http://example.com:8080",
			wantOutput:   "seed removed http://example.com:8080\n",
			wantExitCode: exitSuccess,
		},
		{
			name:         "list",
			args:         []string{"seed", "list"},
			listed:       listedSeeds,
			wantCall:     "seed-list",
			wantOutput:   "https://alpha.example\nhttps://zulu.example:8443\n",
			wantExitCode: exitSuccess,
		},
		{
			name:         "empty list",
			args:         []string{"seed", "list"},
			wantCall:     "seed-list",
			wantExitCode: exitSuccess,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations := &fakeSeedCommandOperations{
				seeds: test.listed,
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := runWithOperations(
				context.Background(),
				test.args,
				&stdout,
				&stderr,
				operations,
			)

			if exitCode != test.wantExitCode {
				t.Errorf(
					"exit code = %d, want %d",
					exitCode,
					test.wantExitCode,
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

			if test.wantOrigin != "" &&
				operations.selected.String() !=
					test.wantOrigin {
				t.Errorf(
					"selected origin = %q, want %q",
					operations.selected.String(),
					test.wantOrigin,
				)
			}
		})
	}
}

func TestRunRejectsInvalidCrawlSeedArguments(
	t *testing.T,
) {
	tests := [][]string{
		{"seed"},
		{"seed", "add"},
		{"seed", "add", "https://example.com", "extra"},
		{"seed", "remove"},
		{"seed", "remove", "https://example.com", "extra"},
		{"seed", "list", "extra"},
		{"seed", "unknown"},
		{"seed", "add", "mailto:test@example.com"},
		{"seed", "add", "https://user@example.com"},
		{"seed", "remove", "not a URL"},
	}

	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			operations := &fakeSeedCommandOperations{}
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := runWithOperations(
				context.Background(),
				args,
				&stdout,
				&stderr,
				operations,
			)

			if exitCode != exitUsage {
				t.Errorf(
					"runWithOperations(%q) code = %d, want %d",
					args,
					exitCode,
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
				"seed",
			) {
				t.Errorf(
					"runWithOperations(%q) stderr = %q, want seed usage",
					args,
					stderr.String(),
				)
			}

			if len(operations.calls) != 0 {
				t.Errorf(
					"runWithOperations(%q) calls = %#v, want none",
					args,
					operations.calls,
				)
			}
		})
	}
}

func TestRunReturnsCrawlSeedOperationFailures(
	t *testing.T,
) {
	operationErr := errors.New(
		"test crawl seed operation failure",
	)

	tests := []struct {
		name      string
		args      []string
		operation string
	}{
		{
			name: "add",
			args: []string{
				"seed",
				"add",
				"https://example.com",
			},
			operation: "seed-add",
		},
		{
			name: "remove",
			args: []string{
				"seed",
				"remove",
				"https://example.com",
			},
			operation: "seed-remove",
		},
		{
			name:      "list",
			args:      []string{"seed", "list"},
			operation: "seed-list",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations := &fakeSeedCommandOperations{
				errors: map[string]error{
					test.operation: operationErr,
				},
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := runWithOperations(
				context.Background(),
				test.args,
				&stdout,
				&stderr,
				operations,
			)

			if exitCode != exitFailure {
				t.Errorf(
					"exit code = %d, want %d",
					exitCode,
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

			if len(operations.calls) != 1 ||
				operations.calls[0] !=
					test.operation {
				t.Errorf(
					"calls = %#v, want [%q]",
					operations.calls,
					test.operation,
				)
			}
		})
	}
}

func TestRuntimeCrawlSeedOperations(t *testing.T) {
	operations, state := newTestRuntimeOperations(t)
	seedStore := &fakeRuntimeCrawlSeedStore{
		seeds: []origin.Origin{
			mustCommandSeedOrigin(
				t,
				"https://alpha.example",
			),
			mustCommandSeedOrigin(
				t,
				"https://zulu.example",
			),
		},
	}

	factoryCalls := 0
	operations.newDiscoveryStore = func(
		pool *pgxpool.Pool,
	) (crawlSeedStore, error) {
		factoryCalls++

		if pool != state.database.Pool() {
			t.Error(
				"crawl seed store received unexpected pool",
			)
		}

		return seedStore, nil
	}

	added := mustCommandSeedOrigin(
		t,
		"https://added.example",
	)
	if err := operations.addCrawlSeed(
		context.Background(),
		added,
	); err != nil {
		t.Fatalf(
			"addCrawlSeed() error = %v, want nil",
			err,
		)
	}

	if seedStore.added != added {
		t.Errorf(
			"added seed = %v, want %v",
			seedStore.added,
			added,
		)
	}

	removed := mustCommandSeedOrigin(
		t,
		"https://removed.example",
	)
	if err := operations.removeCrawlSeed(
		context.Background(),
		removed,
	); err != nil {
		t.Fatalf(
			"removeCrawlSeed() error = %v, want nil",
			err,
		)
	}

	if seedStore.removed != removed {
		t.Errorf(
			"removed seed = %v, want %v",
			seedStore.removed,
			removed,
		)
	}

	seeds, err := operations.crawlSeeds(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"crawlSeeds() error = %v, want nil",
			err,
		)
	}

	if len(seeds) != len(seedStore.seeds) {
		t.Fatalf(
			"seed count = %d, want %d",
			len(seeds),
			len(seedStore.seeds),
		)
	}

	for index := range seeds {
		if seeds[index] != seedStore.seeds[index] {
			t.Errorf(
				"seed %d = %v, want %v",
				index,
				seeds[index],
				seedStore.seeds[index],
			)
		}
	}

	if factoryCalls != 3 {
		t.Errorf(
			"crawl seed store factory calls = %d, want 3",
			factoryCalls,
		)
	}

	if state.database.closeCount != 3 {
		t.Errorf(
			"database close count = %d, want 3",
			state.database.closeCount,
		)
	}
}

func TestRuntimeCrawlSeedStoreConstructionFailure(
	t *testing.T,
) {
	constructionErr := errors.New(
		"test crawl seed store construction failure",
	)
	source := mustCommandSeedOrigin(
		t,
		"https://example.com",
	)

	tests := []struct {
		name   string
		action func(
			runtimeOperations,
		) error
	}{
		{
			name: "add",
			action: func(
				operations runtimeOperations,
			) error {
				return operations.addCrawlSeed(
					context.Background(),
					source,
				)
			},
		},
		{
			name: "remove",
			action: func(
				operations runtimeOperations,
			) error {
				return operations.removeCrawlSeed(
					context.Background(),
					source,
				)
			},
		},
		{
			name: "list",
			action: func(
				operations runtimeOperations,
			) error {
				_, err := operations.crawlSeeds(
					context.Background(),
				)

				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations, _ :=
				newTestRuntimeOperations(t)
			operations.newDiscoveryStore = func(
				*pgxpool.Pool,
			) (crawlSeedStore, error) {
				return nil, constructionErr
			}

			err := test.action(operations)
			if !errors.Is(
				err,
				constructionErr,
			) {
				t.Errorf(
					"operation error = %v, want %v",
					err,
					constructionErr,
				)
			}

			if !strings.Contains(
				err.Error(),
				"construct discovery store",
			) {
				t.Errorf(
					"operation error = %v, want construction context",
					err,
				)
			}
		})
	}
}

func TestRuntimeCrawlSeedStoreFailures(
	t *testing.T,
) {
	source := mustCommandSeedOrigin(
		t,
		"https://example.com",
	)
	addErr := errors.New("test add failure")
	removeErr := errors.New("test remove failure")
	listErr := errors.New("test list failure")

	tests := []struct {
		name      string
		seedStore *fakeRuntimeCrawlSeedStore
		action    func(runtimeOperations) error
		want      error
	}{
		{
			name: "add",
			seedStore: &fakeRuntimeCrawlSeedStore{
				addErr: addErr,
			},
			action: func(
				operations runtimeOperations,
			) error {
				return operations.addCrawlSeed(
					context.Background(),
					source,
				)
			},
			want: addErr,
		},
		{
			name: "remove",
			seedStore: &fakeRuntimeCrawlSeedStore{
				removeErr: removeErr,
			},
			action: func(
				operations runtimeOperations,
			) error {
				return operations.removeCrawlSeed(
					context.Background(),
					source,
				)
			},
			want: removeErr,
		},
		{
			name: "list",
			seedStore: &fakeRuntimeCrawlSeedStore{
				listErr: listErr,
			},
			action: func(
				operations runtimeOperations,
			) error {
				_, err := operations.crawlSeeds(
					context.Background(),
				)

				return err
			},
			want: listErr,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations, _ :=
				newTestRuntimeOperations(t)
			operations.newDiscoveryStore = func(
				*pgxpool.Pool,
			) (crawlSeedStore, error) {
				return test.seedStore, nil
			}

			err := test.action(operations)
			if !errors.Is(err, test.want) {
				t.Errorf(
					"operation error = %v, want %v",
					err,
					test.want,
				)
			}
		})
	}
}

func TestProductionRuntimeIncludesCrawlSeedStore(
	t *testing.T,
) {
	operations := newRuntimeOperations(io.Discard)
	if operations.newDiscoveryStore == nil {
		t.Fatal(
			"newRuntimeOperations() omitted crawl seed store",
		)
	}

	seedStore, err := newRuntimeCrawlSeedStore(nil)
	if err == nil {
		t.Fatal(
			"newRuntimeCrawlSeedStore(nil) error = nil, want non-nil",
		)
	}

	if seedStore != nil {
		t.Errorf(
			"newRuntimeCrawlSeedStore(nil) store = %#v, want nil",
			seedStore,
		)
	}
}

type fakeSeedCommandOperations struct {
	calls    []string
	errors   map[string]error
	selected origin.Origin
	seeds    []origin.Origin
}

func (
	operations *fakeSeedCommandOperations,
) result(name string) error {
	operations.calls = append(
		operations.calls,
		name,
	)

	return operations.errors[name]
}

func (
	operations *fakeSeedCommandOperations,
) health(context.Context) error {
	return nil
}

func (
	operations *fakeSeedCommandOperations,
) migrate(context.Context) error {
	return nil
}

func (
	operations *fakeSeedCommandOperations,
) schedule(
	context.Context,
	origin.Origin,
) error {
	return nil
}

func (
	operations *fakeSeedCommandOperations,
) worker(context.Context) error {
	return nil
}

func (
	operations *fakeSeedCommandOperations,
) discover(
	context.Context,
	bool,
) error {
	return nil
}

func (
	operations *fakeSeedCommandOperations,
) export(
	context.Context,
	string,
) error {
	return nil
}

func (
	operations *fakeSeedCommandOperations,
) publish(
	context.Context,
	string,
) (githubpublish.Result, error) {
	return githubpublish.Result{}, nil
}

func (
	operations *fakeSeedCommandOperations,
) addCrawlSeed(
	_ context.Context,
	source origin.Origin,
) error {
	operations.selected = source

	return operations.result("seed-add")
}

func (
	operations *fakeSeedCommandOperations,
) removeCrawlSeed(
	_ context.Context,
	source origin.Origin,
) error {
	operations.selected = source

	return operations.result("seed-remove")
}

func (
	operations *fakeSeedCommandOperations,
) crawlSeeds(
	context.Context,
) ([]origin.Origin, error) {
	if err := operations.result(
		"seed-list",
	); err != nil {
		return nil, err
	}

	return append(
		[]origin.Origin(nil),
		operations.seeds...,
	), nil
}

type fakeRuntimeCrawlSeedStore struct {
	added     origin.Origin
	removed   origin.Origin
	seeds     []origin.Origin
	addErr    error
	removeErr error
	listErr   error
}

func (
	seedStore *fakeRuntimeCrawlSeedStore,
) AddCrawlSeed(
	_ context.Context,
	source origin.Origin,
) error {
	seedStore.added = source

	return seedStore.addErr
}

func (
	seedStore *fakeRuntimeCrawlSeedStore,
) RemoveCrawlSeed(
	_ context.Context,
	source origin.Origin,
) error {
	seedStore.removed = source

	return seedStore.removeErr
}

func (
	seedStore *fakeRuntimeCrawlSeedStore,
) CrawlSeeds(
	context.Context,
) ([]origin.Origin, error) {
	if seedStore.listErr != nil {
		return nil, seedStore.listErr
	}

	return append(
		[]origin.Origin(nil),
		seedStore.seeds...,
	), nil
}

func mustCommandSeedOrigin(
	t *testing.T,
	rawURL string,
) origin.Origin {
	t.Helper()

	parsed, err := origin.Parse(rawURL)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			rawURL,
			err,
		)
	}

	return parsed
}

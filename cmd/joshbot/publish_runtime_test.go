package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/githubpublish"
	"github.com/joshternet/joshbot/internal/publicdata"
)

func TestPublishRuntimeUsesSnapshotWithoutDatabase(
	t *testing.T,
) {
	target := githubpublish.Config{
		Owner:      "joshternet",
		Repository: "index-data",
		Branch:     "main",
	}
	files := []publicdata.File{
		{
			Path: "registry.json",
			Data: []byte("{}\n"),
		},
	}
	wantResult := githubpublish.Result{
		Changed:   true,
		CommitSHA: "published-commit",
	}
	publisher := &fakeRuntimePublisher{
		result: wantResult,
	}

	databaseEnvironmentReads := 0
	settingsLoads := 0
	snapshotReads := 0
	publisherConstructions := 0

	operations := runtimeOperations{
		getenv: func(name string) string {
			if name == databaseURLEnvironment ||
				name == databasePasswordFileEnvironment {
				databaseEnvironmentReads++
			}

			return ""
		},
		loadPublishSettings: func(
			environmentGetter,
		) (publishSettings, error) {
			settingsLoads++

			return publishSettings{
				target: target,
				token:  "runtime-github-token",
			}, nil
		},
		readRegistry: func(
			_ context.Context,
			root string,
		) ([]publicdata.File, error) {
			snapshotReads++

			if root != "/snapshot" {
				t.Errorf(
					"snapshot root = %q, want %q",
					root,
					"/snapshot",
				)
			}

			return files, nil
		},
		newPublisher: func(
			config githubpublish.Config,
			token string,
		) (registryPublisher, error) {
			publisherConstructions++

			if config != target {
				t.Errorf(
					"publisher config = %#v, want %#v",
					config,
					target,
				)
			}
			if token != "runtime-github-token" {
				t.Error(
					"publisher token does not match loaded token",
				)
			}

			return publisher, nil
		},
	}

	result, err := operations.publish(
		context.Background(),
		"/snapshot",
	)
	if err != nil {
		t.Fatalf(
			"publish() error = %v, want nil",
			err,
		)
	}
	if result != wantResult {
		t.Errorf(
			"publish() result = %#v, want %#v",
			result,
			wantResult,
		)
	}
	if settingsLoads != 1 {
		t.Errorf(
			"settings loads = %d, want 1",
			settingsLoads,
		)
	}
	if snapshotReads != 1 {
		t.Errorf(
			"snapshot reads = %d, want 1",
			snapshotReads,
		)
	}
	if publisherConstructions != 1 {
		t.Errorf(
			"publisher constructions = %d, want 1",
			publisherConstructions,
		)
	}
	if publisher.publishCalls != 1 {
		t.Errorf(
			"publisher calls = %d, want 1",
			publisher.publishCalls,
		)
	}
	if !reflect.DeepEqual(
		publisher.files,
		files,
	) {
		t.Errorf(
			"publisher files = %#v, want %#v",
			publisher.files,
			files,
		)
	}
	if databaseEnvironmentReads != 0 {
		t.Errorf(
			"database environment reads = %d, want 0",
			databaseEnvironmentReads,
		)
	}
}

func TestPublishRuntimeFailureBoundaries(t *testing.T) {
	errSettings := errors.New(
		"settings failed",
	)
	errRead := errors.New(
		"snapshot read failed",
	)
	errConstruct := errors.New(
		"publisher construction failed",
	)
	errPublish := errors.New(
		"publication failed",
	)

	tests := []struct {
		name             string
		settingsError    error
		readError        error
		constructorError error
		publishError     error
		wantError        error
		wantRead         int
		wantConstruct    int
		wantPublish      int
	}{
		{
			name:          "settings failure",
			settingsError: errSettings,
			wantError:     errSettings,
		},
		{
			name:      "reader failure",
			readError: errRead,
			wantError: errRead,
			wantRead:  1,
		},
		{
			name:             "publisher construction failure",
			constructorError: errConstruct,
			wantError:        errConstruct,
			wantRead:         1,
			wantConstruct:    1,
		},
		{
			name:          "publication failure",
			publishError:  errPublish,
			wantError:     errPublish,
			wantRead:      1,
			wantConstruct: 1,
			wantPublish:   1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			readCalls := 0
			constructorCalls := 0
			publisher := &fakeRuntimePublisher{
				err: test.publishError,
			}

			operations := runtimeOperations{
				getenv: func(string) string {
					return ""
				},
				loadPublishSettings: func(
					environmentGetter,
				) (publishSettings, error) {
					if test.settingsError != nil {
						return publishSettings{},
							test.settingsError
					}

					return publishSettings{
						target: githubpublish.Config{
							Owner:      "joshternet",
							Repository: "index-data",
							Branch:     "main",
						},
						token: "runtime-github-token",
					}, nil
				},
				readRegistry: func(
					context.Context,
					string,
				) ([]publicdata.File, error) {
					readCalls++

					if test.readError != nil {
						return nil, test.readError
					}

					return []publicdata.File{
						{
							Path: "registry.json",
							Data: []byte("{}\n"),
						},
					}, nil
				},
				newPublisher: func(
					githubpublish.Config,
					string,
				) (registryPublisher, error) {
					constructorCalls++

					if test.constructorError != nil {
						return nil,
							test.constructorError
					}

					return publisher, nil
				},
			}

			result, err := operations.publish(
				context.Background(),
				"/snapshot",
			)
			if !errors.Is(err, test.wantError) {
				t.Errorf(
					"publish() error = %v, want %v",
					err,
					test.wantError,
				)
			}
			if result != (githubpublish.Result{}) {
				t.Errorf(
					"publish() result = %#v, want zero result",
					result,
				)
			}
			if readCalls != test.wantRead {
				t.Errorf(
					"reader calls = %d, want %d",
					readCalls,
					test.wantRead,
				)
			}
			if constructorCalls != test.wantConstruct {
				t.Errorf(
					"publisher constructor calls = %d, want %d",
					constructorCalls,
					test.wantConstruct,
				)
			}
			if publisher.publishCalls != test.wantPublish {
				t.Errorf(
					"publisher calls = %d, want %d",
					publisher.publishCalls,
					test.wantPublish,
				)
			}
			if err != nil &&
				strings.Contains(
					err.Error(),
					"runtime-github-token",
				) {
				t.Error(
					"publish error contains GitHub token",
				)
			}
		})
	}
}

func TestPublicationRuntimeAdapters(t *testing.T) {
	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if _, err := readRegistry(
		ctx,
		"/snapshot",
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"readRegistry() error = %v, want context.Canceled",
			err,
		)
	}

	if _, err := newGitHubPublisher(
		githubpublish.Config{},
		"",
	); err == nil {
		t.Error(
			"newGitHubPublisher() error = nil, want non-nil",
		)
	}

	operations := newRuntimeOperations(
		discardWriter{},
	)
	if operations.loadPublishSettings == nil ||
		operations.readRegistry == nil ||
		operations.newPublisher == nil {
		t.Error(
			"newRuntimeOperations() omitted publication dependency",
		)
	}
}

type fakeRuntimePublisher struct {
	result       githubpublish.Result
	err          error
	publishCalls int
	files        []publicdata.File
}

func (publisher *fakeRuntimePublisher) Publish(
	_ context.Context,
	files []publicdata.File,
) (githubpublish.Result, error) {
	publisher.publishCalls++
	publisher.files = append(
		[]publicdata.File(nil),
		files...,
	)

	if publisher.err != nil {
		return githubpublish.Result{},
			publisher.err
	}

	return publisher.result, nil
}

type discardWriter struct{}

func (discardWriter) Write(
	data []byte,
) (int, error) {
	return len(data), nil
}

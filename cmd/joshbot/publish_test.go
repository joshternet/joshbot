package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/githubpublish"
)

func (operations *fakeCommandOperations) publish(
	_ context.Context,
	root string,
) (githubpublish.Result, error) {
	operations.outputRoot = root

	err := operations.result("publish")
	if err != nil {
		return githubpublish.Result{}, err
	}

	return githubpublish.Result{
		Changed:   true,
		CommitSHA: "fake-publish-commit",
	}, nil
}

func TestRunHelpIncludesPublish(t *testing.T) {
	if !strings.Contains(helpText, "publish") {
		t.Fatal("help text does not contain publish")
	}
}

func TestRunDispatchesPublish(t *testing.T) {
	tests := []struct {
		name       string
		result     githubpublish.Result
		wantOutput string
	}{
		{
			name: "changed registry",
			result: githubpublish.Result{
				Changed:   true,
				CommitSHA: "published-commit",
			},
			wantOutput: "published published-commit\n",
		},
		{
			name: "unchanged registry",
			result: githubpublish.Result{
				Changed:   false,
				CommitSHA: "current-commit",
			},
			wantOutput: "registry unchanged\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations := &fakePublishCommandOperations{
				publicationResult: test.result,
			}

			var stdout bytes.Buffer
			var stderr bytes.Buffer

			code := runWithOperations(
				context.Background(),
				[]string{
					"publish",
					"--input",
					"/snapshot",
				},
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
			if operations.inputRoot != "/snapshot" {
				t.Errorf(
					"publish input = %q, want %q",
					operations.inputRoot,
					"/snapshot",
				)
			}
			if len(operations.calls) != 1 ||
				operations.calls[0] != "publish" {
				t.Errorf(
					"operation calls = %#v, want [publish]",
					operations.calls,
				)
			}
		})
	}
}

func TestRunRejectsInvalidPublishArguments(t *testing.T) {
	tests := [][]string{
		{"publish"},
		{"publish", "/snapshot"},
		{"publish", "--input"},
		{"publish", "--input", ""},
		{"publish", "--output", "/snapshot"},
		{"publish", "--input", "/snapshot", "extra"},
	}

	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			operations := &fakePublishCommandOperations{}

			var stdout bytes.Buffer
			var stderr bytes.Buffer

			code := runWithOperations(
				context.Background(),
				args,
				&stdout,
				&stderr,
				operations,
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
				"publish requires --input <directory>",
			) {
				t.Errorf(
					"runWithOperations(%q) stderr = %q, want publish usage",
					args,
					stderr.String(),
				)
			}
			if len(operations.calls) != 0 {
				t.Errorf(
					"operation calls = %#v, want none",
					operations.calls,
				)
			}
		})
	}
}

func TestRunReturnsPublishFailure(t *testing.T) {
	operationError := errors.New(
		"publication operation failed",
	)
	operations := &fakePublishCommandOperations{
		publicationError: operationError,
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runWithOperations(
		context.Background(),
		[]string{
			"publish",
			"--input",
			"/snapshot",
		},
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
		operationError.Error(),
	) {
		t.Errorf(
			"stderr = %q, want operation failure",
			stderr.String(),
		)
	}
	if strings.Contains(
		stderr.String(),
		"secret-publication-token",
	) {
		t.Error("stderr contains publication token")
	}
}

func TestLoadPublishSettings(t *testing.T) {
	tokenPath := filepath.Join(
		t.TempDir(),
		"github-token",
	)
	if err := os.WriteFile(
		tokenPath,
		[]byte("test-github-token\n"),
		0o600,
	); err != nil {
		t.Fatalf(
			"write token file: %v",
			err,
		)
	}

	tests := []struct {
		name        string
		environment publishTestEnvironment
		wantBranch  string
	}{
		{
			name: "default branch",
			environment: publishTestEnvironment{
				publishOwnerEnvironment:      "joshternet",
				publishRepositoryEnvironment: "index-data",
				githubTokenFileEnvironment:   tokenPath,
			},
			wantBranch: "main",
		},
		{
			name: "configured branch",
			environment: publishTestEnvironment{
				publishOwnerEnvironment:      "joshternet",
				publishRepositoryEnvironment: "index-data",
				publishBranchEnvironment:     "registry",
				githubTokenFileEnvironment:   tokenPath,
			},
			wantBranch: "registry",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings, err := loadPublishSettings(
				test.environment.get,
			)
			if err != nil {
				t.Fatalf(
					"loadPublishSettings() error = %v, want nil",
					err,
				)
			}

			wantConfig := githubpublish.Config{
				Owner:      "joshternet",
				Repository: "index-data",
				Branch:     test.wantBranch,
			}
			if settings.target != wantConfig {
				t.Errorf(
					"publication target = %#v, want %#v",
					settings.target,
					wantConfig,
				)
			}
			if settings.token != "test-github-token" {
				t.Error(
					"publication token does not match token file",
				)
			}
		})
	}
}

func TestLoadPublishSettingsRejectsMissingConfiguration(
	t *testing.T,
) {
	tokenPath := filepath.Join(
		t.TempDir(),
		"github-token",
	)
	if err := os.WriteFile(
		tokenPath,
		[]byte("test-token\n"),
		0o600,
	); err != nil {
		t.Fatalf(
			"write token file: %v",
			err,
		)
	}

	valid := publishTestEnvironment{
		publishOwnerEnvironment:      "joshternet",
		publishRepositoryEnvironment: "index-data",
		githubTokenFileEnvironment:   tokenPath,
	}

	tests := []struct {
		name    string
		missing string
	}{
		{
			name:    "owner",
			missing: publishOwnerEnvironment,
		},
		{
			name:    "repository",
			missing: publishRepositoryEnvironment,
		},
		{
			name:    "token file",
			missing: githubTokenFileEnvironment,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environment := make(
				publishTestEnvironment,
				len(valid),
			)
			for name, value := range valid {
				environment[name] = value
			}
			delete(environment, test.missing)

			settings, err := loadPublishSettings(
				environment.get,
			)
			if !errors.Is(
				err,
				errInvalidPublishConfiguration,
			) {
				t.Errorf(
					"loadPublishSettings() error = %v, want invalid configuration",
					err,
				)
			}
			if settings != (publishSettings{}) {
				t.Errorf(
					"loadPublishSettings() = %#v, want zero settings",
					settings,
				)
			}
		})
	}
}

func TestLoadPublishSettingsRejectsUnreadableTokenFile(
	t *testing.T,
) {
	missingPath := filepath.Join(
		t.TempDir(),
		"missing-token",
	)

	settings, err := loadPublishSettings(
		publishTestEnvironment{
			publishOwnerEnvironment:      "joshternet",
			publishRepositoryEnvironment: "index-data",
			githubTokenFileEnvironment:   missingPath,
		}.get,
	)
	if !errors.Is(err, errOpenGitHubTokenFile) {
		t.Errorf(
			"loadPublishSettings() error = %v, want errOpenGitHubTokenFile",
			err,
		)
	}
	if settings != (publishSettings{}) {
		t.Errorf(
			"loadPublishSettings() = %#v, want zero settings",
			settings,
		)
	}
}

func TestReadGitHubToken(t *testing.T) {
	maximumToken := strings.Repeat(
		"x",
		maxGitHubTokenSize,
	)

	tests := []struct {
		name      string
		input     string
		want      string
		wantError error
	}{
		{
			name:  "token without newline",
			input: "github-token",
			want:  "github-token",
		},
		{
			name:  "one trailing newline",
			input: "github-token\n",
			want:  "github-token",
		},
		{
			name:  "maximum token size",
			input: maximumToken,
			want:  maximumToken,
		},
		{
			name:      "empty file",
			wantError: errEmptyGitHubToken,
		},
		{
			name:      "newline only",
			input:     "\n",
			wantError: errEmptyGitHubToken,
		},
		{
			name:      "oversized token",
			input:     maximumToken + "x",
			wantError: errGitHubTokenTooLarge,
		},
		{
			name:      "two trailing newlines",
			input:     "github-token\n\n",
			wantError: errInvalidGitHubToken,
		},
		{
			name:      "leading whitespace",
			input:     " github-token",
			wantError: errInvalidGitHubToken,
		},
		{
			name:      "trailing whitespace",
			input:     "github-token ",
			wantError: errInvalidGitHubToken,
		},
		{
			name:      "control character",
			input:     "github\x00token",
			wantError: errInvalidGitHubToken,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token, err := readGitHubToken(
				strings.NewReader(test.input),
			)

			if test.wantError == nil {
				if err != nil {
					t.Fatalf(
						"readGitHubToken() error = %v, want nil",
						err,
					)
				}
				if token != test.want {
					t.Errorf(
						"readGitHubToken() token length = %d, want %d",
						len(token),
						len(test.want),
					)
				}

				return
			}

			if !errors.Is(err, test.wantError) {
				t.Errorf(
					"readGitHubToken() error = %v, want %v",
					err,
					test.wantError,
				)
			}
			if token != "" {
				t.Error(
					"readGitHubToken() returned token with error",
				)
			}
		})
	}
}

func TestReadGitHubTokenReturnsReadFailure(t *testing.T) {
	token, err := readGitHubToken(
		publishFailingReader{
			err: errors.New("token read failed"),
		},
	)
	if !errors.Is(err, errReadGitHubTokenFile) {
		t.Errorf(
			"readGitHubToken() error = %v, want errReadGitHubTokenFile",
			err,
		)
	}
	if token != "" {
		t.Error(
			"readGitHubToken() returned token with read failure",
		)
	}
}

type fakePublishCommandOperations struct {
	fakeCommandOperations
	publicationResult githubpublish.Result
	publicationError  error
	inputRoot         string
}

func (operations *fakePublishCommandOperations) publish(
	_ context.Context,
	root string,
) (githubpublish.Result, error) {
	operations.calls = append(
		operations.calls,
		"publish",
	)
	operations.inputRoot = root

	if operations.publicationError != nil {
		return githubpublish.Result{},
			operations.publicationError
	}

	return operations.publicationResult, nil
}

type publishTestEnvironment map[string]string

func (environment publishTestEnvironment) get(
	name string,
) string {
	return environment[name]
}

type publishFailingReader struct {
	err error
}

func (reader publishFailingReader) Read(
	_ []byte,
) (int, error) {
	return 0, reader.err
}

var _ io.Reader = publishFailingReader{}

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/githubpublish"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/publicdata"
	"github.com/joshternet/joshbot/internal/store"
)

type publishRuntimeIntegrationPublisher struct {
	files []publicdata.File
	calls int
}

func (publisher *publishRuntimeIntegrationPublisher) Publish(
	_ context.Context,
	files []publicdata.File,
) (githubpublish.Result, error) {
	publisher.calls++

	publisher.files = append(
		[]publicdata.File(nil),
		files...,
	)

	return githubpublish.Result{
		Changed:   true,
		CommitSHA: "integration-commit",
	}, nil
}

func TestPublishRuntimeIntegrationReadsRealSnapshotAndConfiguration(
	t *testing.T,
) {
	ctx := context.Background()

	source, err := origin.Parse(
		"https://example.com",
	)
	if err != nil {
		t.Fatalf(
			"origin.Parse() error = %v",
			err,
		)
	}

	declarationState := declaration.Declaration{
		Version:  1,
		Identity: declaration.IdentityAffirmed,
	}
	checkedAt := time.Date(
		2026,
		time.September,
		1,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	files, err := publicdata.BuildRegistry(
		[]store.RegistryOrigin{
			{
				Origin:                        source,
				FirstParticipatedAt:           checkedAt,
				InitialDeclaration:            declarationState,
				LatestDeclarationCheckAt:      checkedAt,
				LatestDeclarationCheckOutcome: declaration.OutcomeValid,
				CurrentDeclaration:            &declarationState,
			},
		},
	)
	if err != nil {
		t.Fatalf(
			"publicdata.BuildRegistry() error = %v",
			err,
		)
	}

	snapshotRoot := filepath.Join(
		t.TempDir(),
		"snapshot",
	)

	if err := publicdata.WriteDirectory(
		ctx,
		snapshotRoot,
		files,
	); err != nil {
		t.Fatalf(
			"publicdata.WriteDirectory() error = %v",
			err,
		)
	}

	tokenFile := filepath.Join(
		t.TempDir(),
		"github-token",
	)

	const token = "integration-github-token"

	if err := os.WriteFile(
		tokenFile,
		[]byte(token+"\n"),
		0o600,
	); err != nil {
		t.Fatalf(
			"write GitHub token: %v",
			err,
		)
	}

	environment := map[string]string{
		publishOwnerEnvironment:      "joshternet",
		publishRepositoryEnvironment: "index-data",
		publishBranchEnvironment:     "integration",
		githubTokenFileEnvironment:   tokenFile,
	}

	publisher :=
		&publishRuntimeIntegrationPublisher{}

	operations := newRuntimeOperations(
		io.Discard,
	)

	operations.getenv = func(
		name string,
	) string {
		return environment[name]
	}

	operations.newPublisher = func(
		config githubpublish.Config,
		gotToken string,
	) (registryPublisher, error) {
		wantConfig := githubpublish.Config{
			Owner:      "joshternet",
			Repository: "index-data",
			Branch:     "integration",
		}

		if config != wantConfig {
			t.Errorf(
				"publisher config = %#v, want %#v",
				config,
				wantConfig,
			)
		}

		if gotToken != token {
			t.Errorf(
				"publisher token = %q, want configured token",
				gotToken,
			)
		}

		return publisher, nil
	}

	result, err := operations.publish(
		ctx,
		snapshotRoot,
	)
	if err != nil {
		t.Fatalf(
			"publish() error = %v",
			err,
		)
	}

	wantResult := githubpublish.Result{
		Changed:   true,
		CommitSHA: "integration-commit",
	}

	if result != wantResult {
		t.Errorf(
			"publish() result = %#v, want %#v",
			result,
			wantResult,
		)
	}

	if publisher.calls != 1 {
		t.Errorf(
			"publisher calls = %d, want 1",
			publisher.calls,
		)
	}

	if !reflect.DeepEqual(
		publisher.files,
		files,
	) {
		t.Errorf(
			"published files = %#v, want %#v",
			publisher.files,
			files,
		)
	}
}

func TestPublishRuntimeIntegrationRejectsInvalidSnapshotBeforePublishing(
	t *testing.T,
) {
	tokenFile := filepath.Join(
		t.TempDir(),
		"github-token",
	)

	if err := os.WriteFile(
		tokenFile,
		[]byte("integration-github-token\n"),
		0o600,
	); err != nil {
		t.Fatalf(
			"write GitHub token: %v",
			err,
		)
	}

	environment := map[string]string{
		publishOwnerEnvironment:      "joshternet",
		publishRepositoryEnvironment: "index-data",
		githubTokenFileEnvironment:   tokenFile,
	}

	publisherConstructions := 0

	operations := newRuntimeOperations(
		io.Discard,
	)

	operations.getenv = func(
		name string,
	) string {
		return environment[name]
	}

	operations.newPublisher = func(
		githubpublish.Config,
		string,
	) (registryPublisher, error) {
		publisherConstructions++

		return &publishRuntimeIntegrationPublisher{},
			nil
	}

	_, err := operations.publish(
		context.Background(),
		t.TempDir(),
	)
	if err == nil {
		t.Fatal(
			"publish() error = nil, want invalid snapshot failure",
		)
	}

	if !strings.Contains(
		err.Error(),
		"read public registry snapshot",
	) {
		t.Errorf(
			"publish() error = %v, want snapshot read failure",
			err,
		)
	}

	if publisherConstructions != 0 {
		t.Errorf(
			"publisher constructions = %d, want 0",
			publisherConstructions,
		)
	}
}

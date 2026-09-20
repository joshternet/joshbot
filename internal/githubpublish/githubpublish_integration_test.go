package githubpublish

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/joshternet/joshbot/internal/publicdata"
)

func TestGitHubPublisherIntegrationPublishesExactSnapshot(
	t *testing.T,
) {
	registryData := []byte(
		"{\"version\":1,\"origins\":[\"https://alpha.example\"]}\n",
	)
	nodeData := []byte(
		"{\"origin\":\"https://alpha.example\",\"name\":\"Alpha\"}\n",
	)

	currentEntries := []testTreeEntry{
		{
			Path: ".joshbot-registry-target",
			Mode: "100644",
			Type: "blob",
			SHA:  testSentinelSHA,
		},
		{
			Path: "nodes/ff/" +
				"ffffffffffffffffffffffffffffffff" +
				"ffffffffffffffffffffffffffffffff.json",
			Mode: "100644",
			Type: "blob",
			SHA:  "stale-node-blob",
		},
		{
			Path: "registry.json",
			Mode: "100644",
			Type: "blob",
			SHA:  "old-registry-blob",
		},
	}

	expectedTree := []testTreeEntry{
		{
			Path: ".joshbot-registry-target",
			Mode: "100644",
			Type: "blob",
			SHA:  testSentinelSHA,
		},
		{
			Path: testNodePath,
			Mode: "100644",
			Type: "blob",
			SHA:  "node-blob",
		},
		{
			Path: "registry.json",
			Mode: "100644",
			Type: "blob",
			SHA:  "registry-blob",
		},
	}

	expectations :=
		publicationReadExpectations(
			currentEntries,
		)

	expectations = append(
		expectations,
		expectBlobCreation(
			testNodePath,
			nodeData,
			"node-blob",
		),
		expectBlobCreation(
			"registry.json",
			registryData,
			"registry-blob",
		),
		expectTreeCreation(
			expectedTree,
			"new-tree",
		),
		expectCommitCreation(
			"new-tree",
			"new-commit",
		),
		expectReferenceUpdate(
			"new-commit",
		),
	)

	script := newGitHubScript(
		t,
		expectations,
	)
	server := httptest.NewServer(script)
	defer server.Close()

	publisher, err := newPublisher(
		testPublisherConfig(),
		testToken,
		server.URL,
	)
	if err != nil {
		t.Fatalf(
			"newPublisher() error = %v, want nil",
			err,
		)
	}

	result, err := publisher.Publish(
		context.Background(),
		[]publicdata.File{
			{
				Path: "registry.json",
				Data: registryData,
			},
			{
				Path: testNodePath,
				Data: nodeData,
			},
		},
	)
	if err != nil {
		t.Fatalf(
			"Publish() error = %v, want nil",
			err,
		)
	}

	if !result.Changed ||
		result.CommitSHA != "new-commit" {
		t.Errorf(
			"Publish() result = %#v, want changed new-commit",
			result,
		)
	}

	script.assertComplete()
}

func TestGitHubPublisherIntegrationRejectsAuthenticatedRedirect(
	t *testing.T,
) {
	server := httptest.NewServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			if request.Header.Get(
				"Authorization",
			) != "Bearer "+testToken {
				t.Error(
					"Authorization header is missing or incorrect",
				)
			}

			writer.Header().Set(
				"Location",
				"/redirected",
			)
			writer.WriteHeader(
				http.StatusFound,
			)
		}),
	)
	defer server.Close()

	publisher, err := newPublisher(
		testPublisherConfig(),
		testToken,
		server.URL,
	)
	if err != nil {
		t.Fatalf(
			"newPublisher() error = %v, want nil",
			err,
		)
	}

	result, err := publisher.Publish(
		context.Background(),
		[]publicdata.File{
			{
				Path: "registry.json",
				Data: []byte(
					"{\"version\":1,\"origins\":[]}\n",
				),
			},
		},
	)

	if !errors.Is(err, ErrRedirect) {
		t.Fatalf(
			"Publish() error = %v, want %v",
			err,
			ErrRedirect,
		)
	}
	if result != (Result{}) {
		t.Errorf(
			"Publish() result = %#v, want zero result",
			result,
		)
	}
}

func TestGitHubPublisherIntegrationReconcilesEquivalentConcurrentUpdate(
	t *testing.T,
) {
	registryData := []byte(
		"{\"version\":1,\"origins\":[]}\n",
	)

	currentEntries := []testTreeEntry{
		{
			Path: ".joshbot-registry-target",
			Mode: "100644",
			Type: "blob",
			SHA:  testSentinelSHA,
		},
	}

	exactTree := []testTreeEntry{
		{
			Path: ".joshbot-registry-target",
			Mode: "100644",
			Type: "blob",
			SHA:  testSentinelSHA,
		},
		{
			Path: "registry.json",
			Mode: "100644",
			Type: "blob",
			SHA:  "registry-blob",
		},
	}

	expectations :=
		publicationReadExpectations(
			currentEntries,
		)

	expectations = append(
		expectations,
		expectBlobCreation(
			"registry.json",
			registryData,
			"registry-blob",
		),
		expectTreeCreation(
			exactTree,
			"desired-tree",
		),
		expectCommitCreation(
			"desired-tree",
			"candidate-commit",
		),
		testRequestExpectation{
			Method: http.MethodPatch,
			Path: "/repos/joshternet/" +
				"index-data/git/refs/heads/main",
			Handler: func(
				t *testing.T,
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				writeTestJSON(
					t,
					writer,
					http.StatusConflict,
					map[string]string{
						"message": "conflict",
					},
				)
			},
		},
		testRequestExpectation{
			Method: http.MethodGet,
			Path: "/repos/joshternet/" +
				"index-data/git/ref/heads/main",
			Handler: func(
				t *testing.T,
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				writeTestJSON(
					t,
					writer,
					http.StatusOK,
					map[string]any{
						"ref": "refs/heads/main",
						"object": map[string]any{
							"type": "commit",
							"sha":  "concurrent-head",
						},
					},
				)
			},
		},
		testRequestExpectation{
			Method: http.MethodGet,
			Path: "/repos/joshternet/" +
				"index-data/git/commits/" +
				"concurrent-head",
			Handler: func(
				t *testing.T,
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				writeTestJSON(
					t,
					writer,
					http.StatusOK,
					map[string]any{
						"sha": "concurrent-head",
						"tree": map[string]any{
							"sha": "desired-tree",
						},
					},
				)
			},
		},
	)

	script := newGitHubScript(
		t,
		expectations,
	)
	server := httptest.NewServer(script)
	defer server.Close()

	publisher, err := newPublisher(
		testPublisherConfig(),
		testToken,
		server.URL,
	)
	if err != nil {
		t.Fatalf(
			"newPublisher() error = %v, want nil",
			err,
		)
	}

	result, err := publisher.Publish(
		context.Background(),
		[]publicdata.File{
			{
				Path: "registry.json",
				Data: registryData,
			},
		},
	)
	if err != nil {
		t.Fatalf(
			"Publish() error = %v, want nil",
			err,
		)
	}

	if result.Changed ||
		result.CommitSHA != "concurrent-head" {
		t.Errorf(
			"Publish() result = %#v, want unchanged concurrent-head",
			result,
		)
	}

	script.assertComplete()
}

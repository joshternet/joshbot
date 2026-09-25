package githubpublish

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/publicdata"
)

func TestGitHubPublisherFailureIntegrationPropagatesPublicationFailures(
	t *testing.T,
) {
	registryData := publisherFailureRegistryData()
	exactTree := publisherFailureExactTree()

	tests := []struct {
		name         string
		expectations []testRequestExpectation
	}{
		{
			name: "current commit request",
			expectations: []testRequestExpectation{
				expectReferenceRead(testHeadSHA),
				expectRawGitHubResponse(
					http.MethodGet,
					"/repos/joshternet/index-data/"+
						"git/commits/head-sha",
					http.StatusInternalServerError,
					`{"message":"commit unavailable"}`,
				),
			},
		},
		{
			name: "current tree request",
			expectations: append(
				initialPublicationExpectations(),
				expectRawGitHubResponse(
					http.MethodGet,
					"/repos/joshternet/index-data/"+
						"git/trees/current-tree",
					http.StatusInternalServerError,
					`{"message":"tree unavailable"}`,
				),
			),
		},
		{
			name: "sentinel blob request",
			expectations: append(
				initialPublicationExpectations(),
				expectCurrentTreeRead(
					[]testTreeEntry{
						publisherFailureSentinelEntry(),
					},
					false,
				),
				expectRawGitHubResponse(
					http.MethodGet,
					"/repos/joshternet/index-data/"+
						"git/blobs/sentinel-blob",
					http.StatusInternalServerError,
					`{"message":"sentinel unavailable"}`,
				),
			),
		},
		{
			name: "registry blob creation",
			expectations: append(
				publisherFailureReadExpectations(),
				expectRawGitHubResponse(
					http.MethodPost,
					"/repos/joshternet/index-data/"+
						"git/blobs",
					http.StatusInternalServerError,
					`{"message":"blob unavailable"}`,
				),
			),
		},
		{
			name: "tree creation",
			expectations: append(
				publisherFailureReadExpectations(),
				expectBlobCreation(
					"registry.json",
					registryData,
					"registry-blob",
				),
				expectRawGitHubResponse(
					http.MethodPost,
					"/repos/joshternet/index-data/"+
						"git/trees",
					http.StatusInternalServerError,
					`{"message":"tree creation unavailable"}`,
				),
			),
		},
		{
			name: "commit creation",
			expectations: append(
				publisherFailureReadExpectations(),
				expectBlobCreation(
					"registry.json",
					registryData,
					"registry-blob",
				),
				expectTreeCreation(
					exactTree,
					"desired-tree",
				),
				expectRawGitHubResponse(
					http.MethodPost,
					"/repos/joshternet/index-data/"+
						"git/commits",
					http.StatusInternalServerError,
					`{"message":"commit creation unavailable"}`,
				),
			),
		},
		{
			name: "reference update",
			expectations: append(
				publisherFailureReadExpectations(),
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
				expectRawGitHubResponse(
					http.MethodPatch,
					"/repos/joshternet/index-data/"+
						"git/refs/heads/main",
					http.StatusInternalServerError,
					`{"message":"reference unavailable"}`,
				),
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := runScriptedPublication(
				t,
				context.Background(),
				[]publicdata.File{
					{
						Path: "registry.json",
						Data: registryData,
					},
				},
				test.expectations,
			)

			if err == nil {
				t.Fatal(
					"Publish() error = nil, want publication failure",
				)
			}

			if result != (Result{}) {
				t.Errorf(
					"Publish() result = %#v, want zero result",
					result,
				)
			}
		})
	}
}

func TestGitHubPublisherFailureIntegrationPropagatesReconciliationFailures(
	t *testing.T,
) {
	tests := []struct {
		name         string
		expectations []testRequestExpectation
	}{
		{
			name: "concurrent head request",
			expectations: append(
				publisherConflictExpectations(),
				expectRawGitHubResponse(
					http.MethodGet,
					"/repos/joshternet/index-data/"+
						"git/ref/heads/main",
					http.StatusInternalServerError,
					`{"message":"concurrent head unavailable"}`,
				),
			),
		},
		{
			name: "concurrent commit request",
			expectations: append(
				publisherConflictExpectations(),
				expectReferenceRead(
					"concurrent-head",
				),
				expectRawGitHubResponse(
					http.MethodGet,
					"/repos/joshternet/index-data/"+
						"git/commits/concurrent-head",
					http.StatusInternalServerError,
					`{"message":"concurrent commit unavailable"}`,
				),
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := runScriptedPublication(
				t,
				context.Background(),
				[]publicdata.File{
					{
						Path: "registry.json",
						Data: publisherFailureRegistryData(),
					},
				},
				test.expectations,
			)

			if err == nil {
				t.Fatal(
					"Publish() error = nil, want reconciliation failure",
				)
			}

			if errors.Is(
				err,
				ErrPublicationConflict,
			) {
				t.Errorf(
					"Publish() error = %v, want underlying GitHub failure",
					err,
				)
			}

			if result != (Result{}) {
				t.Errorf(
					"Publish() result = %#v, want zero result",
					result,
				)
			}
		})
	}
}

func TestGitHubPublisherSafetyIntegrationRejectsUnsafeTargets(
	t *testing.T,
) {
	validSentinel := publisherFailureSentinelEntry()

	tests := []struct {
		name         string
		expectations []testRequestExpectation
	}{
		{
			name: "truncated root tree",
			expectations: append(
				initialPublicationExpectations(),
				expectCurrentTreeRead(
					[]testTreeEntry{
						validSentinel,
					},
					true,
				),
			),
		},
		{
			name: "duplicate sentinel",
			expectations: append(
				initialPublicationExpectations(),
				expectCurrentTreeRead(
					[]testTreeEntry{
						validSentinel,
						validSentinel,
					},
					false,
				),
			),
		},
		{
			name: "missing sentinel",
			expectations: append(
				initialPublicationExpectations(),
				expectCurrentTreeRead(
					nil,
					false,
				),
			),
		},
		{
			name: "wrong sentinel encoding",
			expectations: append(
				initialPublicationExpectations(),
				expectCurrentTreeRead(
					[]testTreeEntry{
						validSentinel,
					},
					false,
				),
				expectSentinelBlobRead(
					testSentinelData,
					"utf-8",
					len(testSentinelData),
				),
			),
		},
		{
			name: "malformed sentinel body",
			expectations: append(
				initialPublicationExpectations(),
				expectCurrentTreeRead(
					[]testTreeEntry{
						validSentinel,
					},
					false,
				),
				expectRawSentinelBlobRead(
					"not-valid-base64!!!",
					"base64",
					len(testSentinelData),
				),
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := runScriptedPublication(
				t,
				context.Background(),
				testRegistrySnapshot(),
				test.expectations,
			)

			if !errors.Is(
				err,
				ErrUnsafeTarget,
			) {
				t.Errorf(
					"Publish() error = %v, want %v",
					err,
					ErrUnsafeTarget,
				)
			}

			if result != (Result{}) {
				t.Errorf(
					"Publish() result = %#v, want zero result",
					result,
				)
			}
		})
	}
}

func TestGitHubPublisherIntegrationHandlesUnchangedAndConflictingTrees(
	t *testing.T,
) {
	t.Run("unchanged tree", func(t *testing.T) {
		registryData := publisherFailureRegistryData()
		currentTree := publisherFailureExactTree()

		expectations := publicationReadExpectations(
			currentTree,
		)

		expectations = append(
			expectations,
			expectBlobCreation(
				"registry.json",
				registryData,
				"registry-blob",
			),
			expectTreeCreation(
				currentTree,
				testCurrentTreeSHA,
			),
		)

		result, err := runScriptedPublication(
			t,
			context.Background(),
			[]publicdata.File{
				{
					Path: "registry.json",
					Data: registryData,
				},
			},
			expectations,
		)
		if err != nil {
			t.Fatalf(
				"Publish() error = %v, want nil",
				err,
			)
		}

		if result.Changed {
			t.Errorf(
				"Publish() Changed = true, want false",
			)
		}

		if result.CommitSHA != testHeadSHA {
			t.Errorf(
				"Publish() CommitSHA = %q, want %q",
				result.CommitSHA,
				testHeadSHA,
			)
		}
	})

	t.Run("concurrent different tree", func(t *testing.T) {
		expectations := append(
			publisherConflictExpectations(),
			expectReferenceRead(
				"concurrent-head",
			),
			expectCommitRead(
				"concurrent-head",
				"different-tree",
			),
		)

		result, err := runScriptedPublication(
			t,
			context.Background(),
			[]publicdata.File{
				{
					Path: "registry.json",
					Data: publisherFailureRegistryData(),
				},
			},
			expectations,
		)

		if !errors.Is(
			err,
			ErrPublicationConflict,
		) {
			t.Errorf(
				"Publish() error = %v, want %v",
				err,
				ErrPublicationConflict,
			)
		}

		if result != (Result{}) {
			t.Errorf(
				"Publish() result = %#v, want zero result",
				result,
			)
		}
	})
}

func TestGitHubPublisherSnapshotIntegrationRejectsInvalidSnapshots(
	t *testing.T,
) {
	publisher, err := New(
		testPublisherConfig(),
		testToken,
	)
	if err != nil {
		t.Fatalf(
			"New() error = %v, want nil",
			err,
		)
	}

	maxFile := make(
		[]byte,
		maxPublicationFileBytes,
	)

	oversizedTotal := []publicdata.File{
		{
			Path: "registry.json",
			Data: maxFile,
		},
	}

	for _, shard := range []string{
		"00",
		"01",
		"02",
		"03",
	} {
		oversizedTotal = append(
			oversizedTotal,
			publicdata.File{
				Path: githubPublisherIntegrationNodePath(
					shard,
				),
				Data: maxFile,
			},
		)
	}

	tests := []struct {
		name  string
		files []publicdata.File
	}{
		{
			name: "missing registry",
			files: []publicdata.File{
				{
					Path: githubPublisherIntegrationNodePath(
						"ab",
					),
					Data: []byte("{}\n"),
				},
			},
		},
		{
			name:  "total snapshot too large",
			files: oversizedTotal,
		},
		{
			name: "non hexadecimal shard",
			files: []publicdata.File{
				{
					Path: "registry.json",
					Data: []byte("{}\n"),
				},
				{
					Path: "nodes/g0/" +
						strings.Repeat(
							"0",
							64,
						) +
						".json",
					Data: []byte("{}\n"),
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := publisher.Publish(
				context.Background(),
				test.files,
			)

			if !errors.Is(
				err,
				ErrInvalidSnapshot,
			) {
				t.Errorf(
					"Publish() error = %v, want %v",
					err,
					ErrInvalidSnapshot,
				)
			}

			if result != (Result{}) {
				t.Errorf(
					"Publish() result = %#v, want zero result",
					result,
				)
			}
		})
	}
}

func githubPublisherIntegrationNodePath(
	shard string,
) string {
	return "nodes/" +
		shard +
		"/" +
		shard +
		strings.Repeat(
			"0",
			62,
		) +
		".json"
}

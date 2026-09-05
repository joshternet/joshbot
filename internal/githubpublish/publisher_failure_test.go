package githubpublish

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/joshternet/joshbot/internal/publicdata"
)

var errTestResponseRead = errors.New(
	"test response read failed",
)

func TestPublisherPropagatesEveryPublicationStageFailure(
	t *testing.T,
) {
	registryData := publisherFailureRegistryData()
	exactTree := publisherFailureExactTree()

	tests := []struct {
		name         string
		expectations []testRequestExpectation
	}{
		{
			name: "get current commit",
			expectations: []testRequestExpectation{
				expectReferenceRead(testHeadSHA),
				expectRawGitHubResponse(
					http.MethodGet,
					"/repos/joshternet/index-data/"+
						"git/commits/head-sha",
					http.StatusInternalServerError,
					`{"message":"`+testToken+`"}`,
				),
			},
		},
		{
			name: "get current root tree",
			expectations: append(
				initialPublicationExpectations(),
				expectRawGitHubResponse(
					http.MethodGet,
					"/repos/joshternet/index-data/"+
						"git/trees/current-tree",
					http.StatusInternalServerError,
					`{"message":"`+testToken+`"}`,
				),
			),
		},
		{
			name: "get sentinel blob",
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
					`{"message":"`+testToken+`"}`,
				),
			),
		},
		{
			name: "create registry blob",
			expectations: append(
				publisherFailureReadExpectations(),
				expectRawGitHubResponse(
					http.MethodPost,
					"/repos/joshternet/index-data/"+
						"git/blobs",
					http.StatusInternalServerError,
					`{"message":"`+testToken+`"}`,
				),
			),
		},
		{
			name: "create exact tree",
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
					`{"message":"`+testToken+`"}`,
				),
			),
		},
		{
			name: "create commit",
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
					`{"message":"`+testToken+`"}`,
				),
			),
		},
		{
			name: "non-conflict reference update failure",
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
					`{"message":"`+testToken+`"}`,
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
					"Publish() error = nil, want non-nil",
				)
			}
			if result.Changed || result.CommitSHA != "" {
				t.Errorf(
					"Publish() result = %#v, want zero result",
					result,
				)
			}

			assertTokenAbsent(t, err)
		})
	}
}

func TestPublisherRejectsInvalidSuccessfulResponses(
	t *testing.T,
) {
	registryData := publisherFailureRegistryData()
	exactTree := publisherFailureExactTree()

	tests := []struct {
		name         string
		expectations []testRequestExpectation
	}{
		{
			name: "reference is not a commit",
			expectations: []testRequestExpectation{
				expectJSONGitHubResponse(
					http.MethodGet,
					"/repos/joshternet/index-data/"+
						"git/ref/heads/main",
					http.StatusOK,
					map[string]any{
						"ref": "refs/heads/main",
						"object": map[string]any{
							"type": "tree",
							"sha":  testHeadSHA,
						},
					},
				),
			},
		},
		{
			name: "reference SHA is missing",
			expectations: []testRequestExpectation{
				expectJSONGitHubResponse(
					http.MethodGet,
					"/repos/joshternet/index-data/"+
						"git/ref/heads/main",
					http.StatusOK,
					map[string]any{
						"ref": "refs/heads/main",
						"object": map[string]any{
							"type": "commit",
						},
					},
				),
			},
		},
		{
			name: "commit tree is missing",
			expectations: []testRequestExpectation{
				expectReferenceRead(testHeadSHA),
				expectJSONGitHubResponse(
					http.MethodGet,
					"/repos/joshternet/index-data/"+
						"git/commits/head-sha",
					http.StatusOK,
					map[string]any{
						"sha":  testHeadSHA,
						"tree": map[string]any{},
					},
				),
			},
		},
		{
			name: "commit SHA is missing",
			expectations: []testRequestExpectation{
				expectReferenceRead(testHeadSHA),
				expectJSONGitHubResponse(
					http.MethodGet,
					"/repos/joshternet/index-data/"+
						"git/commits/head-sha",
					http.StatusOK,
					map[string]any{
						"tree": map[string]any{
							"sha": testCurrentTreeSHA,
						},
					},
				),
			},
		},
		{
			name: "created blob SHA is missing",
			expectations: append(
				publisherFailureReadExpectations(),
				expectJSONGitHubResponse(
					http.MethodPost,
					"/repos/joshternet/index-data/"+
						"git/blobs",
					http.StatusCreated,
					map[string]any{},
				),
			),
		},
		{
			name: "created tree SHA is missing",
			expectations: append(
				publisherFailureReadExpectations(),
				expectBlobCreation(
					"registry.json",
					registryData,
					"registry-blob",
				),
				expectJSONGitHubResponse(
					http.MethodPost,
					"/repos/joshternet/index-data/"+
						"git/trees",
					http.StatusCreated,
					map[string]any{},
				),
			),
		},
		{
			name: "created commit SHA is missing",
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
				expectJSONGitHubResponse(
					http.MethodPost,
					"/repos/joshternet/index-data/"+
						"git/commits",
					http.StatusCreated,
					map[string]any{},
				),
			),
		},
		{
			name: "updated reference points elsewhere",
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
				expectJSONGitHubResponse(
					http.MethodPatch,
					"/repos/joshternet/index-data/"+
						"git/refs/heads/main",
					http.StatusOK,
					map[string]any{
						"ref": "refs/heads/main",
						"object": map[string]any{
							"type": "commit",
							"sha":  "different-commit",
						},
					},
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
			if !errors.Is(
				err,
				ErrInvalidGitHubResponse,
			) {
				t.Errorf(
					"Publish() error = %v, want ErrInvalidGitHubResponse",
					err,
				)
			}
			if result.Changed || result.CommitSHA != "" {
				t.Errorf(
					"Publish() result = %#v, want zero result",
					result,
				)
			}

			assertTokenAbsent(t, err)
		})
	}
}

func TestPublisherPropagatesReconciliationReadFailures(
	t *testing.T,
) {
	tests := []struct {
		name         string
		expectations []testRequestExpectation
	}{
		{
			name: "read concurrent reference",
			expectations: append(
				publisherConflictExpectations(),
				expectRawGitHubResponse(
					http.MethodGet,
					"/repos/joshternet/index-data/"+
						"git/ref/heads/main",
					http.StatusInternalServerError,
					`{"message":"`+testToken+`"}`,
				),
			),
		},
		{
			name: "read concurrent commit",
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
					`{"message":"`+testToken+`"}`,
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
					"Publish() error = nil, want non-nil",
				)
			}
			if errors.Is(err, ErrPublicationConflict) {
				t.Errorf(
					"Publish() error = %v, want underlying read failure",
					err,
				)
			}
			if result.Changed || result.CommitSHA != "" {
				t.Errorf(
					"Publish() result = %#v, want zero result",
					result,
				)
			}

			assertTokenAbsent(t, err)
		})
	}
}

func TestPublisherRequestJSONDefensiveFailures(
	t *testing.T,
) {
	publisher, err := New(
		testPublisherConfig(),
		testToken,
	)
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}

	t.Run("context already canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		cancel()

		err := publisher.requestJSON(
			ctx,
			"canceled request",
			http.MethodGet,
			"/test",
			nil,
			http.StatusOK,
			&referenceResponse{},
		)
		if !errors.Is(err, context.Canceled) {
			t.Errorf(
				"requestJSON() error = %v, want context.Canceled",
				err,
			)
		}
	})

	t.Run("request body cannot be encoded", func(t *testing.T) {
		err := publisher.requestJSON(
			context.Background(),
			"invalid request body",
			http.MethodPost,
			"/test",
			make(chan struct{}),
			http.StatusOK,
			&referenceResponse{},
		)
		if err == nil {
			t.Fatal(
				"requestJSON() error = nil, want non-nil",
			)
		}

		assertTokenAbsent(t, err)
	})

	t.Run("request method is invalid", func(t *testing.T) {
		err := publisher.requestJSON(
			context.Background(),
			"invalid request method",
			"\n",
			"/test",
			nil,
			http.StatusOK,
			&referenceResponse{},
		)
		if err == nil {
			t.Fatal(
				"requestJSON() error = nil, want non-nil",
			)
		}

		assertTokenAbsent(t, err)
	})

	t.Run("context canceled by transport", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)

		testPublisher := *publisher
		testPublisher.client = &http.Client{
			Transport: testRoundTripFunc(
				func(
					_ *http.Request,
				) (*http.Response, error) {
					cancel()
					return nil, errors.New(
						"transport failed",
					)
				},
			),
		}

		err := testPublisher.requestJSON(
			ctx,
			"transport cancellation",
			http.MethodGet,
			"/test",
			nil,
			http.StatusOK,
			&referenceResponse{},
		)
		if !errors.Is(err, context.Canceled) {
			t.Errorf(
				"requestJSON() error = %v, want context.Canceled",
				err,
			)
		}
	})

	t.Run("response body read fails", func(t *testing.T) {
		testPublisher := *publisher
		testPublisher.client = &http.Client{
			Transport: testRoundTripFunc(
				func(
					request *http.Request,
				) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body: &testFailingReadCloser{
							err: errTestResponseRead,
						},
						Request: request,
					}, nil
				},
			),
		}

		err := testPublisher.requestJSON(
			context.Background(),
			"failed response read",
			http.MethodGet,
			"/test",
			nil,
			http.StatusOK,
			&referenceResponse{},
		)
		if !errors.Is(
			err,
			errTestResponseRead,
		) {
			t.Errorf(
				"requestJSON() error = %v, want read failure",
				err,
			)
		}

		assertTokenAbsent(t, err)
	})
}

func TestPublisherInternalValidationBoundaries(
	t *testing.T,
) {
	if lowerHex("") {
		t.Error("lowerHex(\"\") = true, want false")
	}

	if isReferenceConflict(
		errors.New("ordinary failure"),
	) {
		t.Error(
			"isReferenceConflict(ordinary error) = true, want false",
		)
	}
}

func publisherFailureRegistryData() []byte {
	return []byte(
		"{\"version\":1,\"origins\":[]}\n",
	)
}

func publisherFailureSentinelEntry() testTreeEntry {
	return testTreeEntry{
		Path: sentinelPath,
		Mode: "100644",
		Type: "blob",
		SHA:  testSentinelSHA,
	}
}

func publisherFailureExactTree() []testTreeEntry {
	return []testTreeEntry{
		publisherFailureSentinelEntry(),
		{
			Path: "registry.json",
			Mode: "100644",
			Type: "blob",
			SHA:  "registry-blob",
		},
	}
}

func publisherFailureReadExpectations() []testRequestExpectation {
	return publicationReadExpectations(
		[]testTreeEntry{
			publisherFailureSentinelEntry(),
		},
	)
}

func publisherConflictExpectations() []testRequestExpectation {
	expectations := publisherFailureReadExpectations()
	expectations = append(
		expectations,
		expectBlobCreation(
			"registry.json",
			publisherFailureRegistryData(),
			"registry-blob",
		),
		expectTreeCreation(
			publisherFailureExactTree(),
			"desired-tree",
		),
		expectCommitCreation(
			"desired-tree",
			"candidate-commit",
		),
		expectReferenceConflict(
			"candidate-commit",
			http.StatusConflict,
		),
	)

	return expectations
}

func expectJSONGitHubResponse(
	method string,
	path string,
	statusCode int,
	value any,
) testRequestExpectation {
	return testRequestExpectation{
		Method: method,
		Path:   path,
		Handler: func(
			t *testing.T,
			response http.ResponseWriter,
			_ *http.Request,
		) {
			writeTestJSON(
				t,
				response,
				statusCode,
				value,
			)
		},
	}
}

type testRoundTripFunc func(
	*http.Request,
) (*http.Response, error)

func (roundTrip testRoundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTrip(request)
}

type testFailingReadCloser struct {
	err error
}

func (reader *testFailingReadCloser) Read(
	_ []byte,
) (int, error) {
	return 0, reader.err
}

func (reader *testFailingReadCloser) Close() error {
	return nil
}

func decodeFailureTestJSON(
	t *testing.T,
	reader io.Reader,
	output any,
) {
	t.Helper()

	if err := json.NewDecoder(reader).Decode(
		output,
	); err != nil {
		t.Errorf(
			"decode test JSON: %v",
			err,
		)
	}
}

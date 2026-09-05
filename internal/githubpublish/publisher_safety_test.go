package githubpublish

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshternet/joshbot/internal/publicdata"
)

func TestPublisherRejectsUnsafeTargetBeforeMutation(t *testing.T) {
	validSentinel := testTreeEntry{
		Path: sentinelPath,
		Mode: "100644",
		Type: "blob",
		SHA:  testSentinelSHA,
	}

	tests := []struct {
		name         string
		expectations []testRequestExpectation
	}{
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
			name: "sentinel is a tree",
			expectations: append(
				initialPublicationExpectations(),
				expectCurrentTreeRead(
					[]testTreeEntry{
						{
							Path: sentinelPath,
							Mode: "040000",
							Type: "tree",
							SHA:  "sentinel-tree",
						},
					},
					false,
				),
			),
		},
		{
			name: "sentinel is a symbolic link",
			expectations: append(
				initialPublicationExpectations(),
				expectCurrentTreeRead(
					[]testTreeEntry{
						{
							Path: sentinelPath,
							Mode: "120000",
							Type: "blob",
							SHA:  testSentinelSHA,
						},
					},
					false,
				),
			),
		},
		{
			name: "sentinel has wrong contents",
			expectations: append(
				initialPublicationExpectations(),
				expectCurrentTreeRead(
					[]testTreeEntry{validSentinel},
					false,
				),
				expectSentinelBlobRead(
					"joshbot-registry-v2\n",
					"base64",
					len(testSentinelData),
				),
			),
		},
		{
			name: "sentinel has wrong encoding",
			expectations: append(
				initialPublicationExpectations(),
				expectCurrentTreeRead(
					[]testTreeEntry{validSentinel},
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
			name: "sentinel size is wrong",
			expectations: append(
				initialPublicationExpectations(),
				expectCurrentTreeRead(
					[]testTreeEntry{validSentinel},
					false,
				),
				expectSentinelBlobRead(
					testSentinelData,
					"base64",
					len(testSentinelData)+1,
				),
			),
		},
		{
			name: "sentinel base64 is malformed",
			expectations: append(
				initialPublicationExpectations(),
				expectCurrentTreeRead(
					[]testTreeEntry{validSentinel},
					false,
				),
				expectRawSentinelBlobRead(
					"not-valid-base64!!!",
					"base64",
					len(testSentinelData),
				),
			),
		},
		{
			name: "root tree is truncated",
			expectations: append(
				initialPublicationExpectations(),
				expectCurrentTreeRead(
					[]testTreeEntry{validSentinel},
					true,
				),
			),
		},
		{
			name: "root tree SHA is missing",
			expectations: append(
				initialPublicationExpectations(),
				testRequestExpectation{
					Method: http.MethodGet,
					Path: "/repos/joshternet/index-data/" +
						"git/trees/current-tree",
					Handler: func(
						t *testing.T,
						response http.ResponseWriter,
						_ *http.Request,
					) {
						writeTestJSON(
							t,
							response,
							http.StatusOK,
							testTreeResponse{
								Tree: []testTreeEntry{
									validSentinel,
								},
							},
						)
					},
				},
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := runScriptedPublication(
				t,
				context.Background(),
				testRegistrySnapshot(),
				test.expectations,
			)
			if !errors.Is(err, ErrUnsafeTarget) {
				t.Errorf(
					"Publish() error = %v, want ErrUnsafeTarget",
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

func TestPublisherHandlesGitHubStatusFailuresSafely(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{
			name:       "authentication rejected",
			statusCode: http.StatusUnauthorized,
		},
		{
			name:       "permission rejected",
			statusCode: http.StatusForbidden,
		},
		{
			name:       "branch missing",
			statusCode: http.StatusNotFound,
		},
		{
			name:       "rate limited",
			statusCode: http.StatusTooManyRequests,
		},
		{
			name:       "GitHub unavailable",
			statusCode: http.StatusInternalServerError,
		},
		{
			name:       "GitHub service unavailable",
			statusCode: http.StatusServiceUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expectations := []testRequestExpectation{
				expectRawGitHubResponse(
					http.MethodGet,
					"/repos/joshternet/index-data/"+
						"git/ref/heads/main",
					test.statusCode,
					`{"message":"`+testToken+`"}`,
				),
			}

			result, err := runScriptedPublication(
				t,
				context.Background(),
				testRegistrySnapshot(),
				expectations,
			)
			if err == nil {
				t.Fatal("Publish() error = nil, want non-nil")
			}
			if result.Changed || result.CommitSHA != "" {
				t.Errorf(
					"Publish() result = %#v, want zero result",
					result,
				)
			}

			var statusError *githubStatusError
			if !errors.As(err, &statusError) {
				t.Fatalf(
					"Publish() error type = %T, want *githubStatusError",
					err,
				)
			}
			if statusError.statusCode != test.statusCode {
				t.Errorf(
					"status code = %d, want %d",
					statusError.statusCode,
					test.statusCode,
				)
			}

			assertTokenAbsent(t, err)
		})
	}
}

func TestPublisherRejectsMalformedAndOversizedResponses(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantError error
	}{
		{
			name:      "malformed JSON",
			body:      "{" + testToken,
			wantError: ErrInvalidGitHubResponse,
		},
		{
			name: "oversized response",
			body: `{"ref":"` +
				strings.Repeat(
					"x",
					maxGitHubResponseBytes,
				) +
				`"}`,
			wantError: ErrGitHubResponseTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expectations := []testRequestExpectation{
				expectRawGitHubResponse(
					http.MethodGet,
					"/repos/joshternet/index-data/"+
						"git/ref/heads/main",
					http.StatusOK,
					test.body,
				),
			}

			_, err := runScriptedPublication(
				t,
				context.Background(),
				testRegistrySnapshot(),
				expectations,
			)
			if !errors.Is(err, test.wantError) {
				t.Errorf(
					"Publish() error = %v, want %v",
					err,
					test.wantError,
				)
			}

			assertTokenAbsent(t, err)
		})
	}
}

func TestPublisherRejectsAuthenticatedRedirect(t *testing.T) {
	var redirectedRequests atomic.Int32

	redirectTarget := httptest.NewServer(
		http.HandlerFunc(
			func(
				response http.ResponseWriter,
				request *http.Request,
			) {
				redirectedRequests.Add(1)

				if request.Header.Get("Authorization") != "" {
					t.Error(
						"redirect target received Authorization header",
					)
				}

				http.Error(
					response,
					"redirect target reached",
					http.StatusInternalServerError,
				)
			},
		),
	)
	defer redirectTarget.Close()

	redirectSource := httptest.NewServer(
		http.HandlerFunc(
			func(
				response http.ResponseWriter,
				request *http.Request,
			) {
				assertPublicationHeaders(t, request)
				http.Redirect(
					response,
					request,
					redirectTarget.URL,
					http.StatusTemporaryRedirect,
				)
			},
		),
	)
	defer redirectSource.Close()

	publisher, err := newPublisher(
		testPublisherConfig(),
		testToken,
		redirectSource.URL,
	)
	if err != nil {
		t.Fatalf("newPublisher() error = %v, want nil", err)
	}

	result, err := publisher.Publish(
		context.Background(),
		testRegistrySnapshot(),
	)
	if !errors.Is(err, ErrRedirect) {
		t.Errorf(
			"Publish() error = %v, want ErrRedirect",
			err,
		)
	}
	if result.Changed || result.CommitSHA != "" {
		t.Errorf(
			"Publish() result = %#v, want zero result",
			result,
		)
	}
	if redirectedRequests.Load() != 0 {
		t.Errorf(
			"redirect target requests = %d, want 0",
			redirectedRequests.Load(),
		)
	}

	assertTokenAbsent(t, err)
}

func TestPublisherPreservesContextCancellation(t *testing.T) {
	var requests atomic.Int32

	server := httptest.NewServer(
		http.HandlerFunc(
			func(
				response http.ResponseWriter,
				_ *http.Request,
			) {
				requests.Add(1)
				http.Error(
					response,
					"unexpected request",
					http.StatusInternalServerError,
				)
			},
		),
	)
	defer server.Close()

	publisher, err := newPublisher(
		testPublisherConfig(),
		testToken,
		server.URL,
	)
	if err != nil {
		t.Fatalf("newPublisher() error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	_, err = publisher.Publish(
		ctx,
		testRegistrySnapshot(),
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Publish() error = %v, want context.Canceled",
			err,
		)
	}
	if requests.Load() != 0 {
		t.Errorf(
			"GitHub requests = %d, want 0",
			requests.Load(),
		)
	}

	_, err = publisher.Publish(
		nil,
		testRegistrySnapshot(),
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Publish(nil) error = %v, want context.Canceled",
			err,
		)
	}
	if requests.Load() != 0 {
		t.Errorf(
			"GitHub requests after nil context = %d, want 0",
			requests.Load(),
		)
	}
}

func TestPublisherUsesFixedOfficialAPIAndDisablesProxy(t *testing.T) {
	publisher, err := New(
		testPublisherConfig(),
		testToken,
	)
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}

	if publisher.apiBase.String() != githubAPIBase {
		t.Errorf(
			"production API base = %q, want %q",
			publisher.apiBase.String(),
			githubAPIBase,
		)
	}

	transport, ok := publisher.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf(
			"publisher transport type = %T, want *http.Transport",
			publisher.client.Transport,
		)
	}
	if transport.Proxy != nil {
		t.Error("publisher transport Proxy is non-nil")
	}
}

func TestPublisherRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{
			name: "empty owner",
			config: Config{
				Repository: testRepository,
				Branch:     testBranch,
			},
		},
		{
			name: "padded owner",
			config: Config{
				Owner:      " joshternet",
				Repository: testRepository,
				Branch:     testBranch,
			},
		},
		{
			name: "owner slash",
			config: Config{
				Owner:      "joshternet/other",
				Repository: testRepository,
				Branch:     testBranch,
			},
		},
		{
			name: "empty repository",
			config: Config{
				Owner:  testOwner,
				Branch: testBranch,
			},
		},
		{
			name: "repository slash",
			config: Config{
				Owner:      testOwner,
				Repository: "index/data",
				Branch:     testBranch,
			},
		},
		{
			name: "empty branch",
			config: Config{
				Owner:      testOwner,
				Repository: testRepository,
			},
		},
		{
			name: "padded branch",
			config: Config{
				Owner:      testOwner,
				Repository: testRepository,
				Branch:     "main ",
			},
		},
		{
			name: "slash branch",
			config: Config{
				Owner:      testOwner,
				Repository: testRepository,
				Branch:     "release/main",
			},
		},
		{
			name: "double dot branch",
			config: Config{
				Owner:      testOwner,
				Repository: testRepository,
				Branch:     "main..next",
			},
		},
		{
			name: "refs prefix",
			config: Config{
				Owner:      testOwner,
				Repository: testRepository,
				Branch:     "refs/main",
			},
		},
		{
			name: "control character",
			config: Config{
				Owner:      testOwner,
				Repository: testRepository,
				Branch:     "main\n",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			publisher, err := New(
				test.config,
				testToken,
			)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf(
					"New() error = %v, want ErrInvalidConfig",
					err,
				)
			}
			if publisher != nil {
				t.Errorf(
					"New() publisher = %#v, want nil",
					publisher,
				)
			}
		})
	}
}

func TestPublisherRejectsInvalidTokenAndTestAPIBase(t *testing.T) {
	for _, token := range []string{
		"",
		" token",
		"token ",
		"token\n",
		"token\x7f",
	} {
		publisher, err := New(
			testPublisherConfig(),
			token,
		)
		if !errors.Is(err, ErrInvalidToken) {
			t.Errorf(
				"New() token error = %v, want ErrInvalidToken",
				err,
			)
		}
		if publisher != nil {
			t.Errorf(
				"New() publisher = %#v, want nil",
				publisher,
			)
		}
	}

	for _, apiBase := range []string{
		"://invalid",
		"ftp://api.github.com",
		"https://",
		"https://user@api.github.com",
		"https://api.github.com/root",
		"https://api.github.com?destination=other",
		"https://api.github.com#fragment",
	} {
		publisher, err := newPublisher(
			testPublisherConfig(),
			testToken,
			apiBase,
		)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Errorf(
				"newPublisher(%q) error = %v, want ErrInvalidConfig",
				apiBase,
				err,
			)
		}
		if publisher != nil {
			t.Errorf(
				"newPublisher(%q) publisher = %#v, want nil",
				apiBase,
				publisher,
			)
		}
	}
}

func TestPublisherRejectsInvalidSnapshotBeforeNetwork(t *testing.T) {
	var requests atomic.Int32

	server := httptest.NewServer(
		http.HandlerFunc(
			func(
				response http.ResponseWriter,
				_ *http.Request,
			) {
				requests.Add(1)
				http.Error(
					response,
					"unexpected request",
					http.StatusInternalServerError,
				)
			},
		),
	)
	defer server.Close()

	publisher, err := newPublisher(
		testPublisherConfig(),
		testToken,
		server.URL,
	)
	if err != nil {
		t.Fatalf("newPublisher() error = %v, want nil", err)
	}

	tooManyFiles := make(
		[]publicdata.File,
		maxPublicationFiles+1,
	)
	tooManyFiles[0] = publicdata.File{
		Path: "registry.json",
		Data: []byte("{}\n"),
	}

	oversizedFile := []publicdata.File{
		{
			Path: "registry.json",
			Data: make(
				[]byte,
				maxPublicationFileBytes+1,
			),
		},
	}

	totalLimitData := make(
		[]byte,
		maxPublicationFileBytes,
	)
	oversizedTotal := []publicdata.File{
		{
			Path: "registry.json",
			Data: totalLimitData,
		},
	}
	for index := 1; index <= 4; index++ {
		hash := fmt.Sprintf("%064x", index)
		oversizedTotal = append(
			oversizedTotal,
			publicdata.File{
				Path: "nodes/" +
					hash[:2] +
					"/" +
					hash +
					".json",
				Data: totalLimitData,
			},
		)
	}

	tests := []struct {
		name  string
		files []publicdata.File
	}{
		{
			name:  "empty snapshot",
			files: nil,
		},
		{
			name: "missing registry",
			files: []publicdata.File{
				{
					Path: testNodePath,
					Data: []byte("{}\n"),
				},
			},
		},
		{
			name: "duplicate registry",
			files: []publicdata.File{
				{
					Path: "registry.json",
					Data: []byte("{}\n"),
				},
				{
					Path: "registry.json",
					Data: []byte("{}\n"),
				},
			},
		},
		{
			name: "unexpected README",
			files: []publicdata.File{
				{
					Path: "registry.json",
					Data: []byte("{}\n"),
				},
				{
					Path: "README.md",
					Data: []byte("unsafe"),
				},
			},
		},
		{
			name: "workflow path",
			files: []publicdata.File{
				{
					Path: "registry.json",
					Data: []byte("{}\n"),
				},
				{
					Path: ".github/workflows/publish.yml",
					Data: []byte("unsafe"),
				},
			},
		},
		{
			name: "uppercase shard",
			files: []publicdata.File{
				{
					Path: "registry.json",
					Data: []byte("{}\n"),
				},
				{
					Path: "nodes/AB/" +
						testNodeHash +
						".json",
					Data: []byte("{}\n"),
				},
			},
		},
		{
			name: "wrong shard",
			files: []publicdata.File{
				{
					Path: "registry.json",
					Data: []byte("{}\n"),
				},
				{
					Path: "nodes/ff/" +
						testNodeHash +
						".json",
					Data: []byte("{}\n"),
				},
			},
		},
		{
			name: "wrong hash length",
			files: []publicdata.File{
				{
					Path: "registry.json",
					Data: []byte("{}\n"),
				},
				{
					Path: "nodes/ab/abcdef.json",
					Data: []byte("{}\n"),
				},
			},
		},
		{
			name:  "too many files",
			files: tooManyFiles,
		},
		{
			name:  "individual file too large",
			files: oversizedFile,
		},
		{
			name:  "total snapshot too large",
			files: oversizedTotal,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := publisher.Publish(
				context.Background(),
				test.files,
			)
			if !errors.Is(err, ErrInvalidSnapshot) {
				t.Errorf(
					"Publish() error = %v, want ErrInvalidSnapshot",
					err,
				)
			}
			if result.Changed || result.CommitSHA != "" {
				t.Errorf(
					"Publish() result = %#v, want zero result",
					result,
				)
			}
		})
	}

	if requests.Load() != 0 {
		t.Errorf(
			"GitHub requests = %d, want 0",
			requests.Load(),
		)
	}
}

func TestPublisherRejectsInvalidPublisher(t *testing.T) {
	var nilPublisher *Publisher

	_, err := nilPublisher.Publish(
		context.Background(),
		testRegistrySnapshot(),
	)
	if !errors.Is(err, ErrInvalidPublisher) {
		t.Errorf(
			"nil Publisher.Publish() error = %v, want ErrInvalidPublisher",
			err,
		)
	}

	zeroPublisher := &Publisher{}
	_, err = zeroPublisher.Publish(
		context.Background(),
		testRegistrySnapshot(),
	)
	if !errors.Is(err, ErrInvalidPublisher) {
		t.Errorf(
			"zero Publisher.Publish() error = %v, want ErrInvalidPublisher",
			err,
		)
	}
}

func TestPublisherReconcilesConcurrentSameTree(t *testing.T) {
	registryData := []byte(
		"{\"version\":1,\"origins\":[]}\n",
	)
	desiredTreeSHA := "desired-tree"
	concurrentHeadSHA := "concurrent-head"

	expectedTree := []testTreeEntry{
		{
			Path: sentinelPath,
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

	expectations := publicationReadExpectations(
		[]testTreeEntry{
			{
				Path: sentinelPath,
				Mode: "100644",
				Type: "blob",
				SHA:  testSentinelSHA,
			},
		},
	)
	expectations = append(
		expectations,
		expectBlobCreation(
			"registry.json",
			registryData,
			"registry-blob",
		),
		expectTreeCreation(
			expectedTree,
			desiredTreeSHA,
		),
		expectCommitCreation(
			desiredTreeSHA,
			"candidate-commit",
		),
		expectReferenceConflict(
			"candidate-commit",
			http.StatusUnprocessableEntity,
		),
		expectReferenceRead(concurrentHeadSHA),
		expectCommitRead(
			concurrentHeadSHA,
			desiredTreeSHA,
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
		t.Fatalf("Publish() error = %v, want nil", err)
	}
	if result.Changed {
		t.Error("Publish() Changed = true, want false")
	}
	if result.CommitSHA != concurrentHeadSHA {
		t.Errorf(
			"Publish() CommitSHA = %q, want %q",
			result.CommitSHA,
			concurrentHeadSHA,
		)
	}
}

func TestPublisherReportsConcurrentDifferentTree(t *testing.T) {
	registryData := []byte(
		"{\"version\":1,\"origins\":[]}\n",
	)
	desiredTreeSHA := "desired-tree"
	concurrentHeadSHA := "concurrent-head"

	expectedTree := []testTreeEntry{
		{
			Path: sentinelPath,
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

	expectations := publicationReadExpectations(
		[]testTreeEntry{
			{
				Path: sentinelPath,
				Mode: "100644",
				Type: "blob",
				SHA:  testSentinelSHA,
			},
		},
	)
	expectations = append(
		expectations,
		expectBlobCreation(
			"registry.json",
			registryData,
			"registry-blob",
		),
		expectTreeCreation(
			expectedTree,
			desiredTreeSHA,
		),
		expectCommitCreation(
			desiredTreeSHA,
			"candidate-commit",
		),
		expectReferenceConflict(
			"candidate-commit",
			http.StatusConflict,
		),
		expectReferenceRead(concurrentHeadSHA),
		expectCommitRead(
			concurrentHeadSHA,
			"different-tree",
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
	if !errors.Is(err, ErrPublicationConflict) {
		t.Errorf(
			"Publish() error = %v, want ErrPublicationConflict",
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
}

func testRegistrySnapshot() []publicdata.File {
	return []publicdata.File{
		{
			Path: "registry.json",
			Data: []byte(
				"{\"version\":1,\"origins\":[]}\n",
			),
		},
	}
}

func runScriptedPublication(
	t *testing.T,
	ctx context.Context,
	files []publicdata.File,
	expectations []testRequestExpectation,
) (Result, error) {
	t.Helper()

	script := newGitHubScript(
		t,
		expectations,
	)
	server := httptest.NewServer(script)

	publisher, err := newPublisher(
		testPublisherConfig(),
		testToken,
		server.URL,
	)
	if err != nil {
		server.Close()
		t.Fatalf(
			"newPublisher() error = %v, want nil",
			err,
		)
	}

	result, publishErr := publisher.Publish(
		ctx,
		files,
	)

	server.Close()
	script.assertComplete()

	return result, publishErr
}

func initialPublicationExpectations() []testRequestExpectation {
	return []testRequestExpectation{
		expectReferenceRead(testHeadSHA),
		expectCommitRead(
			testHeadSHA,
			testCurrentTreeSHA,
		),
	}
}

func expectReferenceRead(
	headSHA string,
) testRequestExpectation {
	return testRequestExpectation{
		Method: http.MethodGet,
		Path: "/repos/joshternet/index-data/" +
			"git/ref/heads/main",
		Handler: func(
			t *testing.T,
			response http.ResponseWriter,
			_ *http.Request,
		) {
			writeTestJSON(
				t,
				response,
				http.StatusOK,
				map[string]any{
					"ref": "refs/heads/main",
					"object": map[string]any{
						"type": "commit",
						"sha":  headSHA,
					},
				},
			)
		},
	}
}

func expectCommitRead(
	commitSHA string,
	treeSHA string,
) testRequestExpectation {
	return testRequestExpectation{
		Method: http.MethodGet,
		Path: "/repos/joshternet/index-data/" +
			"git/commits/" +
			commitSHA,
		Handler: func(
			t *testing.T,
			response http.ResponseWriter,
			_ *http.Request,
		) {
			writeTestJSON(
				t,
				response,
				http.StatusOK,
				map[string]any{
					"sha": commitSHA,
					"tree": map[string]any{
						"sha": treeSHA,
					},
				},
			)
		},
	}
}

func expectCurrentTreeRead(
	entries []testTreeEntry,
	truncated bool,
) testRequestExpectation {
	return testRequestExpectation{
		Method: http.MethodGet,
		Path: "/repos/joshternet/index-data/" +
			"git/trees/current-tree",
		Handler: func(
			t *testing.T,
			response http.ResponseWriter,
			_ *http.Request,
		) {
			writeTestJSON(
				t,
				response,
				http.StatusOK,
				testTreeResponse{
					SHA:       testCurrentTreeSHA,
					Tree:      entries,
					Truncated: truncated,
				},
			)
		},
	}
}

func expectSentinelBlobRead(
	contents string,
	encoding string,
	size int,
) testRequestExpectation {
	return expectRawSentinelBlobRead(
		base64.StdEncoding.EncodeToString(
			[]byte(contents),
		),
		encoding,
		size,
	)
}

func expectRawSentinelBlobRead(
	content string,
	encoding string,
	size int,
) testRequestExpectation {
	return testRequestExpectation{
		Method: http.MethodGet,
		Path: "/repos/joshternet/index-data/" +
			"git/blobs/sentinel-blob",
		Handler: func(
			t *testing.T,
			response http.ResponseWriter,
			_ *http.Request,
		) {
			writeTestJSON(
				t,
				response,
				http.StatusOK,
				map[string]any{
					"sha":      testSentinelSHA,
					"content":  content,
					"encoding": encoding,
					"size":     size,
				},
			)
		},
	}
}

func expectRawGitHubResponse(
	method string,
	path string,
	statusCode int,
	body string,
) testRequestExpectation {
	return testRequestExpectation{
		Method: method,
		Path:   path,
		Handler: func(
			t *testing.T,
			response http.ResponseWriter,
			_ *http.Request,
		) {
			response.Header().Set(
				"Content-Type",
				"application/json",
			)
			response.WriteHeader(statusCode)

			if _, err := io.WriteString(
				response,
				body,
			); err != nil {
				t.Errorf(
					"write fake GitHub response: %v",
					err,
				)
			}
		},
	}
}

func expectReferenceConflict(
	commitSHA string,
	statusCode int,
) testRequestExpectation {
	return testRequestExpectation{
		Method: http.MethodPatch,
		Path: "/repos/joshternet/index-data/" +
			"git/refs/heads/main",
		Handler: func(
			t *testing.T,
			response http.ResponseWriter,
			request *http.Request,
		) {
			body, ok := readTestRequestBody(
				t,
				request,
			)
			if !ok {
				http.Error(
					response,
					"invalid request",
					http.StatusBadRequest,
				)
				return
			}

			var update testReferenceUpdateRequest
			if err := json.Unmarshal(
				body,
				&update,
			); err != nil {
				t.Errorf(
					"decode reference conflict request: %v",
					err,
				)
				http.Error(
					response,
					"invalid request",
					http.StatusBadRequest,
				)
				return
			}

			expected := testReferenceUpdateRequest{
				SHA:   commitSHA,
				Force: false,
			}
			if update != expected {
				t.Errorf(
					"reference conflict request = %#v, want %#v",
					update,
					expected,
				)
			}

			writeTestJSON(
				t,
				response,
				statusCode,
				map[string]any{
					"message": "reference changed",
				},
			)
		},
	}
}

func assertTokenAbsent(
	t *testing.T,
	err error,
) {
	t.Helper()

	if err != nil &&
		strings.Contains(
			err.Error(),
			testToken,
		) {
		t.Error("error string contains the GitHub token")
	}
}

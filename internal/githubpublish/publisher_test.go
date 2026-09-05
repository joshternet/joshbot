package githubpublish

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/joshternet/joshbot/internal/publicdata"
)

const (
	testOwner          = "joshternet"
	testRepository     = "index-data"
	testBranch         = "main"
	testToken          = "publisher-test-token"
	testHeadSHA        = "head-sha"
	testCurrentTreeSHA = "current-tree"
	testSentinelSHA    = "sentinel-blob"
	testSentinelData   = "joshbot-registry-v1\n"

	testNodeHash = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	testNodePath = "nodes/ab/" + testNodeHash + ".json"
)

func TestPublisherNewConstructsPublisher(t *testing.T) {
	t.Parallel()

	publisher, err := New(testPublisherConfig(), testToken)
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if publisher == nil {
		t.Fatal("New() publisher = nil, want non-nil")
	}
}

func TestPublisherPublishesExactChangedSnapshot(t *testing.T) {
	t.Parallel()

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
			Path: "nodes/ff/ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff.json",
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

	expectations := publicationReadExpectations(currentEntries)
	expectations = append(
		expectations,
		expectBlobCreation(testNodePath, nodeData, "node-blob"),
		expectBlobCreation("registry.json", registryData, "registry-blob"),
		expectTreeCreation(expectedTree, "new-tree"),
		expectCommitCreation("new-tree", "new-commit"),
		expectReferenceUpdate("new-commit"),
	)

	script := newGitHubScript(t, expectations)
	server := httptest.NewServer(script)
	defer server.Close()

	publisher, err := newPublisher(
		testPublisherConfig(),
		testToken,
		server.URL,
	)
	if err != nil {
		t.Fatalf("newPublisher() error = %v, want nil", err)
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
		t.Fatalf("Publish() error = %v, want nil", err)
	}

	if !result.Changed {
		t.Error("Publish() Changed = false, want true")
	}
	if result.CommitSHA != "new-commit" {
		t.Errorf(
			"Publish() CommitSHA = %q, want %q",
			result.CommitSHA,
			"new-commit",
		)
	}

	script.assertComplete()
}

func TestPublisherSkipsCommitWhenTreeIsUnchanged(t *testing.T) {
	t.Parallel()

	registryData := []byte("{\"version\":1,\"origins\":[]}\n")

	currentEntries := []testTreeEntry{
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

	expectedTree := []testTreeEntry{
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

	expectations := publicationReadExpectations(currentEntries)
	expectations = append(
		expectations,
		expectBlobCreation("registry.json", registryData, "registry-blob"),
		expectTreeCreation(expectedTree, testCurrentTreeSHA),
	)

	script := newGitHubScript(t, expectations)
	server := httptest.NewServer(script)
	defer server.Close()

	publisher, err := newPublisher(
		testPublisherConfig(),
		testToken,
		server.URL,
	)
	if err != nil {
		t.Fatalf("newPublisher() error = %v, want nil", err)
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
		t.Fatalf("Publish() error = %v, want nil", err)
	}

	if result.Changed {
		t.Error("Publish() Changed = true, want false")
	}

	script.assertComplete()
}

func testPublisherConfig() Config {
	return Config{
		Owner:      testOwner,
		Repository: testRepository,
		Branch:     testBranch,
	}
}

type testRequestExpectation struct {
	Method  string
	Path    string
	Handler func(*testing.T, http.ResponseWriter, *http.Request)
}

type testGitHubScript struct {
	t            *testing.T
	mu           sync.Mutex
	next         int
	expectations []testRequestExpectation
}

func newGitHubScript(
	t *testing.T,
	expectations []testRequestExpectation,
) *testGitHubScript {
	t.Helper()

	return &testGitHubScript{
		t:            t,
		expectations: expectations,
	}
}

func (script *testGitHubScript) ServeHTTP(
	response http.ResponseWriter,
	request *http.Request,
) {
	script.mu.Lock()

	if script.next >= len(script.expectations) {
		script.mu.Unlock()
		script.t.Errorf(
			"unexpected GitHub request: %s %s",
			request.Method,
			request.URL.Path,
		)
		http.Error(
			response,
			"unexpected request",
			http.StatusInternalServerError,
		)
		return
	}

	expectation := script.expectations[script.next]
	script.next++

	script.mu.Unlock()

	assertPublicationHeaders(script.t, request)

	if request.URL.RawQuery != "" {
		script.t.Errorf(
			"request %s %s query = %q, want empty",
			request.Method,
			request.URL.Path,
			request.URL.RawQuery,
		)
	}

	if request.Method != expectation.Method {
		script.t.Errorf(
			"request method = %q, want %q for %s",
			request.Method,
			expectation.Method,
			expectation.Path,
		)
		http.Error(
			response,
			"unexpected request method",
			http.StatusInternalServerError,
		)
		return
	}

	if request.URL.Path != expectation.Path {
		script.t.Errorf(
			"request path = %q, want %q",
			request.URL.Path,
			expectation.Path,
		)
		http.Error(
			response,
			"unexpected request path",
			http.StatusInternalServerError,
		)
		return
	}

	if request.Method == http.MethodPost ||
		request.Method == http.MethodPatch {
		if contentType := request.Header.Get("Content-Type"); contentType != "application/json" {
			script.t.Errorf(
				"Content-Type header = %q, want %q",
				contentType,
				"application/json",
			)
		}
	}

	expectation.Handler(script.t, response, request)
}

func (script *testGitHubScript) assertComplete() {
	script.t.Helper()

	script.mu.Lock()
	defer script.mu.Unlock()

	if script.next != len(script.expectations) {
		script.t.Errorf(
			"GitHub request count = %d, want %d",
			script.next,
			len(script.expectations),
		)
	}
}

func assertPublicationHeaders(
	t *testing.T,
	request *http.Request,
) {
	t.Helper()

	if accept := request.Header.Get("Accept"); accept != "application/vnd.github+json" {
		t.Errorf(
			"Accept header = %q, want %q",
			accept,
			"application/vnd.github+json",
		)
	}

	if version := request.Header.Get("X-GitHub-Api-Version"); version != "2026-03-10" {
		t.Errorf(
			"X-GitHub-Api-Version header = %q, want %q",
			version,
			"2026-03-10",
		)
	}

	if userAgent := request.Header.Get("User-Agent"); userAgent != "Joshternet-Joshbot" {
		t.Errorf(
			"User-Agent header = %q, want %q",
			userAgent,
			"Joshternet-Joshbot",
		)
	}

	if request.Header.Get("Authorization") != "Bearer "+testToken {
		t.Error("Authorization header is missing or incorrect")
	}
}

func publicationReadExpectations(
	currentEntries []testTreeEntry,
) []testRequestExpectation {
	return []testRequestExpectation{
		{
			Method: http.MethodGet,
			Path:   "/repos/joshternet/index-data/git/ref/heads/main",
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
							"sha":  testHeadSHA,
						},
					},
				)
			},
		},
		{
			Method: http.MethodGet,
			Path:   "/repos/joshternet/index-data/git/commits/head-sha",
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
						"sha": testHeadSHA,
						"tree": map[string]any{
							"sha": testCurrentTreeSHA,
						},
					},
				)
			},
		},
		{
			Method: http.MethodGet,
			Path:   "/repos/joshternet/index-data/git/trees/current-tree",
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
						Tree:      currentEntries,
						Truncated: false,
					},
				)
			},
		},
		{
			Method: http.MethodGet,
			Path:   "/repos/joshternet/index-data/git/blobs/sentinel-blob",
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
						"encoding": "base64",
						"content": base64.StdEncoding.EncodeToString(
							[]byte(testSentinelData),
						),
						"size": len(testSentinelData),
					},
				)
			},
		},
	}
}

func expectBlobCreation(
	logicalPath string,
	data []byte,
	blobSHA string,
) testRequestExpectation {
	return testRequestExpectation{
		Method: http.MethodPost,
		Path:   "/repos/joshternet/index-data/git/blobs",
		Handler: func(
			t *testing.T,
			response http.ResponseWriter,
			request *http.Request,
		) {
			body, ok := readTestRequestBody(t, request)
			if !ok {
				http.Error(
					response,
					"invalid request",
					http.StatusBadRequest,
				)
				return
			}

			var raw map[string]json.RawMessage
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Errorf(
					"decode blob request for %q: %v",
					logicalPath,
					err,
				)
				http.Error(
					response,
					"invalid request",
					http.StatusBadRequest,
				)
				return
			}

			assertExactJSONKeys(
				t,
				raw,
				[]string{"content", "encoding"},
				"blob request",
			)

			var blobRequest testBlobRequest
			if err := json.Unmarshal(body, &blobRequest); err != nil {
				t.Errorf(
					"decode typed blob request for %q: %v",
					logicalPath,
					err,
				)
				http.Error(
					response,
					"invalid request",
					http.StatusBadRequest,
				)
				return
			}

			expected := testBlobRequest{
				Content:  base64.StdEncoding.EncodeToString(data),
				Encoding: "base64",
			}
			if blobRequest != expected {
				t.Errorf(
					"blob request for %q = %#v, want %#v",
					logicalPath,
					blobRequest,
					expected,
				)
			}

			writeTestJSON(
				t,
				response,
				http.StatusCreated,
				map[string]any{
					"sha": blobSHA,
				},
			)
		},
	}
}

func expectTreeCreation(
	expectedEntries []testTreeEntry,
	treeSHA string,
) testRequestExpectation {
	return testRequestExpectation{
		Method: http.MethodPost,
		Path:   "/repos/joshternet/index-data/git/trees",
		Handler: func(
			t *testing.T,
			response http.ResponseWriter,
			request *http.Request,
		) {
			body, ok := readTestRequestBody(t, request)
			if !ok {
				http.Error(
					response,
					"invalid request",
					http.StatusBadRequest,
				)
				return
			}

			var raw map[string]json.RawMessage
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Errorf("decode tree request: %v", err)
				http.Error(
					response,
					"invalid request",
					http.StatusBadRequest,
				)
				return
			}

			if _, found := raw["base_tree"]; found {
				t.Error("tree request contains base_tree, want exact tree without base_tree")
			}

			assertExactJSONKeys(
				t,
				raw,
				[]string{"tree"},
				"tree request",
			)

			var treeRequest testTreeRequest
			if err := json.Unmarshal(body, &treeRequest); err != nil {
				t.Errorf("decode typed tree request: %v", err)
				http.Error(
					response,
					"invalid request",
					http.StatusBadRequest,
				)
				return
			}

			if !reflect.DeepEqual(treeRequest.Tree, expectedEntries) {
				t.Errorf(
					"tree entries = %#v, want %#v",
					treeRequest.Tree,
					expectedEntries,
				)
			}

			for _, entry := range treeRequest.Tree {
				if entry.Mode != "100644" {
					t.Errorf(
						"tree entry %q mode = %q, want %q",
						entry.Path,
						entry.Mode,
						"100644",
					)
				}
				if entry.Type != "blob" {
					t.Errorf(
						"tree entry %q type = %q, want %q",
						entry.Path,
						entry.Type,
						"blob",
					)
				}
			}

			writeTestJSON(
				t,
				response,
				http.StatusCreated,
				map[string]any{
					"sha": treeSHA,
				},
			)
		},
	}
}

func expectCommitCreation(
	treeSHA string,
	commitSHA string,
) testRequestExpectation {
	return testRequestExpectation{
		Method: http.MethodPost,
		Path:   "/repos/joshternet/index-data/git/commits",
		Handler: func(
			t *testing.T,
			response http.ResponseWriter,
			request *http.Request,
		) {
			body, ok := readTestRequestBody(t, request)
			if !ok {
				http.Error(
					response,
					"invalid request",
					http.StatusBadRequest,
				)
				return
			}

			var raw map[string]json.RawMessage
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Errorf("decode commit request: %v", err)
				http.Error(
					response,
					"invalid request",
					http.StatusBadRequest,
				)
				return
			}

			assertExactJSONKeys(
				t,
				raw,
				[]string{"message", "parents", "tree"},
				"commit request",
			)

			var commitRequest testCommitRequest
			if err := json.Unmarshal(body, &commitRequest); err != nil {
				t.Errorf("decode typed commit request: %v", err)
				http.Error(
					response,
					"invalid request",
					http.StatusBadRequest,
				)
				return
			}

			expected := testCommitRequest{
				Message: "Publish Joshternet registry",
				Tree:    treeSHA,
				Parents: []string{testHeadSHA},
			}
			if !reflect.DeepEqual(commitRequest, expected) {
				t.Errorf(
					"commit request = %#v, want %#v",
					commitRequest,
					expected,
				)
			}

			writeTestJSON(
				t,
				response,
				http.StatusCreated,
				map[string]any{
					"sha": commitSHA,
				},
			)
		},
	}
}

func expectReferenceUpdate(
	commitSHA string,
) testRequestExpectation {
	return testRequestExpectation{
		Method: http.MethodPatch,
		Path:   "/repos/joshternet/index-data/git/refs/heads/main",
		Handler: func(
			t *testing.T,
			response http.ResponseWriter,
			request *http.Request,
		) {
			body, ok := readTestRequestBody(t, request)
			if !ok {
				http.Error(
					response,
					"invalid request",
					http.StatusBadRequest,
				)
				return
			}

			var raw map[string]json.RawMessage
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Errorf("decode reference update request: %v", err)
				http.Error(
					response,
					"invalid request",
					http.StatusBadRequest,
				)
				return
			}

			assertExactJSONKeys(
				t,
				raw,
				[]string{"force", "sha"},
				"reference update request",
			)

			if _, found := raw["force"]; !found {
				t.Error("reference update omitted force, want explicit force=false")
			}

			var updateRequest testReferenceUpdateRequest
			if err := json.Unmarshal(body, &updateRequest); err != nil {
				t.Errorf(
					"decode typed reference update request: %v",
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
			if updateRequest != expected {
				t.Errorf(
					"reference update request = %#v, want %#v",
					updateRequest,
					expected,
				)
			}

			writeTestJSON(
				t,
				response,
				http.StatusOK,
				map[string]any{
					"ref": "refs/heads/main",
					"object": map[string]any{
						"type": "commit",
						"sha":  commitSHA,
					},
				},
			)
		},
	}
}

type testTreeResponse struct {
	SHA       string          `json:"sha"`
	Tree      []testTreeEntry `json:"tree"`
	Truncated bool            `json:"truncated"`
}

type testTreeRequest struct {
	Tree []testTreeEntry `json:"tree"`
}

type testTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type testBlobRequest struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

type testCommitRequest struct {
	Message string   `json:"message"`
	Tree    string   `json:"tree"`
	Parents []string `json:"parents"`
}

type testReferenceUpdateRequest struct {
	SHA   string `json:"sha"`
	Force bool   `json:"force"`
}

func readTestRequestBody(
	t *testing.T,
	request *http.Request,
) ([]byte, bool) {
	t.Helper()

	const maximumTestRequestBody = 1 << 20

	body, err := io.ReadAll(
		io.LimitReader(
			request.Body,
			maximumTestRequestBody+1,
		),
	)
	if err != nil {
		t.Errorf("read request body: %v", err)
		return nil, false
	}
	if len(body) > maximumTestRequestBody {
		t.Errorf(
			"request body size = %d, maximum %d",
			len(body),
			maximumTestRequestBody,
		)
		return nil, false
	}

	return body, true
}

func assertExactJSONKeys(
	t *testing.T,
	object map[string]json.RawMessage,
	expected []string,
	description string,
) {
	t.Helper()

	if len(object) != len(expected) {
		t.Errorf(
			"%s field count = %d, want %d",
			description,
			len(object),
			len(expected),
		)
	}

	for _, key := range expected {
		if _, found := object[key]; !found {
			t.Errorf(
				"%s missing required field %q",
				description,
				key,
			)
		}
	}
}

func writeTestJSON(
	t *testing.T,
	response http.ResponseWriter,
	status int,
	value any,
) {
	t.Helper()

	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)

	if err := json.NewEncoder(response).Encode(value); err != nil {
		t.Errorf("encode fake GitHub response: %v", err)
	}
}

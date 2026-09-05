// Package githubpublish publishes exact JoshBot registry snapshots through the
// GitHub Git Database API.
package githubpublish

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/joshternet/joshbot/internal/publicdata"
)

const (
	githubAPIBase    = "https://api.github.com"
	githubAPIVersion = "2026-03-10"
	githubAccept     = "application/vnd.github+json"
	githubUserAgent  = "Joshternet-Joshbot"

	publicationCommitMessage = "Publish Joshternet registry"
	sentinelPath             = ".joshbot-registry-target"
	sentinelContents         = "joshbot-registry-v1\n"

	maxPublicationFiles      = 4_097
	maxPublicationFileBytes  = 8 * 1024 * 1024
	maxPublicationTotalBytes = 32 * 1024 * 1024
	maxGitHubResponseBytes   = 4 * 1024 * 1024
	maxGitHubErrorBytes      = 4 * 1024
	githubRequestTimeout     = 30 * time.Second
)

var (
	// ErrInvalidConfig means the publication target configuration is unsafe.
	ErrInvalidConfig = errors.New(
		"githubpublish: configuration is invalid",
	)

	// ErrInvalidToken means the GitHub token is empty or unsafe for an HTTP
	// Authorization header.
	ErrInvalidToken = errors.New(
		"githubpublish: token is invalid",
	)

	// ErrInvalidPublisher means Publish was called with an unusable Publisher.
	ErrInvalidPublisher = errors.New(
		"githubpublish: publisher is invalid",
	)

	// ErrInvalidSnapshot means the supplied files are not an exact Phase 7
	// public registry snapshot.
	ErrInvalidSnapshot = errors.New(
		"githubpublish: snapshot is invalid",
	)

	// ErrUnsafeTarget means the configured branch could not be proven to be a
	// JoshBot-managed publication target.
	ErrUnsafeTarget = errors.New(
		"githubpublish: publication target is unsafe",
	)

	// ErrRedirect means GitHub attempted to redirect an authenticated request.
	ErrRedirect = errors.New(
		"githubpublish: authenticated redirect rejected",
	)

	// ErrInvalidGitHubResponse means GitHub returned an unusable successful
	// response.
	ErrInvalidGitHubResponse = errors.New(
		"githubpublish: GitHub response is invalid",
	)

	// ErrGitHubResponseTooLarge means a successful GitHub response exceeded
	// the fixed response-size limit.
	ErrGitHubResponseTooLarge = errors.New(
		"githubpublish: GitHub response is too large",
	)

	// ErrPublicationConflict means the publication branch moved to a different
	// snapshot before the non-forced reference update completed.
	ErrPublicationConflict = errors.New(
		"githubpublish: publication conflict",
	)
)

// Config identifies the dedicated GitHub repository branch controlled by the
// JoshBot publisher.
type Config struct {
	Owner      string
	Repository string
	Branch     string
}

// Result describes the effective commit at the end of a publication attempt.
type Result struct {
	Changed   bool
	CommitSHA string
}

// Publisher publishes complete publicdata snapshots to one configured GitHub
// branch.
type Publisher struct {
	config  Config
	token   string
	apiBase *url.URL
	client  *http.Client
}

// New constructs a Publisher that can communicate only with the official
// GitHub API endpoint.
func New(
	config Config,
	token string,
) (*Publisher, error) {
	return newPublisher(
		config,
		token,
		githubAPIBase,
	)
}

// newPublisher is the private HTTP test seam. Production callers use New and
// cannot configure an alternate API host.
func newPublisher(
	config Config,
	token string,
	apiBaseValue string,
) (*Publisher, error) {
	if !validConfig(config) {
		return nil, ErrInvalidConfig
	}
	if !validToken(token) {
		return nil, ErrInvalidToken
	}

	apiBase, err := url.Parse(apiBaseValue)
	if err != nil ||
		(apiBase.Scheme != "https" &&
			apiBase.Scheme != "http") ||
		apiBase.Host == "" ||
		apiBase.User != nil ||
		apiBase.RawQuery != "" ||
		apiBase.Fragment != "" ||
		(apiBase.Path != "" && apiBase.Path != "/") {
		return nil, ErrInvalidConfig
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil

	client := &http.Client{
		Transport: transport,
		Timeout:   githubRequestTimeout,
		CheckRedirect: func(
			_ *http.Request,
			_ []*http.Request,
		) error {
			return ErrRedirect
		},
	}

	return &Publisher{
		config:  config,
		token:   token,
		apiBase: apiBase,
		client:  client,
	}, nil
}

// Publish creates an exact Git tree from files and advances the configured
// branch with a non-forced reference update.
//
// The existing branch sentinel is verified before any publication blobs are
// created. The reference update is the only operation that makes the new
// snapshot public.
func (publisher *Publisher) Publish(
	ctx context.Context,
	files []publicdata.File,
) (Result, error) {
	if ctx == nil {
		return Result{}, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if !publisher.valid() {
		return Result{}, ErrInvalidPublisher
	}

	orderedFiles, err := prepareSnapshot(files)
	if err != nil {
		return Result{}, err
	}

	headSHA, err := publisher.currentHead(ctx)
	if err != nil {
		return Result{}, err
	}

	currentTreeSHA, err := publisher.commitTree(
		ctx,
		headSHA,
	)
	if err != nil {
		return Result{}, err
	}

	sentinelSHA, err := publisher.verifySentinel(
		ctx,
		currentTreeSHA,
	)
	if err != nil {
		return Result{}, err
	}

	treeEntries := make(
		[]treeEntry,
		0,
		len(orderedFiles)+1,
	)
	treeEntries = append(
		treeEntries,
		treeEntry{
			Path: sentinelPath,
			Mode: "100644",
			Type: "blob",
			SHA:  sentinelSHA,
		},
	)

	for _, file := range orderedFiles {
		blobSHA, err := publisher.createBlob(
			ctx,
			file.Data,
		)
		if err != nil {
			return Result{}, err
		}

		treeEntries = append(
			treeEntries,
			treeEntry{
				Path: file.Path,
				Mode: "100644",
				Type: "blob",
				SHA:  blobSHA,
			},
		)
	}

	newTreeSHA, err := publisher.createTree(
		ctx,
		treeEntries,
	)
	if err != nil {
		return Result{}, err
	}

	if newTreeSHA == currentTreeSHA {
		return Result{
			Changed:   false,
			CommitSHA: headSHA,
		}, nil
	}

	commitSHA, err := publisher.createCommit(
		ctx,
		newTreeSHA,
		headSHA,
	)
	if err != nil {
		return Result{}, err
	}

	updateErr := publisher.updateReference(
		ctx,
		commitSHA,
	)
	if updateErr != nil {
		if !isReferenceConflict(updateErr) {
			return Result{}, updateErr
		}

		return publisher.reconcileReferenceConflict(
			ctx,
			newTreeSHA,
		)
	}

	return Result{
		Changed:   true,
		CommitSHA: commitSHA,
	}, nil
}

func (publisher *Publisher) valid() bool {
	return publisher != nil &&
		validConfig(publisher.config) &&
		validToken(publisher.token) &&
		publisher.apiBase != nil &&
		publisher.client != nil
}

func (publisher *Publisher) currentHead(
	ctx context.Context,
) (string, error) {
	var response referenceResponse

	err := publisher.requestJSON(
		ctx,
		"get branch reference",
		http.MethodGet,
		publisher.repositoryPath()+
			"/git/ref/heads/"+
			url.PathEscape(publisher.config.Branch),
		nil,
		http.StatusOK,
		&response,
	)
	if err != nil {
		return "", err
	}

	if response.Object.Type != "commit" ||
		response.Object.SHA == "" {
		return "", ErrInvalidGitHubResponse
	}

	return response.Object.SHA, nil
}

func (publisher *Publisher) commitTree(
	ctx context.Context,
	commitSHA string,
) (string, error) {
	var response commitResponse

	err := publisher.requestJSON(
		ctx,
		"get current commit",
		http.MethodGet,
		publisher.repositoryPath()+
			"/git/commits/"+
			url.PathEscape(commitSHA),
		nil,
		http.StatusOK,
		&response,
	)
	if err != nil {
		return "", err
	}

	if response.SHA == "" ||
		response.Tree.SHA == "" {
		return "", ErrInvalidGitHubResponse
	}

	return response.Tree.SHA, nil
}

func (publisher *Publisher) verifySentinel(
	ctx context.Context,
	treeSHA string,
) (string, error) {
	var response treeResponse

	err := publisher.requestJSON(
		ctx,
		"get current root tree",
		http.MethodGet,
		publisher.repositoryPath()+
			"/git/trees/"+
			url.PathEscape(treeSHA),
		nil,
		http.StatusOK,
		&response,
	)
	if err != nil {
		return "", err
	}

	if response.SHA == "" || response.Truncated {
		return "", ErrUnsafeTarget
	}

	sentinelSHA := ""
	for _, entry := range response.Tree {
		if entry.Path != sentinelPath {
			continue
		}

		if sentinelSHA != "" ||
			entry.Mode != "100644" ||
			entry.Type != "blob" ||
			entry.SHA == "" {
			return "", ErrUnsafeTarget
		}

		sentinelSHA = entry.SHA
	}

	if sentinelSHA == "" {
		return "", ErrUnsafeTarget
	}

	var blob blobResponse
	err = publisher.requestJSON(
		ctx,
		"get publication sentinel",
		http.MethodGet,
		publisher.repositoryPath()+
			"/git/blobs/"+
			url.PathEscape(sentinelSHA),
		nil,
		http.StatusOK,
		&blob,
	)
	if err != nil {
		return "", err
	}

	if blob.Encoding != "base64" ||
		blob.Size != len(sentinelContents) {
		return "", ErrUnsafeTarget
	}

	decoded, err := base64.StdEncoding.DecodeString(
		blob.Content,
	)
	if err != nil ||
		!bytes.Equal(
			decoded,
			[]byte(sentinelContents),
		) {
		return "", ErrUnsafeTarget
	}

	return sentinelSHA, nil
}

func (publisher *Publisher) createBlob(
	ctx context.Context,
	data []byte,
) (string, error) {
	request := createBlobRequest{
		Content: base64.StdEncoding.EncodeToString(
			data,
		),
		Encoding: "base64",
	}

	var response createObjectResponse
	err := publisher.requestJSON(
		ctx,
		"create registry blob",
		http.MethodPost,
		publisher.repositoryPath()+"/git/blobs",
		request,
		http.StatusCreated,
		&response,
	)
	if err != nil {
		return "", err
	}

	if response.SHA == "" {
		return "", ErrInvalidGitHubResponse
	}

	return response.SHA, nil
}

func (publisher *Publisher) createTree(
	ctx context.Context,
	entries []treeEntry,
) (string, error) {
	request := createTreeRequest{
		Tree: entries,
	}

	var response createObjectResponse
	err := publisher.requestJSON(
		ctx,
		"create exact registry tree",
		http.MethodPost,
		publisher.repositoryPath()+"/git/trees",
		request,
		http.StatusCreated,
		&response,
	)
	if err != nil {
		return "", err
	}

	if response.SHA == "" {
		return "", ErrInvalidGitHubResponse
	}

	return response.SHA, nil
}

func (publisher *Publisher) createCommit(
	ctx context.Context,
	treeSHA string,
	parentSHA string,
) (string, error) {
	request := createCommitRequest{
		Message: publicationCommitMessage,
		Tree:    treeSHA,
		Parents: []string{parentSHA},
	}

	var response createObjectResponse
	err := publisher.requestJSON(
		ctx,
		"create registry commit",
		http.MethodPost,
		publisher.repositoryPath()+"/git/commits",
		request,
		http.StatusCreated,
		&response,
	)
	if err != nil {
		return "", err
	}

	if response.SHA == "" {
		return "", ErrInvalidGitHubResponse
	}

	return response.SHA, nil
}

func (publisher *Publisher) updateReference(
	ctx context.Context,
	commitSHA string,
) error {
	request := updateReferenceRequest{
		SHA:   commitSHA,
		Force: false,
	}

	var response referenceResponse
	err := publisher.requestJSON(
		ctx,
		"update publication reference",
		http.MethodPatch,
		publisher.repositoryPath()+
			"/git/refs/heads/"+
			url.PathEscape(publisher.config.Branch),
		request,
		http.StatusOK,
		&response,
	)
	if err != nil {
		return err
	}

	if response.Object.Type != "commit" ||
		response.Object.SHA != commitSHA {
		return ErrInvalidGitHubResponse
	}

	return nil
}

func isReferenceConflict(err error) bool {
	var statusError *githubStatusError
	if !errors.As(err, &statusError) {
		return false
	}

	return statusError.statusCode == http.StatusConflict ||
		statusError.statusCode ==
			http.StatusUnprocessableEntity
}

func (publisher *Publisher) reconcileReferenceConflict(
	ctx context.Context,
	desiredTreeSHA string,
) (Result, error) {
	currentHeadSHA, err := publisher.currentHead(ctx)
	if err != nil {
		return Result{}, err
	}

	currentTreeSHA, err := publisher.commitTree(
		ctx,
		currentHeadSHA,
	)
	if err != nil {
		return Result{}, err
	}

	if currentTreeSHA != desiredTreeSHA {
		return Result{}, ErrPublicationConflict
	}

	return Result{
		Changed:   false,
		CommitSHA: currentHeadSHA,
	}, nil
}

func (publisher *Publisher) repositoryPath() string {
	return "/repos/" +
		url.PathEscape(publisher.config.Owner) +
		"/" +
		url.PathEscape(publisher.config.Repository)
}

func (publisher *Publisher) requestJSON(
	ctx context.Context,
	operation string,
	method string,
	path string,
	input any,
	expectedStatus int,
	output any,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var body io.Reader = http.NoBody
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf(
				"githubpublish: %s: encode request: %w",
				operation,
				err,
			)
		}

		body = bytes.NewReader(encoded)
	}

	endpoint := *publisher.apiBase
	endpoint.Path = strings.TrimRight(
		endpoint.Path,
		"/",
	) + path
	endpoint.RawPath = ""
	endpoint.RawQuery = ""
	endpoint.Fragment = ""

	request, err := http.NewRequestWithContext(
		ctx,
		method,
		endpoint.String(),
		body,
	)
	if err != nil {
		return fmt.Errorf(
			"githubpublish: %s: create request: %w",
			operation,
			err,
		)
	}

	request.Header.Set("Accept", githubAccept)
	request.Header.Set(
		"Authorization",
		"Bearer "+publisher.token,
	)
	request.Header.Set(
		"X-GitHub-Api-Version",
		githubAPIVersion,
	)
	request.Header.Set("User-Agent", githubUserAgent)

	if input != nil {
		request.Header.Set(
			"Content-Type",
			"application/json",
		)
	}

	response, err := publisher.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		return fmt.Errorf(
			"githubpublish: %s: request failed: %w",
			operation,
			err,
		)
	}
	defer response.Body.Close()

	if response.StatusCode != expectedStatus {
		_, _ = io.Copy(
			io.Discard,
			io.LimitReader(
				response.Body,
				maxGitHubErrorBytes,
			),
		)

		return &githubStatusError{
			operation:  operation,
			statusCode: response.StatusCode,
		}
	}

	responseBody, err := io.ReadAll(
		io.LimitReader(
			response.Body,
			maxGitHubResponseBytes+1,
		),
	)
	if err != nil {
		return fmt.Errorf(
			"githubpublish: %s: read response: %w",
			operation,
			err,
		)
	}

	if len(responseBody) > maxGitHubResponseBytes {
		return fmt.Errorf(
			"%w: %s",
			ErrGitHubResponseTooLarge,
			operation,
		)
	}

	if err := json.Unmarshal(
		responseBody,
		output,
	); err != nil {
		return fmt.Errorf(
			"%w: %s",
			ErrInvalidGitHubResponse,
			operation,
		)
	}

	return nil
}

type githubStatusError struct {
	operation  string
	statusCode int
}

func (statusError *githubStatusError) Error() string {
	return fmt.Sprintf(
		"githubpublish: %s: GitHub returned HTTP %d",
		statusError.operation,
		statusError.statusCode,
	)
}

type referenceResponse struct {
	Ref    string `json:"ref"`
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"object"`
}

type commitResponse struct {
	SHA  string `json:"sha"`
	Tree struct {
		SHA string `json:"sha"`
	} `json:"tree"`
}

type treeResponse struct {
	SHA       string      `json:"sha"`
	Tree      []treeEntry `json:"tree"`
	Truncated bool        `json:"truncated"`
}

type blobResponse struct {
	SHA      string `json:"sha"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	Size     int    `json:"size"`
}

type createObjectResponse struct {
	SHA string `json:"sha"`
}

type createBlobRequest struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

type createTreeRequest struct {
	Tree []treeEntry `json:"tree"`
}

type treeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type createCommitRequest struct {
	Message string   `json:"message"`
	Tree    string   `json:"tree"`
	Parents []string `json:"parents"`
}

type updateReferenceRequest struct {
	SHA   string `json:"sha"`
	Force bool   `json:"force"`
}

func prepareSnapshot(
	files []publicdata.File,
) ([]publicdata.File, error) {
	if len(files) == 0 ||
		len(files) > maxPublicationFiles {
		return nil, ErrInvalidSnapshot
	}

	ordered := append(
		[]publicdata.File(nil),
		files...,
	)
	sort.Slice(
		ordered,
		func(left int, right int) bool {
			return ordered[left].Path <
				ordered[right].Path
		},
	)

	hasRegistry := false
	var totalBytes int64
	previousPath := ""

	for _, file := range ordered {
		if !validPublicationPath(file.Path) ||
			file.Path == previousPath ||
			len(file.Data) > maxPublicationFileBytes {
			return nil, ErrInvalidSnapshot
		}

		totalBytes += int64(len(file.Data))
		if totalBytes > maxPublicationTotalBytes {
			return nil, ErrInvalidSnapshot
		}

		if file.Path == "registry.json" {
			hasRegistry = true
		}

		previousPath = file.Path
	}

	if !hasRegistry {
		return nil, ErrInvalidSnapshot
	}

	return ordered, nil
}

func validPublicationPath(path string) bool {
	if path == "registry.json" {
		return true
	}

	parts := strings.Split(path, "/")
	if len(parts) != 3 ||
		parts[0] != "nodes" ||
		len(parts[1]) != 2 ||
		!lowerHex(parts[1]) ||
		!strings.HasSuffix(parts[2], ".json") {
		return false
	}

	hash := strings.TrimSuffix(
		parts[2],
		".json",
	)
	return len(hash) == 64 &&
		lowerHex(hash) &&
		strings.HasPrefix(hash, parts[1])
}

func lowerHex(value string) bool {
	if value == "" {
		return false
	}

	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}

	return true
}

func validConfig(config Config) bool {
	return validPathComponent(config.Owner) &&
		validPathComponent(config.Repository) &&
		validBranch(config.Branch)
}

func validPathComponent(value string) bool {
	return value != "" &&
		strings.TrimSpace(value) == value &&
		value != "." &&
		value != ".." &&
		!strings.ContainsAny(value, `/\`) &&
		!hasControlCharacter(value)
}

func validBranch(value string) bool {
	return validPathComponent(value) &&
		!strings.Contains(value, "..") &&
		!strings.HasPrefix(value, "refs/")
}

func validToken(token string) bool {
	return token != "" &&
		strings.TrimSpace(token) == token &&
		!hasControlCharacter(token)
}

func hasControlCharacter(value string) bool {
	for _, character := range value {
		if character < 0x20 ||
			character == 0x7f {
			return true
		}
	}

	return false
}

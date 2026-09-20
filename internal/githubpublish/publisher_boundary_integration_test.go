//lint:file-ignore SA1012 Intentional negative tests verify defensive nil-context rejection; production callers must never pass a nil context.
package githubpublish

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/publicdata"
)

const githubPublishBoundaryIntegrationToken = "integration-publisher-token"

var errGitHubPublishBoundaryIntegrationRead = errors.New(
	"integration GitHub response read failure",
)

type githubPublishBoundaryIntegrationRoundTripFunc func(
	*http.Request,
) (*http.Response, error)

func (roundTrip githubPublishBoundaryIntegrationRoundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTrip(request)
}

type githubPublishBoundaryIntegrationFailingBody struct{}

func (*githubPublishBoundaryIntegrationFailingBody) Read(
	[]byte,
) (int, error) {
	return 0, errGitHubPublishBoundaryIntegrationRead
}

func (*githubPublishBoundaryIntegrationFailingBody) Close() error {
	return nil
}

func TestGitHubPublishBoundaryIntegrationConstructors(t *testing.T) {
	publisher, err := New(
		githubPublishBoundaryIntegrationConfig(),
		githubPublishBoundaryIntegrationToken,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if publisher == nil || !publisher.valid() {
		t.Fatalf(
			"New() publisher = %#v, want valid publisher",
			publisher,
		)
	}
	if publisher.apiBase == nil ||
		publisher.apiBase.String() != githubAPIBase {
		t.Errorf(
			"New() api base = %v, want %s",
			publisher.apiBase,
			githubAPIBase,
		)
	}

	invalidConfigs := []Config{
		{
			Repository: "index-data",
			Branch:     "main",
		},
		{
			Owner:  "joshternet",
			Branch: "main",
		},
		{
			Owner:      "joshternet",
			Repository: "index-data",
		},
		{
			Owner:      "../joshternet",
			Repository: "index-data",
			Branch:     "main",
		},
		{
			Owner:      "joshternet",
			Repository: "index-data",
			Branch:     "refs/main",
		},
	}

	for _, config := range invalidConfigs {
		got, err := New(
			config,
			githubPublishBoundaryIntegrationToken,
		)
		if !errors.Is(err, ErrInvalidConfig) ||
			got != nil {
			t.Errorf(
				"New(%#v) = %#v, %v, want nil, %v",
				config,
				got,
				err,
				ErrInvalidConfig,
			)
		}
	}

	invalidTokens := []string{
		"",
		" token",
		"token ",
		"token\nvalue",
	}

	for _, token := range invalidTokens {
		got, err := New(
			githubPublishBoundaryIntegrationConfig(),
			token,
		)
		if !errors.Is(err, ErrInvalidToken) ||
			got != nil {
			t.Errorf(
				"New(token %q) = %#v, %v, want nil, %v",
				token,
				got,
				err,
				ErrInvalidToken,
			)
		}
	}

	invalidBases := []string{
		"://bad",
		"ftp://example.com",
		"http:///missing-host",
		"http://user@example.com",
		"http://example.com?query=1",
		"http://example.com#fragment",
		"http://example.com/path",
	}

	for _, apiBase := range invalidBases {
		got, err := newPublisher(
			githubPublishBoundaryIntegrationConfig(),
			githubPublishBoundaryIntegrationToken,
			apiBase,
		)
		if !errors.Is(err, ErrInvalidConfig) ||
			got != nil {
			t.Errorf(
				"newPublisher(%q) = %#v, %v, want nil, %v",
				apiBase,
				got,
				err,
				ErrInvalidConfig,
			)
		}
	}
}

func TestGitHubPublishBoundaryIntegrationPublishPreflight(
	t *testing.T,
) {
	publisher, err := New(
		githubPublishBoundaryIntegrationConfig(),
		githubPublishBoundaryIntegrationToken,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := publisher.Publish(
		nil,
		githubPublishBoundaryIntegrationSnapshot(),
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Publish(nil) error = %v, want context.Canceled",
			err,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if _, err := publisher.Publish(
		ctx,
		githubPublishBoundaryIntegrationSnapshot(),
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Publish(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	var missing *Publisher
	if _, err := missing.Publish(
		context.Background(),
		githubPublishBoundaryIntegrationSnapshot(),
	); !errors.Is(err, ErrInvalidPublisher) {
		t.Errorf(
			"nil Publisher.Publish() error = %v, want %v",
			err,
			ErrInvalidPublisher,
		)
	}

	invalid := *publisher
	invalid.client = nil
	if _, err := invalid.Publish(
		context.Background(),
		githubPublishBoundaryIntegrationSnapshot(),
	); !errors.Is(err, ErrInvalidPublisher) {
		t.Errorf(
			"invalid Publisher.Publish() error = %v, want %v",
			err,
			ErrInvalidPublisher,
		)
	}

	invalidSnapshots := [][]publicdata.File{
		nil,
		{
			{
				Path: "nodes/ab/" +
					strings.Repeat("0", 64) +
					".json",
				Data: []byte("{}\n"),
			},
		},
		{
			{
				Path: "registry.json",
				Data: []byte("first\n"),
			},
			{
				Path: "registry.json",
				Data: []byte("second\n"),
			},
		},
		{
			{
				Path: "../registry.json",
				Data: []byte("{}\n"),
			},
		},
		{
			{
				Path: "registry.json",
				Data: make(
					[]byte,
					maxPublicationFileBytes+1,
				),
			},
		},
	}

	for index, files := range invalidSnapshots {
		ordered, err := prepareSnapshot(files)
		if !errors.Is(err, ErrInvalidSnapshot) ||
			ordered != nil {
			t.Errorf(
				"prepareSnapshot case %d = %#v, %v, want nil, %v",
				index,
				ordered,
				err,
				ErrInvalidSnapshot,
			)
		}
	}

	nodePath :=
		githubPublishBoundaryIntegrationNodePath("ab")

	ordered, err := prepareSnapshot(
		[]publicdata.File{
			{
				Path: "registry.json",
				Data: []byte("registry\n"),
			},
			{
				Path: nodePath,
				Data: []byte("node\n"),
			},
		},
	)
	if err != nil {
		t.Fatalf(
			"prepareSnapshot(valid) error = %v",
			err,
		)
	}

	if len(ordered) != 2 ||
		ordered[0].Path != nodePath ||
		ordered[1].Path != "registry.json" {
		t.Errorf(
			"prepareSnapshot(valid) = %#v",
			ordered,
		)
	}
}

func TestGitHubPublishBoundaryIntegrationRequestFailures(
	t *testing.T,
) {
	t.Run("already canceled", func(t *testing.T) {
		publisher :=
			githubPublishBoundaryIntegrationTestPublisher(
				t,
				nil,
			)

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

	t.Run("request encoding", func(t *testing.T) {
		publisher :=
			githubPublishBoundaryIntegrationTestPublisher(
				t,
				nil,
			)

		err := publisher.requestJSON(
			context.Background(),
			"encode request",
			http.MethodPost,
			"/test",
			make(chan struct{}),
			http.StatusOK,
			&referenceResponse{},
		)
		if err == nil ||
			!strings.Contains(
				err.Error(),
				"encode request",
			) {
			t.Errorf(
				"requestJSON() error = %v, want encoding failure",
				err,
			)
		}
	})

	t.Run("invalid method", func(t *testing.T) {
		publisher :=
			githubPublishBoundaryIntegrationTestPublisher(
				t,
				nil,
			)

		err := publisher.requestJSON(
			context.Background(),
			"create request",
			"\n",
			"/test",
			nil,
			http.StatusOK,
			&referenceResponse{},
		)
		if err == nil ||
			!strings.Contains(
				err.Error(),
				"create request",
			) {
			t.Errorf(
				"requestJSON() error = %v, want request creation failure",
				err,
			)
		}
	})

	t.Run("transport cancellation", func(t *testing.T) {
		publisher :=
			githubPublishBoundaryIntegrationTestPublisher(
				t,
				nil,
			)

		ctx, cancel := context.WithCancel(
			context.Background(),
		)

		publisher.client = &http.Client{
			Transport: githubPublishBoundaryIntegrationRoundTripFunc(
				func(
					*http.Request,
				) (*http.Response, error) {
					cancel()

					return nil,
						errors.New(
							"integration transport failure",
						)
				},
			),
		}

		err := publisher.requestJSON(
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

	t.Run("transport failure", func(t *testing.T) {
		publisher :=
			githubPublishBoundaryIntegrationTestPublisher(
				t,
				nil,
			)

		transportErr := errors.New(
			"integration transport failure",
		)

		publisher.client = &http.Client{
			Transport: githubPublishBoundaryIntegrationRoundTripFunc(
				func(
					*http.Request,
				) (*http.Response, error) {
					return nil,
						transportErr
				},
			),
		}

		err := publisher.requestJSON(
			context.Background(),
			"transport failure",
			http.MethodGet,
			"/test",
			nil,
			http.StatusOK,
			&referenceResponse{},
		)
		if !errors.Is(err, transportErr) {
			t.Errorf(
				"requestJSON() error = %v, want %v",
				err,
				transportErr,
			)
		}
	})

	t.Run("response read failure", func(t *testing.T) {
		publisher :=
			githubPublishBoundaryIntegrationTestPublisher(
				t,
				nil,
			)

		publisher.client = &http.Client{
			Transport: githubPublishBoundaryIntegrationRoundTripFunc(
				func(
					request *http.Request,
				) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header: make(
							http.Header,
						),
						Body:    &githubPublishBoundaryIntegrationFailingBody{},
						Request: request,
					}, nil
				},
			),
		}

		err := publisher.requestJSON(
			context.Background(),
			"read failure",
			http.MethodGet,
			"/test",
			nil,
			http.StatusOK,
			&referenceResponse{},
		)
		if !errors.Is(
			err,
			errGitHubPublishBoundaryIntegrationRead,
		) {
			t.Errorf(
				"requestJSON() error = %v, want %v",
				err,
				errGitHubPublishBoundaryIntegrationRead,
			)
		}
	})

	t.Run("status error", func(t *testing.T) {
		publisher :=
			githubPublishBoundaryIntegrationTestPublisher(
				t,
				func(
					response http.ResponseWriter,
					request *http.Request,
				) {
					if request.Header.Get(
						"Authorization",
					) !=
						"Bearer "+
							githubPublishBoundaryIntegrationToken {
						t.Errorf(
							"Authorization = %q",
							request.Header.Get(
								"Authorization",
							),
						)
					}

					response.WriteHeader(
						http.StatusConflict,
					)
					_, _ = io.WriteString(
						response,
						strings.Repeat(
							"x",
							maxGitHubErrorBytes+100,
						),
					)
				},
			)

		err := publisher.requestJSON(
			context.Background(),
			"status failure",
			http.MethodGet,
			"/test",
			nil,
			http.StatusOK,
			&referenceResponse{},
		)

		var statusErr *githubStatusError
		if !errors.As(err, &statusErr) ||
			statusErr.statusCode !=
				http.StatusConflict {
			t.Fatalf(
				"requestJSON() error = %v, want HTTP 409 status error",
				err,
			)
		}

		if !isReferenceConflict(err) {
			t.Error(
				"isReferenceConflict(409) = false, want true",
			)
		}

		if !strings.Contains(
			err.Error(),
			"HTTP 409",
		) ||
			strings.Contains(
				err.Error(),
				githubPublishBoundaryIntegrationToken,
			) {
			t.Errorf(
				"status error string = %q",
				err.Error(),
			)
		}

		if isReferenceConflict(
			errors.New("ordinary failure"),
		) {
			t.Error(
				"isReferenceConflict(ordinary error) = true",
			)
		}
	})

	t.Run("malformed success response", func(t *testing.T) {
		publisher :=
			githubPublishBoundaryIntegrationTestPublisher(
				t,
				func(
					response http.ResponseWriter,
					_ *http.Request,
				) {
					response.WriteHeader(
						http.StatusOK,
					)
					_, _ = io.WriteString(
						response,
						"{",
					)
				},
			)

		err := publisher.requestJSON(
			context.Background(),
			"malformed response",
			http.MethodGet,
			"/test",
			nil,
			http.StatusOK,
			&referenceResponse{},
		)
		if !errors.Is(
			err,
			ErrInvalidGitHubResponse,
		) {
			t.Errorf(
				"requestJSON() error = %v, want %v",
				err,
				ErrInvalidGitHubResponse,
			)
		}
	})

	t.Run("oversized success response", func(t *testing.T) {
		publisher :=
			githubPublishBoundaryIntegrationTestPublisher(
				t,
				func(
					response http.ResponseWriter,
					_ *http.Request,
				) {
					response.WriteHeader(
						http.StatusOK,
					)
					_, _ = io.WriteString(
						response,
						strings.Repeat(
							"x",
							maxGitHubResponseBytes+1,
						),
					)
				},
			)

		err := publisher.requestJSON(
			context.Background(),
			"oversized response",
			http.MethodGet,
			"/test",
			nil,
			http.StatusOK,
			&referenceResponse{},
		)
		if !errors.Is(
			err,
			ErrGitHubResponseTooLarge,
		) {
			t.Errorf(
				"requestJSON() error = %v, want %v",
				err,
				ErrGitHubResponseTooLarge,
			)
		}
	})

	t.Run("authenticated redirect rejected", func(t *testing.T) {
		redirected := false

		target := httptest.NewServer(
			http.HandlerFunc(
				func(
					response http.ResponseWriter,
					request *http.Request,
				) {
					redirected = true

					if request.Header.Get(
						"Authorization",
					) != "" {
						t.Errorf(
							"redirect target Authorization = %q, want empty",
							request.Header.Get(
								"Authorization",
							),
						)
					}

					response.WriteHeader(
						http.StatusOK,
					)
					_, _ = io.WriteString(
						response,
						`{}`,
					)
				},
			),
		)
		defer target.Close()

		source := httptest.NewServer(
			http.HandlerFunc(
				func(
					response http.ResponseWriter,
					request *http.Request,
				) {
					if request.Header.Get(
						"Authorization",
					) !=
						"Bearer "+
							githubPublishBoundaryIntegrationToken {
						t.Errorf(
							"source Authorization = %q",
							request.Header.Get(
								"Authorization",
							),
						)
					}

					http.Redirect(
						response,
						request,
						target.URL,
						http.StatusFound,
					)
				},
			),
		)
		defer source.Close()

		publisher, err := newPublisher(
			githubPublishBoundaryIntegrationConfig(),
			githubPublishBoundaryIntegrationToken,
			source.URL,
		)
		if err != nil {
			t.Fatalf(
				"newPublisher() error = %v",
				err,
			)
		}

		err = publisher.requestJSON(
			context.Background(),
			"redirect",
			http.MethodGet,
			"/test",
			nil,
			http.StatusOK,
			&referenceResponse{},
		)
		if !errors.Is(err, ErrRedirect) {
			t.Errorf(
				"requestJSON() error = %v, want %v",
				err,
				ErrRedirect,
			)
		}

		if redirected {
			t.Error(
				"redirect target was reached",
			)
		}
	})
}

func TestGitHubPublishBoundaryIntegrationRejectsInvalidObjectResponses(
	t *testing.T,
) {
	tests := []struct {
		name   string
		status int
		body   string
		call   func(
			context.Context,
			*Publisher,
		) error
	}{
		{
			name:   "head type",
			status: http.StatusOK,
			body: `{
				"object":{
					"type":"tree",
					"sha":"head"
				}
			}`,
			call: func(
				ctx context.Context,
				publisher *Publisher,
			) error {
				_, err :=
					publisher.currentHead(ctx)

				return err
			},
		},
		{
			name:   "head missing SHA",
			status: http.StatusOK,
			body: `{
				"object":{
					"type":"commit"
				}
			}`,
			call: func(
				ctx context.Context,
				publisher *Publisher,
			) error {
				_, err :=
					publisher.currentHead(ctx)

				return err
			},
		},
		{
			name:   "commit missing tree",
			status: http.StatusOK,
			body: `{
				"sha":"head",
				"tree":{}
			}`,
			call: func(
				ctx context.Context,
				publisher *Publisher,
			) error {
				_, err := publisher.commitTree(
					ctx,
					"head",
				)

				return err
			},
		},
		{
			name:   "blob missing SHA",
			status: http.StatusCreated,
			body:   `{}`,
			call: func(
				ctx context.Context,
				publisher *Publisher,
			) error {
				_, err := publisher.createBlob(
					ctx,
					[]byte("registry\n"),
				)

				return err
			},
		},
		{
			name:   "tree missing SHA",
			status: http.StatusCreated,
			body:   `{}`,
			call: func(
				ctx context.Context,
				publisher *Publisher,
			) error {
				_, err := publisher.createTree(
					ctx,
					[]treeEntry{
						{
							Path: sentinelPath,
							Mode: "100644",
							Type: "blob",
							SHA:  "sentinel",
						},
					},
				)

				return err
			},
		},
		{
			name:   "commit missing SHA",
			status: http.StatusCreated,
			body:   `{}`,
			call: func(
				ctx context.Context,
				publisher *Publisher,
			) error {
				_, err := publisher.createCommit(
					ctx,
					"tree",
					"parent",
				)

				return err
			},
		},
		{
			name:   "reference points elsewhere",
			status: http.StatusOK,
			body: `{
				"object":{
					"type":"commit",
					"sha":"other"
				}
			}`,
			call: func(
				ctx context.Context,
				publisher *Publisher,
			) error {
				return publisher.updateReference(
					ctx,
					"wanted",
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			publisher :=
				githubPublishBoundaryIntegrationTestPublisher(
					t,
					func(
						response http.ResponseWriter,
						_ *http.Request,
					) {
						response.Header().Set(
							"Content-Type",
							"application/json",
						)
						response.WriteHeader(
							test.status,
						)
						_, _ = io.WriteString(
							response,
							test.body,
						)
					},
				)

			err := test.call(
				context.Background(),
				publisher,
			)
			if !errors.Is(
				err,
				ErrInvalidGitHubResponse,
			) {
				t.Errorf(
					"operation error = %v, want %v",
					err,
					ErrInvalidGitHubResponse,
				)
			}
		})
	}
}

func githubPublishBoundaryIntegrationTestPublisher(
	t *testing.T,
	handler func(
		http.ResponseWriter,
		*http.Request,
	),
) *Publisher {
	t.Helper()

	if handler == nil {
		handler = func(
			response http.ResponseWriter,
			_ *http.Request,
		) {
			response.WriteHeader(
				http.StatusOK,
			)
			_, _ = io.WriteString(
				response,
				`{}`,
			)
		}
	}

	server := httptest.NewServer(
		http.HandlerFunc(handler),
	)
	t.Cleanup(server.Close)

	publisher, err := newPublisher(
		githubPublishBoundaryIntegrationConfig(),
		githubPublishBoundaryIntegrationToken,
		server.URL,
	)
	if err != nil {
		t.Fatalf(
			"newPublisher() error = %v",
			err,
		)
	}

	return publisher
}

func githubPublishBoundaryIntegrationConfig() Config {
	return Config{
		Owner:      "joshternet",
		Repository: "index-data",
		Branch:     "main",
	}
}

func githubPublishBoundaryIntegrationSnapshot() []publicdata.File {
	return []publicdata.File{
		{
			Path: "registry.json",
			Data: []byte("{}\n"),
		},
	}
}

func githubPublishBoundaryIntegrationNodePath(
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

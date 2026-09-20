//lint:file-ignore SA1012 Intentional negative tests verify defensive nil-context rejection; production callers must never pass a nil context.
package discovery

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/robots"
)

var (
	errDiscoverFailureIntegrationFetch = errors.New(
		"integration discovery fetch failure",
	)
	errDiscoverFailureIntegrationRead = errors.New(
		"integration discovery read failure",
	)
	errDiscoverFailureIntegrationClose = errors.New(
		"integration discovery close failure",
	)
)

type discoverFailureIntegrationGetterFunc func(
	context.Context,
	*url.URL,
) (*http.Response, error)

func (getter discoverFailureIntegrationGetterFunc) Get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	return getter(ctx, target)
}

type discoverFailureIntegrationBody struct {
	reader   io.Reader
	readErr  error
	closeErr error
	closed   bool
}

func (body *discoverFailureIntegrationBody) Read(
	buffer []byte,
) (int, error) {
	if body.readErr != nil {
		return 0, body.readErr
	}

	return body.reader.Read(buffer)
}

func (body *discoverFailureIntegrationBody) Close() error {
	body.closed = true
	return body.closeErr
}

type discoverFailureIntegrationCancelBody struct {
	cancel context.CancelFunc
	reader io.Reader
	done   bool
}

func (body *discoverFailureIntegrationCancelBody) Read(
	buffer []byte,
) (int, error) {
	count, err := body.reader.Read(buffer)

	if !body.done {
		body.done = true
		body.cancel()
	}

	return count, err
}

func (*discoverFailureIntegrationCancelBody) Close() error {
	return nil
}

func TestDiscoverFailureIntegrationValidatesInputs(
	t *testing.T,
) {
	source := discoverFailureIntegrationOrigin(
		t,
		"https://example.com",
	)

	var missing *Crawler
	if _, err := missing.Discover(
		context.Background(),
		source,
	); !errors.Is(err, errCrawlerUnavailable) {
		t.Errorf(
			"nil Discover() error = %v, want %v",
			err,
			errCrawlerUnavailable,
		)
	}

	crawler := NewCrawler(
		discoverFailureIntegrationHTMLGetter(
			"<html></html>",
		),
	)

	if _, err := crawler.Discover(
		nil,
		source,
	); !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"Discover(nil) error = %v, want %v",
			err,
			errInvalidContext,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if _, err := crawler.Discover(
		ctx,
		source,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Discover(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	if _, err := crawler.Discover(
		context.Background(),
		origin.Origin{},
	); !errors.Is(err, errInvalidSource) {
		t.Errorf(
			"Discover(zero source) error = %v, want %v",
			err,
			errInvalidSource,
		)
	}

	if _, err := NewCrawler(nil).Discover(
		context.Background(),
		source,
	); !errors.Is(err, errGetterUnavailable) {
		t.Errorf(
			"Discover(nil getter) error = %v, want %v",
			err,
			errGetterUnavailable,
		)
	}
}

func TestDiscoverFailureIntegrationClassifiesRemoteFailures(
	t *testing.T,
) {
	source := discoverFailureIntegrationOrigin(
		t,
		"https://example.com",
	)

	tests := []struct {
		name   string
		getter Getter
		want   Status
	}{
		{
			name: "robots denied",
			getter: discoverFailureIntegrationGetterFunc(
				func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					return nil,
						robots.ErrDisallowed
				},
			),
			want: StatusRobotsDenied,
		},
		{
			name: "network failure",
			getter: discoverFailureIntegrationGetterFunc(
				func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					return nil,
						errDiscoverFailureIntegrationFetch
				},
			),
			want: StatusUnavailable,
		},
		{
			name: "nil response",
			getter: discoverFailureIntegrationGetterFunc(
				func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					return nil, nil
				},
			),
			want: StatusUnavailable,
		},
		{
			name: "nil body",
			getter: discoverFailureIntegrationGetterFunc(
				func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header: make(
							http.Header,
						),
					}, nil
				},
			),
			want: StatusUnavailable,
		},
		{
			name: "HTTP failure",
			getter: discoverFailureIntegrationResponseGetter(
				http.StatusBadGateway,
				"text/html",
				"failed",
			),
			want: StatusUnavailable,
		},
		{
			name: "unsupported content",
			getter: discoverFailureIntegrationResponseGetter(
				http.StatusOK,
				"application/json",
				`{"not":"html"}`,
			),
			want: StatusUnsupportedContent,
		},
		{
			name: "unsupported malformed content type",
			getter: discoverFailureIntegrationResponseGetter(
				http.StatusOK,
				"text/html;=",
				"<html></html>",
			),
			want: StatusUnsupportedContent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := NewCrawler(
				test.getter,
			).Discover(
				context.Background(),
				source,
			)
			if err != nil {
				t.Fatalf(
					"Discover() error = %v",
					err,
				)
			}

			if result.Status != test.want {
				t.Errorf(
					"Discover() status = %v, want %v",
					result.Status,
					test.want,
				)
			}

			if len(result.Candidates) != 0 {
				t.Errorf(
					"Discover() candidates = %#v, want none",
					result.Candidates,
				)
			}
		})
	}
}

func TestDiscoverFailureIntegrationHandlesBodyFailures(
	t *testing.T,
) {
	source := discoverFailureIntegrationOrigin(
		t,
		"https://example.com",
	)

	t.Run("oversized", func(t *testing.T) {
		body := &discoverFailureIntegrationBody{
			reader: bytes.NewReader(
				bytes.Repeat(
					[]byte("x"),
					MaxRawBody+1,
				),
			),
		}

		result, err := NewCrawler(
			discoverFailureIntegrationGetterFunc(
				func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header: http.Header{
							"Content-Type": {
								"text/html",
							},
						},
						Body: body,
					}, nil
				},
			),
		).Discover(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf(
				"Discover() error = %v",
				err,
			)
		}

		if result.Status != StatusTooLarge {
			t.Errorf(
				"Discover() status = %v, want %v",
				result.Status,
				StatusTooLarge,
			)
		}

		if !body.closed {
			t.Error(
				"oversized response body was not closed",
			)
		}
	})

	t.Run("read failure", func(t *testing.T) {
		body := &discoverFailureIntegrationBody{
			reader:  strings.NewReader("unused"),
			readErr: errDiscoverFailureIntegrationRead,
		}

		result, err := NewCrawler(
			discoverFailureIntegrationGetterFunc(
				func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header: http.Header{
							"Content-Type": {
								"text/html",
							},
						},
						Body: body,
					}, nil
				},
			),
		).Discover(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf(
				"Discover() error = %v",
				err,
			)
		}

		if result.Status != StatusUnavailable {
			t.Errorf(
				"Discover() status = %v, want %v",
				result.Status,
				StatusUnavailable,
			)
		}

		if !body.closed {
			t.Error(
				"failed response body was not closed",
			)
		}
	})

	t.Run("close failure", func(t *testing.T) {
		body := &discoverFailureIntegrationBody{
			reader: strings.NewReader(
				"<html></html>",
			),
			closeErr: errDiscoverFailureIntegrationClose,
		}

		result, err := NewCrawler(
			discoverFailureIntegrationGetterFunc(
				func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header: http.Header{
							"Content-Type": {
								"text/html",
							},
						},
						Body: body,
					}, nil
				},
			),
		).Discover(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf(
				"Discover() error = %v",
				err,
			)
		}

		if result.Status != StatusUnavailable {
			t.Errorf(
				"Discover() status = %v, want %v",
				result.Status,
				StatusUnavailable,
			)
		}

		if !body.closed {
			t.Error(
				"response body close was not attempted",
			)
		}
	})

	t.Run("cancellation during read wins", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)

		body := &discoverFailureIntegrationCancelBody{
			cancel: cancel,
			reader: strings.NewReader(
				"<html></html>",
			),
		}

		_, err := NewCrawler(
			discoverFailureIntegrationGetterFunc(
				func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header: http.Header{
							"Content-Type": {
								"text/html",
							},
						},
						Body: body,
					}, nil
				},
			),
		).Discover(
			ctx,
			source,
		)

		if !errors.Is(err, context.Canceled) {
			t.Errorf(
				"Discover() error = %v, want context.Canceled",
				err,
			)
		}
	})
}

func TestDiscoverFailureIntegrationRequestCancellationWins(
	t *testing.T,
) {
	source := discoverFailureIntegrationOrigin(
		t,
		"https://example.com",
	)

	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	crawler := NewCrawler(
		discoverFailureIntegrationGetterFunc(
			func(
				context.Context,
				*url.URL,
			) (*http.Response, error) {
				cancel()

				return nil,
					errDiscoverFailureIntegrationFetch
			},
		),
	)

	_, err := crawler.Discover(ctx, source)
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"Discover() error = %v, want context.Canceled",
			err,
		)
	}
}

func TestDiscoverFailureIntegrationRedirectContract(
	t *testing.T,
) {
	source := discoverFailureIntegrationOrigin(
		t,
		"https://example.com",
	)

	tests := []struct {
		name     string
		location string
		closeErr error
	}{
		{
			name: "missing location",
		},
		{
			name:     "malformed location",
			location: "%zz",
		},
		{
			name: "credential-bearing location",
			location: "https://user:" +
				"secret@example.net/",
		},
		{
			name:     "close failure",
			location: "/home",
			closeErr: errDiscoverFailureIntegrationClose,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &discoverFailureIntegrationBody{
				reader:   strings.NewReader("redirect"),
				closeErr: test.closeErr,
			}

			crawler := NewCrawler(
				discoverFailureIntegrationGetterFunc(
					func(
						context.Context,
						*url.URL,
					) (*http.Response, error) {
						response :=
							discoverFailureIntegrationResponse(
								http.StatusFound,
								"",
								"redirect",
							)
						response.Body = body

						if test.location != "" {
							response.Header.Set(
								"Location",
								test.location,
							)
						}

						return response, nil
					},
				),
			)

			result, err := crawler.Discover(
				context.Background(),
				source,
			)
			if err != nil {
				t.Fatalf(
					"Discover() error = %v",
					err,
				)
			}

			if result.Status != StatusUnavailable {
				t.Errorf(
					"Discover() status = %v, want %v",
					result.Status,
					StatusUnavailable,
				)
			}

			if !body.closed {
				t.Error(
					"redirect response body was not closed",
				)
			}
		})
	}

	t.Run("cross-origin becomes candidate", func(t *testing.T) {
		calls := 0

		crawler := NewCrawler(
			discoverFailureIntegrationGetterFunc(
				func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					calls++

					response :=
						discoverFailureIntegrationResponse(
							http.StatusPermanentRedirect,
							"",
							"redirect",
						)
					response.Header.Set(
						"Location",
						"https://other.example/path?secret=yes#private",
					)

					return response, nil
				},
			),
		)

		result, err := crawler.Discover(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf(
				"Discover() error = %v",
				err,
			)
		}

		if calls != 1 {
			t.Errorf(
				"getter calls = %d, want 1",
				calls,
			)
		}

		if result.Status != StatusRedirected ||
			len(result.Candidates) != 1 ||
			result.Candidates[0].Kind != KindRedirect ||
			result.Candidates[0].Origin.String() !=
				"https://other.example" {
			t.Errorf(
				"Discover() result = %#v",
				result,
			)
		}
	})

	t.Run("same-origin redirect succeeds", func(t *testing.T) {
		calls := 0

		crawler := NewCrawler(
			discoverFailureIntegrationGetterFunc(
				func(
					_ context.Context,
					target *url.URL,
				) (*http.Response, error) {
					calls++

					if calls == 1 {
						response :=
							discoverFailureIntegrationResponse(
								http.StatusSeeOther,
								"",
								"redirect",
							)
						response.Header.Set(
							"Location",
							"/home#private",
						)

						return response, nil
					}

					if target.String() !=
						"https://example.com/home" {
						t.Fatalf(
							"redirect target = %q",
							target.String(),
						)
					}

					return discoverFailureIntegrationResponse(
						http.StatusOK,
						"text/html",
						`<a href="https://linked.example/path">linked</a>`,
					), nil
				},
			),
		)

		result, err := crawler.Discover(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf(
				"Discover() error = %v",
				err,
			)
		}

		if calls != 2 ||
			result.Status != StatusComplete ||
			len(result.Candidates) != 1 ||
			result.Candidates[0].Origin.String() !=
				"https://linked.example" {
			t.Errorf(
				"Discover() result = %#v, calls=%d",
				result,
				calls,
			)
		}
	})

	t.Run("redirect budget exhausted", func(t *testing.T) {
		calls := 0

		crawler := NewCrawler(
			discoverFailureIntegrationGetterFunc(
				func(
					context.Context,
					*url.URL,
				) (*http.Response, error) {
					calls++

					response :=
						discoverFailureIntegrationResponse(
							http.StatusTemporaryRedirect,
							"",
							"redirect",
						)
					response.Header.Set(
						"Location",
						"/redirect-"+string(
							rune('a'+calls),
						),
					)

					return response, nil
				},
			),
		)

		result, err := crawler.Discover(
			context.Background(),
			source,
		)
		if err != nil {
			t.Fatalf(
				"Discover() error = %v",
				err,
			)
		}

		if result.Status != StatusUnavailable {
			t.Errorf(
				"Discover() status = %v, want %v",
				result.Status,
				StatusUnavailable,
			)
		}

		if calls != MaxRedirects+1 {
			t.Errorf(
				"getter calls = %d, want %d",
				calls,
				MaxRedirects+1,
			)
		}
	})
}

func discoverFailureIntegrationHTMLGetter(
	body string,
) Getter {
	return discoverFailureIntegrationResponseGetter(
		http.StatusOK,
		"text/html",
		body,
	)
}

func discoverFailureIntegrationResponseGetter(
	status int,
	contentType string,
	body string,
) Getter {
	return discoverFailureIntegrationGetterFunc(
		func(
			context.Context,
			*url.URL,
		) (*http.Response, error) {
			return discoverFailureIntegrationResponse(
				status,
				contentType,
				body,
			), nil
		},
	)
}

func discoverFailureIntegrationResponse(
	status int,
	contentType string,
	body string,
) *http.Response {
	header := make(http.Header)

	if contentType != "" {
		header.Set(
			"Content-Type",
			contentType,
		)
	}

	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body: io.NopCloser(
			strings.NewReader(body),
		),
	}
}

func discoverFailureIntegrationOrigin(
	t *testing.T,
	raw string,
) origin.Origin {
	t.Helper()

	source, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			raw,
			err,
		)
	}

	return source
}

package discovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/robots"
)

type crawlerFailureIntegrationGetterFunc func(context.Context, *url.URL) (*http.Response, error)

func (getter crawlerFailureIntegrationGetterFunc) Get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	return getter(ctx, target)
}

type crawlerFailureIntegrationSink struct{}

func (*crawlerFailureIntegrationSink) RecordCandidates(
	context.Context,
	origin.Origin,
	[]Candidate,
) error {
	return nil
}

type crawlerFailureIntegrationBody struct {
	reader   io.Reader
	readErr  error
	closeErr error
	closed   bool
}

func (body *crawlerFailureIntegrationBody) Read(buffer []byte) (int, error) {
	if body.readErr != nil {
		return 0, body.readErr
	}
	return body.reader.Read(buffer)
}

func (body *crawlerFailureIntegrationBody) Close() error {
	body.closed = true
	return body.closeErr
}

type crawlerFailureIntegrationClock struct{ now time.Time }

func (clock crawlerFailureIntegrationClock) Now() time.Time { return clock.now }

func TestCrawlerFailureIntegrationConstructorAndValidationBoundaries(t *testing.T) {
	source := crawlerFailureIntegrationOrigin(t, "https://source.example")
	getter := crawlerFailureIntegrationHTMLGetter("<html></html>")
	sink := &crawlerFailureIntegrationSink{}
	config := crawlerFailureIntegrationConfig()

	constructors := []struct {
		name string
		make func() (*MultiPageCrawler, error)
		want error
	}{
		{"missing getter", func() (*MultiPageCrawler, error) {
			return NewMultiPageCrawler(nil, sink, config)
		}, errGetterUnavailable},
		{"missing sink", func() (*MultiPageCrawler, error) {
			return NewMultiPageCrawler(getter, nil, config)
		}, errCandidateSinkUnavailable},
		{"invalid config", func() (*MultiPageCrawler, error) {
			invalid := config
			invalid.MaxPages = 0
			return NewMultiPageCrawler(getter, sink, invalid)
		}, errInvalidCrawlConfig},
		{"missing waiter", func() (*MultiPageCrawler, error) {
			return newMultiPageCrawler(
				getter, sink, config, nil, contextTimeoutFactory{}, discardCrawlTelemetry{},
			)
		}, errWaiterUnavailable},
		{"missing timeout factory", func() (*MultiPageCrawler, error) {
			return newMultiPageCrawler(
				getter, sink, config, timerWaitStrategy{}, nil, discardCrawlTelemetry{},
			)
		}, errTimeoutFactoryUnavailable},
		{"missing telemetry", func() (*MultiPageCrawler, error) {
			return newMultiPageCrawler(
				getter, sink, config, timerWaitStrategy{}, contextTimeoutFactory{}, nil,
			)
		}, errCrawlTelemetryUnavailable},
	}

	for _, test := range constructors {
		t.Run(test.name, func(t *testing.T) {
			crawler, err := test.make()
			if !errors.Is(err, test.want) || crawler != nil {
				t.Errorf("constructor = %#v, %v, want nil, %v", crawler, err, test.want)
			}
		})
	}

	customClock := crawlerFailureIntegrationClock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	customConfig := config
	customConfig.RetryClock = customClock
	customCrawler, err := NewMultiPageCrawler(getter, sink, customConfig)
	if err != nil {
		t.Fatalf("NewMultiPageCrawler(custom clock) error = %v", err)
	}
	if customCrawler.clock.Now() != customClock.now {
		t.Errorf("crawler clock = %v, want %v", customCrawler.clock.Now(), customClock.now)
	}

	var missing *MultiPageCrawler
	if _, err := missing.Crawl(context.Background(), source); !errors.Is(err, errMultiPageCrawlerUnavailable) {
		t.Errorf("nil Crawl() error = %v, want %v", err, errMultiPageCrawlerUnavailable)
	}

	valid, err := NewMultiPageCrawler(getter, sink, config)
	if err != nil {
		t.Fatalf("NewMultiPageCrawler() error = %v", err)
	}

	var nilContext context.Context
	if _, err := valid.Crawl(nilContext, source); !errors.Is(err, errInvalidContext) {
		t.Errorf("Crawl(nil) error = %v, want %v", err, errInvalidContext)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := valid.Crawl(canceled, source); !errors.Is(err, context.Canceled) {
		t.Errorf("Crawl(canceled) error = %v, want context.Canceled", err)
	}

	if _, err := valid.Crawl(context.Background(), origin.Origin{}); !errors.Is(err, errInvalidSource) {
		t.Errorf("Crawl(zero source) error = %v, want %v", err, errInvalidSource)
	}

	validation := []struct {
		name   string
		change func(*MultiPageCrawler)
		want   error
	}{
		{"missing getter", func(c *MultiPageCrawler) { c.getter = nil }, errGetterUnavailable},
		{"missing sink", func(c *MultiPageCrawler) { c.sink = nil }, errCandidateSinkUnavailable},
		{"invalid config", func(c *MultiPageCrawler) { c.config.RedirectLimit = 0 }, errInvalidCrawlConfig},
		{"missing waiter", func(c *MultiPageCrawler) { c.waiter = nil }, errWaiterUnavailable},
		{"missing timeout factory", func(c *MultiPageCrawler) { c.timeoutFactory = nil }, errTimeoutFactoryUnavailable},
		{"missing telemetry", func(c *MultiPageCrawler) { c.telemetry = nil }, errCrawlTelemetryUnavailable},
	}

	for _, test := range validation {
		t.Run("runtime "+test.name, func(t *testing.T) {
			crawler := *valid
			test.change(&crawler)
			_, err := crawler.Crawl(context.Background(), source)
			if !errors.Is(err, test.want) {
				t.Errorf("Crawl() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestCrawlerFailureIntegrationFetchFailures(t *testing.T) {
	source := crawlerFailureIntegrationOrigin(t, "https://source.example")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name    string
		ctx     context.Context
		getter  Getter
		config  func(*CrawlConfig)
		outcome PageOutcome
		want    retry.Category
		retry   time.Duration
	}{
		{
			name:    "robots denied",
			ctx:     context.Background(),
			getter:  crawlerFailureIntegrationErrorGetter(robots.ErrDisallowed),
			outcome: PageRobotsDenied,
			want:    retry.CategoryRobotsDenied,
		},
		{
			name:    "robots temporary",
			ctx:     context.Background(),
			getter:  crawlerFailureIntegrationErrorGetter(robots.ErrTemporary),
			outcome: PageNetworkError,
			want:    retry.CategoryRobotsTemporary,
		},
		{
			name:    "generic transport",
			ctx:     context.Background(),
			getter:  crawlerFailureIntegrationErrorGetter(errors.New("integration transport failure")),
			outcome: PageNetworkError,
			want:    retry.CategoryTransport,
		},
		{
			name: "canceled context",
			ctx:  canceled,
			getter: crawlerFailureIntegrationGetterFunc(func(ctx context.Context, _ *url.URL) (*http.Response, error) {
				return nil, ctx.Err()
			}),
			outcome: PageTimeout,
			want:    retry.CategoryTimeout,
		},
		{
			name: "nil response",
			ctx:  context.Background(),
			getter: crawlerFailureIntegrationGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, nil
			}),
			outcome: PageInvalidResponse,
			want:    retry.CategoryTransport,
		},
		{
			name: "nil response body",
			ctx:  context.Background(),
			getter: crawlerFailureIntegrationGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}, nil
			}),
			outcome: PageInvalidResponse,
			want:    retry.CategoryTransport,
		},
		{
			name: "HTTP retry after",
			ctx:  context.Background(),
			getter: crawlerFailureIntegrationGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				response := crawlerFailureIntegrationResponse(http.StatusTooManyRequests, "text/html", "slow down")
				response.Header.Set("Retry-After", "60")
				return response, nil
			}),
			outcome: PageHTTPError,
			want:    retry.CategoryHTTP429,
			retry:   time.Minute,
		},
		{
			name:    "permanent HTTP error",
			ctx:     context.Background(),
			getter:  crawlerFailureIntegrationResponseGetter(http.StatusBadRequest, "text/html", "bad"),
			outcome: PageHTTPError,
			want:    retry.CategoryHTTP4xx,
		},
		{
			name:    "oversized body",
			ctx:     context.Background(),
			getter:  crawlerFailureIntegrationResponseGetter(http.StatusOK, "text/html", "12345"),
			config:  func(config *CrawlConfig) { config.MaxPageBytes = 4 },
			outcome: PageTooLarge,
			want:    retry.CategoryOversizedContent,
		},
		{
			name:    "unsupported content",
			ctx:     context.Background(),
			getter:  crawlerFailureIntegrationResponseGetter(http.StatusOK, "image/png", "png"),
			outcome: PageUnsupportedContent,
			want:    retry.CategoryUnsupportedContent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := crawlerFailureIntegrationConfig()
			if test.config != nil {
				test.config(&config)
			}
			crawler := crawlerFailureIntegrationCrawler(t, test.getter, config)
			page := crawlerFailureIntegrationFetch(t, crawler, test.ctx, source)
			if page.failureCategory != test.want || page.attempt.Outcome != test.outcome || page.retryAfter != test.retry {
				t.Errorf(
					"fetchPage() = category %q outcome %q retry %v, want %q/%q/%v",
					page.failureCategory, page.attempt.Outcome, page.retryAfter,
					test.want, test.outcome, test.retry,
				)
			}
		})
	}
}

func TestCrawlerFailureIntegrationReadAndCloseFailures(t *testing.T) {
	source := crawlerFailureIntegrationOrigin(t, "https://source.example")

	tests := []struct {
		name string
		body *crawlerFailureIntegrationBody
	}{
		{
			name: "read",
			body: &crawlerFailureIntegrationBody{
				reader:  strings.NewReader("unused"),
				readErr: errors.New("integration read failure"),
			},
		},
		{
			name: "close",
			body: &crawlerFailureIntegrationBody{
				reader:   strings.NewReader("<html></html>"),
				closeErr: errors.New("integration close failure"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			crawler := crawlerFailureIntegrationCrawler(
				t,
				crawlerFailureIntegrationGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"text/html"}},
						Body:       test.body,
					}, nil
				}),
				crawlerFailureIntegrationConfig(),
			)

			page := crawlerFailureIntegrationFetch(t, crawler, context.Background(), source)
			if !test.body.closed || page.failureCategory != retry.CategoryTransport || page.attempt.Outcome != PageNetworkError {
				t.Errorf("fetchPage() = %#v, closed=%v", page, test.body.closed)
			}
		})
	}
}

func TestCrawlerFailureIntegrationRedirectBoundaries(t *testing.T) {
	source := crawlerFailureIntegrationOrigin(t, "https://source.example")

	tests := []struct {
		name     string
		location string
		want     retry.Category
	}{
		{name: "missing location", want: retry.CategoryMalformedOrigin},
		{name: "malformed location", location: "%zz", want: retry.CategoryMalformedOrigin},
		{
			name:     "unsupported redirect scheme",
			location: "mailto:josh@example.com",
			want:     retry.CategoryMalformedOrigin,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			crawler := crawlerFailureIntegrationCrawler(
				t,
				crawlerFailureIntegrationGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
					response := crawlerFailureIntegrationResponse(http.StatusFound, "text/html", "redirect")
					if test.location != "" {
						response.Header.Set("Location", test.location)
					}
					return response, nil
				}),
				crawlerFailureIntegrationConfig(),
			)

			page := crawlerFailureIntegrationFetch(t, crawler, context.Background(), source)
			if page.failureCategory != test.want {
				t.Errorf("fetchPage() category = %q, want %q", page.failureCategory, test.want)
			}
		})
	}

	t.Run("cross-origin candidate", func(t *testing.T) {
		crawler := crawlerFailureIntegrationCrawler(
			t,
			crawlerFailureIntegrationGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				response := crawlerFailureIntegrationResponse(http.StatusTemporaryRedirect, "text/html", "redirect")
				response.Header.Set("Location", "https://other.example/path?secret=yes#private")
				return response, nil
			}),
			crawlerFailureIntegrationConfig(),
		)

		page := crawlerFailureIntegrationFetch(t, crawler, context.Background(), source)
		if page.attempt.Outcome != PageRedirected ||
			page.attempt.RedirectCount != 1 ||
			page.attempt.FinalURL != "https://other.example/path" ||
			len(page.links.Candidates) != 1 ||
			page.links.Candidates[0].Kind != KindRedirect ||
			page.links.Candidates[0].Origin.String() != "https://other.example" {
			t.Errorf("fetchPage() = %#v", page)
		}
	})

	t.Run("same-origin redirect limit", func(t *testing.T) {
		calls := 0
		config := crawlerFailureIntegrationConfig()
		config.RedirectLimit = 1
		crawler := crawlerFailureIntegrationCrawler(
			t,
			crawlerFailureIntegrationGetterFunc(func(_ context.Context, target *url.URL) (*http.Response, error) {
				calls++
				response := crawlerFailureIntegrationResponse(http.StatusFound, "text/html", "redirect")
				if target.Path == "/" {
					response.Header.Set("Location", "/one")
				} else {
					response.Header.Set("Location", "/two")
				}
				return response, nil
			}),
			config,
		)

		page := crawlerFailureIntegrationFetch(t, crawler, context.Background(), source)
		if calls != 2 || page.failureCategory != retry.CategoryMalformedOrigin || page.attempt.RedirectCount != 1 {
			t.Errorf("fetchPage() = %#v, calls=%d", page, calls)
		}
	})
}

func crawlerFailureIntegrationCrawler(t *testing.T, getter Getter, config CrawlConfig) *MultiPageCrawler {
	t.Helper()
	crawler, err := NewMultiPageCrawler(getter, &crawlerFailureIntegrationSink{}, config)
	if err != nil {
		t.Fatalf("NewMultiPageCrawler() error = %v", err)
	}
	return crawler
}

func crawlerFailureIntegrationFetch(
	t *testing.T,
	crawler *MultiPageCrawler,
	ctx context.Context,
	source origin.Origin,
) crawledPage {
	t.Helper()
	pageURL, err := url.Parse(source.String() + "/")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	return crawler.fetchPage(ctx, source, pageURL, make(map[string]struct{}))
}

func crawlerFailureIntegrationErrorGetter(err error) Getter {
	return crawlerFailureIntegrationGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
		return nil, err
	})
}

func crawlerFailureIntegrationHTMLGetter(body string) Getter {
	return crawlerFailureIntegrationResponseGetter(http.StatusOK, "text/html", body)
}

func crawlerFailureIntegrationResponseGetter(status int, contentType, body string) Getter {
	return crawlerFailureIntegrationGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
		return crawlerFailureIntegrationResponse(status, contentType, body), nil
	})
}

func crawlerFailureIntegrationResponse(status int, contentType, body string) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func crawlerFailureIntegrationConfig() CrawlConfig {
	return CrawlConfig{
		MaxDepth:                     1,
		MaxPages:                     4,
		MaxPageBytes:                 64 * 1024,
		RequestDelay:                 0,
		RedirectLimit:                2,
		PageTimeout:                  time.Second,
		MaxAutomaticPromotionsPerRun: 0,
	}
}

func crawlerFailureIntegrationOrigin(t *testing.T, raw string) origin.Origin {
	t.Helper()
	source, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf("origin.Parse(%q) error = %v", raw, err)
	}
	return source
}

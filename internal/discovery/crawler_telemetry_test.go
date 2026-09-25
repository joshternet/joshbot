package discovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/netguard"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/robots"
)

type recordingTelemetry struct {
	runID      CrawlRunID
	beginErr   error
	recordErr  error
	finishErr  error
	attempts   []PageAttempt
	outcome    CrawlRunOutcome
	stopReason string
}

type recordingSummaryTelemetry struct {
	recordingTelemetry
	frontierRemaining int
}

func (telemetry *recordingSummaryTelemetry) FinishCrawlSummary(
	ctx context.Context,
	runID CrawlRunID,
	result CrawlResult,
	outcome CrawlRunOutcome,
	reason string,
	frontierRemaining int,
) error {
	telemetry.frontierRemaining = frontierRemaining
	return telemetry.FinishCrawl(ctx, runID, result, outcome, reason)
}

func TestFetchPageTelemetryClassifiesResponseFailures(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://source.example")
	testErr := errors.New("response failure")
	tests := []struct {
		name    string
		getter  Getter
		outcome PageOutcome
	}{
		{
			name: "missing response",
			getter: frontierGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, nil
			}),
			outcome: PageInvalidResponse,
		},
		{
			name: "HTTP error",
			getter: frontierGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{}, Body: io.NopCloser(failingReader{err: testErr})}, nil
			}),
			outcome: PageHTTPError,
		},
		{
			name: "body read error",
			getter: frontierGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: &trackedBody{reader: failingReader{err: testErr}}}, nil
			}),
			outcome: PageNetworkError,
		},
		{
			name: "body close error",
			getter: frontierGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: &trackedBody{reader: strings.NewReader("ok"), closeErr: testErr}}, nil
			}),
			outcome: PageNetworkError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			crawler := mustNewMultiPageCrawler(t, test.getter, &frontierCandidateSink{}, frontierCrawlConfig(), &frontierWaiter{}, &frontierTimeoutFactory{})
			pageURL, _ := url.Parse(source.String() + "/")
			page := crawler.fetchPage(context.Background(), source, pageURL, map[string]struct{}{})
			if page.attempt.Outcome != test.outcome {
				t.Errorf("outcome = %q, want %q", page.attempt.Outcome, test.outcome)
			}
		})
	}
}

func TestMultiPageCrawlerCarriesTransientRootFailure(t *testing.T) {
	now := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	source := mustDiscoveryOrigin(t, "https://source.example")
	calls := 0
	getter := frontierGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
		calls++
		header := http.Header{}
		header.Set("Retry-After", now.Add(time.Hour).Format(http.TimeFormat))
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader("unavailable")),
		}, nil
	})
	config := frontierCrawlConfig()
	config.RetryClock = fixedDiscoveryClock{now: now}
	crawler, err := NewMultiPageCrawler(getter, &frontierCandidateSink{}, config)
	if err != nil {
		t.Fatal(err)
	}
	result, err := crawler.Crawl(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if result.FailureCategory != retry.CategoryHTTP5xx ||
		result.RetryAfter != time.Hour ||
		result.PagesParsed != 0 {
		t.Fatalf("root failure result = %#v", result)
	}
	if calls != retry.MaxAttemptsPerCycle {
		t.Fatalf("root attempts = %d, want %d", calls, retry.MaxAttemptsPerCycle)
	}
}

func TestMultiPageCrawlerMarksRootFailureFailed(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://source.example")
	telemetry := &recordingSummaryTelemetry{
		recordingTelemetry: recordingTelemetry{runID: 92},
	}

	crawler, err := NewMultiPageCrawlerWithTelemetry(
		responseGetter(
			http.StatusServiceUnavailable,
			"text/html",
			"unavailable",
		),
		&frontierCandidateSink{},
		telemetry,
		frontierCrawlConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := crawler.Crawl(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}

	if result.PagesParsed != 0 {
		t.Fatalf("pages parsed = %d, want 0", result.PagesParsed)
	}
	if result.FailureCategory != retry.CategoryHTTP5xx {
		t.Fatalf(
			"failure category = %q, want %q",
			result.FailureCategory,
			retry.CategoryHTTP5xx,
		)
	}
	if telemetry.outcome != CrawlRunFailed {
		t.Errorf(
			"outcome = %q, want %q",
			telemetry.outcome,
			CrawlRunFailed,
		)
	}
	if telemetry.stopReason != "root_failure" {
		t.Errorf(
			"stop reason = %q, want root_failure",
			telemetry.stopReason,
		)
	}
}

func TestMultiPageCrawlerUsesSummaryTelemetry(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://source.example")
	telemetry := &recordingSummaryTelemetry{
		recordingTelemetry: recordingTelemetry{runID: 91},
	}
	crawler, err := NewMultiPageCrawlerWithTelemetry(
		newFrontierGetter(map[string][]frontierStep{
			"https://source.example/": {frontierHTML(`<a href="/next">next</a>`)},
		}),
		&frontierCandidateSink{},
		telemetry,
		frontierCrawlConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crawler.Crawl(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if telemetry.outcome != CrawlRunComplete || telemetry.frontierRemaining != 0 {
		t.Errorf("summary = %q, frontier %d", telemetry.outcome, telemetry.frontierRemaining)
	}
}

func TestMultiPageCrawlerDoesNotRetryPartiallySuccessfulCrawl(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://source.example")
	getter := newFrontierGetter(map[string][]frontierStep{
		"https://source.example/": {
			frontierHTML(`<a href="/next">next</a>`),
		},
		"https://source.example/next": {
			{status: http.StatusTooManyRequests},
		},
	})
	crawler, err := NewMultiPageCrawler(
		getter,
		&frontierCandidateSink{},
		frontierCrawlConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := crawler.Crawl(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if result.PagesParsed != 1 || result.FailureCategory != retry.CategoryNone {
		t.Fatalf("partial crawl result = %#v", result)
	}
}

func TestFetchPageSeparatesRetryableAndPermanentCategories(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://source.example")
	unsafeOrigin := mustDiscoveryOrigin(t, "https://127.0.0.1")
	_, unsafeError := netguard.Resolve(context.Background(), unsafeOrigin, nil)
	timeoutContext, cancelTimeout := context.WithCancel(context.Background())
	cancelTimeout()
	tests := []struct {
		name      string
		getter    Getter
		context   context.Context
		configure func(*CrawlConfig)
		want      retry.Category
	}{
		{
			name: "timeout",
			getter: frontierGetterFunc(func(ctx context.Context, _ *url.URL) (*http.Response, error) {
				return nil, ctx.Err()
			}),
			context: timeoutContext,
			want:    retry.CategoryTimeout,
		},
		{
			name: "unsafe address",
			getter: frontierGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, unsafeError
			}),
			context: context.Background(),
			want:    retry.CategoryUnsafeAddress,
		},
		{
			name: "temporary robots",
			getter: frontierGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, robots.ErrTemporary
			}),
			context: context.Background(),
			want:    retry.CategoryRobotsTemporary,
		},
		{
			name: "robots denied",
			getter: frontierGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
				return nil, robots.ErrDisallowed
			}),
			context: context.Background(),
			want:    retry.CategoryRobotsDenied,
		},
		{
			name:    "HTTP 408",
			getter:  responseGetter(http.StatusRequestTimeout, "text/html", "later"),
			context: context.Background(),
			want:    retry.CategoryHTTP408,
		},
		{
			name:    "HTTP 429",
			getter:  responseGetter(http.StatusTooManyRequests, "text/html", "later"),
			context: context.Background(),
			want:    retry.CategoryHTTP429,
		},
		{
			name:    "non-4xx permanent HTTP status",
			getter:  responseGetter(http.StatusContinue, "text/html", "continue"),
			context: context.Background(),
			want:    retry.CategoryUnsupportedOrigin,
		},
		{
			name:    "HTTP 400",
			getter:  responseGetter(http.StatusBadRequest, "text/html", "bad"),
			context: context.Background(),
			want:    retry.CategoryHTTP4xx,
		},
		{
			name:    "HTTP 403",
			getter:  responseGetter(http.StatusForbidden, "text/html", "forbidden"),
			context: context.Background(),
			want:    retry.CategoryHTTP4xx,
		},
		{
			name:    "HTTP 404",
			getter:  responseGetter(http.StatusNotFound, "text/html", "missing"),
			context: context.Background(),
			want:    retry.CategoryHTTP4xx,
		},
		{
			name:    "unsupported content",
			getter:  responseGetter(http.StatusOK, "image/png", "png"),
			context: context.Background(),
			want:    retry.CategoryUnsupportedContent,
		},
		{
			name:    "oversized content",
			getter:  responseGetter(http.StatusOK, "text/html", "too long"),
			context: context.Background(),
			configure: func(config *CrawlConfig) {
				config.MaxPageBytes = 1
			},
			want: retry.CategoryOversizedContent,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := frontierCrawlConfig()
			if test.configure != nil {
				test.configure(&config)
			}
			crawler, err := NewMultiPageCrawler(
				test.getter, &frontierCandidateSink{}, config,
			)
			if err != nil {
				t.Fatal(err)
			}
			pageURL, _ := url.Parse(source.String() + "/")
			page := crawler.fetchPage(test.context, source, pageURL, map[string]struct{}{})
			if page.failureCategory != test.want {
				t.Errorf("failure category = %q, want %q", page.failureCategory, test.want)
			}
		})
	}
}

func responseGetter(status int, contentType string, body string) Getter {
	return frontierGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
		header := http.Header{}
		header.Set("Content-Type", contentType)
		return &http.Response{
			StatusCode: status,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
}

type fixedDiscoveryClock struct{ now time.Time }

func (clock fixedDiscoveryClock) Now() time.Time { return clock.now }

func (telemetry *recordingTelemetry) BeginCrawl(
	context.Context,
	origin.Origin,
	CrawlConfig,
) (CrawlRunID, error) {
	return telemetry.runID, telemetry.beginErr
}

func (telemetry *recordingTelemetry) RecordPageAttempt(
	_ context.Context,
	_ CrawlRunID,
	attempt PageAttempt,
) error {
	telemetry.attempts = append(telemetry.attempts, attempt)
	return telemetry.recordErr
}

func (telemetry *recordingTelemetry) FinishCrawl(
	_ context.Context,
	_ CrawlRunID,
	_ CrawlResult,
	outcome CrawlRunOutcome,
	stopReason string,
) error {
	telemetry.outcome = outcome
	telemetry.stopReason = stopReason
	return telemetry.finishErr
}

func TestMultiPageCrawlerRecordsSafePageTelemetry(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://source.example")
	getter := newFrontierGetter(map[string][]frontierStep{
		"https://source.example/":     {frontierHTML(`<a href="/next?secret=yes#private">next</a><a href="https://outside.example/path?token=no">out</a>`)},
		"https://source.example/next": {frontierHTML("done")},
	})
	telemetry := &recordingTelemetry{runID: 7}
	crawler, err := NewMultiPageCrawlerWithTelemetry(
		getter, &frontierCandidateSink{}, telemetry, frontierCrawlConfig(),
	)
	if err != nil {
		t.Fatalf("NewMultiPageCrawlerWithTelemetry() error = %v", err)
	}
	result, err := crawler.Crawl(context.Background(), source)
	if err != nil {
		t.Fatalf("Crawl() error = %v", err)
	}
	if result.PagesAttempted != 2 || len(telemetry.attempts) != 2 {
		t.Fatalf("attempts = %d/%d, want 2/2", result.PagesAttempted, len(telemetry.attempts))
	}
	if got := telemetry.attempts[1].RequestedURL; got != "https://source.example/next" {
		t.Errorf("sanitized URL = %q", got)
	}
	if got := telemetry.attempts[0].ContentType; got != "text/html" {
		t.Errorf("content type = %q, want text/html", got)
	}
	if telemetry.attempts[0].RequestedURL != "https://source.example/" ||
		telemetry.attempts[0].FinalURL != "https://source.example/" ||
		telemetry.attempts[0].URLsFound != 2 ||
		telemetry.attempts[0].URLsEnqueued != 1 ||
		telemetry.attempts[1].URLsFound != 0 ||
		telemetry.attempts[1].URLsEnqueued != 0 {
		t.Errorf("URL telemetry = %#v", telemetry.attempts)
	}
	if telemetry.outcome != CrawlRunComplete || telemetry.stopReason != "frontier_exhausted" {
		t.Errorf("finish = %q/%q", telemetry.outcome, telemetry.stopReason)
	}
}

func TestSafeTelemetryURLRemovesCredentialsQueryAndFragment(t *testing.T) {
	raw, err := url.Parse(
		"https://user:password@source.example/path?token=secret#private",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := safeTelemetryURL(raw); got != "https://source.example/path" {
		t.Errorf("safeTelemetryURL() = %q", got)
	}
}

func TestRedirectTelemetryDoesNotExposeQueryValues(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://source.example")
	calls := 0
	getter := frontierGetterFunc(
		func(context.Context, *url.URL) (*http.Response, error) {
			calls++
			if calls == 1 {
				header := http.Header{}
				header.Set("Location", "/next?secret=yes#private")
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     header,
					Body:       io.NopCloser(strings.NewReader("redirect")),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/html"},
				},
				Body: io.NopCloser(strings.NewReader("done")),
			}, nil
		},
	)
	crawler := mustNewMultiPageCrawler(
		t, getter, &frontierCandidateSink{}, frontierCrawlConfig(),
		&frontierWaiter{}, &frontierTimeoutFactory{},
	)
	pageURL, _ := url.Parse(source.String() + "/?root=private")
	page := crawler.fetchPage(
		context.Background(), source, pageURL, map[string]struct{}{},
	)
	if calls != 2 || page.attempt.RequestedURL != "https://source.example/" ||
		page.attempt.FinalURL != "https://source.example/next" ||
		strings.Contains(page.attempt.FinalURL, "secret") {
		t.Errorf("redirect telemetry = calls:%d %#v", calls, page.attempt)
	}
}

func TestMultiPageCrawlerFinalizesCandidateAdmissionAfterAllPages(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://source.example")
	getter := newFrontierGetter(map[string][]frontierStep{
		"https://source.example/": {
			frontierHTML(`<a href="/next">next</a><a href="https://z.example/">z</a>`),
		},
		"https://source.example/next": {
			frontierHTML(`<a href="https://a.example/">a</a>`),
		},
	})
	sink := &finalizingCandidateSink{}
	crawler, err := NewMultiPageCrawlerWithTelemetry(
		getter,
		sink,
		&recordingTelemetry{runID: 73},
		frontierCrawlConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crawler.Crawl(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if sink.runID != 73 {
		t.Fatalf("finalized run ID = %d, want 73", sink.runID)
	}
	if sink.recordedBatches != 2 {
		t.Fatalf("recorded batches before finalization = %d, want 2", sink.recordedBatches)
	}
	if !reflect.DeepEqual(sink.recordRunIDs, []CrawlRunID{73, 73}) {
		t.Fatalf("recorded run IDs = %#v, want [73 73]", sink.recordRunIDs)
	}
}

func TestMultiPageCrawlerReturnsCandidateFinalizationFailure(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://source.example")
	testError := errors.New("finalization failure")
	crawler, err := NewMultiPageCrawlerWithTelemetry(
		newFrontierGetter(map[string][]frontierStep{
			"https://source.example/": {frontierHTML("done")},
		}),
		&finalizingCandidateSink{err: testError},
		&recordingTelemetry{runID: 74},
		frontierCrawlConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crawler.Crawl(context.Background(), source); !errors.Is(err, testError) {
		t.Fatalf("Crawl() error = %v, want %v", err, testError)
	}
}

func TestMultiPageCrawlerPropagatesTelemetryFailures(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://source.example")
	testErr := errors.New("telemetry failure")
	tests := []struct {
		name      string
		telemetry *recordingTelemetry
	}{
		{"begin", &recordingTelemetry{beginErr: testErr}},
		{"record", &recordingTelemetry{runID: 1, recordErr: testErr}},
		{"finish", &recordingTelemetry{runID: 1, finishErr: testErr}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getter := newFrontierGetter(map[string][]frontierStep{
				"https://source.example/": {frontierHTML("done")},
			})
			crawler, err := NewMultiPageCrawlerWithTelemetry(
				getter, &frontierCandidateSink{}, test.telemetry, frontierCrawlConfig(),
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = crawler.Crawl(context.Background(), source)
			if !errors.Is(err, testErr) {
				t.Errorf("Crawl() error = %v, want telemetry failure", err)
			}
		})
	}
}

func TestMultiPageCrawlerTelemetryValidationAndSanitizing(t *testing.T) {
	_, err := NewMultiPageCrawlerWithTelemetry(
		newFrontierGetter(nil), &frontierCandidateSink{}, nil, frontierCrawlConfig(),
	)
	if !errors.Is(err, errCrawlTelemetryUnavailable) {
		t.Errorf("constructor error = %v", err)
	}

	crawler := &MultiPageCrawler{}
	crawler.getter = newFrontierGetter(nil)
	crawler.sink = &frontierCandidateSink{}
	crawler.config = frontierCrawlConfig()
	crawler.waiter = &frontierWaiter{}
	crawler.timeoutFactory = &frontierTimeoutFactory{}
	_, err = crawler.Crawl(context.Background(), mustDiscoveryOrigin(t, "https://source.example"))
	if !errors.Is(err, errCrawlTelemetryUnavailable) {
		t.Errorf("validation error = %v", err)
	}

	parsed, _ := url.Parse("https://user:pass@example.test/path?q=secret#fragment")
	if got := safeTelemetryURL(parsed); got != "https://example.test/path" {
		t.Errorf("safeTelemetryURL() = %q", got)
	}
	if got := safeTelemetryContentType("text/html; charset"); got != "" {
		t.Errorf("invalid media type = %q", got)
	}
}

type finalizingCandidateSink struct {
	recordedBatches int
	recordRunIDs    []CrawlRunID
	runID           CrawlRunID
	err             error
}

func (sink *finalizingCandidateSink) RecordCandidates(
	context.Context,
	origin.Origin,
	[]Candidate,
) error {
	sink.recordedBatches++
	return nil
}

func (sink *finalizingCandidateSink) RecordCandidatesForRun(
	_ context.Context,
	runID CrawlRunID,
	_ origin.Origin,
	_ []Candidate,
) error {
	sink.recordedBatches++
	sink.recordRunIDs = append(sink.recordRunIDs, runID)
	return nil
}

func (sink *finalizingCandidateSink) FinalizeCandidates(
	_ context.Context,
	runID CrawlRunID,
) error {
	sink.runID = runID
	return sink.err
}

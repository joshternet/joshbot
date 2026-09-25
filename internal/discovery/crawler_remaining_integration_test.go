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
)

type crawlerRemainingIntegrationGetterFunc func(
	context.Context,
	*url.URL,
) (*http.Response, error)

func (getter crawlerRemainingIntegrationGetterFunc) Get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	return getter(ctx, target)
}

type crawlerRemainingIntegrationTelemetry struct {
	runID             CrawlRunID
	summaryCalled     bool
	outcome           CrawlRunOutcome
	reason            string
	frontierRemaining int
}

func (telemetry *crawlerRemainingIntegrationTelemetry) BeginCrawl(
	context.Context,
	origin.Origin,
	CrawlConfig,
) (CrawlRunID, error) {
	return telemetry.runID, nil
}

func (*crawlerRemainingIntegrationTelemetry) RecordPageAttempt(
	context.Context,
	CrawlRunID,
	PageAttempt,
) error {
	return nil
}

func (telemetry *crawlerRemainingIntegrationTelemetry) FinishCrawl(
	_ context.Context,
	_ CrawlRunID,
	_ CrawlResult,
	outcome CrawlRunOutcome,
	reason string,
) error {
	telemetry.outcome = outcome
	telemetry.reason = reason

	return nil
}

func (telemetry *crawlerRemainingIntegrationTelemetry) FinishCrawlSummary(
	_ context.Context,
	_ CrawlRunID,
	_ CrawlResult,
	outcome CrawlRunOutcome,
	reason string,
	frontierRemaining int,
) error {
	telemetry.summaryCalled = true
	telemetry.outcome = outcome
	telemetry.reason = reason
	telemetry.frontierRemaining = frontierRemaining

	return nil
}

type crawlerRemainingIntegrationSink struct {
	recordErr   error
	finalizeErr error
	recordCalls int
	finalCalls  int
}

func (sink *crawlerRemainingIntegrationSink) RecordCandidates(
	context.Context,
	origin.Origin,
	[]Candidate,
) error {
	sink.recordCalls++

	return sink.recordErr
}

func (sink *crawlerRemainingIntegrationSink) FinalizeCandidates(
	context.Context,
	CrawlRunID,
) error {
	sink.finalCalls++

	return sink.finalizeErr
}

type crawlerRemainingIntegrationTimeoutFactory struct {
	calls      int
	cancelCall int
}

func (factory *crawlerRemainingIntegrationTimeoutFactory) WithTimeout(
	ctx context.Context,
	_ time.Duration,
) (context.Context, context.CancelFunc) {
	factory.calls++

	pageCtx, cancel := context.WithCancel(ctx)

	if factory.calls == factory.cancelCall {
		cancel()
	}

	return pageCtx, cancel
}

func TestCrawlerRemainingIntegrationRootFailureSummary(
	t *testing.T,
) {
	source := crawlerRemainingIntegrationOrigin(
		t,
		"https://source.example",
	)

	telemetry := &crawlerRemainingIntegrationTelemetry{}

	getter := crawlerRemainingIntegrationGetterFunc(
		func(
			context.Context,
			*url.URL,
		) (*http.Response, error) {
			return crawlerRemainingIntegrationResponse(
				http.StatusNotModified,
				"text/html",
				"",
			), nil
		},
	)

	crawler, err := NewMultiPageCrawlerWithTelemetry(
		getter,
		&crawlerRemainingIntegrationSink{},
		telemetry,
		crawlerRemainingIntegrationConfig(),
	)
	if err != nil {
		t.Fatalf(
			"NewMultiPageCrawlerWithTelemetry() error = %v",
			err,
		)
	}

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v",
			err,
		)
	}

	if result.PagesParsed != 0 {
		t.Errorf(
			"PagesParsed = %d, want 0",
			result.PagesParsed,
		)
	}

	if result.FailureCategory !=
		retry.CategoryUnsupportedOrigin {
		t.Errorf(
			"FailureCategory = %q, want %q",
			result.FailureCategory,
			retry.CategoryUnsupportedOrigin,
		)
	}

	if !telemetry.summaryCalled {
		t.Fatal(
			"FinishCrawlSummary() was not called",
		)
	}

	if telemetry.outcome != CrawlRunFailed ||
		telemetry.reason != "root_failure" {
		t.Errorf(
			"summary = outcome %q reason %q, want failed/root_failure",
			telemetry.outcome,
			telemetry.reason,
		)
	}
}

func TestCrawlerRemainingIntegrationVisitedFrontierAndBudget(
	t *testing.T,
) {
	source := crawlerRemainingIntegrationOrigin(
		t,
		"https://source.example",
	)

	var calls []string

	getter := crawlerRemainingIntegrationGetterFunc(
		func(
			ctx context.Context,
			target *url.URL,
		) (*http.Response, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			calls = append(
				calls,
				target.String(),
			)

			switch target.Path {
			case "/":
				return crawlerRemainingIntegrationResponse(
					http.StatusOK,
					"text/html",
					`<a href="/a">a</a>
<a href="/b">b</a>
<a href="/c">c</a>`,
				), nil

			case "/a":
				response :=
					crawlerRemainingIntegrationResponse(
						http.StatusFound,
						"text/html",
						"",
					)
				response.Header.Set(
					"Location",
					"/b",
				)

				return response, nil

			case "/b":
				return crawlerRemainingIntegrationResponse(
					http.StatusOK,
					"text/html",
					`<a href="/c">c</a>`,
				), nil

			default:
				return nil, errors.New(
					"unexpected crawler integration URL",
				)
			}
		},
	)

	config := crawlerRemainingIntegrationConfig()
	config.MaxDepth = 2
	config.MaxPages = 2

	crawler, err := NewMultiPageCrawlerWithTelemetry(
		getter,
		&crawlerRemainingIntegrationSink{},
		&crawlerRemainingIntegrationTelemetry{},
		config,
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v",
			err,
		)
	}

	if result.PagesAttempted != 2 {
		t.Errorf(
			"PagesAttempted = %d, want 2",
			result.PagesAttempted,
		)
	}

	if result.PagesParsed != 2 {
		t.Errorf(
			"PagesParsed = %d, want 2",
			result.PagesParsed,
		)
	}

	if !result.BudgetExhausted {
		t.Error(
			"BudgetExhausted = false, want true",
		)
	}

	wantCalls := []string{
		"https://source.example/",
		"https://source.example/a",
		"https://source.example/b",
	}

	if len(calls) != len(wantCalls) {
		t.Fatalf(
			"fetch calls = %#v, want %#v",
			calls,
			wantCalls,
		)
	}

	for index := range wantCalls {
		if calls[index] != wantCalls[index] {
			t.Errorf(
				"fetch calls = %#v, want %#v",
				calls,
				wantCalls,
			)
			break
		}
	}
}

func TestCrawlerRemainingIntegrationParentCancellation(
	t *testing.T,
) {
	source := crawlerRemainingIntegrationOrigin(
		t,
		"https://source.example",
	)

	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	getter := crawlerRemainingIntegrationGetterFunc(
		func(
			context.Context,
			*url.URL,
		) (*http.Response, error) {
			cancel()

			return crawlerRemainingIntegrationResponse(
				http.StatusOK,
				"text/html",
				"<html></html>",
			), nil
		},
	)

	crawler, err := NewMultiPageCrawlerWithTelemetry(
		getter,
		&crawlerRemainingIntegrationSink{},
		&crawlerRemainingIntegrationTelemetry{},
		crawlerRemainingIntegrationConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = crawler.Crawl(
		ctx,
		source,
	)
	if !errors.Is(
		err,
		context.Canceled,
	) {
		t.Errorf(
			"Crawl() error = %v, want context.Canceled",
			err,
		)
	}
}

func TestCrawlerRemainingIntegrationContinuesAfterPageTimeout(
	t *testing.T,
) {
	source := crawlerRemainingIntegrationOrigin(
		t,
		"https://source.example",
	)

	getter := crawlerRemainingIntegrationGetterFunc(
		func(
			ctx context.Context,
			target *url.URL,
		) (*http.Response, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			switch target.Path {
			case "/":
				return crawlerRemainingIntegrationResponse(
					http.StatusOK,
					"text/html",
					`<a href="/timeout">timeout</a>
<a href="/ok">ok</a>`,
				), nil

			case "/ok":
				return crawlerRemainingIntegrationResponse(
					http.StatusOK,
					"text/html",
					"<html></html>",
				), nil

			default:
				return nil, errors.New(
					"unexpected crawler integration URL",
				)
			}
		},
	)

	timeoutFactory :=
		&crawlerRemainingIntegrationTimeoutFactory{
			cancelCall: 2,
		}

	crawler, err := newMultiPageCrawler(
		getter,
		&crawlerRemainingIntegrationSink{},
		crawlerRemainingIntegrationConfig(),
		timerWaitStrategy{},
		timeoutFactory,
		&crawlerRemainingIntegrationTelemetry{},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := crawler.Crawl(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf(
			"Crawl() error = %v",
			err,
		)
	}

	if result.PagesAttempted != 3 ||
		result.PagesParsed != 2 {
		t.Errorf(
			"Crawl() = %#v, want 3 attempted and 2 parsed",
			result,
		)
	}
}

func TestCrawlerRemainingIntegrationCandidateStoreFailure(
	t *testing.T,
) {
	source := crawlerRemainingIntegrationOrigin(
		t,
		"https://source.example",
	)

	recordErr := errors.New(
		"integration candidate store failure",
	)

	sink := &crawlerRemainingIntegrationSink{
		recordErr: recordErr,
	}

	getter := crawlerRemainingIntegrationGetterFunc(
		func(
			context.Context,
			*url.URL,
		) (*http.Response, error) {
			return crawlerRemainingIntegrationResponse(
				http.StatusOK,
				"text/html",
				`<a href="https://candidate.example/path">candidate</a>`,
			), nil
		},
	)

	crawler, err := NewMultiPageCrawlerWithTelemetry(
		getter,
		sink,
		&crawlerRemainingIntegrationTelemetry{},
		crawlerRemainingIntegrationConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = crawler.Crawl(
		context.Background(),
		source,
	)
	if !errors.Is(err, recordErr) {
		t.Errorf(
			"Crawl() error = %v, want %v",
			err,
			recordErr,
		)
	}

	if sink.recordCalls != 1 {
		t.Errorf(
			"RecordCandidates() calls = %d, want 1",
			sink.recordCalls,
		)
	}
}

func TestCrawlerRemainingIntegrationCandidateFinalizationFailure(
	t *testing.T,
) {
	source := crawlerRemainingIntegrationOrigin(
		t,
		"https://source.example",
	)

	finalizeErr := errors.New(
		"integration candidate finalization failure",
	)

	sink := &crawlerRemainingIntegrationSink{
		finalizeErr: finalizeErr,
	}

	telemetry := &crawlerRemainingIntegrationTelemetry{
		runID: 73,
	}

	getter := crawlerRemainingIntegrationGetterFunc(
		func(
			context.Context,
			*url.URL,
		) (*http.Response, error) {
			return crawlerRemainingIntegrationResponse(
				http.StatusOK,
				"text/html",
				"<html></html>",
			), nil
		},
	)

	crawler, err := NewMultiPageCrawlerWithTelemetry(
		getter,
		sink,
		telemetry,
		crawlerRemainingIntegrationConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = crawler.Crawl(
		context.Background(),
		source,
	)
	if !errors.Is(err, finalizeErr) {
		t.Errorf(
			"Crawl() error = %v, want %v",
			err,
			finalizeErr,
		)
	}

	if sink.finalCalls != 1 {
		t.Errorf(
			"FinalizeCandidates() calls = %d, want 1",
			sink.finalCalls,
		)
	}
}

func crawlerRemainingIntegrationResponse(
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

func crawlerRemainingIntegrationConfig() CrawlConfig {
	return CrawlConfig{
		MaxDepth:                     2,
		MaxPages:                     8,
		MaxPageBytes:                 64 * 1024,
		RequestDelay:                 0,
		RedirectLimit:                2,
		PageTimeout:                  time.Second,
		MaxAutomaticPromotionsPerRun: 0,
	}
}

func crawlerRemainingIntegrationOrigin(
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

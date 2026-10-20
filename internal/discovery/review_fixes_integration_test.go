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

	"github.com/joshternet/joshbot/internal/retry"
)

type blockingCrawlFinish struct {
	recordingTelemetry
	entered chan context.Context
}

func (telemetry *blockingCrawlFinish) FinishCrawl(ctx context.Context, _ CrawlRunID, _ CrawlResult, _ CrawlRunOutcome, _ string) error {
	telemetry.entered <- ctx
	<-ctx.Done()
	return ctx.Err()
}

func TestCrawlFinalizationHasIndependentDeadline(t *testing.T) {
	telemetry := &blockingCrawlFinish{recordingTelemetry: recordingTelemetry{runID: 1}, entered: make(chan context.Context, 1)}
	crawler, err := NewMultiPageCrawlerWithTelemetry(newFrontierGetter(map[string][]frontierStep{"https://source.example/": {frontierHTML("done")}}), &frontierCandidateSink{}, telemetry, frontierCrawlConfig())
	if err != nil {
		t.Fatal(err)
	}
	crawler.finishTimeout = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := crawler.Crawl(ctx, mustDiscoveryOrigin(t, "https://source.example")); done <- err }()
	select {
	case finishContext := <-telemetry.entered:
		if _, ok := finishContext.Deadline(); !ok {
			t.Error("finalization has no deadline")
		}
		cancel()
	case <-time.After(time.Second):
		t.Fatal("finalization was not reached")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("finish error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled finalization did not return")
	}
}

func TestPartialCrawlRetainsTransientFailure(t *testing.T) {
	telemetry := &recordingTelemetry{runID: 1}
	getter := newFrontierGetter(map[string][]frontierStep{
		"https://source.example/":     {frontierHTML(`<a href="/next">next</a><a href="/last">last</a>`)},
		"https://source.example/next": {{status: 503, body: "busy"}, {status: 503, body: "busy"}, {status: 503, body: "busy"}},
		"https://source.example/last": {{status: 404}},
	})
	crawler, err := NewMultiPageCrawlerWithTelemetry(getter, &frontierCandidateSink{}, telemetry, frontierCrawlConfig())
	if err != nil {
		t.Fatal(err)
	}
	result, err := crawler.Crawl(context.Background(), mustDiscoveryOrigin(t, "https://source.example"))
	if err != nil || result.PagesParsed != 1 || len(telemetry.attempts) != 3 || result.FailureCategory != retry.CategoryHTTP5xx || telemetry.outcome != CrawlRunFailed || telemetry.stopReason != "partial_failure" {
		t.Fatalf("result=%#v outcome=%v/%v err=%v", result, telemetry.outcome, telemetry.stopReason, err)
	}
	if len(getter.calls) != 4 {
		t.Fatalf("network attempts = %v", getter.calls)
	}
}

func TestChildRetryAfterDefersRemainingFrontier(t *testing.T) {
	calls := 0
	getter := frontierGetterFunc(func(_ context.Context, target *url.URL) (*http.Response, error) {
		calls++
		response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(`<a href="/limited">limited</a><a href="/later">later</a>`))}
		if target.Path != "/" {
			if target.Path != "/limited" {
				t.Error("requested another page during Retry-After")
			}
			response.StatusCode = http.StatusTooManyRequests
			response.Header.Set("Retry-After", "86400")
		}
		return response, nil
	})
	telemetry := &recordingSummaryTelemetry{recordingTelemetry: recordingTelemetry{runID: 1}}
	crawler, err := NewMultiPageCrawlerWithTelemetry(getter, &frontierCandidateSink{}, telemetry, frontierCrawlConfig())
	if err != nil {
		t.Fatal(err)
	}
	result, err := crawler.Crawl(context.Background(), mustDiscoveryOrigin(t, "https://source.example"))
	if err != nil || calls != 2 || result.RetryAfter != retry.MaxDelay || result.FailureCategory != retry.CategoryHTTP429 || telemetry.outcome != CrawlRunFailed || telemetry.frontierRemaining != 1 {
		t.Fatalf("result=%#v calls=%d summary=%#v err=%v", result, calls, telemetry, err)
	}
}

func TestChildTimeoutExhaustionContinuesToHealthySibling(t *testing.T) {
	calls := 0
	getter := frontierGetterFunc(func(ctx context.Context, target *url.URL) (*http.Response, error) {
		calls++
		if target.Path == "/stalled" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		body := "done"
		if target.Path == "/" {
			body = `<a href="/stalled">stalled</a><a href="/healthy">healthy</a>`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	config := frontierCrawlConfig()
	config.PageTimeout = 5 * time.Millisecond
	telemetry := &recordingTelemetry{runID: 1}
	crawler, err := NewMultiPageCrawlerWithTelemetry(getter, &frontierCandidateSink{}, telemetry, config)
	if err != nil {
		t.Fatal(err)
	}
	result, err := crawler.Crawl(context.Background(), mustDiscoveryOrigin(t, "https://source.example"))
	if err != nil || calls != 4 || result.PagesAttempted != 3 || result.PagesParsed != 2 || result.FailureCategory != retry.CategoryTimeout || telemetry.outcome != CrawlRunFailed {
		t.Fatalf("crawl=%#v calls=%d outcome=%s err=%v", result, calls, telemetry.outcome, err)
	}
	if len(telemetry.attempts) != 3 || telemetry.attempts[1].FailureCategory != retry.CategoryTimeout || telemetry.attempts[2].FailureCategory != retry.CategoryNone {
		t.Fatalf("attempts=%#v", telemetry.attempts)
	}
}

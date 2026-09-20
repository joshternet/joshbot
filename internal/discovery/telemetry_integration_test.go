package discovery_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
)

type discoveryTelemetryIntegrationGetter struct {
	requests []string
}

func (getter *discoveryTelemetryIntegrationGetter) Get(
	_ context.Context,
	pageURL *url.URL,
) (*http.Response, error) {
	getter.requests = append(
		getter.requests,
		pageURL.String(),
	)

	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
	}

	switch pageURL.Path {
	case "/":
		response.Header.Set(
			"Content-Type",
			"text/html; charset=utf-8",
		)
		response.Body = io.NopCloser(
			strings.NewReader(
				`<!doctype html>
<html>
<body>
<a href="/next?token=private#section">Next</a>
<a href="/binary?token=private#section">Binary</a>
<a href="https://external.example/path?secret=yes#fragment">External</a>
</body>
</html>`,
			),
		)

	case "/next":
		response.Header.Set(
			"Content-Type",
			"text/html; charset=utf-8",
		)
		response.Body = io.NopCloser(
			strings.NewReader(
				`<!doctype html><html><body>done</body></html>`,
			),
		)

	case "/binary":
		response.Header.Set(
			"Content-Type",
			`text/html; charset="unterminated`,
		)
		response.Body = io.NopCloser(
			strings.NewReader("not html metadata"),
		)

	default:
		response.StatusCode = http.StatusNotFound
		response.Body = io.NopCloser(
			strings.NewReader("not found"),
		)
	}

	return response, nil
}

type discoveryTelemetryIntegrationSink struct {
	legacyCalls int
	runIDs      []discovery.CrawlRunID
	candidates  []discovery.Candidate
	finalized   []discovery.CrawlRunID
}

func (sink *discoveryTelemetryIntegrationSink) RecordCandidates(
	_ context.Context,
	_ origin.Origin,
	candidates []discovery.Candidate,
) error {
	sink.legacyCalls++

	sink.candidates = append(
		sink.candidates,
		candidates...,
	)

	return nil
}

func (sink *discoveryTelemetryIntegrationSink) RecordCandidatesForRun(
	_ context.Context,
	runID discovery.CrawlRunID,
	_ origin.Origin,
	candidates []discovery.Candidate,
) error {
	sink.runIDs = append(
		sink.runIDs,
		runID,
	)

	sink.candidates = append(
		sink.candidates,
		candidates...,
	)

	return nil
}

func (sink *discoveryTelemetryIntegrationSink) FinalizeCandidates(
	_ context.Context,
	runID discovery.CrawlRunID,
) error {
	sink.finalized = append(
		sink.finalized,
		runID,
	)

	return nil
}

type discoveryTelemetryIntegrationFinish struct {
	result  discovery.CrawlResult
	outcome discovery.CrawlRunOutcome
	reason  string
}

type discoveryTelemetryIntegrationRecorder struct {
	runID discovery.CrawlRunID

	begins   int
	attempts []discovery.PageAttempt
	finishes []discoveryTelemetryIntegrationFinish

	beginErr  error
	recordErr error
	finishErr error
}

func (recorder *discoveryTelemetryIntegrationRecorder) BeginCrawl(
	context.Context,
	origin.Origin,
	discovery.CrawlConfig,
) (discovery.CrawlRunID, error) {
	recorder.begins++

	if recorder.beginErr != nil {
		return 0, recorder.beginErr
	}

	return recorder.runID, nil
}

func (recorder *discoveryTelemetryIntegrationRecorder) RecordPageAttempt(
	_ context.Context,
	_ discovery.CrawlRunID,
	attempt discovery.PageAttempt,
) error {
	recorder.attempts = append(
		recorder.attempts,
		attempt,
	)

	return recorder.recordErr
}

func (recorder *discoveryTelemetryIntegrationRecorder) FinishCrawl(
	_ context.Context,
	_ discovery.CrawlRunID,
	result discovery.CrawlResult,
	outcome discovery.CrawlRunOutcome,
	reason string,
) error {
	recorder.finishes = append(
		recorder.finishes,
		discoveryTelemetryIntegrationFinish{
			result:  result,
			outcome: outcome,
			reason:  reason,
		},
	)

	return recorder.finishErr
}

type discoveryTelemetryIntegrationSummary struct {
	*discoveryTelemetryIntegrationRecorder

	summaries []struct {
		result            discovery.CrawlResult
		outcome           discovery.CrawlRunOutcome
		reason            string
		frontierRemaining int
	}
}

func (summary *discoveryTelemetryIntegrationSummary) FinishCrawlSummary(
	_ context.Context,
	_ discovery.CrawlRunID,
	result discovery.CrawlResult,
	outcome discovery.CrawlRunOutcome,
	reason string,
	frontierRemaining int,
) error {
	summary.summaries = append(
		summary.summaries,
		struct {
			result            discovery.CrawlResult
			outcome           discovery.CrawlRunOutcome
			reason            string
			frontierRemaining int
		}{
			result:            result,
			outcome:           outcome,
			reason:            reason,
			frontierRemaining: frontierRemaining,
		},
	)

	return summary.finishErr
}

func TestDiscoveryTelemetryIntegrationRecordsSanitizedRun(
	t *testing.T,
) {
	source := mustDiscoveryTelemetryIntegrationOrigin(
		t,
		"https://example.com",
	)

	getter := &discoveryTelemetryIntegrationGetter{}
	sink := &discoveryTelemetryIntegrationSink{}

	recorder := &discoveryTelemetryIntegrationRecorder{
		runID: 77,
	}

	telemetry := &discoveryTelemetryIntegrationSummary{
		discoveryTelemetryIntegrationRecorder: recorder,
	}

	crawler, err := discovery.NewMultiPageCrawlerWithTelemetry(
		getter,
		sink,
		telemetry,
		discovery.CrawlConfig{
			MaxDepth:                     1,
			MaxPages:                     4,
			MaxPageBytes:                 64 * 1024,
			RequestDelay:                 0,
			RedirectLimit:                5,
			PageTimeout:                  time.Second,
			MaxAutomaticPromotionsPerRun: 0,
		},
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

	if result.PagesAttempted != 3 ||
		result.PagesParsed != 2 ||
		result.CandidatesDiscovered != 1 ||
		result.BudgetExhausted {
		t.Errorf(
			"Crawl() result = %#v",
			result,
		)
	}

	if recorder.begins != 1 {
		t.Errorf(
			"BeginCrawl() calls = %d, want 1",
			recorder.begins,
		)
	}

	if len(recorder.attempts) != 3 {
		t.Fatalf(
			"page attempts = %d, want 3",
			len(recorder.attempts),
		)
	}

	for _, attempt := range recorder.attempts {
		if strings.Contains(
			attempt.RequestedURL,
			"token=private",
		) || strings.Contains(
			attempt.RequestedURL,
			"#section",
		) {
			t.Errorf(
				"telemetry URL exposed query or fragment: %q",
				attempt.RequestedURL,
			)
		}
	}

	var binaryAttempt *discovery.PageAttempt

	for index := range recorder.attempts {
		attempt := &recorder.attempts[index]

		if strings.HasSuffix(
			attempt.RequestedURL,
			"/binary",
		) {
			binaryAttempt = attempt
			break
		}
	}

	if binaryAttempt == nil {
		t.Fatal(
			"binary page telemetry was not recorded",
		)
	}

	if binaryAttempt.ContentType != "" {
		t.Errorf(
			"binary ContentType = %q, want empty sanitized value",
			binaryAttempt.ContentType,
		)
	}

	if binaryAttempt.Outcome !=
		discovery.PageUnsupportedContent {
		t.Errorf(
			"binary outcome = %q, want %q",
			binaryAttempt.Outcome,
			discovery.PageUnsupportedContent,
		)
	}

	if sink.legacyCalls != 0 {
		t.Errorf(
			"legacy candidate calls = %d, want 0",
			sink.legacyCalls,
		)
	}

	if len(sink.runIDs) != 1 ||
		sink.runIDs[0] != 77 {
		t.Errorf(
			"run candidate IDs = %#v, want [77]",
			sink.runIDs,
		)
	}

	if len(sink.candidates) != 1 ||
		sink.candidates[0].Origin.String() !=
			"https://external.example" {
		t.Errorf(
			"recorded candidates = %#v",
			sink.candidates,
		)
	}

	if len(sink.finalized) != 1 ||
		sink.finalized[0] != 77 {
		t.Errorf(
			"finalized run IDs = %#v, want [77]",
			sink.finalized,
		)
	}

	if len(recorder.finishes) != 0 {
		t.Errorf(
			"legacy FinishCrawl() calls = %d, want 0",
			len(recorder.finishes),
		)
	}

	if len(telemetry.summaries) != 1 {
		t.Fatalf(
			"summary finishes = %d, want 1",
			len(telemetry.summaries),
		)
	}

	summary := telemetry.summaries[0]

	if summary.outcome != discovery.CrawlRunComplete ||
		summary.reason != "frontier_exhausted" ||
		summary.frontierRemaining != 0 {
		t.Errorf(
			"crawl summary = %#v",
			summary,
		)
	}
}

func TestDiscoveryTelemetryIntegrationPropagatesTelemetryFailures(
	t *testing.T,
) {
	source := mustDiscoveryTelemetryIntegrationOrigin(
		t,
		"https://example.com",
	)

	newCrawler := func(
		t *testing.T,
		telemetry discovery.CrawlTelemetry,
	) *discovery.MultiPageCrawler {
		t.Helper()

		crawler, err :=
			discovery.NewMultiPageCrawlerWithTelemetry(
				&discoveryTelemetryIntegrationGetter{},
				&discoveryTelemetryIntegrationSink{},
				telemetry,
				discovery.CrawlConfig{
					MaxDepth:                     0,
					MaxPages:                     1,
					MaxPageBytes:                 64 * 1024,
					RequestDelay:                 0,
					RedirectLimit:                5,
					PageTimeout:                  time.Second,
					MaxAutomaticPromotionsPerRun: 0,
				},
			)
		if err != nil {
			t.Fatalf(
				"NewMultiPageCrawlerWithTelemetry() error = %v",
				err,
			)
		}

		return crawler
	}

	t.Run(
		"begin",
		func(t *testing.T) {
			expected := errors.New(
				"integration begin telemetry failure",
			)

			recorder :=
				&discoveryTelemetryIntegrationRecorder{
					runID:    1,
					beginErr: expected,
				}

			_, err := newCrawler(
				t,
				recorder,
			).Crawl(
				context.Background(),
				source,
			)

			if !errors.Is(
				err,
				expected,
			) {
				t.Fatalf(
					"Crawl() error = %v, want %v",
					err,
					expected,
				)
			}

			if len(recorder.finishes) != 0 {
				t.Errorf(
					"FinishCrawl() calls = %d, want 0",
					len(recorder.finishes),
				)
			}
		},
	)

	t.Run(
		"record",
		func(t *testing.T) {
			expected := errors.New(
				"integration record telemetry failure",
			)

			recorder :=
				&discoveryTelemetryIntegrationRecorder{
					runID:     2,
					recordErr: expected,
				}

			_, err := newCrawler(
				t,
				recorder,
			).Crawl(
				context.Background(),
				source,
			)

			if !errors.Is(
				err,
				expected,
			) {
				t.Fatalf(
					"Crawl() error = %v, want %v",
					err,
					expected,
				)
			}

			if len(recorder.finishes) != 1 {
				t.Fatalf(
					"FinishCrawl() calls = %d, want 1",
					len(recorder.finishes),
				)
			}

			finish := recorder.finishes[0]

			if finish.outcome !=
				discovery.CrawlRunFailed ||
				finish.reason != "crawler_error" {
				t.Errorf(
					"finish = %#v",
					finish,
				)
			}
		},
	)

	t.Run(
		"finish",
		func(t *testing.T) {
			expected := errors.New(
				"integration finish telemetry failure",
			)

			recorder :=
				&discoveryTelemetryIntegrationRecorder{
					runID:     3,
					finishErr: expected,
				}

			_, err := newCrawler(
				t,
				recorder,
			).Crawl(
				context.Background(),
				source,
			)

			if !errors.Is(
				err,
				expected,
			) {
				t.Fatalf(
					"Crawl() error = %v, want %v",
					err,
					expected,
				)
			}

			if len(recorder.finishes) != 1 {
				t.Fatalf(
					"FinishCrawl() calls = %d, want 1",
					len(recorder.finishes),
				)
			}

			finish := recorder.finishes[0]

			if finish.outcome !=
				discovery.CrawlRunBudgetExhausted ||
				finish.reason !=
					"crawl_budget" {
				t.Errorf(
					"finish = %#v",
					finish,
				)
			}
		},
	)
}

func mustDiscoveryTelemetryIntegrationOrigin(
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

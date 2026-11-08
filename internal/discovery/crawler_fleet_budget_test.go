package discovery

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/robots"
)

type fleetBudgetCrawlTelemetry struct {
	outcome CrawlRunOutcome
	reason  string
}

func (telemetry *fleetBudgetCrawlTelemetry) BeginCrawl(
	context.Context,
	origin.Origin,
	CrawlConfig,
) (CrawlRunID, error) {
	return 1, nil
}

func (telemetry *fleetBudgetCrawlTelemetry) RecordPageAttempt(
	context.Context,
	CrawlRunID,
	PageAttempt,
) error {
	return nil
}

func (telemetry *fleetBudgetCrawlTelemetry) FinishCrawl(
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

type fleetBudgetCandidateSink struct{}

func (fleetBudgetCandidateSink) RecordCandidates(
	context.Context,
	origin.Origin,
	[]Candidate,
) error {
	return nil
}

func TestCrawlStopsForFleetByteBudget(t *testing.T) {
	telemetry := &fleetBudgetCrawlTelemetry{}
	crawler, err := newMultiPageCrawler(
		frontierGetterFunc(func(context.Context, *url.URL) (*http.Response, error) {
			return nil, &robots.FleetByteBudgetError{RetryAfter: time.Hour}
		}),
		fleetBudgetCandidateSink{},
		frontierCrawlConfig(),
		timerWaitStrategy{},
		contextTimeoutFactory{},
		telemetry,
	)
	if err != nil {
		t.Fatal(err)
	}

	source, err := origin.Parse("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	_, err = crawler.Crawl(context.Background(), source)
	if !errors.Is(err, robots.ErrFleetByteBudget) {
		t.Fatalf("Crawl() error = %v, want the fleet byte budget", err)
	}
	if telemetry.outcome != CrawlRunCanceled || telemetry.reason != "fleet_byte_budget" {
		t.Fatalf(
			"crawl run = %s/%s, want canceled/fleet_byte_budget",
			telemetry.outcome,
			telemetry.reason,
		)
	}
}

package discovery

import (
	"context"

	"github.com/joshternet/joshbot/internal/origin"
)

type discardCrawlTelemetry struct{}

func (discardCrawlTelemetry) BeginCrawl(
	context.Context,
	origin.Origin,
	CrawlConfig,
) (CrawlRunID, error) {
	return 0, nil
}

func (discardCrawlTelemetry) RecordPageAttempt(
	context.Context,
	CrawlRunID,
	PageAttempt,
) error {
	return nil
}

func (discardCrawlTelemetry) FinishCrawl(
	context.Context,
	CrawlRunID,
	CrawlResult,
	CrawlRunOutcome,
	string,
) error {
	return nil
}

func NewMultiPageCrawler(
	getter Getter,
	sink CandidateSink,
	config CrawlConfig,
) (*MultiPageCrawler, error) {
	return newMultiPageCrawler(
		getter,
		sink,
		config,
		timerWaitStrategy{},
		contextTimeoutFactory{},
		discardCrawlTelemetry{},
	)
}

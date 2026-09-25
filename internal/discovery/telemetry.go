package discovery

import (
	"context"
	"errors"
	"mime"
	"net/url"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

var errCrawlTelemetryUnavailable = errors.New(
	"discovery: crawl telemetry unavailable",
)

// CrawlRunID identifies one durable crawl attempt.
type CrawlRunID int64

// CrawlRunOutcome is the bounded final state of a crawl run.
type CrawlRunOutcome string

const (
	CrawlRunComplete        CrawlRunOutcome = "complete"
	CrawlRunBudgetExhausted CrawlRunOutcome = "budget_exhausted"
	CrawlRunCanceled        CrawlRunOutcome = "canceled"
	CrawlRunFailed          CrawlRunOutcome = "failed"
)

// PageOutcome is the bounded result of one attempted frontier page.
type PageOutcome string

const (
	PageComplete           PageOutcome = "complete"
	PageRedirected         PageOutcome = "redirected"
	PageRobotsDenied       PageOutcome = "robots_denied"
	PageUnsupportedContent PageOutcome = "unsupported_content"
	PageTooLarge           PageOutcome = "too_large"
	PageHTTPError          PageOutcome = "http_error"
	PageTimeout            PageOutcome = "timeout"
	PageNetworkError       PageOutcome = "network_error"
	PageInvalidResponse    PageOutcome = "invalid_response"
)

// RobotsDecision records only the crawler-relevant robots result.
type RobotsDecision string

const (
	RobotsAllowed RobotsDecision = "allowed"
	RobotsDenied  RobotsDecision = "denied"
	RobotsUnknown RobotsDecision = "unknown"
)

// PageAttempt is safe, bounded request metadata without bodies or credentials.
type PageAttempt struct {
	Sequence          int
	RequestedURL      string
	FinalURL          string
	Depth             int
	StartedAt         time.Time
	Duration          time.Duration
	StatusCode        *int
	ResponseBytes     int64
	ContentType       string
	RedirectCount     int
	RobotsDecision    RobotsDecision
	InternalLinkCount int
	ExternalLinkCount int
	Outcome           PageOutcome
	FailureCategory   retry.Category
	URLsFound         int
	URLsEnqueued      int
}

// CrawlTelemetry persists crawl progress independently of crawl decisions.
type CrawlTelemetry interface {
	BeginCrawl(context.Context, origin.Origin, CrawlConfig) (CrawlRunID, error)
	RecordPageAttempt(context.Context, CrawlRunID, PageAttempt) error
	FinishCrawl(context.Context, CrawlRunID, CrawlResult, CrawlRunOutcome, string) error
}

type crawlSummaryTelemetry interface {
	FinishCrawlSummary(
		context.Context,
		CrawlRunID,
		CrawlResult,
		CrawlRunOutcome,
		string,
		int,
	) error
}

func safeTelemetryURL(pageURL *url.URL) string {
	safe := *pageURL
	safe.User = nil
	safe.RawQuery = ""
	safe.ForceQuery = false
	safe.Fragment = ""
	safe.RawFragment = ""
	return safe.String()
}

func safeTelemetryContentType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil || len(mediaType) > 256 {
		return ""
	}
	return mediaType
}

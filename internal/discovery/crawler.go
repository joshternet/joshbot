package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/joshternet/joshbot/internal/netguard"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/robots"
)

var (
	errMultiPageCrawlerUnavailable = errors.New(
		"discovery: multi-page crawler unavailable",
	)
	errCandidateSinkUnavailable = errors.New(
		"discovery: candidate sink unavailable",
	)
	errInvalidCrawlConfig = errors.New(
		"discovery: invalid crawl configuration",
	)
)

// CandidateSink persists origin-level candidates during a crawl.
type CandidateSink interface {
	RecordCandidates(
		context.Context,
		origin.Origin,
		[]Candidate,
	) error
}

// RunCandidateSink durably attributes candidate evidence to its crawl run.
type RunCandidateSink interface {
	RecordCandidatesForRun(
		context.Context,
		CrawlRunID,
		origin.Origin,
		[]Candidate,
	) error
}

// CandidateFinalizer performs admission only after every crawl page has had
// its discovery evidence persisted.
type CandidateFinalizer interface {
	FinalizeCandidates(context.Context, CrawlRunID) error
}

// CrawlConfig bounds one source crawl attempt.
type CrawlConfig struct {
	MaxDepth                     int
	MaxPages                     int
	MaxPageBytes                 int
	RequestDelay                 time.Duration
	RedirectLimit                int
	PageTimeout                  time.Duration
	MaxAutomaticPromotionsPerRun int
	RetryClock                   retry.Clock
}

// CrawlResult contains ephemeral statistics for one source crawl.
type CrawlResult struct {
	Source               origin.Origin
	PagesAttempted       int
	PagesParsed          int
	CandidatesDiscovered int
	BudgetExhausted      bool
	FailureCategory      retry.Category
	RetryAfter           time.Duration
}

// MultiPageCrawler performs a bounded breadth-first crawl of one origin.
type MultiPageCrawler struct {
	getter         Getter
	sink           CandidateSink
	config         CrawlConfig
	waiter         waitStrategy
	timeoutFactory timeoutFactory
	telemetry      CrawlTelemetry
	clock          retry.Clock
}

type crawlFrontierEntry struct {
	pageURL *url.URL
	depth   int
}

type crawledPage struct {
	links           PageLinks
	parsed          bool
	attempt         PageAttempt
	failureCategory retry.Category
	retryAfter      time.Duration
}

// NewMultiPageCrawlerWithTelemetry constructs a crawler that durably reports
// every run and attempted frontier page.
func NewMultiPageCrawlerWithTelemetry(
	getter Getter,
	sink CandidateSink,
	telemetry CrawlTelemetry,
	config CrawlConfig,
) (*MultiPageCrawler, error) {
	return newMultiPageCrawler(
		getter,
		sink,
		config,
		timerWaitStrategy{},
		contextTimeoutFactory{},
		telemetry,
	)
}

func newMultiPageCrawler(
	getter Getter,
	sink CandidateSink,
	config CrawlConfig,
	waiter waitStrategy,
	timeoutFactory timeoutFactory,
	telemetry CrawlTelemetry,
) (*MultiPageCrawler, error) {
	if getter == nil {
		return nil, errGetterUnavailable
	}

	if sink == nil {
		return nil, errCandidateSinkUnavailable
	}

	if !validCrawlConfig(config) {
		return nil, errInvalidCrawlConfig
	}

	if waiter == nil {
		return nil, errWaiterUnavailable
	}

	if timeoutFactory == nil {
		return nil, errTimeoutFactoryUnavailable
	}
	if telemetry == nil {
		return nil, errCrawlTelemetryUnavailable
	}

	return &MultiPageCrawler{
		getter:         getter,
		sink:           sink,
		config:         config,
		waiter:         waiter,
		timeoutFactory: timeoutFactory,
		telemetry:      telemetry,
		clock:          crawlRetryClock(config.RetryClock),
	}, nil
}

// Crawl performs one bounded breadth-first crawl of source.
//
// PagesAttempted counts frontier entries selected for fetching. Redirect hops
// do not consume additional frontier-page slots. Candidate batches are written
// as pages are processed rather than accumulated until crawl completion.
func (c *MultiPageCrawler) Crawl(
	ctx context.Context,
	source origin.Origin,
) (result CrawlResult, crawlErr error) {
	if err := c.validate(ctx, source); err != nil {
		return CrawlResult{}, err
	}

	result = CrawlResult{
		Source: source,
	}
	runID, err := c.telemetry.BeginCrawl(ctx, source, c.config)
	if err != nil {
		return result, fmt.Errorf("discovery: begin crawl telemetry: %w", err)
	}
	frontierRemaining := 0
	defer func() {
		outcome := CrawlRunComplete
		reason := "frontier_exhausted"
		if ctx.Err() != nil {
			outcome = CrawlRunCanceled
			reason = "context_canceled"
		} else if crawlErr != nil {
			outcome = CrawlRunFailed
			reason = "crawler_error"
		} else if result.BudgetExhausted {
			outcome = CrawlRunBudgetExhausted
			reason = "crawl_budget"
		}
		finishContext := context.WithoutCancel(ctx)
		var finishErr error
		if summaryTelemetry, ok := c.telemetry.(crawlSummaryTelemetry); ok {
			finishErr = summaryTelemetry.FinishCrawlSummary(
				finishContext, runID, result, outcome, reason, frontierRemaining,
			)
		} else {
			finishErr = c.telemetry.FinishCrawl(
				finishContext, runID, result, outcome, reason,
			)
		}
		if finishErr != nil && crawlErr == nil {
			crawlErr = fmt.Errorf(
				"discovery: finish crawl telemetry: %w",
				finishErr,
			)
		}
	}()

	root, _ := url.Parse(source.String() + "/")
	frontier := []crawlFrontierEntry{
		{
			pageURL: root,
			depth:   0,
		},
	}

	queued := map[string]struct{}{
		root.String(): {},
	}
	visited := make(map[string]struct{})
	candidateSeen := make(
		map[origin.Origin]struct{},
	)
	for len(frontier) > 0 {
		entry := frontier[0]
		frontier = frontier[1:]
		frontierRemaining = len(frontier)

		entryKey := entry.pageURL.String()
		if _, alreadyVisited :=
			visited[entryKey]; alreadyVisited {
			continue
		}

		if result.PagesAttempted >=
			c.config.MaxPages {
			result.BudgetExhausted = true
			frontierRemaining = len(frontier) + 1
			break
		}

		if result.PagesAttempted > 0 &&
			c.config.RequestDelay > 0 {
			if err := c.waiter.Wait(
				ctx,
				c.config.RequestDelay,
			); err != nil {
				return result, err
			}
		}

		result.PagesAttempted++

		var (
			page           crawledPage
			pageContextErr error
		)
		for attempt := 1; attempt <= retry.MaxAttemptsPerCycle; attempt++ {
			pageContext, cancelPage := c.timeoutFactory.WithTimeout(
				ctx,
				c.config.PageTimeout,
			)
			page = c.fetchPage(
				pageContext,
				source,
				entry.pageURL,
				visited,
			)
			pageContextErr = pageContext.Err()
			cancelPage()
			if result.PagesAttempted != 1 ||
				!page.failureCategory.Transient() {
				break
			}
		}
		page.attempt.Sequence = result.PagesAttempted
		page.attempt.Depth = entry.depth

		if parentErr := ctx.Err(); parentErr != nil {
			return result, parentErr
		}
		if result.PagesAttempted == 1 {
			result.FailureCategory = page.failureCategory
			result.RetryAfter = page.retryAfter
		}

		page.attempt.FailureCategory = page.failureCategory
		page.attempt.URLsFound = len(page.links.Internal) +
			len(page.links.Candidates)

		if page.parsed {
			nextDepth := entry.depth + 1
			for _, internal := range page.links.Internal {
				canonical := internal.String()
				_, alreadyVisited := visited[canonical]
				_, alreadyQueued := queued[canonical]
				if !alreadyVisited && !alreadyQueued &&
					nextDepth <= c.config.MaxDepth {
					page.attempt.URLsEnqueued++
				}
			}
		}

		if err := c.telemetry.RecordPageAttempt(
			ctx,
			runID,
			page.attempt,
		); err != nil {
			return result, fmt.Errorf(
				"discovery: record crawl telemetry: %w",
				err,
			)
		}

		if pageContextErr != nil {
			continue
		}

		if page.parsed {
			result.PagesParsed++
		}

		freshCandidates := make(
			[]Candidate,
			0,
			len(page.links.Candidates),
		)
		for _, candidate := range page.links.Candidates {
			if _, duplicate :=
				candidateSeen[candidate.Origin]; duplicate {
				continue
			}

			freshCandidates = append(
				freshCandidates,
				candidate,
			)
		}

		if len(freshCandidates) > 0 {
			recordErr := error(nil)
			if runSink, ok := c.sink.(RunCandidateSink); ok && runID > 0 {
				recordErr = runSink.RecordCandidatesForRun(
					ctx,
					runID,
					source,
					freshCandidates,
				)
			} else {
				recordErr = c.sink.RecordCandidates(
					ctx,
					source,
					freshCandidates,
				)
			}
			if recordErr != nil {
				return result, fmt.Errorf(
					"discovery: record candidates: %w",
					recordErr,
				)
			}

			for _, candidate := range freshCandidates {
				candidateSeen[candidate.Origin] =
					struct{}{}
			}

			result.CandidatesDiscovered +=
				len(freshCandidates)
		}

		if !page.parsed {
			continue
		}

		nextDepth := entry.depth + 1
		for _, internal := range page.links.Internal {
			canonical := internal.String()

			_, alreadyVisited := visited[canonical]
			_, alreadyQueued := queued[canonical]
			if alreadyVisited || alreadyQueued {
				continue
			}

			if nextDepth > c.config.MaxDepth {
				result.BudgetExhausted = true
				continue
			}

			queued[canonical] = struct{}{}
			frontier = append(
				frontier,
				crawlFrontierEntry{
					pageURL: internal,
					depth:   nextDepth,
				},
			)
		}
		frontierRemaining = len(frontier)
	}

	if runID > 0 {
		if finalizer, ok := c.sink.(CandidateFinalizer); ok {
			if err := finalizer.FinalizeCandidates(ctx, runID); err != nil {
				return result, fmt.Errorf(
					"discovery: finalize candidates: %w",
					err,
				)
			}
		}
	}
	if result.PagesParsed > 0 {
		result.FailureCategory = retry.CategoryNone
		result.RetryAfter = 0
	}

	return result, nil
}

func (c *MultiPageCrawler) fetchPage(
	ctx context.Context,
	source origin.Origin,
	initial *url.URL,
	visited map[string]struct{},
) crawledPage {
	started := c.clock.Now().UTC()
	current := canonicalPageURL(
		source,
		initial,
	)
	redirects := 0
	attempt := PageAttempt{
		RequestedURL:   safeTelemetryURL(current),
		FinalURL:       safeTelemetryURL(current),
		StartedAt:      started,
		RobotsDecision: RobotsUnknown,
		Outcome:        PageInvalidResponse,
	}
	finish := func(page crawledPage) crawledPage {
		page.attempt = attempt
		page.attempt.Duration = c.clock.Now().UTC().Sub(started)
		return page
	}

	for {
		visited[current.String()] = struct{}{}

		response, err := c.getter.Get(
			ctx,
			current,
		)
		if err != nil {
			category := retry.CategoryTransport
			if errors.Is(err, robots.ErrDisallowed) {
				attempt.RobotsDecision = RobotsDenied
				attempt.Outcome = PageRobotsDenied
				category = retry.CategoryRobotsDenied
			} else if ctx.Err() != nil {
				attempt.Outcome = PageTimeout
				category = retry.CategoryTimeout
			} else if errors.Is(err, robots.ErrTemporary) {
				attempt.Outcome = PageNetworkError
				category = retry.CategoryRobotsTemporary
			} else {
				attempt.Outcome = PageNetworkError
				category = netguard.FailureCategory(err)
				if category == retry.CategoryUnsupportedOrigin {
					category = retry.CategoryTransport
				}
			}
			return finish(crawledPage{failureCategory: category})
		}
		if response == nil || response.Body == nil {
			return finish(crawledPage{failureCategory: retry.CategoryTransport})
		}

		attempt.RobotsDecision = RobotsAllowed
		attempt.StatusCode = new(response.StatusCode)
		attempt.FinalURL = safeTelemetryURL(current)
		attempt.ContentType = safeTelemetryContentType(
			response.Header.Get("Content-Type"),
		)

		if isRedirectStatus(response.StatusCode) {
			next, redirectErr := redirectTarget(
				current,
				response,
			)
			_ = response.Body.Close()

			if redirectErr == nil {
				nextOrigin, parseErr := origin.Parse(
					next.String(),
				)
				if parseErr == nil {
					if nextOrigin != source {
						attempt.RedirectCount = redirects + 1
						attempt.FinalURL = safeTelemetryURL(next)
						attempt.Outcome = PageRedirected
						return finish(crawledPage{
							links: PageLinks{
								Candidates: []Candidate{
									{
										Origin: nextOrigin,
										Kind:   KindRedirect,
									},
								},
							},
						})
					}

					next = canonicalPageURL(
						source,
						next,
					)
					_, alreadyVisited :=
						visited[next.String()]

					if redirects <
						c.config.RedirectLimit &&
						!alreadyVisited {
						redirects++
						current = next
						attempt.RedirectCount = redirects
						attempt.FinalURL = safeTelemetryURL(current)
						continue
					}
				}
			}

			return finish(crawledPage{failureCategory: retry.CategoryMalformedOrigin})
		}

		body, tooLarge, readErr := readBounded(
			response.Body,
			c.config.MaxPageBytes,
		)
		closeErr := response.Body.Close()
		attempt.ResponseBytes = int64(len(body))

		if response.StatusCode < http.StatusOK ||
			response.StatusCode >= http.StatusMultipleChoices {
			attempt.Outcome = PageHTTPError
			category, transient := retry.HTTPStatusCategory(response.StatusCode)
			if !transient {
				category = retry.CategoryUnsupportedOrigin
			}
			return finish(crawledPage{
				failureCategory: category,
				retryAfter: retry.ParseRetryAfter(
					response.Header.Get("Retry-After"),
					c.clock.Now().UTC(),
				),
			})
		}
		if readErr != nil || closeErr != nil {
			attempt.Outcome = PageNetworkError
			return finish(crawledPage{failureCategory: retry.CategoryTransport})
		}
		if tooLarge {
			attempt.Outcome = PageTooLarge
			return finish(crawledPage{failureCategory: retry.CategoryOversizedContent})
		}

		links, status, _ :=
			ExtractPageLinks(
				source,
				current,
				response.Header.Get(
					"Content-Type",
				),
				body,
			)
		if status != StatusComplete {
			attempt.Outcome = PageUnsupportedContent
			return finish(crawledPage{failureCategory: retry.CategoryUnsupportedContent})
		}

		attempt.InternalLinkCount = len(links.Internal)
		attempt.ExternalLinkCount = len(links.Candidates)
		attempt.Outcome = PageComplete

		return finish(crawledPage{
			links:  links,
			parsed: true,
		})
	}
}

func (c *MultiPageCrawler) validate(
	ctx context.Context,
	source origin.Origin,
) error {
	if c == nil {
		return errMultiPageCrawlerUnavailable
	}

	if ctx == nil {
		return errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if source.String() == "" {
		return errInvalidSource
	}

	if c.getter == nil {
		return errGetterUnavailable
	}

	if c.sink == nil {
		return errCandidateSinkUnavailable
	}

	if !validCrawlConfig(c.config) {
		return errInvalidCrawlConfig
	}

	if c.waiter == nil {
		return errWaiterUnavailable
	}

	if c.timeoutFactory == nil {
		return errTimeoutFactoryUnavailable
	}

	if c.telemetry == nil {
		return errCrawlTelemetryUnavailable
	}

	return nil
}

func validCrawlConfig(
	config CrawlConfig,
) bool {
	return config.MaxDepth >= 0 &&
		config.MaxPages > 0 &&
		config.MaxPageBytes > 0 &&
		config.RequestDelay >= 0 &&
		config.RedirectLimit > 0 &&
		config.PageTimeout > 0 &&
		config.MaxAutomaticPromotionsPerRun >= 0
}

func crawlRetryClock(clock retry.Clock) retry.Clock {
	if clock != nil {
		return clock
	}
	return retry.SystemClock{}
}

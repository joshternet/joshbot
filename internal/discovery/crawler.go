package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
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

// CrawlConfig bounds one source crawl attempt.
type CrawlConfig struct {
	MaxDepth      int
	MaxPages      int
	MaxPageBytes  int
	RequestDelay  time.Duration
	RedirectLimit int
	PageTimeout   time.Duration
}

// CrawlResult contains ephemeral statistics for one source crawl.
type CrawlResult struct {
	Source               origin.Origin
	PagesAttempted       int
	PagesParsed          int
	CandidatesDiscovered int
	BudgetExhausted      bool
}

// MultiPageCrawler performs a bounded breadth-first crawl of one origin.
type MultiPageCrawler struct {
	getter         Getter
	sink           CandidateSink
	config         CrawlConfig
	waiter         waitStrategy
	timeoutFactory timeoutFactory
}

type crawlFrontierEntry struct {
	pageURL *url.URL
	depth   int
}

type crawledPage struct {
	links  PageLinks
	parsed bool
}

// NewMultiPageCrawler constructs a bounded multi-page crawler.
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
	)
}

func newMultiPageCrawler(
	getter Getter,
	sink CandidateSink,
	config CrawlConfig,
	waiter waitStrategy,
	timeoutFactory timeoutFactory,
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

	return &MultiPageCrawler{
		getter:         getter,
		sink:           sink,
		config:         config,
		waiter:         waiter,
		timeoutFactory: timeoutFactory,
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
) (CrawlResult, error) {
	if err := c.validate(ctx, source); err != nil {
		return CrawlResult{}, err
	}

	result := CrawlResult{
		Source: source,
	}

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

		entryKey := entry.pageURL.String()
		if _, alreadyVisited :=
			visited[entryKey]; alreadyVisited {
			continue
		}

		if result.PagesAttempted >=
			c.config.MaxPages {
			result.BudgetExhausted = true
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

		pageContext, cancelPage :=
			c.timeoutFactory.WithTimeout(
				ctx,
				c.config.PageTimeout,
			)

		page := c.fetchPage(
			pageContext,
			source,
			entry.pageURL,
			visited,
		)
		pageContextErr := pageContext.Err()
		cancelPage()

		if parentErr := ctx.Err(); parentErr != nil {
			return result, parentErr
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
			if err := c.sink.RecordCandidates(
				ctx,
				source,
				freshCandidates,
			); err != nil {
				return result, fmt.Errorf(
					"discovery: record candidates: %w",
					err,
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
	}

	return result, nil
}

func (c *MultiPageCrawler) fetchPage(
	ctx context.Context,
	source origin.Origin,
	initial *url.URL,
	visited map[string]struct{},
) crawledPage {
	current := canonicalPageURL(
		source,
		initial,
	)
	redirects := 0

	for {
		visited[current.String()] = struct{}{}

		response, err := c.getter.Get(
			ctx,
			current,
		)
		if err != nil ||
			response == nil ||
			response.Body == nil {
			return crawledPage{}
		}

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
						return crawledPage{
							links: PageLinks{
								Candidates: []Candidate{
									{
										Origin: nextOrigin,
										Kind:   KindRedirect,
									},
								},
							},
						}
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
						continue
					}
				}
			}

			return crawledPage{}
		}

		body, tooLarge, readErr := readBounded(
			response.Body,
			c.config.MaxPageBytes,
		)
		closeErr := response.Body.Close()

		if response.StatusCode < http.StatusOK ||
			response.StatusCode >=
				http.StatusMultipleChoices ||
			readErr != nil ||
			closeErr != nil ||
			tooLarge {
			return crawledPage{}
		}

		links, status, extractErr :=
			ExtractPageLinks(
				source,
				current,
				response.Header.Get(
					"Content-Type",
				),
				body,
			)
		if extractErr != nil ||
			status != StatusComplete {
			return crawledPage{}
		}

		return crawledPage{
			links:  links,
			parsed: true,
		}
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
		config.PageTimeout > 0
}

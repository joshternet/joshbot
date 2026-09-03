// Package discovery performs bounded homepage candidate discovery.
package discovery

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/robots"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	MaxRawBody     = 1024 * 1024
	MaxDecodedBody = 2 * 1024 * 1024
	MaxCandidates  = 64
	MaxRedirects   = 5
)

var (
	errCrawlerUnavailable = errors.New("discovery: crawler unavailable")
	errGetterUnavailable  = errors.New("discovery: getter unavailable")
	errInvalidContext     = errors.New("discovery: invalid context")
	errInvalidSource      = errors.New("discovery: invalid source")
	errInvalidPageURL     = errors.New("discovery: invalid page URL")
)

// Kind describes how a candidate was discovered.
type Kind uint8

const (
	KindLink Kind = iota + 1
	KindRedirect
)

// Status describes the result of one homepage attempt.
type Status uint8

const (
	StatusComplete Status = iota + 1
	StatusRedirected
	StatusRobotsDenied
	StatusUnavailable
	StatusUnsupportedContent
	StatusTooLarge
)

// Candidate is one canonical origin worth independently probing.
type Candidate struct {
	Origin origin.Origin
	Kind   Kind
}

// Result is the bounded semantic result of one homepage attempt.
type Result struct {
	Source     origin.Origin
	Status     Status
	Candidates []Candidate
	Truncated  bool
}

// RecordResult reports discovery persistence admission.
type RecordResult struct {
	Accepted int
	Dropped  int
}

// Getter is the robots-aware guarded HTTP behavior used by Crawler.
type Getter interface {
	Get(context.Context, *url.URL) (*http.Response, error)
}

// Crawler fetches and parses one verified origin's homepage.
type Crawler struct {
	getter Getter
}

// NewCrawler constructs a homepage crawler.
func NewCrawler(getter Getter) *Crawler {
	return &Crawler{getter: getter}
}

// Discover fetches only the canonical homepage and bounded same-origin
// redirects. A cross-origin redirect becomes one candidate and is not fetched.
func (c *Crawler) Discover(
	ctx context.Context,
	source origin.Origin,
) (Result, error) {
	result := Result{Source: source}

	if c == nil {
		return Result{}, errCrawlerUnavailable
	}

	if ctx == nil {
		return Result{}, errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	if source.String() == "" {
		return Result{}, errInvalidSource
	}

	if c.getter == nil {
		return Result{}, errGetterUnavailable
	}

	// A validated canonical origin always produces a parseable root URL.
	current, _ := url.Parse(source.String() + "/")
	redirects := 0

	for {
		response, requestErr := c.getter.Get(ctx, current)
		if requestErr != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return Result{}, contextErr
			}

			if errors.Is(requestErr, robots.ErrDisallowed) {
				result.Status = StatusRobotsDenied
				return result, nil
			}

			result.Status = StatusUnavailable
			return result, nil
		}

		if response == nil || response.Body == nil {
			result.Status = StatusUnavailable
			return result, nil
		}

		if isRedirectStatus(response.StatusCode) {
			next, redirectErr := redirectTarget(current, response)
			closeErr := response.Body.Close()

			if redirectErr != nil || closeErr != nil {
				result.Status = StatusUnavailable
				return result, nil
			}

			// redirectTarget already validates the resolved origin.
			nextOrigin, _ := origin.Parse(next.String())
			if nextOrigin != source {
				result.Status = StatusRedirected
				result.Candidates = []Candidate{
					{
						Origin: nextOrigin,
						Kind:   KindRedirect,
					},
				}
				return result, nil
			}

			if redirects >= MaxRedirects {
				result.Status = StatusUnavailable
				return result, nil
			}

			redirects++
			current = next
			continue
		}

		if response.StatusCode < http.StatusOK ||
			response.StatusCode >= http.StatusMultipleChoices {
			_ = response.Body.Close()
			result.Status = StatusUnavailable
			return result, nil
		}

		contentType := response.Header.Get("Content-Type")
		if contentType != "" && !isHTMLContentType(contentType) {
			_ = response.Body.Close()
			result.Status = StatusUnsupportedContent
			return result, nil
		}

		body, tooLarge, readErr := readBounded(
			response.Body,
			MaxRawBody,
		)
		closeErr := response.Body.Close()

		if contextErr := ctx.Err(); contextErr != nil {
			return Result{}, contextErr
		}

		if readErr != nil || closeErr != nil {
			result.Status = StatusUnavailable
			return result, nil
		}

		if tooLarge {
			result.Status = StatusTooLarge
			return result, nil
		}

		// The source and page URL satisfy Extract's validated input contract.
		extracted, _ := Extract(
			source,
			current,
			contentType,
			body,
		)
		return extracted, nil
	}
}

// Extract parses a bounded HTML document without executing remote content.
func Extract(
	source origin.Origin,
	pageURL *url.URL,
	contentType string,
	body []byte,
) (Result, error) {
	return extract(
		source,
		pageURL,
		contentType,
		body,
		decodeHTML,
	)
}

func extract(
	source origin.Origin,
	pageURL *url.URL,
	contentType string,
	body []byte,
	decoder func(io.Reader, string) ([]byte, bool, error),
) (Result, error) {
	result := Result{
		Source: source,
		Status: StatusComplete,
	}

	if source.String() == "" {
		return Result{}, errInvalidSource
	}

	if pageURL == nil {
		return Result{}, errInvalidPageURL
	}

	pageOrigin, err := origin.Parse(pageURL.String())
	if err != nil || pageOrigin != source {
		return Result{}, errInvalidPageURL
	}

	if len(body) > MaxRawBody {
		result.Status = StatusTooLarge
		return result, nil
	}

	effectiveContentType := contentType
	if effectiveContentType == "" {
		effectiveContentType = http.DetectContentType(body)
	}

	if !isHTMLContentType(effectiveContentType) {
		result.Status = StatusUnsupportedContent
		return result, nil
	}

	decoded, tooLarge, err := decoder(
		bytes.NewReader(body),
		effectiveContentType,
	)
	if err != nil {
		result.Status = StatusUnavailable
		return result, nil
	}

	if tooLarge {
		result.Status = StatusTooLarge
		return result, nil
	}

	// bytes.Reader cannot return a parsing read error.
	document, _ := html.Parse(bytes.NewReader(decoded))
	baseURL := documentBaseURL(document, pageURL)

	seen := make(
		map[origin.Origin]struct{},
		MaxCandidates,
	)
	selected := make(
		[]Candidate,
		0,
		MaxCandidates,
	)

	for node := range document.Descendants() {
		if node.Type != html.ElementNode ||
			(node.Data != "a" && node.Data != "area") {
			continue
		}

		href, found := attributeValue(node, "href")
		if !found {
			continue
		}

		reference, parseErr := url.Parse(
			strings.TrimSpace(href),
		)
		if parseErr != nil {
			continue
		}

		resolved := baseURL.ResolveReference(reference)
		candidateOrigin, parseErr := origin.Parse(
			resolved.String(),
		)
		if parseErr != nil || candidateOrigin == source {
			continue
		}

		if _, duplicate := seen[candidateOrigin]; duplicate {
			continue
		}

		if len(selected) == MaxCandidates {
			result.Truncated = true
			continue
		}

		seen[candidateOrigin] = struct{}{}
		selected = append(
			selected,
			Candidate{
				Origin: candidateOrigin,
				Kind:   KindLink,
			},
		)
	}

	sort.Slice(
		selected,
		func(left int, right int) bool {
			return selected[left].Origin.String() <
				selected[right].Origin.String()
		},
	)

	result.Candidates = selected
	return result, nil
}

func decodeHTML(
	reader io.Reader,
	contentType string,
) ([]byte, bool, error) {
	decodedReader, err := charset.NewReader(
		reader,
		contentType,
	)
	if err != nil {
		return nil, false, err
	}

	return readBounded(
		decodedReader,
		MaxDecodedBody,
	)
}

func readBounded(
	reader io.Reader,
	limit int,
) ([]byte, bool, error) {
	data, err := io.ReadAll(
		io.LimitReader(
			reader,
			int64(limit)+1,
		),
	)
	if err != nil {
		return nil, false, err
	}

	if len(data) > limit {
		return nil, true, nil
	}

	return data, false, nil
}

func isRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func redirectTarget(
	current *url.URL,
	response *http.Response,
) (*url.URL, error) {
	location := response.Header.Get("Location")
	if location == "" {
		return nil, errInvalidPageURL
	}

	reference, err := url.Parse(location)
	if err != nil {
		return nil, errInvalidPageURL
	}

	next := current.ResolveReference(reference)
	next.Fragment = ""
	next.RawFragment = ""

	if _, err := origin.Parse(next.String()); err != nil {
		return nil, errInvalidPageURL
	}

	return next, nil
}

func isHTMLContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}

	switch strings.ToLower(mediaType) {
	case "text/html", "application/xhtml+xml":
		return true
	default:
		return false
	}
}

func documentBaseURL(
	document *html.Node,
	pageURL *url.URL,
) *url.URL {
	baseURL := cloneURL(pageURL)

	for node := range document.Descendants() {
		if node.Type != html.ElementNode ||
			node.Data != "base" {
			continue
		}

		href, found := attributeValue(node, "href")
		if !found {
			continue
		}

		reference, err := url.Parse(
			strings.TrimSpace(href),
		)
		if err != nil {
			return baseURL
		}

		resolved := pageURL.ResolveReference(reference)
		if _, err := origin.Parse(resolved.String()); err != nil {
			return baseURL
		}

		return resolved
	}

	return baseURL
}

func attributeValue(
	node *html.Node,
	name string,
) (string, bool) {
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return attribute.Val, true
		}
	}

	return "", false
}

func cloneURL(source *url.URL) *url.URL {
	cloned := *source
	return &cloned
}

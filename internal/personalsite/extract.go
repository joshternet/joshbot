package personalsite

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	maxRawBody     = 1024 * 1024
	maxDecodedBody = 2 * 1024 * 1024
)

// parseHTMLDocument is html.Parse; tests may replace it to force failures.
var parseHTMLDocument = html.Parse

// decodeHTMLBody decodes an HTML body; tests may replace it to force failures.
var decodeHTMLBody = decodeHTML

// newCharsetReader creates a charset-aware reader; tests may replace it.
var newCharsetReader = charset.NewReader

// ExtractSignals derives bounded structural booleans from one HTML page.
//
// It never executes scripts, never loads external resources, and never
// returns page text. Malformed HTML yields ExtractStatusUnavailable without
// panicking. Oversized bodies are truncated to the configured bounds and
// still assessed; truncated is true when a prefix was used.
//
// @param pageURL - Canonical URL of the page being inspected.
// @param contentType - Declared Content-Type, or empty to sniff.
// @param body - Raw response body bytes.
// @returns SiteSignals, ExtractStatus, whether input was truncated, and a
// non-nil error only for invalid arguments.
func ExtractSignals(
	pageURL *url.URL,
	contentType string,
	body []byte,
) (SiteSignals, ExtractStatus, bool, error) {
	if pageURL == nil {
		return SiteSignals{}, 0, false, errInvalidPageURL
	}

	truncated := false
	if len(body) > maxRawBody {
		body = body[:maxRawBody]
		truncated = true
	}

	effectiveContentType := contentType
	if effectiveContentType == "" {
		effectiveContentType = http.DetectContentType(body)
	}

	if !isHTMLContentType(effectiveContentType) {
		return SiteSignals{},
			ExtractStatusUnsupportedContent,
			truncated,
			nil
	}

	decoded, decodeTruncated, decodeErr := decodeHTMLBody(
		body,
		effectiveContentType,
	)
	if decodeErr != nil {
		return SiteSignals{}, ExtractStatusUnavailable, truncated, nil
	}
	if decodeTruncated {
		truncated = true
	}

	document, parseErr := parseHTMLDocument(
		bytes.NewReader(decoded),
	)
	if parseErr != nil {
		return SiteSignals{}, ExtractStatusUnavailable, truncated, nil
	}

	baseURL := documentBaseURL(document, pageURL)
	signals := SiteSignals{}

	for node := range document.Descendants() {
		if node.Type != html.ElementNode {
			continue
		}

		switch strings.ToLower(node.Data) {
		case "a", "area":
			inspectAnchor(node, baseURL, &signals)
		case "link":
			inspectLink(node, &signals)
		case "meta":
			inspectMeta(node, &signals)
		case "script":
			inspectScript(node, &signals)
		case "div", "article", "section", "address", "p", "span", "header", "footer", "main":
			inspectMicroformat(node, &signals)
		}
	}

	return signals, ExtractStatusComplete, truncated, nil
}

func inspectAnchor(
	node *html.Node,
	baseURL *url.URL,
	signals *SiteSignals,
) {
	if classContains(node, "h-card") {
		signals.HasHCard = true
	}

	if relContains(node, "me") {
		signals.HasRelMe = true
	}
	if relContains(node, "author") {
		signals.HasAuthorMetadata = true
	}

	href, found := attributeValue(node, "href")
	if !found {
		return
	}

	reference, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return
	}

	resolved := baseURL.ResolveReference(reference)
	path := strings.ToLower(strings.TrimSuffix(resolved.Path, "/"))
	if path == "" {
		path = "/"
	}

	classifyPath(path, signals)
	classifyParkedHost(resolved.Hostname(), signals)
}

func inspectLink(node *html.Node, signals *SiteSignals) {
	if relContains(node, "me") {
		signals.HasRelMe = true
	}
	if relContains(node, "author") {
		signals.HasAuthorMetadata = true
	}

	relAlternate := relContains(node, "alternate")
	linkType, _ := attributeValue(node, "type")
	mediaType, _, _ := mime.ParseMediaType(linkType)
	mediaType = strings.ToLower(mediaType)
	if relAlternate &&
		(mediaType == "application/rss+xml" ||
			mediaType == "application/atom+xml" ||
			mediaType == "application/feed+json" ||
			mediaType == "application/json") {
		signals.HasFeed = true
	}

	href, found := attributeValue(node, "href")
	if !found {
		return
	}
	path := strings.ToLower(strings.TrimSuffix(
		strings.TrimSpace(href),
		"/",
	))
	classifyPath(path, signals)
}

func inspectMeta(node *html.Node, signals *SiteSignals) {
	name, _ := attributeValue(node, "name")
	property, _ := attributeValue(node, "property")
	content, _ := attributeValue(node, "content")

	lowerName := strings.ToLower(name)
	lowerProperty := strings.ToLower(property)
	lowerContent := strings.ToLower(content)

	if lowerName == "author" && strings.TrimSpace(content) != "" {
		signals.HasAuthorMetadata = true
	}

	if (lowerName == "robots" || lowerName == "googlebot") &&
		strings.Contains(lowerContent, "noindex") &&
		(strings.Contains(lowerContent, "parked") ||
			strings.Contains(lowerContent, "forsale")) {
		signals.HasParkedDomain = true
	}

	if lowerProperty == "og:type" &&
		(lowerContent == "product" || lowerContent == "product.group") {
		signals.HasProductSchema = true
	}
}

func inspectScript(node *html.Node, signals *SiteSignals) {
	scriptType, _ := attributeValue(node, "type")
	mediaType, _, _ := mime.ParseMediaType(scriptType)
	if strings.ToLower(mediaType) != "application/ld+json" {
		return
	}

	raw := strings.TrimSpace(nodeText(node))
	if raw == "" {
		return
	}

	applyJSONLDTypes(raw, signals)
}

func inspectMicroformat(node *html.Node, signals *SiteSignals) {
	if classContains(node, "h-card") {
		signals.HasHCard = true
	}
}

func classifyPath(path string, signals *SiteSignals) {
	normalized := path
	if strings.HasPrefix(normalized, "http://") ||
		strings.HasPrefix(normalized, "https://") {
		parsed, err := url.Parse(normalized)
		if err != nil {
			return
		}
		normalized = strings.ToLower(
			strings.TrimSuffix(parsed.Path, "/"),
		)
	}
	if !strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}

	switch {
	case pathMatches(normalized, "/about", "/about-me", "/me", "/bio", "/profile"):
		signals.HasAboutLink = true
	case pathMatches(normalized, "/now"):
		signals.HasNowLink = true
	case pathMatches(normalized, "/uses", "/usesthis"):
		signals.HasUsesLink = true
	case pathMatches(normalized, "/projects", "/project", "/portfolio", "/work"):
		signals.HasProjectsLink = true
	case pathMatches(normalized, "/blog", "/writing", "/posts", "/journal", "/notes"):
		signals.HasBlogLink = true
	case pathMatches(normalized, "/cart", "/basket", "/checkout", "/bag"):
		signals.HasShoppingCart = true
	case pathMatches(normalized, "/careers", "/jobs", "/job-openings", "/join-us"):
		signals.HasCareersLink = true
	case pathMatches(normalized, "/investors", "/investor", "/investor-relations", "/ir"):
		signals.HasInvestorLink = true
	case pathMatches(normalized, "/shop", "/store", "/marketplace", "/catalog", "/products", "/collections"):
		signals.HasMarketplaceSignals = true
	}
}

func pathMatches(path string, candidates ...string) bool {
	for _, candidate := range candidates {
		if path == candidate || strings.HasPrefix(path, candidate+"/") {
			return true
		}
	}
	return false
}

func classifyParkedHost(host string, signals *SiteSignals) {
	lower := strings.ToLower(host)
	switch {
	case strings.Contains(lower, "sedo.com"),
		strings.Contains(lower, "godaddy.com"),
		strings.Contains(lower, "dan.com"),
		strings.Contains(lower, "afternic.com"),
		strings.Contains(lower, "hugedomains.com"):
		signals.HasParkedDomain = true
	}
}

func applyJSONLDTypes(raw string, signals *SiteSignals) {
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return
	}
	walkJSONLD(payload, signals)
}

func walkJSONLD(value any, signals *SiteSignals) {
	switch typed := value.(type) {
	case map[string]any:
		if types, ok := typed["@type"]; ok {
			markSchemaTypes(types, signals)
		}
		if graph, ok := typed["@graph"]; ok {
			walkJSONLD(graph, signals)
		}
		for key, nested := range typed {
			if key == "@type" || key == "@graph" {
				continue
			}
			walkJSONLD(nested, signals)
		}
	case []any:
		for _, item := range typed {
			walkJSONLD(item, signals)
		}
	}
}

func markSchemaTypes(value any, signals *SiteSignals) {
	switch typed := value.(type) {
	case string:
		markOneSchemaType(typed, signals)
	case []any:
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				continue
			}
			markOneSchemaType(text, signals)
		}
	}
}

func markOneSchemaType(raw string, signals *SiteSignals) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.TrimPrefix(normalized, "http://schema.org/")
	normalized = strings.TrimPrefix(normalized, "https://schema.org/")
	normalized = strings.TrimPrefix(normalized, "schema:")

	switch normalized {
	case "person":
		signals.HasPersonSchema = true
	case "organization", "corporation", "localbusiness", "ngo", "governmentorganization":
		signals.HasOrganizationSchema = true
	case "product", "productgroup", "offer", "aggregateoffer":
		signals.HasProductSchema = true
	}
}

func decodeHTML(
	body []byte,
	contentType string,
) ([]byte, bool, error) {
	decodedReader, err := newCharsetReader(
		bytes.NewReader(body),
		contentType,
	)
	if err != nil {
		return nil, false, err
	}
	return readBounded(decodedReader, maxDecodedBody)
}

func readBounded(
	reader io.Reader,
	limit int,
) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > limit {
		// Keep the bounded prefix so oversized HTML can still be assessed.
		return data[:limit], true, nil
	}
	return data, false, nil
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
		if node.Type != html.ElementNode || node.Data != "base" {
			continue
		}
		href, found := attributeValue(node, "href")
		if !found {
			continue
		}
		reference, err := url.Parse(strings.TrimSpace(href))
		if err != nil {
			return baseURL
		}
		return pageURL.ResolveReference(reference)
	}
	return baseURL
}

func attributeValue(node *html.Node, name string) (string, bool) {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, name) {
			return attribute.Val, true
		}
	}
	return "", false
}

func classContains(node *html.Node, token string) bool {
	value, found := attributeValue(node, "class")
	if !found {
		return false
	}
	for _, part := range strings.Fields(value) {
		if strings.EqualFold(part, token) {
			return true
		}
	}
	return false
}

func relContains(node *html.Node, token string) bool {
	value, found := attributeValue(node, "rel")
	if !found {
		return false
	}
	for _, part := range strings.Fields(value) {
		if strings.EqualFold(part, token) {
			return true
		}
	}
	return false
}

func nodeText(node *html.Node) string {
	var builder strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			builder.WriteString(child.Data)
		}
	}
	return builder.String()
}

func cloneURL(source *url.URL) *url.URL {
	cloned := *source
	return &cloned
}

var errInvalidPageURL = errString("personalsite: invalid page URL")

type errString string

func (err errString) Error() string {
	return string(err)
}

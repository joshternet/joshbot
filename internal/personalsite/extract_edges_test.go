package personalsite

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestExtractSignalsEdgeCases(t *testing.T) {
	// Not parallel: subtests mutate package-level decode/parse hooks.
	pageURL := mustParseURL(t, "https://example.com/dir/")

	t.Run("sniff_html_when_content_type_empty", func(t *testing.T) {
		body := []byte(`<!doctype html><html><body><a href="/about">a</a></body></html>`)
		signals, status, _, err := ExtractSignals(pageURL, "", body)
		if err != nil {
			t.Fatalf("ExtractSignals() error = %v", err)
		}
		if status != ExtractStatusComplete || !signals.HasAboutLink {
			t.Fatalf(
				"status/signals = %v %#v",
				status,
				signals,
			)
		}
	})

	t.Run("anchor_hcard_without_href", func(t *testing.T) {
		body := []byte(`<!doctype html><a class="h-card" rel="me">x</a>`)
		signals, status, _, err := ExtractSignals(
			pageURL,
			"text/html",
			body,
		)
		if err != nil {
			t.Fatalf("ExtractSignals() error = %v", err)
		}
		if status != ExtractStatusComplete ||
			!signals.HasHCard ||
			!signals.HasRelMe {
			t.Fatalf("signals = %#v", signals)
		}
	})

	t.Run("invalid_href_and_absolute_path_classify", func(t *testing.T) {
		body := []byte(
			`<!doctype html><a href="http://%zz/">bad</a>` +
				`<a href="https://example.com/blog/post">blog</a>` +
				`<base href="https://cdn.example/">`,
		)
		signals, status, _, err := ExtractSignals(
			pageURL,
			"text/html",
			body,
		)
		if err != nil {
			t.Fatalf("ExtractSignals() error = %v", err)
		}
		if status != ExtractStatusComplete || !signals.HasBlogLink {
			t.Fatalf("signals = %#v", signals)
		}
	})

	t.Run("empty_ldjson_and_typed_array", func(t *testing.T) {
		body := []byte(`<!doctype html>
<script type="application/ld+json"></script>
<script type="application/ld+json">{"@type":["Person","CreativeWork"]}</script>
<script type="text/javascript">{"@type":"Person"}</script>`)
		signals, status, _, err := ExtractSignals(
			pageURL,
			"text/html",
			body,
		)
		if err != nil {
			t.Fatalf("ExtractSignals() error = %v", err)
		}
		if status != ExtractStatusComplete || !signals.HasPersonSchema {
			t.Fatalf("signals = %#v", signals)
		}
	})

	t.Run("invalid_json_ld", func(t *testing.T) {
		body := []byte(
			`<!doctype html><script type="application/ld+json">{</script>`,
		)
		signals, status, _, err := ExtractSignals(
			pageURL,
			"text/html",
			body,
		)
		if err != nil {
			t.Fatalf("ExtractSignals() error = %v", err)
		}
		if status != ExtractStatusComplete || signals != (SiteSignals{}) {
			t.Fatalf("signals = %#v status=%v", signals, status)
		}
	})

	t.Run("parse_failure", func(t *testing.T) {
		original := parseHTMLDocument
		t.Cleanup(func() { parseHTMLDocument = original })
		parseHTMLDocument = func(io.Reader) (*html.Node, error) {
			return nil, errors.New("forced parse failure")
		}
		_, status, _, err := ExtractSignals(
			pageURL,
			"text/html",
			[]byte(`<html></html>`),
		)
		if err != nil {
			t.Fatalf("ExtractSignals() error = %v", err)
		}
		if status != ExtractStatusUnavailable {
			t.Fatalf("status = %v, want unavailable", status)
		}
	})

	t.Run("decode_failure", func(t *testing.T) {
		original := decodeHTMLBody
		t.Cleanup(func() { decodeHTMLBody = original })
		decodeHTMLBody = func([]byte, string) ([]byte, bool, error) {
			return nil, false, errors.New("forced decode failure")
		}
		_, status, _, err := ExtractSignals(
			pageURL,
			"text/html",
			[]byte(`<html></html>`),
		)
		if err != nil {
			t.Fatalf("ExtractSignals() error = %v", err)
		}
		if status != ExtractStatusUnavailable {
			t.Fatalf("status = %v, want unavailable", status)
		}
	})

	t.Run("decode_truncated_still_assessed", func(t *testing.T) {
		original := decodeHTMLBody
		t.Cleanup(func() { decodeHTMLBody = original })
		decodeHTMLBody = func([]byte, string) ([]byte, bool, error) {
			return []byte(`<html><body><a href="/cart">c</a></body></html>`), true, nil
		}
		signals, status, truncated, err := ExtractSignals(
			pageURL,
			"text/html",
			[]byte(`<html></html>`),
		)
		if err != nil {
			t.Fatalf("ExtractSignals() error = %v", err)
		}
		if status != ExtractStatusComplete || !truncated {
			t.Fatalf(
				"status=%v truncated=%v, want complete+truncated",
				status,
				truncated,
			)
		}
		if !signals.HasShoppingCart {
			t.Fatalf("signals = %#v, want cart", signals)
		}
	})

	t.Run("decoded_too_large_keeps_prefix", func(t *testing.T) {
		reader := bytes.NewReader(bytes.Repeat([]byte("a"), maxDecodedBody+1))
		data, tooLarge, err := readBounded(reader, maxDecodedBody)
		if err != nil {
			t.Fatalf("readBounded() error = %v", err)
		}
		if !tooLarge || len(data) != maxDecodedBody {
			t.Fatalf(
				"readBounded() = %d tooLarge=%v",
				len(data),
				tooLarge,
			)
		}
	})

	t.Run("read_error", func(t *testing.T) {
		_, _, err := readBounded(errReader{}, 8)
		if err == nil {
			t.Fatal("readBounded() error = nil")
		}
	})

	t.Run("invalid_content_type_media", func(t *testing.T) {
		if isHTMLContentType(`text/html; charset="`) {
			t.Fatal("invalid media type accepted")
		}
	})

	t.Run("base_href_invalid", func(t *testing.T) {
		document, err := html.Parse(strings.NewReader(
			`<html><head><base href="http://%zz/"></head></html>`,
		))
		if err != nil {
			t.Fatalf("html.Parse() error = %v", err)
		}
		base := documentBaseURL(document, pageURL)
		if base.String() != pageURL.String() {
			t.Fatalf("base = %q, want page URL", base)
		}
	})

	t.Run("base_without_href", func(t *testing.T) {
		document, err := html.Parse(strings.NewReader(
			`<html><head><base></head></html>`,
		))
		if err != nil {
			t.Fatalf("html.Parse() error = %v", err)
		}
		base := documentBaseURL(document, pageURL)
		if base.String() != pageURL.String() {
			t.Fatalf("base = %q", base)
		}
	})

	t.Run("mark_schema_non_string_array", func(t *testing.T) {
		signals := SiteSignals{}
		markSchemaTypes([]any{1, "Person"}, &signals)
		if !signals.HasPersonSchema {
			t.Fatal("expected person schema from mixed array")
		}
	})

	t.Run("classify_relative_without_slash", func(t *testing.T) {
		signals := SiteSignals{}
		classifyPath("about", &signals)
		if !signals.HasAboutLink {
			t.Fatal("expected about link")
		}
	})

	t.Run("classify_absolute_parse_error", func(t *testing.T) {
		signals := SiteSignals{}
		classifyPath("https://%zz/", &signals)
		if signals != (SiteSignals{}) {
			t.Fatalf("signals = %#v", signals)
		}
	})

	t.Run("link_without_href", func(t *testing.T) {
		body := []byte(
			`<!doctype html><link rel="me"><link rel="alternate" type="application/feed+json">`,
		)
		signals, status, _, err := ExtractSignals(
			pageURL,
			"text/html",
			body,
		)
		if err != nil {
			t.Fatalf("ExtractSignals() error = %v", err)
		}
		if status != ExtractStatusComplete ||
			!signals.HasRelMe ||
			!signals.HasFeed {
			t.Fatalf("signals = %#v", signals)
		}
	})

	t.Run("parked_hosts_and_author_rel", func(t *testing.T) {
		body := []byte(`<!doctype html>
<a href="https://godaddy.com/forsale">x</a>
<a href="https://afternic.com/x">y</a>
<a href="https://hugedomains.com/x">z</a>
<a rel="author" href="/profile">a</a>
<meta name="googlebot" content="noindex, parked">
`)
		signals, status, _, err := ExtractSignals(
			pageURL,
			"text/html",
			body,
		)
		if err != nil {
			t.Fatalf("ExtractSignals() error = %v", err)
		}
		if status != ExtractStatusComplete ||
			!signals.HasParkedDomain ||
			!signals.HasAuthorMetadata ||
			!signals.HasAboutLink {
			t.Fatalf("signals = %#v", signals)
		}
	})

	t.Run("err_string", func(t *testing.T) {
		if errInvalidPageURL.Error() == "" {
			t.Fatal("empty error string")
		}
	})

	t.Run("decode_html_passthrough", func(t *testing.T) {
		decoded, tooLarge, err := decodeHTML(
			[]byte("<html></html>"),
			"text/html; charset=utf-8",
		)
		if err != nil || tooLarge || len(decoded) == 0 {
			t.Fatalf(
				"decodeHTML() = %q %v %v",
				decoded,
				tooLarge,
				err,
			)
		}
	})

	t.Run("decode_html_charset_error", func(t *testing.T) {
		original := newCharsetReader
		t.Cleanup(func() { newCharsetReader = original })
		newCharsetReader = func(io.Reader, string) (io.Reader, error) {
			return nil, errors.New("charset failed")
		}
		_, _, err := decodeHTML([]byte("<html></html>"), "text/html")
		if err == nil {
			t.Fatal("decodeHTML() error = nil")
		}
	})
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

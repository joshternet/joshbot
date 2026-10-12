package discovery

import (
	"bytes"
	"errors"
	"io"
	"net/url"
	"testing"

	"github.com/joshternet/joshbot/internal/origin"
	"golang.org/x/net/html"
)

func TestExtractPageLinksRejectsDecodedBodyOverLimit(
	t *testing.T,
) {
	source, err := origin.Parse(
		"https://example.com",
	)
	if err != nil {
		t.Fatalf(
			"origin.Parse() error = %v",
			err,
		)
	}

	pageURL, err := url.Parse(
		"https://example.com/",
	)
	if err != nil {
		t.Fatalf(
			"url.Parse() error = %v",
			err,
		)
	}

	// Windows-1252 0x80 decodes to the UTF-8 euro sign,
	// expanding one input byte to three output bytes.
	// MaxRawBody bytes therefore stay within the raw limit
	// while exceeding MaxDecodedBody after decoding.
	body := bytes.Repeat(
		[]byte{0x80},
		MaxRawBody,
	)

	links, status, err := ExtractPageLinks(
		source,
		pageURL,
		"text/html; charset=windows-1252",
		body,
	)
	if err != nil {
		t.Fatalf(
			"ExtractPageLinks() error = %v",
			err,
		)
	}

	if status != StatusTooLarge {
		t.Errorf(
			"status = %v, want %v",
			status,
			StatusTooLarge,
		)
	}

	if len(links.Internal) != 0 {
		t.Errorf(
			"internal links = %#v, want none",
			links.Internal,
		)
	}

	if len(links.Candidates) != 0 {
		t.Errorf(
			"candidates = %#v, want none",
			links.Candidates,
		)
	}
}

func TestExtractPageLinksEmptyExplicitHTMLIsUnavailable(
	t *testing.T,
) {
	source, err := origin.Parse(
		"https://example.com",
	)
	if err != nil {
		t.Fatalf(
			"origin.Parse() error = %v",
			err,
		)
	}

	pageURL, err := url.Parse(
		"https://example.com/",
	)
	if err != nil {
		t.Fatalf(
			"url.Parse() error = %v",
			err,
		)
	}

	links, status, err := ExtractPageLinks(
		source,
		pageURL,
		"text/html",
		nil,
	)
	if err != nil {
		t.Fatalf(
			"ExtractPageLinks() error = %v",
			err,
		)
	}

	if status != StatusUnavailable {
		t.Errorf(
			"status = %v, want %v",
			status,
			StatusUnavailable,
		)
	}

	if len(links.Internal) != 0 ||
		len(links.Candidates) != 0 {
		t.Errorf(
			"links = %#v, want empty",
			links,
		)
	}
}

func TestExtractPageLinksTreatsHTMLParseFailureAsUnavailable(
	t *testing.T,
) {
	source, err := origin.Parse("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	pageURL, err := url.Parse("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}

	want := errors.New("html parse failed")
	original := parseHTMLDocument
	parseHTMLDocument = func(io.Reader) (*html.Node, error) {
		return nil, want
	}
	t.Cleanup(func() {
		parseHTMLDocument = original
	})

	links, status, err := ExtractPageLinks(
		source,
		pageURL,
		"text/html",
		[]byte("<html></html>"),
	)
	if err != nil {
		t.Fatalf("ExtractPageLinks() error = %v", err)
	}
	if status != StatusUnavailable {
		t.Fatalf("status = %v, want %v", status, StatusUnavailable)
	}
	if len(links.Internal) != 0 || len(links.Candidates) != 0 {
		t.Fatalf("links = %#v, want empty", links)
	}
}

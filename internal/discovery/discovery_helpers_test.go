package discovery

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/origin"
)

var (
	errTestRead        = errors.New("test read failure")
	errUnexpectedFetch = errors.New("unexpected fetch")
)

func TestHTMLDecodingBoundsAndErrors(t *testing.T) {
	decoded, tooLarge, err := decodeHTML(
		strings.NewReader("<html></html>"),
		"text/html; charset=utf-8",
	)
	if err != nil {
		t.Fatalf("decodeHTML() error = %v", err)
	}
	if tooLarge {
		t.Error("decodeHTML() tooLarge = true")
	}
	if string(decoded) != "<html></html>" {
		t.Errorf("decoded = %q", decoded)
	}

	_, _, err = decodeHTML(
		failingReader{err: errTestRead},
		"text/html",
	)
	if !errors.Is(err, errTestRead) {
		t.Errorf("initial read error = %v", err)
	}

	_, _, err = decodeHTML(
		&previewThenErrorReader{
			remaining: 1024,
			err:       errTestRead,
		},
		"text/html; charset=utf-8",
	)
	if !errors.Is(err, errTestRead) {
		t.Errorf("decoded read error = %v", err)
	}

	_, tooLarge, err = decodeHTML(
		bytes.NewReader(
			bytes.Repeat(
				[]byte{0x80},
				MaxRawBody,
			),
		),
		"text/html; charset=windows-1252",
	)
	if err != nil {
		t.Fatalf("expanded decode error = %v", err)
	}
	if !tooLarge {
		t.Error("expanded decode tooLarge = false")
	}
}

func TestDiscoveryHelpers(t *testing.T) {
	data, tooLarge, err := readBounded(
		strings.NewReader("1234"),
		4,
	)
	if err != nil {
		t.Fatalf("readBounded() error = %v", err)
	}
	if string(data) != "1234" || tooLarge {
		t.Errorf("readBounded() = %q, %v", data, tooLarge)
	}

	data, tooLarge, err = readBounded(
		strings.NewReader("12345"),
		4,
	)
	if err != nil {
		t.Fatalf("overflow readBounded() error = %v", err)
	}
	if data != nil || !tooLarge {
		t.Errorf("overflow readBounded() = %q, %v", data, tooLarge)
	}

	_, _, err = readBounded(
		failingReader{err: errTestRead},
		4,
	)
	if !errors.Is(err, errTestRead) {
		t.Errorf("readBounded() error = %v", err)
	}

	for _, status := range []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	} {
		if !isRedirectStatus(status) {
			t.Errorf("%d not recognized as redirect", status)
		}
	}
	if isRedirectStatus(http.StatusOK) {
		t.Error("200 recognized as redirect")
	}

	if !isHTMLContentType("TEXT/HTML; charset=utf-8") {
		t.Error("text/html not recognized")
	}
	if !isHTMLContentType("application/xhtml+xml") {
		t.Error("application/xhtml+xml not recognized")
	}
	if isHTMLContentType("application/json") {
		t.Error("application/json recognized as HTML")
	}
	if isHTMLContentType(`text/html; charset="`) {
		t.Error("invalid media type recognized as HTML")
	}

	current := mustDiscoveryURL(t, "https://example.com/start")
	response := discoveryResponse(
		http.StatusFound,
		"/next#fragment",
	)
	next, err := redirectTarget(current, response)
	if err != nil {
		t.Fatalf("redirectTarget() error = %v", err)
	}
	if next.String() != "https://example.com/next" {
		t.Errorf("redirect target = %q", next.String())
	}

	for _, location := range []string{
		"",
		"https://[broken",
		"https://user:secret@example.net/",
	} {
		if _, err := redirectTarget(
			current,
			discoveryResponse(http.StatusFound, location),
		); !errors.Is(err, errInvalidPageURL) {
			t.Errorf("redirectTarget(%q) error = %v", location, err)
		}
	}
}

type trackedBody struct {
	reader     io.Reader
	closeErr   error
	closeCount int
}

func (body *trackedBody) Read(destination []byte) (int, error) {
	if body.reader == nil {
		return 0, io.EOF
	}
	return body.reader.Read(destination)
}

func (body *trackedBody) Close() error {
	body.closeCount++
	return body.closeErr
}

type failingReader struct {
	err error
}

func (reader failingReader) Read([]byte) (int, error) {
	return 0, reader.err
}

type previewThenErrorReader struct {
	remaining int
	err       error
}

func (reader *previewThenErrorReader) Read(destination []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, reader.err
	}

	count := len(destination)
	if count > reader.remaining {
		count = reader.remaining
	}
	for index := 0; index < count; index++ {
		destination[index] = 'a'
	}
	reader.remaining -= count
	return count, nil
}

func discoveryResponse(status int, location string) *http.Response {
	header := make(http.Header)
	if location != "" {
		header.Set("Location", location)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func mustDiscoveryOrigin(t *testing.T, rawURL string) origin.Origin {
	t.Helper()
	value, err := origin.Parse(rawURL)
	if err != nil {
		t.Fatalf("origin.Parse(%q) error = %v", rawURL, err)
	}
	return value
}

func mustDiscoveryURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	value, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", rawURL, err)
	}
	return value
}

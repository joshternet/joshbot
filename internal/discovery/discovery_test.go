package discovery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/robots"
)

var (
	errTestFetch       = errors.New("test fetch failure")
	errTestRead        = errors.New("test read failure")
	errTestClose       = errors.New("test close failure")
	errTestWait        = errors.New("test wait failure")
	errTestClaim       = errors.New("test claim failure")
	errTestRecord      = errors.New("test record failure")
	errUnexpectedFetch = errors.New("unexpected fetch")
)

func TestCrawlerUsesExactRootAndFollowsSameOriginRedirect(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	redirectBody := newTrackedBody("")
	pageBody := newTrackedBody(
		`<!doctype html><a href="https://example.net/about">site</a>`,
	)
	getter := &scriptedGetter{
		steps: []getterStep{
			{
				response: discoveryResponse(
					http.StatusFound,
					"",
					"/home#fragment",
					redirectBody,
				),
			},
			{
				response: discoveryResponse(
					http.StatusOK,
					"text/html; charset=utf-8",
					"",
					pageBody,
				),
			},
		},
	}

	result, err := NewCrawler(getter).Discover(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf("Discover() error = %v, want nil", err)
	}

	if !reflect.DeepEqual(
		getter.calls,
		[]string{
			"https://example.com/",
			"https://example.com/home",
		},
	) {
		t.Errorf("getter calls = %#v", getter.calls)
	}

	want := Result{
		Source: source,
		Status: StatusComplete,
		Candidates: []Candidate{
			{
				Origin: mustDiscoveryOrigin(
					t,
					"https://example.net",
				),
				Kind: KindLink,
			},
		},
	}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("result = %#v, want %#v", result, want)
	}

	assertDiscoveryBodyClosedOnce(t, redirectBody)
	assertDiscoveryBodyClosedOnce(t, pageBody)
}

func TestCrawlerReturnsCrossOriginRedirectCandidate(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	body := newTrackedBody("")
	getter := &scriptedGetter{
		steps: []getterStep{
			{
				response: discoveryResponse(
					http.StatusTemporaryRedirect,
					"",
					"https://www.example.net/home#section",
					body,
				),
			},
		},
	}

	result, err := NewCrawler(getter).Discover(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf("Discover() error = %v, want nil", err)
	}

	want := Result{
		Source: source,
		Status: StatusRedirected,
		Candidates: []Candidate{
			{
				Origin: mustDiscoveryOrigin(
					t,
					"https://www.example.net",
				),
				Kind: KindRedirect,
			},
		},
	}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("result = %#v, want %#v", result, want)
	}

	if len(getter.calls) != 1 {
		t.Errorf(
			"getter call count = %d, want 1",
			len(getter.calls),
		)
	}

	assertDiscoveryBodyClosedOnce(t, body)
}

func TestCrawlerRejectsRequiredSixthRedirect(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	getter := &scriptedGetter{}

	for index := 1; index <= MaxRedirects+1; index++ {
		getter.steps = append(
			getter.steps,
			getterStep{
				response: discoveryResponse(
					http.StatusFound,
					"",
					fmt.Sprintf("/redirect-%d", index),
					newTrackedBody(""),
				),
			},
		)
	}

	result, err := NewCrawler(getter).Discover(
		context.Background(),
		source,
	)
	if err != nil {
		t.Fatalf("Discover() error = %v, want nil", err)
	}

	if result.Status != StatusUnavailable {
		t.Errorf(
			"status = %v, want StatusUnavailable",
			result.Status,
		)
	}

	if len(result.Candidates) != 0 {
		t.Errorf(
			"candidate count = %d, want 0",
			len(result.Candidates),
		)
	}

	if len(getter.calls) != MaxRedirects+1 {
		t.Errorf(
			"getter call count = %d, want %d",
			len(getter.calls),
			MaxRedirects+1,
		)
	}

	for _, step := range getter.steps {
		assertDiscoveryBodyClosedOnce(
			t,
			step.response.Body.(*trackedBody),
		)
	}
}

func TestCrawlerRemoteOutcomesAreNonfatal(t *testing.T) {
	tests := []struct {
		name        string
		requestErr  error
		response    func() *http.Response
		wantStatus  Status
		wantClosure bool
	}{
		{
			name:       "robots denied",
			requestErr: robots.ErrDisallowed,
			wantStatus: StatusRobotsDenied,
		},
		{
			name:       "network failure",
			requestErr: errTestFetch,
			wantStatus: StatusUnavailable,
		},
		{
			name:       "nil response",
			wantStatus: StatusUnavailable,
		},
		{
			name: "nil body",
			response: func() *http.Response {
				return discoveryResponse(
					http.StatusOK,
					"text/html",
					"",
					nil,
				)
			},
			wantStatus: StatusUnavailable,
		},
		{
			name: "not found",
			response: func() *http.Response {
				return discoveryResponse(
					http.StatusNotFound,
					"text/html",
					"",
					newTrackedBody("missing"),
				)
			},
			wantStatus:  StatusUnavailable,
			wantClosure: true,
		},
		{
			name: "server error",
			response: func() *http.Response {
				return discoveryResponse(
					http.StatusInternalServerError,
					"text/html",
					"",
					newTrackedBody("failed"),
				)
			},
			wantStatus:  StatusUnavailable,
			wantClosure: true,
		},
		{
			name: "unsupported content",
			response: func() *http.Response {
				return discoveryResponse(
					http.StatusOK,
					"application/json",
					"",
					newTrackedBody(
						`<a href="https://example.net">`,
					),
				)
			},
			wantStatus:  StatusUnsupportedContent,
			wantClosure: true,
		},
		{
			name: "oversized body",
			response: func() *http.Response {
				return discoveryResponse(
					http.StatusOK,
					"text/html",
					"",
					&trackedBody{
						reader: bytes.NewReader(
							bytes.Repeat(
								[]byte("x"),
								MaxRawBody+1,
							),
						),
					},
				)
			},
			wantStatus:  StatusTooLarge,
			wantClosure: true,
		},
		{
			name: "body read failure",
			response: func() *http.Response {
				return discoveryResponse(
					http.StatusOK,
					"text/html",
					"",
					&trackedBody{
						reader: failingReader{
							err: errTestRead,
						},
					},
				)
			},
			wantStatus:  StatusUnavailable,
			wantClosure: true,
		},
		{
			name: "body close failure",
			response: func() *http.Response {
				return discoveryResponse(
					http.StatusOK,
					"text/html",
					"",
					&trackedBody{
						reader:   strings.NewReader("<html></html>"),
						closeErr: errTestClose,
					},
				)
			},
			wantStatus:  StatusUnavailable,
			wantClosure: true,
		},
	}

	source := mustDiscoveryOrigin(t, "https://example.com")

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			step := getterStep{err: test.requestErr}
			if test.response != nil {
				step.response = test.response()
			}

			getter := &scriptedGetter{
				steps: []getterStep{step},
			}

			result, err := NewCrawler(getter).Discover(
				context.Background(),
				source,
			)
			if err != nil {
				t.Fatalf(
					"Discover() error = %v, want nil",
					err,
				)
			}

			if result.Status != test.wantStatus {
				t.Errorf(
					"status = %v, want %v",
					result.Status,
					test.wantStatus,
				)
			}

			if len(result.Candidates) != 0 {
				t.Errorf(
					"candidate count = %d, want 0",
					len(result.Candidates),
				)
			}

			if test.wantClosure {
				assertDiscoveryBodyClosedOnce(
					t,
					step.response.Body.(*trackedBody),
				)
			}
		})
	}
}

func TestCrawlerInvalidRedirectsAreUnavailable(
	t *testing.T,
) {
	tests := []struct {
		name     string
		location string
		closeErr error
	}{
		{
			name: "missing location",
		},
		{
			name:     "malformed location",
			location: "https://[broken",
		},
		{
			name:     "credential-bearing location",
			location: "https://user:secret@example.net/",
		},
		{
			name:     "close failure",
			location: "/home",
			closeErr: errTestClose,
		},
	}

	source := mustDiscoveryOrigin(t, "https://example.com")

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &trackedBody{
				reader:   strings.NewReader(""),
				closeErr: test.closeErr,
			}
			getter := &scriptedGetter{
				steps: []getterStep{
					{
						response: discoveryResponse(
							http.StatusFound,
							"",
							test.location,
							body,
						),
					},
				},
			}

			result, err := NewCrawler(getter).Discover(
				context.Background(),
				source,
			)
			if err != nil {
				t.Fatalf(
					"Discover() error = %v, want nil",
					err,
				)
			}

			if result.Status != StatusUnavailable {
				t.Errorf(
					"status = %v, want unavailable",
					result.Status,
				)
			}

			assertDiscoveryBodyClosedOnce(t, body)
		})
	}
}

func TestCrawlerReturnsContextCancellation(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://example.com")

	t.Run("before request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		cancel()

		_, err := NewCrawler(&scriptedGetter{}).Discover(
			ctx,
			source,
		)
		if !errors.Is(err, context.Canceled) {
			t.Errorf(
				"error = %v, want context.Canceled",
				err,
			)
		}
	})

	t.Run("during request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		getter := getterFunc(
			func(
				context.Context,
				*url.URL,
			) (*http.Response, error) {
				cancel()
				return nil, errTestFetch
			},
		)

		_, err := NewCrawler(getter).Discover(ctx, source)
		if !errors.Is(err, context.Canceled) {
			t.Errorf(
				"error = %v, want context.Canceled",
				err,
			)
		}
	})

	t.Run("during body read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		body := &cancelingBody{
			cancel: cancel,
			data:   []byte("<html></html>"),
		}
		getter := &scriptedGetter{
			steps: []getterStep{
				{
					response: discoveryResponse(
						http.StatusOK,
						"text/html",
						"",
						body,
					),
				},
			},
		}

		_, err := NewCrawler(getter).Discover(ctx, source)
		if !errors.Is(err, context.Canceled) {
			t.Errorf(
				"error = %v, want context.Canceled",
				err,
			)
		}

		if body.closeCount != 1 {
			t.Errorf(
				"close count = %d, want 1",
				body.closeCount,
			)
		}
	})
}

func TestCrawlerValidatesDependencies(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	ctx := context.Background()

	var nilCrawler *Crawler
	_, err := nilCrawler.Discover(ctx, source)
	if !errors.Is(err, errCrawlerUnavailable) {
		t.Errorf("nil crawler error = %v", err)
	}

	_, err = NewCrawler(&scriptedGetter{}).Discover(nil, source)
	if !errors.Is(err, errInvalidContext) {
		t.Errorf("nil context error = %v", err)
	}

	_, err = NewCrawler(&scriptedGetter{}).Discover(
		ctx,
		origin.Origin{},
	)
	if !errors.Is(err, errInvalidSource) {
		t.Errorf("zero source error = %v", err)
	}

	_, err = NewCrawler(nil).Discover(ctx, source)
	if !errors.Is(err, errGetterUnavailable) {
		t.Errorf("nil getter error = %v", err)
	}
}

func TestExtractHyperlinkSemantics(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	pageURL := mustDiscoveryURL(
		t,
		"https://example.com/directory/page",
	)

	body := []byte(`
		<!doctype html>
		<a href="/about">same relative</a>
		<a href="https://example.com/posts/one">same absolute</a>
		<a href="https://example.net/a?query=1#fragment">external</a>
		<a href="https://EXAMPLE.NET:443/b">duplicate</a>
		<a href="http://plain.example:80/path">http</a>
		<a href="//scheme.example/path">scheme relative</a>
		<a href="https://example.com:8443/path">port</a>
		<a href="https://[2001:db8::1]/path">ipv6</a>
		<a href="https://bücher.example/path">idna</a>
		<a href="https://entity.example/path?a=1&amp;b=2">entity</a>
		<area href="https://map.example/area">
		<a href="mailto:person@example.net">mail</a>
		<a href="tel:+10000000000">phone</a>
		<a href="javascript:alert(1)">script</a>
		<a href="data:text/plain,hello">data</a>
		<a href="ftp://example.net/file">ftp</a>
		<a href="file:///tmp/file">file</a>
		<a href="wss://example.net/socket">websocket</a>
		<a href="https://user:password@secret.example/">credentials</a>
		<a href="https://[broken">malformed</a>
		<a>missing href</a>
		<img src="https://image.example/image.png">
		<script src="https://script.example/app.js"></script>
		<iframe src="https://frame.example/"></iframe>
		<link href="https://style.example/site.css">
		<form action="https://form.example/"></form>
	`)

	result, err := Extract(
		source,
		pageURL,
		"text/html; charset=utf-8",
		body,
	)
	if err != nil {
		t.Fatalf("Extract() error = %v, want nil", err)
	}

	wantOrigins := []string{
		"http://plain.example",
		"https://[2001:db8::1]",
		"https://entity.example",
		"https://example.com:8443",
		"https://example.net",
		"https://map.example",
		"https://scheme.example",
		"https://xn--bcher-kva.example",
	}
	sort.Strings(wantOrigins)

	gotOrigins := candidateOriginStrings(result.Candidates)
	if !reflect.DeepEqual(gotOrigins, wantOrigins) {
		t.Errorf(
			"candidate origins = %#v, want %#v",
			gotOrigins,
			wantOrigins,
		)
	}

	for _, candidate := range result.Candidates {
		if candidate.Kind != KindLink {
			t.Errorf(
				"candidate kind = %v, want KindLink",
				candidate.Kind,
			)
		}
	}

	if result.Status != StatusComplete {
		t.Errorf(
			"status = %v, want StatusComplete",
			result.Status,
		)
	}

}

func TestExtractUsesFirstDocumentBase(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	pageURL := mustDiscoveryURL(
		t,
		"https://example.com/directory/page",
	)

	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "first external base wins",
			body: `
				<base href="https://assets.example.net/site/">
				<base href="https://ignored.example/">
				<a href="../josh">link</a>
			`,
			want: []string{"https://assets.example.net"},
		},
		{
			name: "relative base",
			body: `
				<base href="../assets/">
				<a href="page">same origin</a>
				<a href="https://external.example/">external</a>
			`,
			want: []string{"https://external.example"},
		},
		{
			name: "invalid first base falls back",
			body: `
				<base href="%">
				<base href="https://ignored.example/">
				<a href="../same">same origin</a>
				<a href="https://external.example/">external</a>
			`,
			want: []string{"https://external.example"},
		},
		{
			name: "invalid base origin falls back",
			body: `
				<base href="mailto:person@example.net">
				<a href="https://external.example/">external</a>
			`,
			want: []string{"https://external.example"},
		},
		{
			name: "base without href is skipped",
			body: `
				<base>
				<base href="https://assets.example.net/">
				<a href="page">external</a>
			`,
			want: []string{"https://assets.example.net"},
		},
		{
			name: "no base uses final page",
			body: `
				<a href="../same">same origin</a>
				<a href="https://external.example/">external</a>
			`,
			want: []string{"https://external.example"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Extract(
				source,
				pageURL,
				"text/html",
				[]byte(test.body),
			)
			if err != nil {
				t.Fatalf(
					"Extract() error = %v, want nil",
					err,
				)
			}

			got := candidateOriginStrings(
				result.Candidates,
			)
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf(
					"origins = %#v, want %#v",
					got,
					test.want,
				)
			}
		})
	}
}

func TestExtractHandlesContentTypesAndEncoding(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	pageURL := mustDiscoveryURL(t, "https://example.com/")

	tests := []struct {
		name        string
		contentType string
		body        []byte
		wantStatus  Status
		wantOrigins []string
	}{
		{
			name: "missing content type is sniffed",
			body: []byte(
				`<!doctype html><a href="https://example.net/">site</a>`,
			),
			wantStatus:  StatusComplete,
			wantOrigins: []string{"https://example.net"},
		},
		{
			name:        "xhtml",
			contentType: "application/xhtml+xml; charset=utf-8",
			body: []byte(
				`<html><body><a href="https://example.net/">site</a></body></html>`,
			),
			wantStatus:  StatusComplete,
			wantOrigins: []string{"https://example.net"},
		},
		{
			name:        "explicit json remains unsupported",
			contentType: "application/json",
			body: []byte(
				`<a href="https://example.net/">site</a>`,
			),
			wantStatus: StatusUnsupportedContent,
		},
		{
			name:        "invalid media type",
			contentType: `text/html; charset="`,
			body:        []byte("<html></html>"),
			wantStatus:  StatusUnsupportedContent,
		},
		{
			name:        "decoded body is too large",
			contentType: "text/html; charset=x-user-defined",
			body: bytes.Repeat(
				[]byte{0x80},
				MaxDecodedBody/3+1,
			),
			wantStatus: StatusTooLarge,
		},
		{
			name:       "empty sniffed body is not html",
			body:       nil,
			wantStatus: StatusUnsupportedContent,
		},
		{
			name:        "malformed html is recovered",
			contentType: "text/html",
			body: []byte(
				`<html><body><a href="https://example.net/"><div>`,
			),
			wantStatus:  StatusComplete,
			wantOrigins: []string{"https://example.net"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Extract(
				source,
				pageURL,
				test.contentType,
				test.body,
			)
			if err != nil {
				t.Fatalf(
					"Extract() error = %v, want nil",
					err,
				)
			}

			if result.Status != test.wantStatus {
				t.Errorf(
					"status = %v, want %v",
					result.Status,
					test.wantStatus,
				)
			}

			got := candidateOriginStrings(
				result.Candidates,
			)
			if !reflect.DeepEqual(
				got,
				test.wantOrigins,
			) {
				t.Errorf(
					"origins = %#v, want %#v",
					got,
					test.wantOrigins,
				)
			}
		})
	}

	latin1 := append(
		[]byte(`<a href="https://b`),
		byte(0xfc),
	)
	latin1 = append(
		latin1,
		[]byte(`cher.example/">site</a>`)...,
	)

	result, err := Extract(
		source,
		pageURL,
		"text/html; charset=iso-8859-1",
		latin1,
	)
	if err != nil {
		t.Fatalf("Latin-1 Extract() error = %v", err)
	}

	want := []string{"https://xn--bcher-kva.example"}
	if got := candidateOriginStrings(
		result.Candidates,
	); !reflect.DeepEqual(got, want) {
		t.Errorf(
			"Latin-1 origins = %#v, want %#v",
			got,
			want,
		)
	}
}

func TestExtractReturnsUnavailableForDecoderFailure(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	pageURL := mustDiscoveryURL(t, "https://example.com/")

	result, err := extract(
		source,
		pageURL,
		"text/html",
		[]byte("<html></html>"),
		func(
			io.Reader,
			string,
		) ([]byte, bool, error) {
			return nil, false, errTestRead
		},
	)
	if err != nil {
		t.Fatalf("extract() error = %v, want nil", err)
	}

	if result.Status != StatusUnavailable {
		t.Errorf(
			"status = %v, want %v",
			result.Status,
			StatusUnavailable,
		)
	}

	if result.Candidates != nil {
		t.Errorf(
			"candidates = %#v, want nil",
			result.Candidates,
		)
	}
}

func TestExtractEnforcesBodyLimitWithoutCandidateTruncation(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	pageURL := mustDiscoveryURL(t, "https://example.com/")

	exactHTML := []byte(
		`<a href="https://example.net/">site</a>`,
	)
	exactHTML = append(
		exactHTML,
		bytes.Repeat(
			[]byte(" "),
			MaxRawBody-len(exactHTML),
		)...,
	)

	result, err := Extract(
		source,
		pageURL,
		"text/html",
		exactHTML,
	)
	if err != nil {
		t.Fatalf("exact-limit Extract() error = %v", err)
	}

	if result.Status != StatusComplete {
		t.Errorf(
			"exact-limit status = %v, want complete",
			result.Status,
		)
	}

	result, err = Extract(
		source,
		pageURL,
		"text/html",
		bytes.Repeat([]byte("x"), MaxRawBody+1),
	)
	if err != nil {
		t.Fatalf("oversize Extract() error = %v", err)
	}

	if result.Status != StatusTooLarge {
		t.Errorf(
			"oversize status = %v, want too large",
			result.Status,
		)
	}

	const candidateCount = 2083

	var builder strings.Builder
	for index := candidateCount - 1; index >= 0; index-- {
		fmt.Fprintf(
			&builder,
			`<a href="https://candidate-%04d.example/path">site</a>`,
			index,
		)
	}

	result, err = Extract(
		source,
		pageURL,
		"text/html",
		[]byte(builder.String()),
	)
	if err != nil {
		t.Fatalf("uncapped Extract() error = %v", err)
	}

	if len(result.Candidates) != candidateCount {
		t.Errorf(
			"candidate count = %d, want %d",
			len(result.Candidates),
			candidateCount,
		)
	}

	wantOrigins := make([]string, 0, candidateCount)
	for index := 0; index < candidateCount; index++ {
		wantOrigins = append(
			wantOrigins,
			fmt.Sprintf(
				"https://candidate-%04d.example",
				index,
			),
		)
	}

	if got := candidateOriginStrings(
		result.Candidates,
	); !reflect.DeepEqual(got, wantOrigins) {
		t.Errorf(
			"candidate origins = %#v, want %#v",
			got,
			wantOrigins,
		)
	}
}

func TestExtractRejectsInvalidInput(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	pageURL := mustDiscoveryURL(t, "https://example.com/")

	_, err := Extract(
		origin.Origin{},
		pageURL,
		"text/html",
		nil,
	)
	if !errors.Is(err, errInvalidSource) {
		t.Errorf("zero source error = %v", err)
	}

	_, err = Extract(source, nil, "text/html", nil)
	if !errors.Is(err, errInvalidPageURL) {
		t.Errorf("nil page error = %v", err)
	}

	_, err = Extract(
		source,
		mustDiscoveryURL(t, "https://other.example/"),
		"text/html",
		nil,
	)
	if !errors.Is(err, errInvalidPageURL) {
		t.Errorf("other-origin page error = %v", err)
	}

	_, err = Extract(
		source,
		&url.URL{
			Scheme: "mailto",
			Opaque: "person@example.net",
		},
		"text/html",
		nil,
	)
	if !errors.Is(err, errInvalidPageURL) {
		t.Errorf("invalid page URL error = %v", err)
	}
}

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
		t.Errorf(
			"readBounded() = %q, %v",
			data,
			tooLarge,
		)
	}

	data, tooLarge, err = readBounded(
		strings.NewReader("12345"),
		4,
	)
	if err != nil {
		t.Fatalf("overflow readBounded() error = %v", err)
	}

	if data != nil || !tooLarge {
		t.Errorf(
			"overflow readBounded() = %q, %v",
			data,
			tooLarge,
		)
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

	current := mustDiscoveryURL(
		t,
		"https://example.com/start",
	)
	response := discoveryResponse(
		http.StatusFound,
		"",
		"/next#fragment",
		newTrackedBody(""),
	)
	next, err := redirectTarget(current, response)
	if err != nil {
		t.Fatalf("redirectTarget() error = %v", err)
	}

	if next.String() != "https://example.com/next" {
		t.Errorf(
			"redirect target = %q",
			next.String(),
		)
	}

	for _, location := range []string{
		"",
		"https://[broken",
		"https://user:secret@example.net/",
	} {
		response := discoveryResponse(
			http.StatusFound,
			"",
			location,
			newTrackedBody(""),
		)
		if _, err := redirectTarget(
			current,
			response,
		); !errors.Is(err, errInvalidPageURL) {
			t.Errorf(
				"redirectTarget(%q) error = %v",
				location,
				err,
			)
		}
	}
}

func TestRunnerProcessesOneSource(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	candidates := []Candidate{
		{
			Origin: mustDiscoveryOrigin(
				t,
				"https://example.net",
			),
			Kind: KindLink,
		},
	}

	store := &fakeDiscoveryStore{
		claims: []fakeDiscoveryClaim{
			{
				source: source,
				found:  true,
			},
		},
		recordResult: RecordResult{
			Accepted: 1,
		},
	}
	discoverer := discovererFunc(
		func(
			context.Context,
			origin.Origin,
		) (Result, error) {
			return Result{
				Source:     source,
				Status:     StatusComplete,
				Candidates: candidates,
			}, nil
		},
	)
	timeout := &recordingTimeoutFactory{}

	runner, err := newRunner(
		store,
		discoverer,
		testRunnerConfig(),
		waiterFunc(
			func(context.Context, time.Duration) error {
				return nil
			},
		),
		timeout,
	)
	if err != nil {
		t.Fatalf("newRunner() error = %v", err)
	}

	report, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	want := Report{
		Worked:     true,
		Source:     source,
		Status:     StatusComplete,
		Candidates: 1,
		Accepted:   1,
	}
	if report != want {
		t.Errorf("report = %#v, want %#v", report, want)
	}

	if timeout.duration != testRunnerConfig().PageTimeout {
		t.Errorf(
			"timeout = %v, want %v",
			timeout.duration,
			testRunnerConfig().PageTimeout,
		)
	}

	if !reflect.DeepEqual(
		store.recordedCandidates,
		candidates,
	) {
		t.Errorf(
			"recorded candidates = %#v",
			store.recordedCandidates,
		)
	}
}

func TestRunnerHandlesNoWorkAndRemoteFailure(
	t *testing.T,
) {
	t.Run("no source", func(t *testing.T) {
		discoverCalls := 0
		runner, err := NewRunner(
			&fakeDiscoveryStore{},
			discovererFunc(
				func(
					context.Context,
					origin.Origin,
				) (Result, error) {
					discoverCalls++
					return Result{}, nil
				},
			),
			testRunnerConfig(),
		)
		if err != nil {
			t.Fatalf("NewRunner() error = %v", err)
		}

		report, err := runner.RunOnce(
			context.Background(),
		)
		if err != nil {
			t.Fatalf("RunOnce() error = %v", err)
		}

		if report != (Report{}) {
			t.Errorf("report = %#v, want zero", report)
		}

		if discoverCalls != 0 {
			t.Errorf(
				"discover calls = %d, want 0",
				discoverCalls,
			)
		}
	})

	t.Run("remote status", func(t *testing.T) {
		source := mustDiscoveryOrigin(
			t,
			"https://example.com",
		)
		store := &fakeDiscoveryStore{
			claims: []fakeDiscoveryClaim{
				{
					source: source,
					found:  true,
				},
			},
		}
		runner, err := NewRunner(
			store,
			discovererFunc(
				func(
					context.Context,
					origin.Origin,
				) (Result, error) {
					return Result{
						Source: source,
						Status: StatusRobotsDenied,
					}, nil
				},
			),
			testRunnerConfig(),
		)
		if err != nil {
			t.Fatalf("NewRunner() error = %v", err)
		}

		report, err := runner.RunOnce(
			context.Background(),
		)
		if err != nil {
			t.Fatalf("RunOnce() error = %v", err)
		}

		if !report.Worked ||
			report.Status != StatusRobotsDenied {
			t.Errorf("report = %#v", report)
		}

		if store.recordCalls != 0 {
			t.Errorf(
				"record calls = %d, want 0",
				store.recordCalls,
			)
		}
	})

	t.Run("discoverer error", func(t *testing.T) {
		source := mustDiscoveryOrigin(
			t,
			"https://example.com",
		)
		runner, err := NewRunner(
			&fakeDiscoveryStore{
				claims: []fakeDiscoveryClaim{
					{
						source: source,
						found:  true,
					},
				},
			},
			discovererFunc(
				func(
					context.Context,
					origin.Origin,
				) (Result, error) {
					return Result{}, errTestFetch
				},
			),
			testRunnerConfig(),
		)
		if err != nil {
			t.Fatalf("NewRunner() error = %v", err)
		}

		report, err := runner.RunOnce(
			context.Background(),
		)
		if err != nil {
			t.Fatalf("RunOnce() error = %v", err)
		}

		if !report.Worked ||
			report.Status != StatusUnavailable {
			t.Errorf("report = %#v", report)
		}
	})
}

func TestRunnerTreatsPageTimeoutAsNonfatal(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	runner, err := newRunner(
		&fakeDiscoveryStore{
			claims: []fakeDiscoveryClaim{
				{
					source: source,
					found:  true,
				},
			},
		},
		discovererFunc(
			func(
				context.Context,
				origin.Origin,
			) (Result, error) {
				return Result{}, context.DeadlineExceeded
			},
		),
		testRunnerConfig(),
		waiterFunc(
			func(context.Context, time.Duration) error {
				return nil
			},
		),
		timeoutFactoryFunc(
			func(
				ctx context.Context,
				_ time.Duration,
			) (context.Context, context.CancelFunc) {
				return context.WithDeadline(
					ctx,
					time.Unix(0, 0),
				)
			},
		),
	)
	if err != nil {
		t.Fatalf("newRunner() error = %v", err)
	}

	report, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if !report.Worked ||
		report.Status != StatusUnavailable {
		t.Errorf("report = %#v", report)
	}
}

func TestRunnerPreservesParentCancellation(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	runner, err := NewRunner(
		&fakeDiscoveryStore{
			claims: []fakeDiscoveryClaim{
				{
					source: source,
					found:  true,
				},
			},
		},
		discovererFunc(
			func(
				context.Context,
				origin.Origin,
			) (Result, error) {
				cancel()
				return Result{}, context.Canceled
			},
		),
		testRunnerConfig(),
	)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	_, err = runner.RunOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("RunOnce() error = %v", err)
	}
}

func TestRunnerReturnsStoreFailures(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	candidate := Candidate{
		Origin: mustDiscoveryOrigin(
			t,
			"https://example.net",
		),
		Kind: KindLink,
	}

	t.Run("claim", func(t *testing.T) {
		runner, err := NewRunner(
			&fakeDiscoveryStore{
				claims: []fakeDiscoveryClaim{
					{err: errTestClaim},
				},
			},
			discovererFunc(
				func(
					context.Context,
					origin.Origin,
				) (Result, error) {
					return Result{}, nil
				},
			),
			testRunnerConfig(),
		)
		if err != nil {
			t.Fatalf("NewRunner() error = %v", err)
		}

		_, err = runner.RunOnce(context.Background())
		if !errors.Is(err, errTestClaim) {
			t.Errorf("RunOnce() error = %v", err)
		}
	})

	t.Run("record", func(t *testing.T) {
		runner, err := NewRunner(
			&fakeDiscoveryStore{
				claims: []fakeDiscoveryClaim{
					{
						source: source,
						found:  true,
					},
				},
				recordErr: errTestRecord,
			},
			discovererFunc(
				func(
					context.Context,
					origin.Origin,
				) (Result, error) {
					return Result{
						Source:     source,
						Status:     StatusComplete,
						Candidates: []Candidate{candidate},
					}, nil
				},
			),
			testRunnerConfig(),
		)
		if err != nil {
			t.Fatalf("NewRunner() error = %v", err)
		}

		_, err = runner.RunOnce(context.Background())
		if !errors.Is(err, errTestRecord) {
			t.Errorf("RunOnce() error = %v", err)
		}
	})
}

func TestRunnerLongRunningScheduling(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://example.com")

	t.Run("work immediately claims again", func(t *testing.T) {
		waitCalls := 0
		store := &fakeDiscoveryStore{
			claims: []fakeDiscoveryClaim{
				{
					source: source,
					found:  true,
				},
				{err: errTestClaim},
			},
		}
		runner, err := newRunner(
			store,
			discovererFunc(
				func(
					context.Context,
					origin.Origin,
				) (Result, error) {
					return Result{
						Source: source,
						Status: StatusUnavailable,
					}, nil
				},
			),
			testRunnerConfig(),
			waiterFunc(
				func(
					context.Context,
					time.Duration,
				) error {
					waitCalls++
					return nil
				},
			),
			contextTimeoutFactory{},
		)
		if err != nil {
			t.Fatalf("newRunner() error = %v", err)
		}

		err = runner.Run(context.Background())
		if !errors.Is(err, errTestClaim) {
			t.Errorf("Run() error = %v", err)
		}

		if waitCalls != 0 {
			t.Errorf(
				"wait calls = %d, want 0",
				waitCalls,
			)
		}
	})

	t.Run("idle waits", func(t *testing.T) {
		waitCalls := 0
		runner, err := newRunner(
			&fakeDiscoveryStore{},
			discovererFunc(
				func(
					context.Context,
					origin.Origin,
				) (Result, error) {
					return Result{}, nil
				},
			),
			testRunnerConfig(),
			waiterFunc(
				func(
					context.Context,
					time.Duration,
				) error {
					waitCalls++
					return errTestWait
				},
			),
			contextTimeoutFactory{},
		)
		if err != nil {
			t.Fatalf("newRunner() error = %v", err)
		}

		err = runner.Run(context.Background())
		if !errors.Is(err, errTestWait) {
			t.Errorf("Run() error = %v", err)
		}

		if waitCalls != 1 {
			t.Errorf(
				"wait calls = %d, want 1",
				waitCalls,
			)
		}
	})

	t.Run("cancellation interrupts wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(
			context.Background(),
		)
		runner, err := newRunner(
			&fakeDiscoveryStore{},
			discovererFunc(
				func(
					context.Context,
					origin.Origin,
				) (Result, error) {
					return Result{}, nil
				},
			),
			testRunnerConfig(),
			waiterFunc(
				func(
					waitContext context.Context,
					_ time.Duration,
				) error {
					cancel()
					return waitContext.Err()
				},
			),
			contextTimeoutFactory{},
		)
		if err != nil {
			t.Fatalf("newRunner() error = %v", err)
		}

		err = runner.Run(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run() error = %v", err)
		}
	})
}

func TestRunnerValidation(t *testing.T) {
	validStore := &fakeDiscoveryStore{}
	validDiscoverer := discovererFunc(
		func(
			context.Context,
			origin.Origin,
		) (Result, error) {
			return Result{}, nil
		},
	)
	validWaiter := waiterFunc(
		func(context.Context, time.Duration) error {
			return nil
		},
	)
	validTimeout := contextTimeoutFactory{}

	tests := []struct {
		name       string
		store      Store
		discoverer Discoverer
		config     Config
		waiter     waitStrategy
		timeout    timeoutFactory
		want       error
	}{
		{
			name:       "nil store",
			discoverer: validDiscoverer,
			config:     testRunnerConfig(),
			waiter:     validWaiter,
			timeout:    validTimeout,
			want:       errStoreUnavailable,
		},
		{
			name:    "nil discoverer",
			store:   validStore,
			config:  testRunnerConfig(),
			waiter:  validWaiter,
			timeout: validTimeout,
			want:    errDiscovererUnavailable,
		},
		{
			name:       "invalid config",
			store:      validStore,
			discoverer: validDiscoverer,
			waiter:     validWaiter,
			timeout:    validTimeout,
			want:       errInvalidConfig,
		},
		{
			name:       "nil waiter",
			store:      validStore,
			discoverer: validDiscoverer,
			config:     testRunnerConfig(),
			timeout:    validTimeout,
			want:       errWaiterUnavailable,
		},
		{
			name:       "nil timeout",
			store:      validStore,
			discoverer: validDiscoverer,
			config:     testRunnerConfig(),
			waiter:     validWaiter,
			want:       errTimeoutFactoryUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner, err := newRunner(
				test.store,
				test.discoverer,
				test.config,
				test.waiter,
				test.timeout,
			)
			if !errors.Is(err, test.want) {
				t.Errorf(
					"newRunner() error = %v, want %v",
					err,
					test.want,
				)
			}

			if runner != nil {
				t.Errorf("runner = %#v, want nil", runner)
			}
		})
	}

	runner, err := NewRunner(
		validStore,
		validDiscoverer,
		testRunnerConfig(),
	)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	var nilRunner *Runner
	_, err = nilRunner.RunOnce(context.Background())
	if !errors.Is(err, errRunnerUnavailable) {
		t.Errorf("nil runner error = %v", err)
	}

	_, err = runner.RunOnce(nil)
	if !errors.Is(err, errInvalidContext) {
		t.Errorf("nil context error = %v", err)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()
	_, err = runner.RunOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("canceled context error = %v", err)
	}

	broken := *runner
	broken.store = nil
	_, err = broken.RunOnce(context.Background())
	if !errors.Is(err, errStoreUnavailable) {
		t.Errorf("nil stored store error = %v", err)
	}

	broken = *runner
	broken.discoverer = nil
	_, err = broken.RunOnce(context.Background())
	if !errors.Is(err, errDiscovererUnavailable) {
		t.Errorf("nil stored discoverer error = %v", err)
	}

	broken = *runner
	broken.config = Config{}
	_, err = broken.RunOnce(context.Background())
	if !errors.Is(err, errInvalidConfig) {
		t.Errorf("invalid stored config error = %v", err)
	}

	broken = *runner
	broken.timeoutFactory = nil
	_, err = broken.RunOnce(context.Background())
	if !errors.Is(err, errTimeoutFactoryUnavailable) {
		t.Errorf("nil stored timeout error = %v", err)
	}

	broken = *runner
	broken.waiter = nil
	err = broken.Run(context.Background())
	if !errors.Is(err, errWaiterUnavailable) {
		t.Errorf("nil stored waiter error = %v", err)
	}

	for _, config := range []Config{
		{
			PollInterval: time.Second,
			PageTimeout:  time.Second,
		},
		{
			DiscoveryInterval: time.Second,
			PageTimeout:       time.Second,
		},
		{
			DiscoveryInterval: time.Second,
			PollInterval:      time.Second,
		},
	} {
		if validConfig(config) {
			t.Errorf("validConfig(%#v) = true", config)
		}
	}

	if !validConfig(testRunnerConfig()) {
		t.Error("validConfig(valid) = false")
	}
}

func TestDefaultWaitAndTimeoutStrategies(t *testing.T) {
	if err := (timerWaitStrategy{}).Wait(
		context.Background(),
		0,
	); err != nil {
		t.Errorf("zero wait error = %v", err)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := (timerWaitStrategy{}).Wait(
		ctx,
		time.Hour,
	); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled wait error = %v", err)
	}

	timeoutContext, cancelTimeout :=
		(contextTimeoutFactory{}).WithTimeout(
			context.Background(),
			time.Hour,
		)
	cancelTimeout()

	if !errors.Is(
		timeoutContext.Err(),
		context.Canceled,
	) {
		t.Errorf(
			"timeout context error = %v",
			timeoutContext.Err(),
		)
	}
}

type getterStep struct {
	response *http.Response
	err      error
}

type scriptedGetter struct {
	steps []getterStep
	calls []string
}

func (getter *scriptedGetter) Get(
	_ context.Context,
	target *url.URL,
) (*http.Response, error) {
	index := len(getter.calls)
	getter.calls = append(getter.calls, target.String())

	if index >= len(getter.steps) {
		return nil, errUnexpectedFetch
	}

	step := getter.steps[index]
	return step.response, step.err
}

type getterFunc func(
	context.Context,
	*url.URL,
) (*http.Response, error)

func (function getterFunc) Get(
	ctx context.Context,
	target *url.URL,
) (*http.Response, error) {
	return function(ctx, target)
}

type trackedBody struct {
	reader     io.Reader
	closeErr   error
	closeCount int
}

func newTrackedBody(value string) *trackedBody {
	return &trackedBody{
		reader: strings.NewReader(value),
	}
}

func (body *trackedBody) Read(
	destination []byte,
) (int, error) {
	if body.reader == nil {
		return 0, io.EOF
	}

	return body.reader.Read(destination)
}

func (body *trackedBody) Close() error {
	body.closeCount++
	return body.closeErr
}

type cancelingBody struct {
	cancel     context.CancelFunc
	data       []byte
	sent       bool
	closeCount int
}

func (body *cancelingBody) Read(
	destination []byte,
) (int, error) {
	if body.sent {
		return 0, io.EOF
	}

	body.sent = true
	count := copy(destination, body.data)
	body.cancel()
	return count, nil
}

func (body *cancelingBody) Close() error {
	body.closeCount++
	return nil
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

func (reader *previewThenErrorReader) Read(
	destination []byte,
) (int, error) {
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

func discoveryResponse(
	status int,
	contentType string,
	location string,
	body io.ReadCloser,
) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	if location != "" {
		header.Set("Location", location)
	}

	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       body,
	}
}

func assertDiscoveryBodyClosedOnce(
	t *testing.T,
	body *trackedBody,
) {
	t.Helper()

	if body.closeCount != 1 {
		t.Errorf(
			"body close count = %d, want 1",
			body.closeCount,
		)
	}
}

func mustDiscoveryOrigin(
	t *testing.T,
	rawURL string,
) origin.Origin {
	t.Helper()

	value, err := origin.Parse(rawURL)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			rawURL,
			err,
		)
	}

	return value
}

func mustDiscoveryURL(
	t *testing.T,
	rawURL string,
) *url.URL {
	t.Helper()

	value, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf(
			"url.Parse(%q) error = %v",
			rawURL,
			err,
		)
	}

	return value
}

func candidateOriginStrings(
	candidates []Candidate,
) []string {
	if candidates == nil {
		return nil
	}

	result := make([]string, len(candidates))
	for index, candidate := range candidates {
		result[index] = candidate.Origin.String()
	}

	return result
}

type fakeDiscoveryClaim struct {
	source origin.Origin
	found  bool
	err    error
}

type fakeDiscoveryStore struct {
	claims             []fakeDiscoveryClaim
	claimIndex         int
	recordResult       RecordResult
	recordErr          error
	recordCalls        int
	recordedSource     origin.Origin
	recordedCandidates []Candidate
}

func (store *fakeDiscoveryStore) ClaimDiscoverySource(
	context.Context,
	time.Duration,
) (origin.Origin, bool, error) {
	if store.claimIndex >= len(store.claims) {
		return origin.Origin{}, false, nil
	}

	claim := store.claims[store.claimIndex]
	store.claimIndex++

	return claim.source, claim.found, claim.err
}

func (store *fakeDiscoveryStore) RecordDiscovery(
	_ context.Context,
	source origin.Origin,
	candidates []Candidate,
) (RecordResult, error) {
	store.recordCalls++
	store.recordedSource = source
	store.recordedCandidates = append(
		[]Candidate(nil),
		candidates...,
	)

	return store.recordResult, store.recordErr
}

type discovererFunc func(
	context.Context,
	origin.Origin,
) (Result, error)

func (function discovererFunc) Discover(
	ctx context.Context,
	source origin.Origin,
) (Result, error) {
	return function(ctx, source)
}

type waiterFunc func(
	context.Context,
	time.Duration,
) error

func (function waiterFunc) Wait(
	ctx context.Context,
	duration time.Duration,
) error {
	return function(ctx, duration)
}

type timeoutFactoryFunc func(
	context.Context,
	time.Duration,
) (context.Context, context.CancelFunc)

func (function timeoutFactoryFunc) WithTimeout(
	ctx context.Context,
	duration time.Duration,
) (context.Context, context.CancelFunc) {
	return function(ctx, duration)
}

type recordingTimeoutFactory struct {
	duration time.Duration
}

func (factory *recordingTimeoutFactory) WithTimeout(
	ctx context.Context,
	duration time.Duration,
) (context.Context, context.CancelFunc) {
	factory.duration = duration
	return context.WithCancel(ctx)
}

func testRunnerConfig() Config {
	return Config{
		DiscoveryInterval: 24 * time.Hour,
		PollInterval:      time.Minute,
		PageTimeout:       30 * time.Second,
	}
}

package discovery

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/origin"
)

const largeDirectoryCandidateCount = 2083

func TestExtractPageLinksClassifiesNavigableHyperlinks(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	pageURL := mustDiscoveryURL(
		t,
		"https://example.com/directory/index.html",
	)

	body := []byte(`
		<!doctype html>
		<html>
		<head>
			<link
				rel="stylesheet"
				href="https://stylesheet.example/site.css"
			>
		</head>
		<body>
			<a href="/root#first">root relative</a>
			<a href="child?view=all#section">relative</a>
			<a href="https://EXAMPLE.COM:443/absolute">
				same-origin absolute
			</a>
			<a href="https://example.com/root#second">
				duplicate after fragment removal
			</a>
			<a href="?page=2#top">query on current path</a>
			<area href="/map?a=1&amp;b=2#spot">
			<area href="https://area.example/zone">

			<a href="https://external.example/articles/1">
				external HTTPS
			</a>
			<a href="http://plain.example:80/path">
				external HTTP
			</a>
			<a href="https://port.example:8443/path">
				external non-default port
			</a>
			<a href="//scheme.example/path">
				scheme-relative external
			</a>
			<a href="https://EXTERNAL.example:443/duplicate">
				duplicate external origin
			</a>

			<a href="mailto:person@example.net">mail</a>
			<a href="tel:+10000000000">phone</a>
			<a href="javascript:alert(1)">script</a>
			<a href="data:text/plain,hello">data</a>
			<a href="ftp://files.example/file">FTP</a>
			<a href="file:///tmp/file">file</a>
			<a href="wss://socket.example/path">websocket</a>
			<a href="https://user:password@credential.example/">
				credentials
			</a>
			<a href="https://[broken">malformed</a>
			<a>missing href</a>

			<img src="https://image.example/image.png">
			<script src="https://script.example/app.js"></script>
			<iframe src="https://frame.example/"></iframe>
			<form action="https://form.example/"></form>
			<video src="https://video.example/movie.mp4"></video>
			<audio src="https://audio.example/song.mp3"></audio>
		</body>
		</html>
	`)

	links, status := mustExtractPageLinks(
		t,
		source,
		pageURL,
		"text/html; charset=utf-8",
		body,
	)

	wantInternal := []string{
		"https://example.com/root",
		"https://example.com/directory/child?view=all",
		"https://example.com/absolute",
		"https://example.com/directory/index.html?page=2",
		"https://example.com/map?a=1&b=2",
	}
	gotInternal := pageURLStrings(links.Internal)

	if !reflect.DeepEqual(gotInternal, wantInternal) {
		t.Errorf(
			"internal URLs = %#v, want %#v",
			gotInternal,
			wantInternal,
		)
	}

	wantCandidates := []string{
		"http://plain.example",
		"https://area.example",
		"https://external.example",
		"https://port.example:8443",
		"https://scheme.example",
	}
	gotCandidates := pageCandidateStrings(
		links.Candidates,
	)

	if !reflect.DeepEqual(
		gotCandidates,
		wantCandidates,
	) {
		t.Errorf(
			"candidate origins = %#v, want %#v",
			gotCandidates,
			wantCandidates,
		)
	}

	for _, candidate := range links.Candidates {
		if candidate.Kind != KindLink {
			t.Errorf(
				"candidate kind = %v, want KindLink",
				candidate.Kind,
			)
		}
	}

	if status != StatusComplete {
		t.Errorf(
			"status = %v, want StatusComplete",
			status,
		)
	}
}

func TestExtractPageLinksPreservesQueriesAndCollapsesFragments(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	pageURL := mustDiscoveryURL(
		t,
		"https://example.com/",
	)

	links, status := mustExtractPageLinks(
		t,
		source,
		pageURL,
		"text/html",
		[]byte(`
			<a href="/articles?page=1#top">one</a>
			<a href="/articles?page=1#middle">duplicate</a>
			<a href="/articles?page=2#top">two</a>
			<a href="/articles#one">plain</a>
			<a href="/articles#two">duplicate plain</a>
		`),
	)

	want := []string{
		"https://example.com/articles?page=1",
		"https://example.com/articles?page=2",
		"https://example.com/articles",
	}
	got := pageURLStrings(links.Internal)

	if !reflect.DeepEqual(got, want) {
		t.Errorf(
			"internal URLs = %#v, want %#v",
			got,
			want,
		)
	}

	if len(links.Candidates) != 0 {
		t.Errorf(
			"candidates = %#v, want empty",
			links.Candidates,
		)
	}

	if status != StatusComplete {
		t.Errorf(
			"status = %v, want StatusComplete",
			status,
		)
	}
}

func TestExtractPageLinksHonorsDocumentBase(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	pageURL := mustDiscoveryURL(
		t,
		"https://example.com/directory/page",
	)

	tests := []struct {
		name           string
		body           string
		wantInternal   []string
		wantCandidates []string
	}{
		{
			name: "relative same-origin base",
			body: `
				<base href="../assets/">
				<a href="one">one</a>
				<a href="two?q=1#fragment">two</a>
				<a href="https://external.example/path">
					external
				</a>
			`,
			wantInternal: []string{
				"https://example.com/assets/one",
				"https://example.com/assets/two?q=1",
			},
			wantCandidates: []string{
				"https://external.example",
			},
		},
		{
			name: "first external base wins",
			body: `
				<base href="https://assets.example.net/root/">
				<base href="https://ignored.example/">
				<a href="relative/page">external through base</a>
				<a href="/absolute">external through base root</a>
				<a href="https://example.com/local#fragment">
					explicit internal
				</a>
			`,
			wantInternal: []string{
				"https://example.com/local",
			},
			wantCandidates: []string{
				"https://assets.example.net",
			},
		},
		{
			name: "invalid first base falls back to page",
			body: `
				<base href="%">
				<base href="https://ignored.example/">
				<a href="../local">local</a>
				<a href="https://external.example/path">
					external
				</a>
			`,
			wantInternal: []string{
				"https://example.com/local",
			},
			wantCandidates: []string{
				"https://external.example",
			},
		},
		{
			name: "base without href is skipped",
			body: `
				<base>
				<base href="/selected/">
				<a href="page">local</a>
				<a href="https://external.example/path">
					external
				</a>
			`,
			wantInternal: []string{
				"https://example.com/selected/page",
			},
			wantCandidates: []string{
				"https://external.example",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			links, status := mustExtractPageLinks(
				t,
				source,
				pageURL,
				"text/html",
				[]byte(test.body),
			)

			gotInternal := pageURLStrings(
				links.Internal,
			)
			if !reflect.DeepEqual(
				gotInternal,
				test.wantInternal,
			) {
				t.Errorf(
					"internal URLs = %#v, want %#v",
					gotInternal,
					test.wantInternal,
				)
			}

			gotCandidates := pageCandidateStrings(
				links.Candidates,
			)
			if !reflect.DeepEqual(
				gotCandidates,
				test.wantCandidates,
			) {
				t.Errorf(
					"candidate origins = %#v, want %#v",
					gotCandidates,
					test.wantCandidates,
				)
			}

			if status != StatusComplete {
				t.Errorf(
					"status = %v, want StatusComplete",
					status,
				)
			}
		})
	}
}

func TestExtractPageLinksCanonicalizesIDNA(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://bücher.example",
	)
	pageURL := mustDiscoveryURL(
		t,
		"https://xn--bcher-kva.example/directory/",
	)

	links, status := mustExtractPageLinks(
		t,
		source,
		pageURL,
		"text/html",
		[]byte(`
			<a href="https://bücher.example/seite#abschnitt">
				internal IDNA
			</a>
			<a href="mehr?q=1#abschnitt">
				relative IDNA
			</a>
			<a href="https://münchen.example/path">
				external IDNA
			</a>
		`),
	)

	wantInternal := []string{
		"https://xn--bcher-kva.example/seite",
		"https://xn--bcher-kva.example/directory/mehr?q=1",
	}
	if got := pageURLStrings(
		links.Internal,
	); !reflect.DeepEqual(got, wantInternal) {
		t.Errorf(
			"internal URLs = %#v, want %#v",
			got,
			wantInternal,
		)
	}

	wantCandidates := []string{
		"https://xn--mnchen-3ya.example",
	}
	if got := pageCandidateStrings(
		links.Candidates,
	); !reflect.DeepEqual(got, wantCandidates) {
		t.Errorf(
			"candidate origins = %#v, want %#v",
			got,
			wantCandidates,
		)
	}

	if status != StatusComplete {
		t.Errorf(
			"status = %v, want StatusComplete",
			status,
		)
	}
}

func TestExtractPageLinksCanonicalizesIPv6(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://[2001:db8::1]",
	)
	pageURL := mustDiscoveryURL(
		t,
		"https://[2001:db8::1]/directory/page",
	)

	links, status := mustExtractPageLinks(
		t,
		source,
		pageURL,
		"text/html",
		[]byte(`
			<a href="https://[2001:0db8:0:0:0:0:0:1]:443/inside#fragment">
				internal IPv6
			</a>
			<a href="../relative?q=1#fragment">
				relative IPv6
			</a>
			<a href="https://[2001:db8::2]:8443/path">
				external IPv6
			</a>
		`),
	)

	wantInternal := []string{
		"https://[2001:db8::1]/inside",
		"https://[2001:db8::1]/relative?q=1",
	}
	if got := pageURLStrings(
		links.Internal,
	); !reflect.DeepEqual(got, wantInternal) {
		t.Errorf(
			"internal URLs = %#v, want %#v",
			got,
			wantInternal,
		)
	}

	wantCandidates := []string{
		"https://[2001:db8::2]:8443",
	}
	if got := pageCandidateStrings(
		links.Candidates,
	); !reflect.DeepEqual(got, wantCandidates) {
		t.Errorf(
			"candidate origins = %#v, want %#v",
			got,
			wantCandidates,
		)
	}

	if status != StatusComplete {
		t.Errorf(
			"status = %v, want StatusComplete",
			status,
		)
	}
}

func TestExtractPageLinksReturnsEveryLargeDirectoryCandidate(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://directory.example",
	)
	pageURL := mustDiscoveryURL(
		t,
		"https://directory.example/",
	)

	var document strings.Builder
	document.WriteString("<!doctype html><html><body>")

	for index := largeDirectoryCandidateCount - 1; index >= 0; index-- {
		fmt.Fprintf(
			&document,
			`<a href="https://candidate-%04d.example/path/%d#fragment">candidate</a>`,
			index,
			index,
		)
	}

	document.WriteString(
		`<a href="https://candidate-0000.example/duplicate">duplicate</a>`,
	)
	document.WriteString("</body></html>")

	body := []byte(document.String())
	if len(body) > MaxRawBody {
		t.Fatalf(
			"generated body size = %d, maximum = %d",
			len(body),
			MaxRawBody,
		)
	}

	links, status := mustExtractPageLinks(
		t,
		source,
		pageURL,
		"text/html; charset=utf-8",
		body,
	)

	if len(links.Internal) != 0 {
		t.Errorf(
			"internal URL count = %d, want 0",
			len(links.Internal),
		)
	}

	if len(links.Candidates) !=
		largeDirectoryCandidateCount {
		t.Fatalf(
			"candidate count = %d, want %d",
			len(links.Candidates),
			largeDirectoryCandidateCount,
		)
	}

	for index, candidate := range links.Candidates {
		wantOrigin := fmt.Sprintf(
			"https://candidate-%04d.example",
			index,
		)

		if candidate.Origin.String() != wantOrigin {
			t.Errorf(
				"candidate %d origin = %q, want %q",
				index,
				candidate.Origin,
				wantOrigin,
			)
		}

		if candidate.Kind != KindLink {
			t.Errorf(
				"candidate %d kind = %v, want KindLink",
				index,
				candidate.Kind,
			)
		}
	}

	if status != StatusComplete {
		t.Errorf(
			"status = %v, want StatusComplete",
			status,
		)
	}
}

func TestExtractPageLinksOrderIsDeterministic(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	pageURL := mustDiscoveryURL(
		t,
		"https://example.com/root/",
	)
	body := []byte(`
		<a href="/z">z</a>
		<a href="a">a</a>
		<a href="/m#one">m one</a>
		<a href="/m#two">m two</a>
		<a href="https://z.example/path">external z</a>
		<a href="https://a.example/path">external a</a>
		<a href="https://m.example/path">external m</a>
	`)

	first, firstStatus := mustExtractPageLinks(
		t,
		source,
		pageURL,
		"text/html",
		body,
	)
	second, secondStatus := mustExtractPageLinks(
		t,
		source,
		pageURL,
		"text/html",
		body,
	)

	if !reflect.DeepEqual(first, second) {
		t.Errorf(
			"identical input produced different links:\nfirst: %#v\nsecond: %#v",
			first,
			second,
		)
	}

	if firstStatus != secondStatus {
		t.Errorf(
			"identical input statuses = %v and %v",
			firstStatus,
			secondStatus,
		)
	}

	wantInternal := []string{
		"https://example.com/z",
		"https://example.com/root/a",
		"https://example.com/m",
	}
	if got := pageURLStrings(
		first.Internal,
	); !reflect.DeepEqual(got, wantInternal) {
		t.Errorf(
			"internal order = %#v, want %#v",
			got,
			wantInternal,
		)
	}

	wantCandidates := []string{
		"https://a.example",
		"https://m.example",
		"https://z.example",
	}
	if got := pageCandidateStrings(
		first.Candidates,
	); !reflect.DeepEqual(got, wantCandidates) {
		t.Errorf(
			"candidate order = %#v, want %#v",
			got,
			wantCandidates,
		)
	}
}

func TestExtractPageLinksPreservesContentBoundaries(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	pageURL := mustDiscoveryURL(
		t,
		"https://example.com/",
	)

	tests := []struct {
		name        string
		contentType string
		body        []byte
		wantStatus  Status
	}{
		{
			name:        "explicit non-HTML",
			contentType: "application/json",
			body: []byte(
				`<a href="https://external.example/">site</a>`,
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
			name:        "raw body too large",
			contentType: "text/html",
			body: bytes.Repeat(
				[]byte("x"),
				MaxRawBody+1,
			),
			wantStatus: StatusTooLarge,
		},
		{
			name: "missing content type is sniffed",
			body: []byte(
				`<!doctype html><a href="/page">page</a>`,
			),
			wantStatus: StatusComplete,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			links, status, err := ExtractPageLinks(
				source,
				pageURL,
				test.contentType,
				test.body,
			)
			if err != nil {
				t.Fatalf(
					"ExtractPageLinks() error = %v, want nil",
					err,
				)
			}

			if status != test.wantStatus {
				t.Errorf(
					"status = %v, want %v",
					status,
					test.wantStatus,
				)
			}

			if status != StatusComplete &&
				(len(links.Internal) != 0 ||
					len(links.Candidates) != 0) {
				t.Errorf(
					"links = %#v, want empty",
					links,
				)
			}
		})
	}
}

func TestExtractPageLinksRejectsInvalidInput(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	pageURL := mustDiscoveryURL(
		t,
		"https://example.com/",
	)

	_, _, err := ExtractPageLinks(
		origin.Origin{},
		pageURL,
		"text/html",
		nil,
	)
	if !errors.Is(err, errInvalidSource) {
		t.Errorf(
			"zero source error = %v, want errInvalidSource",
			err,
		)
	}

	_, _, err = ExtractPageLinks(
		source,
		nil,
		"text/html",
		nil,
	)
	if !errors.Is(err, errInvalidPageURL) {
		t.Errorf(
			"nil page error = %v, want errInvalidPageURL",
			err,
		)
	}

	_, _, err = ExtractPageLinks(
		source,
		mustDiscoveryURL(
			t,
			"https://other.example/",
		),
		"text/html",
		nil,
	)
	if !errors.Is(err, errInvalidPageURL) {
		t.Errorf(
			"cross-origin page error = %v, want errInvalidPageURL",
			err,
		)
	}

	_, _, err = ExtractPageLinks(
		source,
		&url.URL{
			Scheme: "mailto",
			Opaque: "person@example.net",
		},
		"text/html",
		nil,
	)
	if !errors.Is(err, errInvalidPageURL) {
		t.Errorf(
			"invalid page error = %v, want errInvalidPageURL",
			err,
		)
	}
}

func TestPageLinksContainsNoPageContentStorage(
	t *testing.T,
) {
	pageLinksType := reflect.TypeOf(PageLinks{})

	wantFields := map[string]reflect.Type{
		"Internal": reflect.TypeOf(
			[]*url.URL(nil),
		),
		"Candidates": reflect.TypeOf(
			[]Candidate(nil),
		),
	}

	if pageLinksType.NumField() != len(wantFields) {
		t.Fatalf(
			"PageLinks field count = %d, want %d",
			pageLinksType.NumField(),
			len(wantFields),
		)
	}

	for fieldName, wantType := range wantFields {
		field, found := pageLinksType.FieldByName(
			fieldName,
		)
		if !found {
			t.Errorf(
				"PageLinks is missing field %q",
				fieldName,
			)
			continue
		}

		if field.Type != wantType {
			t.Errorf(
				"PageLinks.%s type = %v, want %v",
				fieldName,
				field.Type,
				wantType,
			)
		}
	}
}

func mustExtractPageLinks(
	t *testing.T,
	source origin.Origin,
	pageURL *url.URL,
	contentType string,
	body []byte,
) (PageLinks, Status) {
	t.Helper()

	links, status, err := ExtractPageLinks(
		source,
		pageURL,
		contentType,
		body,
	)
	if err != nil {
		t.Fatalf(
			"ExtractPageLinks() error = %v, want nil",
			err,
		)
	}

	return links, status
}

func pageURLStrings(
	pageURLs []*url.URL,
) []string {
	result := make(
		[]string,
		len(pageURLs),
	)

	for index, pageURL := range pageURLs {
		if pageURL == nil {
			result[index] = "<nil>"
			continue
		}

		result[index] = pageURL.String()
	}

	return result
}

func pageCandidateStrings(
	candidates []Candidate,
) []string {
	result := make(
		[]string,
		len(candidates),
	)

	for index, candidate := range candidates {
		result[index] = candidate.Origin.String()
	}

	return result
}

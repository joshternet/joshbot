package discovery

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/joshternet/joshbot/internal/origin"
)

func FuzzExtractPageLinks(f *testing.F) {
	seeds := [][]byte{
		[]byte(
			`<!doctype html><a href="https://example.net/">site</a>`,
		),
		[]byte(
			`<base href="https://example.org/"><area href="/map">`,
		),
		[]byte(
			`<a href="/internal#fragment">internal</a>`,
		),
		[]byte(
			`<a href="/page?q=1#one">one</a><a href="/page?q=1#two">two</a>`,
		),
		[]byte(
			`<a href="javascript:alert(1)">ignored</a>`,
		),
		[]byte(
			`<a href="https://user:password@example.net/">ignored</a>`,
		),
		{0xff, 0xfe, 0x00, '<', 'a', '>'},
	}

	var largeSeed strings.Builder
	for index := 0; index < 80; index++ {
		fmt.Fprintf(
			&largeSeed,
			`<a href="https://candidate-%03d.example/path">site</a>`,
			index,
		)
	}
	seeds = append(
		seeds,
		[]byte(largeSeed.String()),
	)

	for _, seed := range seeds {
		f.Add(seed)
	}

	source, err := origin.Parse(
		"https://example.com",
	)
	if err != nil {
		f.Fatal(err)
	}

	pageURL, err := url.Parse(
		"https://example.com/directory/page",
	)
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > MaxRawBody {
			body = body[:MaxRawBody]
		}

		first, firstStatus, firstErr :=
			ExtractPageLinks(
				source,
				pageURL,
				"text/html; charset=utf-8",
				body,
			)
		second, secondStatus, secondErr :=
			ExtractPageLinks(
				source,
				pageURL,
				"text/html; charset=utf-8",
				body,
			)

		if (firstErr == nil) != (secondErr == nil) {
			t.Fatal(
				"identical input produced inconsistent errors",
			)
		}

		if firstErr != nil {
			return
		}

		if firstStatus != secondStatus {
			t.Fatalf(
				"identical input statuses = %v and %v",
				firstStatus,
				secondStatus,
			)
		}

		if !reflect.DeepEqual(first, second) {
			t.Fatal(
				"identical input produced different links",
			)
		}

		internalSeen := make(map[string]struct{})

		for _, page := range first.Internal {
			if page == nil {
				t.Fatal("nil internal page URL")
			}

			if page.Fragment != "" ||
				page.RawFragment != "" {
				t.Fatalf(
					"internal page contains fragment: %q",
					page,
				)
			}

			pageOrigin, parseErr := origin.Parse(
				page.String(),
			)
			if parseErr != nil {
				t.Fatalf(
					"invalid internal page URL %q: %v",
					page,
					parseErr,
				)
			}

			if pageOrigin != source {
				t.Fatalf(
					"internal page origin = %q, want %q",
					pageOrigin,
					source,
				)
			}

			canonical := page.String()
			if _, duplicate := internalSeen[canonical]; duplicate {
				t.Fatalf(
					"duplicate internal page URL %q",
					canonical,
				)
			}
			internalSeen[canonical] = struct{}{}
		}

		candidateSeen := make(
			map[origin.Origin]struct{},
		)

		previous := ""
		for _, candidate := range first.Candidates {
			if candidate.Origin.String() == "" {
				t.Fatal("zero candidate origin")
			}

			if candidate.Origin == source {
				t.Fatal(
					"source returned as external candidate",
				)
			}

			if candidate.Kind != KindLink {
				t.Fatalf(
					"candidate kind = %v, want KindLink",
					candidate.Kind,
				)
			}

			if _, duplicate :=
				candidateSeen[candidate.Origin]; duplicate {
				t.Fatalf(
					"duplicate candidate origin %q",
					candidate.Origin,
				)
			}
			candidateSeen[candidate.Origin] = struct{}{}

			canonical := candidate.Origin.String()
			if previous != "" &&
				canonical < previous {
				t.Fatalf(
					"candidate ordering is not deterministic: %q before %q",
					previous,
					canonical,
				)
			}
			previous = canonical
		}
	})
}

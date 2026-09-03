package discovery

import (
	"net/url"
	"reflect"
	"testing"

	"github.com/joshternet/joshbot/internal/origin"
)

func FuzzExtract(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`<!doctype html><a href="https://example.net/">site</a>`),
		[]byte(`<base href="https://example.org/"><area href="/map">`),
		[]byte(`<a href="javascript:alert(1)">ignored</a>`),
		[]byte{0xff, 0xfe, 0x00, '<', 'a', '>'},
	} {
		f.Add(seed)
	}

	source, err := origin.Parse("https://example.com")
	if err != nil {
		f.Fatal(err)
	}

	pageURL, err := url.Parse("https://example.com/")
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > MaxRawBody {
			body = body[:MaxRawBody]
		}

		first, firstErr := Extract(
			source,
			pageURL,
			"text/html; charset=utf-8",
			body,
		)
		second, secondErr := Extract(
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

		if !reflect.DeepEqual(first, second) {
			t.Fatal(
				"identical input produced different results",
			)
		}

		if len(first.Candidates) > MaxCandidates {
			t.Fatalf(
				"candidate count = %d, maximum = %d",
				len(first.Candidates),
				MaxCandidates,
			)
		}

		seen := make(map[origin.Origin]struct{})
		previous := ""

		for _, candidate := range first.Candidates {
			if candidate.Origin.String() == "" {
				t.Fatal("zero candidate origin")
			}

			if candidate.Origin == source {
				t.Fatal("source returned as candidate")
			}

			if candidate.Kind != KindLink {
				t.Fatalf(
					"candidate kind = %v, want KindLink",
					candidate.Kind,
				)
			}

			if _, duplicate := seen[candidate.Origin]; duplicate {
				t.Fatal("duplicate candidate origin")
			}
			seen[candidate.Origin] = struct{}{}

			canonical := candidate.Origin.String()
			if previous != "" && canonical < previous {
				t.Fatal("candidate ordering is unstable")
			}
			previous = canonical
		}
	})
}

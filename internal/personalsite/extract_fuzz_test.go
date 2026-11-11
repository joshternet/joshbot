package personalsite

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func FuzzExtractSignals(f *testing.F) {
	seeds := [][]byte{
		[]byte(`<!doctype html><html><body><a href="/about">a</a></body></html>`),
		[]byte(`<script type="application/ld+json">{"@type":"Person"}</script>`),
		[]byte(`<div class="h-card"></div><a rel="me" href="https://x.example/">x</a>`),
		[]byte(`<link rel="alternate" type="application/rss+xml" href="/feed">`),
		[]byte(`<a href="/cart"><a href="/careers"><a href="/shop">`),
		[]byte(`{"not":"html"}`),
		{0xff, 0xfe, 0x00, '<', 'a', '>'},
	}

	var largeSeed strings.Builder
	for index := 0; index < 80; index++ {
		fmt.Fprintf(
			&largeSeed,
			`<a href="/about/%d">link</a>`,
			index,
		)
	}
	seeds = append(seeds, []byte(largeSeed.String()))

	for _, seed := range seeds {
		f.Add(seed)
	}

	pageURL, err := url.Parse("https://example.com/")
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > maxRawBody {
			body = body[:maxRawBody]
		}

		first, firstStatus, _, firstErr := ExtractSignals(
			pageURL,
			"text/html; charset=utf-8",
			body,
		)
		second, secondStatus, _, secondErr := ExtractSignals(
			pageURL,
			"text/html; charset=utf-8",
			body,
		)

		if firstErr != nil || secondErr != nil {
			t.Fatalf(
				"ExtractSignals() errors = %v, %v",
				firstErr,
				secondErr,
			)
		}
		if firstStatus != secondStatus {
			t.Fatalf(
				"status mismatch: %v vs %v",
				firstStatus,
				secondStatus,
			)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf(
				"non-deterministic signals: %#v vs %#v",
				first,
				second,
			)
		}

		assessment := ScorePersonalSite(first)
		if assessment.Score < 0 || assessment.Score > 100 {
			t.Fatalf("score out of range: %d", assessment.Score)
		}
		if assessment.Version != AssessmentVersion {
			t.Fatalf(
				"version = %d, want %d",
				assessment.Version,
				AssessmentVersion,
			)
		}
	})
}

package robots

import (
	"bytes"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func FuzzParseAndEvaluate(f *testing.F) {
	f.Add(
		[]byte(
			"User-agent: Joshternet-Joshbot\n"+
				"Disallow: /private\n",
		),
		"https://example.com/private/page",
	)
	f.Add(
		[]byte(
			"# policy comment\n"+
				"User-agent: * # fallback\n"+
				"Allow: /\n",
		),
		"https://example.com/public?view=full#section",
	)
	f.Add(
		[]byte(
			"User-agent: Joshternet-Joshbot\n"+
				"Disallow: /*.gif$\n"+
				"Allow: /public/*.gif$\n",
		),
		"https://example.com/public/image.gif",
	)
	f.Add(
		[]byte{
			0xff,
			0xfe,
			'\n',
			'U',
			's',
			'e',
			'r',
			'-',
			'a',
			'g',
			'e',
			'n',
			't',
			':',
			' ',
			'*',
			'\n',
			'D',
			'i',
			's',
			'a',
			'l',
			'l',
			'o',
			'w',
			':',
			' ',
			'/',
			'p',
			'r',
			'i',
			'v',
			'a',
			't',
			'e',
			'\n',
		},
		"https://example.com/private",
	)
	f.Add(
		[]byte(
			"malformed\n"+
				"User-agent:\x00 Joshternet-Joshbot\n"+
				"Disallow: missing-leading-slash\n"+
				"Unknown: value\n",
		),
		":// malformed target",
	)
	f.Add(
		[]byte(
			"User-agent: Joshternet-Joshbot\r\n"+
				"Disallow: /ツ\r\n",
		),
		"https://example.com/%E3%83%84",
	)

	exactBoundary := make([]byte, MaxBodySize)
	boundaryPrefix := []byte(
		"User-agent: Joshternet-Joshbot\n" +
			"Disallow: /boundary\n" +
			"#",
	)
	copy(exactBoundary, boundaryPrefix)
	for index := len(boundaryPrefix); index < len(exactBoundary); index++ {
		exactBoundary[index] = '#'
	}
	f.Add(
		exactBoundary,
		"https://example.com/boundary",
	)

	oversizedBoundary := make([]byte, MaxBodySize+1)
	copy(oversizedBoundary, exactBoundary)
	oversizedBoundary[len(oversizedBoundary)-1] = '#'
	f.Add(
		oversizedBoundary,
		"https://example.com/public",
	)

	f.Fuzz(func(
		t *testing.T,
		data []byte,
		rawTarget string,
	) {
		first, firstErr := readPolicy(bytes.NewReader(data))
		second, secondErr := readPolicy(bytes.NewReader(data))

		firstTooLarge := errors.Is(
			firstErr,
			errBodyTooLarge,
		)
		secondTooLarge := errors.Is(
			secondErr,
			errBodyTooLarge,
		)

		if firstTooLarge != secondTooLarge {
			t.Fatalf(
				"readPolicy() oversized results differ: %v and %v",
				firstErr,
				secondErr,
			)
		}

		if firstErr != nil && !firstTooLarge {
			t.Fatalf(
				"readPolicy() unexpected error = %v",
				firstErr,
			)
		}

		if secondErr != nil && !secondTooLarge {
			t.Fatalf(
				"second readPolicy() unexpected error = %v",
				secondErr,
			)
		}

		if !reflect.DeepEqual(first, second) {
			t.Fatalf(
				"readPolicy() produced nondeterministic policies: "+
					"%#v and %#v",
				first,
				second,
			)
		}

		target, err := url.Parse(rawTarget)
		if err != nil {
			target = nil
		}

		firstDecision := first.Allowed(target)
		repeatedDecision := first.Allowed(target)
		secondDecision := second.Allowed(target)

		if firstDecision != repeatedDecision ||
			firstDecision != secondDecision {
			t.Fatalf(
				"Policy.Allowed() decisions differ: %t, %t, %t",
				firstDecision,
				repeatedDecision,
				secondDecision,
			)
		}
	})
}

func TestPatternMatchesAdversarialWildcardInput(t *testing.T) {
	const wildcardCount = 4096

	pattern := strings.Repeat("*a", wildcardCount) + "b$"
	matchingTarget := strings.Repeat("a", wildcardCount) + "b"
	nonmatchingTarget := strings.Repeat("a", wildcardCount) + "c"

	if !patternMatches(pattern, matchingTarget) {
		t.Fatal("patternMatches() = false for matching input")
	}

	if patternMatches(pattern, nonmatchingTarget) {
		t.Fatal("patternMatches() = true for nonmatching input")
	}
}

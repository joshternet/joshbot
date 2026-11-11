package personalsite

import (
	"net/http"
	"testing"
	"time"
)

func TestRetryClassForAllOutcomes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   Qualification
		want RetryClass
	}{
		{
			name: "assessed",
			in:   Qualification{Outcome: QualificationAssessed},
			want: RetryClassNone,
		},
		{
			name: "timeout",
			in:   Qualification{Outcome: QualificationTimeout},
			want: RetryClassTransient,
		},
		{
			name: "dns",
			in:   Qualification{Outcome: QualificationDNSFailure},
			want: RetryClassTransient,
		},
		{
			name: "unsupported",
			in: Qualification{
				Outcome: QualificationUnsupportedContent,
			},
			want: RetryClassUnsupported,
		},
		{
			name: "parser",
			in:   Qualification{Outcome: QualificationParserFailure},
			want: RetryClassParser,
		},
		{
			name: "robots",
			in:   Qualification{Outcome: QualificationRobotsDenied},
			want: RetryClassRobots,
		},
		{
			name: "network",
			in: Qualification{
				Outcome: QualificationNetworkRejected,
			},
			want: RetryClassNetworkRejected,
		},
		{
			name: "unavailable",
			in:   Qualification{Outcome: QualificationUnavailable},
			want: RetryClassUnavailable,
		},
		{
			name: "too large",
			in:   Qualification{Outcome: QualificationTooLarge},
			want: RetryClassUnavailable,
		},
		{
			name: "unknown outcome",
			in:   Qualification{Outcome: "mystery"},
			want: RetryClassUnavailable,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got := RetryClassFor(testCase.in)
			if got != testCase.want {
				t.Fatalf("class = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestNextFailureStepNoneAndRetryDelayBranches(t *testing.T) {
	t.Parallel()

	if got := NextFailureStep(RetryClassTransient, 2, RetryClassNone); got != 0 {
		t.Fatalf("none class = %d", got)
	}
	if got := RetryDelay(RetryClassNone, 1, 0); got != 0 {
		t.Fatalf("none delay = %v", got)
	}
	if got := RetryDelay(RetryClassRateLimited, 0, 500*time.Millisecond); got != 24*time.Hour {
		t.Fatalf("rate limited below min = %v", got)
	}
	if got := RetryDelay(RetryClassParser, 2, 0); got != 30*24*time.Hour {
		t.Fatalf("parser step2 = %v", got)
	}
	if got := RetryDelay(RetryClassRobots, 1, 0); got != 24*time.Hour {
		t.Fatalf("robots = %v", got)
	}
	if got := RetryDelay(RetryClassNetworkRejected, 1, 0); got != 24*time.Hour {
		t.Fatalf("network = %v", got)
	}
	if got := RetryDelay(RetryClass("unknown"), 1, 0); got != 24*time.Hour {
		t.Fatalf("default = %v", got)
	}
	if got := RetryDelay(RetryClassHTTPOther, 1, 0); got != 24*time.Hour {
		t.Fatalf("http other = %v", got)
	}
	if got := RetryDelay(RetryClassUnavailable, 1, 0); got != 24*time.Hour {
		t.Fatalf("unavailable = %v", got)
	}
}

func TestParseRetryAfterEdgeCases(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	if _, ok := ParseRetryAfter("", now); ok {
		t.Fatal("empty accepted")
	}
	if _, ok := ParseRetryAfter("not-a-date", now); ok {
		t.Fatal("garbage accepted")
	}
	past := now.Add(-time.Minute).Format(http.TimeFormat)
	if _, ok := ParseRetryAfter(past, now); ok {
		t.Fatal("past http-date accepted")
	}
	far := now.Add(8 * 24 * time.Hour).Format(http.TimeFormat)
	if _, ok := ParseRetryAfter(far, now); ok {
		t.Fatal("far http-date accepted")
	}
}

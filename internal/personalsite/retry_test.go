package personalsite

import (
	"net/http"
	"testing"
	"time"
)

func TestRetryClassForHTTPStatuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		status int
		want   RetryClass
	}{
		{status: http.StatusTooManyRequests, want: RetryClassRateLimited},
		{status: http.StatusNotFound, want: RetryClassGone},
		{status: http.StatusGone, want: RetryClassGone},
		{status: http.StatusInternalServerError, want: RetryClassTransient},
		{status: http.StatusBadRequest, want: RetryClassHTTPOther},
	}
	for _, testCase := range cases {
		got := RetryClassFor(Qualification{
			Outcome:    QualificationHTTPError,
			HTTPStatus: testCase.status,
		})
		if got != testCase.want {
			t.Fatalf(
				"status %d class = %q, want %q",
				testCase.status,
				got,
				testCase.want,
			)
		}
	}
}

func TestRetryDelayLadders(t *testing.T) {
	t.Parallel()

	if got := RetryDelay(RetryClassTransient, 1, 0); got != 24*time.Hour {
		t.Fatalf("transient step1 = %v", got)
	}
	if got := RetryDelay(RetryClassTransient, 2, 0); got != 3*24*time.Hour {
		t.Fatalf("transient step2 = %v", got)
	}
	if got := RetryDelay(RetryClassTransient, 3, 0); got != 7*24*time.Hour {
		t.Fatalf("transient step3 = %v", got)
	}
	if got := RetryDelay(RetryClassTransient, 4, 0); got != 30*24*time.Hour {
		t.Fatalf("transient step4 = %v", got)
	}
	if got := RetryDelay(RetryClassGone, 1, 0); got != 30*24*time.Hour {
		t.Fatalf("gone step1 = %v", got)
	}
	if got := RetryDelay(RetryClassGone, 2, 0); got != 90*24*time.Hour {
		t.Fatalf("gone step2 = %v", got)
	}
	if got := RetryDelay(RetryClassUnsupported, 1, 0); got != 90*24*time.Hour {
		t.Fatalf("unsupported = %v", got)
	}
	if got := RetryDelay(RetryClassParser, 1, 0); got != 7*24*time.Hour {
		t.Fatalf("parser step1 = %v", got)
	}
	if got := RetryDelay(RetryClassRateLimited, 1, 90*time.Second); got != 90*time.Second {
		t.Fatalf("429 respected = %v", got)
	}
	if got := RetryDelay(RetryClassRateLimited, 1, 0); got != 24*time.Hour {
		t.Fatalf("429 fallback = %v", got)
	}
}

func TestNextFailureStepResetsOnClassChange(t *testing.T) {
	t.Parallel()

	if got := NextFailureStep(RetryClassTransient, 3, RetryClassGone); got != 1 {
		t.Fatalf("class switch = %d, want 1", got)
	}
	if got := NextFailureStep(RetryClassGone, 1, RetryClassGone); got != 2 {
		t.Fatalf("same class = %d, want 2", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	delay, ok := ParseRetryAfter("120", now)
	if !ok || delay != 120*time.Second {
		t.Fatalf("delta = %v ok=%v", delay, ok)
	}
	_, ok = ParseRetryAfter("0", now)
	if ok {
		t.Fatal("zero delta accepted")
	}
	_, ok = ParseRetryAfter("999999999", now)
	if ok {
		t.Fatal("absurd delta accepted")
	}
	httpDate := now.Add(2 * time.Hour).Format(http.TimeFormat)
	delay, ok = ParseRetryAfter(httpDate, now)
	if !ok || delay != 2*time.Hour {
		t.Fatalf("http-date = %v ok=%v", delay, ok)
	}
}

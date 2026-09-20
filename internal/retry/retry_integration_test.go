package retry_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/retry"
)

type retryIntegrationJitter float64

func (jitter retryIntegrationJitter) Float64() float64 {
	return float64(jitter)
}

func TestRetryIntegrationClassifiesHTTPAndRetryAfter(t *testing.T) {
	tests := []struct {
		status    int
		category  retry.Category
		transient bool
	}{
		{http.StatusRequestTimeout, retry.CategoryHTTP408, true},
		{http.StatusTooManyRequests, retry.CategoryHTTP429, true},
		{http.StatusInternalServerError, retry.CategoryHTTP5xx, true},
		{http.StatusNetworkAuthenticationRequired, retry.CategoryHTTP5xx, true},
		{http.StatusNotFound, retry.CategoryDeclarationAbsent, false},
		{http.StatusGone, retry.CategoryDeclarationAbsent, false},
		{http.StatusBadRequest, retry.CategoryDeclarationUnavailable, false},
	}

	for _, test := range tests {
		category, transient := retry.HTTPStatusCategory(test.status)

		if category != test.category || transient != test.transient {
			t.Errorf(
				"HTTPStatusCategory(%d) = %q, %v, want %q, %v",
				test.status,
				category,
				transient,
				test.category,
				test.transient,
			)
		}
	}

	now := time.Date(
		2026,
		time.September,
		19,
		16,
		0,
		0,
		0,
		time.UTC,
	)

	retryAfterTests := []struct {
		value string
		want  time.Duration
	}{
		{"", 0},
		{"   ", 0},
		{"0", 0},
		{"-5", 0},
		{"60", time.Minute},
		{"999999999", retry.MaxDelay},
		{now.Add(30 * time.Minute).Format(http.TimeFormat), 30 * time.Minute},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{"not-a-date", 0},
	}

	for _, test := range retryAfterTests {
		if got := retry.ParseRetryAfter(test.value, now); got != test.want {
			t.Errorf(
				"ParseRetryAfter(%q) = %v, want %v",
				test.value,
				got,
				test.want,
			)
		}
	}
}

func TestRetryIntegrationAppliesBoundedDurableBackoff(t *testing.T) {
	withoutJitter := retry.NewPolicy(nil)

	tests := []struct {
		failures   int
		retryAfter time.Duration
		want       time.Duration
	}{
		{0, 0, 5 * time.Minute},
		{1, 0, 5 * time.Minute},
		{2, 0, 30 * time.Minute},
		{3, 0, 2 * time.Hour},
		{4, 0, 12 * time.Hour},
		{5, 0, retry.MaxDelay},
		{50, 0, retry.MaxDelay},
		{1, time.Hour, time.Hour},
		{1, 48 * time.Hour, retry.MaxDelay},
		{1, -time.Hour, 5 * time.Minute},
	}

	for _, test := range tests {
		if got := withoutJitter.Delay(
			test.failures,
			test.retryAfter,
		); got != test.want {
			t.Errorf(
				"Delay(%d, %v) = %v, want %v",
				test.failures,
				test.retryAfter,
				got,
				test.want,
			)
		}
	}

	jitterTests := []struct {
		sample float64
		want   time.Duration
	}{
		{-1, 4 * time.Minute},
		{0, 4 * time.Minute},
		{0.5, 5 * time.Minute},
		{1, 6 * time.Minute},
		{2, 6 * time.Minute},
	}

	for _, test := range jitterTests {
		policy := retry.NewPolicy(
			retryIntegrationJitter(test.sample),
		)

		if got := policy.Delay(1, 0); got != test.want {
			t.Errorf(
				"jitter %v delay = %v, want %v",
				test.sample,
				got,
				test.want,
			)
		}
	}
}

func TestRetryIntegrationExposesStableCategoryAndClockContract(t *testing.T) {
	if !retry.CategoryTimeout.Transient() {
		t.Error("timeout category is not transient")
	}

	if retry.CategoryRobotsDenied.Transient() {
		t.Error("robots-denied category is transient")
	}

	if !retry.CategoryNone.Valid() ||
		!retry.CategoryCrossOriginRedirect.Valid() {
		t.Error("documented retry category is invalid")
	}

	if retry.Category("not-a-category").Valid() {
		t.Error("unknown retry category is valid")
	}

	before := time.Now().UTC()
	got := (retry.SystemClock{}).Now()
	after := time.Now().UTC()

	if got.Location() != time.UTC {
		t.Errorf(
			"SystemClock location = %v, want UTC",
			got.Location(),
		)
	}

	if got.Before(before) || got.After(after) {
		t.Errorf(
			"SystemClock.Now() = %v, want between %v and %v",
			got,
			before,
			after,
		)
	}
}

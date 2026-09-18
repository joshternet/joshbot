package retry

import (
	"net/http"
	"testing"
	"time"
)

func TestPolicyDurableScheduleAndCap(t *testing.T) {
	policy := NewPolicy(fixedJitter(0.5))
	want := []time.Duration{
		5 * time.Minute,
		30 * time.Minute,
		2 * time.Hour,
		12 * time.Hour,
		24 * time.Hour,
		24 * time.Hour,
	}
	for index, expected := range want {
		got := policy.Delay(index+1, 0)
		if got != expected {
			t.Errorf("Delay(%d) = %v, want %v", index+1, got, expected)
		}
	}
}

func TestPolicyJitterBounds(t *testing.T) {
	for _, sample := range []float64{-1, 0, 0.5, 1, 2} {
		delay := NewPolicy(fixedJitter(sample)).Delay(1, 0)
		if delay < 4*time.Minute || delay > 6*time.Minute {
			t.Errorf("jitter %v delay = %v, want [4m, 6m]", sample, delay)
		}
	}
}

func TestPolicyBoundsRetryAfter(t *testing.T) {
	policy := NewPolicy(fixedJitter(0.5))
	if got := policy.Delay(1, -time.Hour); got != 5*time.Minute {
		t.Errorf("negative Retry-After delay = %v, want 5m", got)
	}
	if got := policy.Delay(1, 48*time.Hour); got != 24*time.Hour {
		t.Errorf("large Retry-After delay = %v, want 24h", got)
	}
	if got := policy.Delay(1, time.Hour); got != time.Hour {
		t.Errorf("Retry-After delay = %v, want 1h", got)
	}
}

func TestSystemClockReturnsUTC(t *testing.T) {
	if got := (SystemClock{}).Now(); got.Location() != time.UTC {
		t.Errorf("SystemClock location = %v, want UTC", got.Location())
	}
}

func TestTransientCategories(t *testing.T) {
	transient := []Category{
		CategoryDNS,
		CategoryTransport,
		CategoryTimeout,
		CategoryRobotsTemporary,
		CategoryHTTP408,
		CategoryHTTP429,
		CategoryHTTP5xx,
		CategoryDeclarationUnavailable,
		CategoryProcessor,
		CategoryStore,
	}
	for _, category := range transient {
		if !category.Transient() {
			t.Errorf("%q Transient() = false", category)
		}
	}
	permanent := []Category{
		CategoryUnsafeAddress,
		CategoryPolicyBlocked,
		CategoryRobotsDenied,
		CategoryMalformedOrigin,
		CategoryUnsupportedOrigin,
		CategoryOversizedContent,
		CategoryUnsupportedContent,
		CategoryBudgetExhausted,
		CategoryDeclarationInvalid,
		CategoryDeclarationUnsupported,
		CategoryCrossOriginRedirect,
		CategoryDeclarationAbsent,
	}
	for _, category := range permanent {
		if category.Transient() {
			t.Errorf("%q Transient() = true", category)
		}
	}
	for _, category := range append(transient, append(permanent, CategoryNone)...) {
		if !category.Valid() {
			t.Errorf("%q Valid() = false", category)
		}
	}
	if Category("unknown").Valid() {
		t.Error("unknown category Valid() = true")
	}
}

func TestHTTPStatusCategory(t *testing.T) {
	tests := map[int]struct {
		category  Category
		retryable bool
	}{
		http.StatusRequestTimeout:      {CategoryHTTP408, true},
		http.StatusTooManyRequests:     {CategoryHTTP429, true},
		http.StatusInternalServerError: {CategoryHTTP5xx, true},
		http.StatusServiceUnavailable:  {CategoryHTTP5xx, true},
		http.StatusNotFound:            {CategoryDeclarationAbsent, false},
		http.StatusGone:                {CategoryDeclarationAbsent, false},
		http.StatusBadRequest:          {CategoryDeclarationUnavailable, false},
	}
	for status, expected := range tests {
		got, retryable := HTTPStatusCategory(status)
		if got != expected.category || retryable != expected.retryable {
			t.Errorf("HTTPStatusCategory(%d) = %q, %v", status, got, retryable)
		}
	}
}

func TestPolicyWithoutJitterAndNonpositiveFailureCount(t *testing.T) {
	policy := NewPolicy(nil)
	if got := policy.Delay(0, 0); got != 5*time.Minute {
		t.Errorf("Delay(0) = %v, want 5m", got)
	}
}

func TestCapDurationRejectsNonpositive(t *testing.T) {
	if got := capDuration(-time.Second); got != 0 {
		t.Errorf("capDuration(-1s) = %v, want 0", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	tests := map[string]time.Duration{
		"":          0,
		"-5":        0,
		"60":        time.Minute,
		"999999999": 24 * time.Hour,
		now.Add(30 * time.Minute).Format(http.TimeFormat): 30 * time.Minute,
		now.Add(-time.Hour).Format(http.TimeFormat):       0,
		"invalid": 0,
	}
	for value, expected := range tests {
		if got := ParseRetryAfter(value, now); got != expected {
			t.Errorf("ParseRetryAfter(%q) = %v, want %v", value, got, expected)
		}
	}
}

type fixedJitter float64

func (jitter fixedJitter) Float64() float64 { return float64(jitter) }

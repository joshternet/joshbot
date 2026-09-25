package retry

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	MaxAttemptsPerCycle = 3
	MaxDelay            = 24 * time.Hour
)

type Category string

const (
	CategoryNone                   Category = ""
	CategoryDNS                    Category = "dns"
	CategoryTransport              Category = "transport"
	CategoryTimeout                Category = "timeout"
	CategoryRobotsTemporary        Category = "robots_temporary"
	CategoryHTTP408                Category = "http_408"
	CategoryHTTP429                Category = "http_429"
	CategoryHTTP4xx                Category = "http_4xx"
	CategoryHTTP5xx                Category = "http_5xx"
	CategoryDeclarationUnavailable Category = "declaration_unavailable"
	CategoryProcessor              Category = "processor"
	CategoryStore                  Category = "store"
	CategoryUnsafeAddress          Category = "unsafe_address"
	CategoryPolicyBlocked          Category = "policy_blocked"
	CategoryRobotsDenied           Category = "robots_denied"
	CategoryMalformedOrigin        Category = "malformed_origin"
	CategoryUnsupportedOrigin      Category = "unsupported_origin"
	CategoryOversizedContent       Category = "oversized_content"
	CategoryUnsupportedContent     Category = "unsupported_content"
	CategoryBudgetExhausted        Category = "budget_exhausted"
	CategoryDeclarationInvalid     Category = "declaration_invalid"
	CategoryDeclarationUnsupported Category = "declaration_unsupported"
	CategoryCrossOriginRedirect    Category = "cross_origin_redirect"
	CategoryDeclarationAbsent      Category = "declaration_absent"
)

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now().UTC()
}

type Jitter interface {
	Float64() float64
}

type Policy struct {
	jitter Jitter
}

func NewPolicy(jitter Jitter) Policy {
	return Policy{jitter: jitter}
}

func (category Category) Transient() bool {
	switch category {
	case CategoryDNS, CategoryTransport, CategoryTimeout,
		CategoryRobotsTemporary, CategoryHTTP408, CategoryHTTP429,
		CategoryHTTP5xx, CategoryDeclarationUnavailable,
		CategoryProcessor, CategoryStore:
		return true
	default:
		return false
	}
}

func (category Category) Valid() bool {
	switch category {
	case CategoryNone, CategoryDNS, CategoryTransport, CategoryTimeout,
		CategoryRobotsTemporary, CategoryHTTP408, CategoryHTTP429,
		CategoryHTTP4xx, CategoryHTTP5xx, CategoryDeclarationUnavailable,
		CategoryProcessor, CategoryStore, CategoryUnsafeAddress,
		CategoryPolicyBlocked, CategoryRobotsDenied, CategoryMalformedOrigin,
		CategoryUnsupportedOrigin, CategoryOversizedContent,
		CategoryUnsupportedContent, CategoryBudgetExhausted,
		CategoryDeclarationInvalid, CategoryDeclarationUnsupported,
		CategoryCrossOriginRedirect, CategoryDeclarationAbsent:
		return true
	default:
		return false
	}
}

func HTTPStatusCategory(status int) (Category, bool) {
	switch {
	case status == http.StatusRequestTimeout:
		return CategoryHTTP408, true
	case status == http.StatusTooManyRequests:
		return CategoryHTTP429, true
	case status >= 500 && status <= 599:
		return CategoryHTTP5xx, true
	case status == http.StatusNotFound || status == http.StatusGone:
		return CategoryDeclarationAbsent, false
	default:
		return CategoryDeclarationUnavailable, false
	}
}

func ParseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return capDuration(time.Duration(seconds) * time.Second)
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return capDuration(when.Sub(now))
}

func (policy Policy) Delay(consecutiveFailures int, retryAfter time.Duration) time.Duration {
	schedule := [...]time.Duration{
		5 * time.Minute,
		30 * time.Minute,
		2 * time.Hour,
		12 * time.Hour,
		24 * time.Hour,
	}
	index := consecutiveFailures - 1
	if index < 0 {
		index = 0
	}
	if index >= len(schedule) {
		index = len(schedule) - 1
	}
	delay := schedule[index]
	if policy.jitter != nil {
		sample := policy.jitter.Float64()
		if sample < 0 {
			sample = 0
		}
		if sample > 1 {
			sample = 1
		}
		delay = time.Duration(float64(delay) * (0.8 + sample*0.4))
	}
	if retryAfter > delay {
		delay = retryAfter
	}
	return capDuration(delay)
}

func capDuration(duration time.Duration) time.Duration {
	if duration <= 0 {
		return 0
	}
	if duration > MaxDelay {
		return MaxDelay
	}
	return duration
}

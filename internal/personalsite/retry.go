package personalsite

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RetryClass groups qualification failures that share one backoff ladder.
type RetryClass string

const (
	// RetryClassNone means assessed or unset.
	RetryClassNone RetryClass = ""
	// RetryClassTransient covers timeouts, DNS, 5xx, and temporary network errors.
	RetryClassTransient RetryClass = "transient"
	// RetryClassRateLimited covers HTTP 429.
	RetryClassRateLimited RetryClass = "rate_limited"
	// RetryClassGone covers HTTP 404 and 410.
	RetryClassGone RetryClass = "gone"
	// RetryClassUnsupported covers non-HTML roots.
	RetryClassUnsupported RetryClass = "unsupported"
	// RetryClassParser covers HTML parse/decode failures.
	RetryClassParser RetryClass = "parser"
	// RetryClassRobots covers robots denials (recheck policy only).
	RetryClassRobots RetryClass = "robots"
	// RetryClassNetworkRejected covers unsafe destinations.
	RetryClassNetworkRejected RetryClass = "network_rejected"
	// RetryClassHTTPOther covers other non-success HTTP statuses.
	RetryClassHTTPOther RetryClass = "http_other"
	// RetryClassUnavailable covers generic fetch failures.
	RetryClassUnavailable RetryClass = "unavailable"
)

const (
	maxRetryAfter = 7 * 24 * time.Hour
	minRetryAfter = time.Second
)

// RetryClassFor maps a qualification result onto a backoff class.
//
// @param qualification - Latest attempt result.
// @returns Retry class used for scheduling.
func RetryClassFor(qualification Qualification) RetryClass {
	switch qualification.Outcome {
	case QualificationAssessed:
		return RetryClassNone
	case QualificationTimeout,
		QualificationDNSFailure:
		return RetryClassTransient
	case QualificationHTTPError:
		status := qualification.HTTPStatus
		switch {
		case status == http.StatusTooManyRequests:
			return RetryClassRateLimited
		case status == http.StatusNotFound ||
			status == http.StatusGone:
			return RetryClassGone
		case status >= 500 && status <= 599:
			return RetryClassTransient
		default:
			return RetryClassHTTPOther
		}
	case QualificationUnsupportedContent:
		return RetryClassUnsupported
	case QualificationParserFailure:
		return RetryClassParser
	case QualificationRobotsDenied:
		return RetryClassRobots
	case QualificationNetworkRejected:
		return RetryClassNetworkRejected
	case QualificationUnavailable,
		QualificationTooLarge:
		return RetryClassUnavailable
	default:
		return RetryClassUnavailable
	}
}

// NextFailureStep returns the failure_count to persist after this attempt.
//
// Switching retry class resets the ladder to step 1.
//
// @param previousClass - Class stored from the prior failure, if any.
// @param previousCount - Prior failure_count for previousClass.
// @param nextClass - Class for the latest failure.
// @returns Updated failure_count (minimum 1).
func NextFailureStep(
	previousClass RetryClass,
	previousCount int,
	nextClass RetryClass,
) int {
	if nextClass == RetryClassNone {
		return 0
	}
	if previousClass != nextClass || previousCount < 1 {
		return 1
	}
	return previousCount + 1
}

// RetryDelay returns how long to wait before the next qualification attempt.
//
// @param retryClass - Backoff class for this failure.
// @param failureCount - 1-based step within the class ladder.
// @param retryAfter - Optional Retry-After duration from HTTP 429.
// @returns Delay until the next attempt.
func RetryDelay(
	retryClass RetryClass,
	failureCount int,
	retryAfter time.Duration,
) time.Duration {
	if failureCount < 1 {
		failureCount = 1
	}

	switch retryClass {
	case RetryClassRateLimited:
		if retryAfter >= minRetryAfter && retryAfter <= maxRetryAfter {
			return retryAfter
		}
		return transientDelay(failureCount)
	case RetryClassTransient, RetryClassHTTPOther, RetryClassUnavailable:
		return transientDelay(failureCount)
	case RetryClassGone:
		if failureCount <= 1 {
			return 30 * 24 * time.Hour
		}
		return 90 * 24 * time.Hour
	case RetryClassUnsupported:
		return 90 * 24 * time.Hour
	case RetryClassParser:
		if failureCount <= 1 {
			return 7 * 24 * time.Hour
		}
		return 30 * 24 * time.Hour
	case RetryClassRobots:
		// Reconsider robots policy on the transient ladder without root fetch
		// bypass; callers must still honor robots on the next attempt.
		return transientDelay(failureCount)
	case RetryClassNetworkRejected:
		return transientDelay(failureCount)
	case RetryClassNone:
		return 0
	default:
		return transientDelay(failureCount)
	}
}

func transientDelay(failureCount int) time.Duration {
	switch {
	case failureCount <= 1:
		return 24 * time.Hour
	case failureCount == 2:
		return 3 * 24 * time.Hour
	case failureCount == 3:
		return 7 * 24 * time.Hour
	default:
		return 30 * 24 * time.Hour
	}
}

// ParseRetryAfter interprets a Retry-After header value.
//
// Accepts delta-seconds or HTTP-date. Values outside [1s, 7d] are rejected.
//
// @param raw - Header value.
// @param now - Clock used for HTTP-date conversion.
// @returns Parsed delay and whether it was usable.
func ParseRetryAfter(raw string, now time.Time) (time.Duration, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false
	}

	if seconds, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		if seconds <= 0 {
			return 0, false
		}
		delay := time.Duration(seconds) * time.Second
		if delay < minRetryAfter || delay > maxRetryAfter {
			return 0, false
		}
		return delay, true
	}

	parsed, err := http.ParseTime(trimmed)
	if err != nil {
		return 0, false
	}
	delay := parsed.Sub(now)
	if delay < minRetryAfter || delay > maxRetryAfter {
		return 0, false
	}
	return delay, true
}

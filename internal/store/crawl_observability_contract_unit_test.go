package store

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/testutil"
)

func TestCrawlObservabilityHelperContracts(
	t *testing.T,
) {
	config := validObservabilityCrawlConfig()

	if !validTelemetryConfig(config) {
		t.Fatal(
			"validTelemetryConfig(valid) = false",
		)
	}

	invalidConfigs := []discovery.CrawlConfig{
		func() discovery.CrawlConfig {
			value := config
			value.MaxDepth = -1
			return value
		}(),
		func() discovery.CrawlConfig {
			value := config
			value.MaxPages = 0
			return value
		}(),
		func() discovery.CrawlConfig {
			value := config
			value.MaxPageBytes = 0
			return value
		}(),
		func() discovery.CrawlConfig {
			value := config
			value.RequestDelay = -time.Second
			return value
		}(),
		func() discovery.CrawlConfig {
			value := config
			value.RedirectLimit = -1
			return value
		}(),
		func() discovery.CrawlConfig {
			value := config
			value.PageTimeout = 0
			return value
		}(),
		func() discovery.CrawlConfig {
			value := config
			value.MaxAutomaticPromotionsPerRun = -1
			return value
		}(),
	}

	for index, invalid := range invalidConfigs {
		if validTelemetryConfig(invalid) {
			t.Errorf(
				"validTelemetryConfig(invalid %d) = true",
				index,
			)
		}
	}

	attempt := validObservabilityPageAttempt()

	if !validPageAttempt(attempt) {
		t.Fatal(
			"validPageAttempt(valid) = false",
		)
	}

	invalidAttempts := []discovery.PageAttempt{
		func() discovery.PageAttempt {
			value := attempt
			value.Sequence = 0
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.Depth = -1
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.StartedAt = time.Time{}
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.Duration = -time.Second
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.ResponseBytes = -1
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.RedirectCount = -1
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.InternalLinkCount = -1
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.ExternalLinkCount = -1
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.URLsFound = -1
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.URLsEnqueued = -1
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.ContentType = strings.Repeat(
				"x",
				257,
			)
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.RequestedURL =
				"https://example.com/?secret=yes"
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.FinalURL =
				"https://user:password@example.com/"
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.RobotsDecision = "invalid"
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.Outcome = "invalid"
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			status := 99
			value.StatusCode = &status
			return value
		}(),
		func() discovery.PageAttempt {
			value := attempt
			value.FailureCategory = retry.Category(
				"invalid",
			)
			return value
		}(),
	}

	for index, invalid := range invalidAttempts {
		if validPageAttempt(invalid) {
			t.Errorf(
				"validPageAttempt(invalid %d) = true",
				index,
			)
		}
	}

	for _, rawURL := range []string{
		"https://example.com/",
		"http://example.com/path",
	} {
		if !safeStoredPageURL(rawURL) {
			t.Errorf(
				"safeStoredPageURL(%q) = false",
				rawURL,
			)
		}
	}

	for _, rawURL := range []string{
		"://bad",
		"/relative",
		"ftp://example.com/file",
		"https://user@example.com/",
		"https://example.com/?secret=yes",
		"https://example.com/#fragment",
		"https://example.com/path?",
	} {
		if safeStoredPageURL(rawURL) {
			t.Errorf(
				"safeStoredPageURL(%q) = true",
				rawURL,
			)
		}
	}

	for _, decision := range []discovery.RobotsDecision{
		discovery.RobotsAllowed,
		discovery.RobotsDenied,
		discovery.RobotsUnknown,
	} {
		if !validRobotsDecision(decision) {
			t.Errorf(
				"validRobotsDecision(%q) = false",
				decision,
			)
		}
	}

	if validRobotsDecision("invalid") {
		t.Error(
			"validRobotsDecision(invalid) = true",
		)
	}

	for _, outcome := range []discovery.PageOutcome{
		discovery.PageComplete,
		discovery.PageRedirected,
		discovery.PageRobotsDenied,
		discovery.PageUnsupportedContent,
		discovery.PageTooLarge,
		discovery.PageHTTPError,
		discovery.PageTimeout,
		discovery.PageNetworkError,
		discovery.PageInvalidResponse,
	} {
		if !validPageOutcome(outcome) {
			t.Errorf(
				"validPageOutcome(%q) = false",
				outcome,
			)
		}
	}

	if validPageOutcome("invalid") {
		t.Error(
			"validPageOutcome(invalid) = true",
		)
	}

	for _, outcome := range []discovery.CrawlRunOutcome{
		discovery.CrawlRunComplete,
		discovery.CrawlRunBudgetExhausted,
		discovery.CrawlRunCanceled,
		discovery.CrawlRunFailed,
	} {
		if !validRunOutcome(outcome) {
			t.Errorf(
				"validRunOutcome(%q) = false",
				outcome,
			)
		}
	}

	if validRunOutcome("invalid") {
		t.Error(
			"validRunOutcome(invalid) = true",
		)
	}

	status100 := 100
	status599 := 599
	status99 := 99
	status600 := 600

	for name, status := range map[string]*int{
		"nil": nil,
		"100": &status100,
		"599": &status599,
	} {
		if !validStatusCode(status) {
			t.Errorf(
				"validStatusCode(%s) = false",
				name,
			)
		}
	}

	for name, status := range map[string]*int{
		"99":  &status99,
		"600": &status600,
	} {
		if validStatusCode(status) {
			t.Errorf(
				"validStatusCode(%s) = true",
				name,
			)
		}
	}

	if got := storedFailureCategory(
		retry.CategoryNone,
	); got != "none" {
		t.Errorf(
			"storedFailureCategory(none) = %q, want none",
			got,
		)
	}

	if got := storedFailureCategory(
		retry.CategoryDNS,
	); got != "dns" {
		t.Errorf(
			"storedFailureCategory(dns) = %q, want dns",
			got,
		)
	}

	heartbeat := validObservabilityHeartbeat(
		t,
	)

	if !validHeartbeat(heartbeat) {
		t.Fatal(
			"validHeartbeat(valid) = false",
		)
	}

	invalidHeartbeats := []ServiceHeartbeat{
		func() ServiceHeartbeat {
			value := heartbeat
			value.Service = "other"
			return value
		}(),
		func() ServiceHeartbeat {
			value := heartbeat
			value.State = "other"
			return value
		}(),
		func() ServiceHeartbeat {
			value := heartbeat
			value.InstanceID = " "
			return value
		}(),
		func() ServiceHeartbeat {
			value := heartbeat
			value.InstanceID = strings.Repeat(
				"x",
				129,
			)
			return value
		}(),
		func() ServiceHeartbeat {
			value := heartbeat
			value.Message = strings.Repeat(
				"x",
				257,
			)
			return value
		}(),
		func() ServiceHeartbeat {
			value := heartbeat
			value.StartedAt = time.Time{}
			return value
		}(),
		func() ServiceHeartbeat {
			value := heartbeat
			value.UpdatedAt = time.Time{}
			return value
		}(),
		func() ServiceHeartbeat {
			value := heartbeat
			value.UpdatedAt = value.StartedAt.Add(
				-time.Second,
			)
			return value
		}(),
	}

	for index, invalid := range invalidHeartbeats {
		if validHeartbeat(invalid) {
			t.Errorf(
				"validHeartbeat(invalid %d) = true",
				index,
			)
		}
	}
}

func TestCrawlObservabilityContractGolden(
	t *testing.T,
) {
	var snapshot bytes.Buffer

	config := validObservabilityCrawlConfig()

	fmt.Fprintln(
		&snapshot,
		"=== promotion budget ===",
	)
	fmt.Fprintf(
		&snapshot,
		"explicit=%d\n",
		automaticPromotionBudget(
			discovery.CrawlConfig{
				MaxAutomaticPromotionsPerRun: 7,
			},
			AutomaticCrawlConfig{},
		),
	)
	fmt.Fprintf(
		&snapshot,
		"default=%d\n",
		automaticPromotionBudget(
			discovery.CrawlConfig{},
			AutomaticCrawlConfig{},
		),
	)
	fmt.Fprintf(
		&snapshot,
		"automatic-config=%d\n",
		automaticPromotionBudget(
			discovery.CrawlConfig{},
			AutomaticCrawlConfig{
				MaxAutomaticPromotionsPerRun: 23,
			},
		),
	)

	fmt.Fprintln(
		&snapshot,
		"\n=== telemetry config ===",
	)
	fmt.Fprintf(
		&snapshot,
		"valid=%t\n",
		validTelemetryConfig(config),
	)

	zeroRedirect := config
	zeroRedirect.RedirectLimit = 0

	fmt.Fprintf(
		&snapshot,
		"zero-redirect-limit=%t\n",
		validTelemetryConfig(zeroRedirect),
	)

	negativeRedirect := config
	negativeRedirect.RedirectLimit = -1

	fmt.Fprintf(
		&snapshot,
		"negative-redirect-limit=%t\n",
		validTelemetryConfig(negativeRedirect),
	)

	fmt.Fprintln(
		&snapshot,
		"\n=== stored page URLs ===",
	)

	for _, rawURL := range []string{
		"https://example.com/path",
		"http://example.com/path",
		"https://example.com/?secret=yes",
		"https://example.com/#fragment",
		"https://user@example.com/",
		"ftp://example.com/file",
		"/relative",
	} {
		fmt.Fprintf(
			&snapshot,
			"%s=%t\n",
			rawURL,
			safeStoredPageURL(rawURL),
		)
	}

	fmt.Fprintln(
		&snapshot,
		"\n=== robots decisions ===",
	)

	for _, decision := range []discovery.RobotsDecision{
		discovery.RobotsAllowed,
		discovery.RobotsDenied,
		discovery.RobotsUnknown,
		"invalid",
	} {
		fmt.Fprintf(
			&snapshot,
			"%s=%t\n",
			decision,
			validRobotsDecision(decision),
		)
	}

	fmt.Fprintln(
		&snapshot,
		"\n=== page outcomes ===",
	)

	for _, outcome := range []discovery.PageOutcome{
		discovery.PageComplete,
		discovery.PageRedirected,
		discovery.PageRobotsDenied,
		discovery.PageUnsupportedContent,
		discovery.PageTooLarge,
		discovery.PageHTTPError,
		discovery.PageTimeout,
		discovery.PageNetworkError,
		discovery.PageInvalidResponse,
		"invalid",
	} {
		fmt.Fprintf(
			&snapshot,
			"%s=%t\n",
			outcome,
			validPageOutcome(outcome),
		)
	}

	fmt.Fprintln(
		&snapshot,
		"\n=== run outcomes ===",
	)

	for _, outcome := range []discovery.CrawlRunOutcome{
		discovery.CrawlRunComplete,
		discovery.CrawlRunBudgetExhausted,
		discovery.CrawlRunCanceled,
		discovery.CrawlRunFailed,
		"invalid",
	} {
		fmt.Fprintf(
			&snapshot,
			"%s=%t\n",
			outcome,
			validRunOutcome(outcome),
		)
	}

	fmt.Fprintln(
		&snapshot,
		"\n=== status codes ===",
	)

	status100 := 100
	status599 := 599
	status99 := 99
	status600 := 600

	statuses := []struct {
		name   string
		status *int
	}{
		{
			name: "nil",
		},
		{
			name:   "100",
			status: &status100,
		},
		{
			name:   "599",
			status: &status599,
		},
		{
			name:   "99",
			status: &status99,
		},
		{
			name:   "600",
			status: &status600,
		},
	}

	for _, status := range statuses {
		fmt.Fprintf(
			&snapshot,
			"%s=%t\n",
			status.name,
			validStatusCode(status.status),
		)
	}

	fmt.Fprintln(
		&snapshot,
		"\n=== failure category storage ===",
	)
	fmt.Fprintf(
		&snapshot,
		"none=%s\n",
		storedFailureCategory(
			retry.CategoryNone,
		),
	)
	fmt.Fprintf(
		&snapshot,
		"dns=%s\n",
		storedFailureCategory(
			retry.CategoryDNS,
		),
	)

	fmt.Fprintln(
		&snapshot,
		"\n=== page attempt ===",
	)
	fmt.Fprintf(
		&snapshot,
		"valid=%t\n",
		validPageAttempt(
			validObservabilityPageAttempt(),
		),
	)

	unsafeAttempt :=
		validObservabilityPageAttempt()
	unsafeAttempt.RequestedURL =
		"https://example.com/?token=secret"

	fmt.Fprintf(
		&snapshot,
		"query-bearing-url=%t\n",
		validPageAttempt(unsafeAttempt),
	)

	fmt.Fprintln(
		&snapshot,
		"\n=== heartbeat ===",
	)

	heartbeat := validObservabilityHeartbeat(
		t,
	)

	fmt.Fprintf(
		&snapshot,
		"valid=%t\n",
		validHeartbeat(heartbeat),
	)

	invalidHeartbeat := heartbeat
	invalidHeartbeat.State = "unknown"

	fmt.Fprintf(
		&snapshot,
		"unknown-state=%t\n",
		validHeartbeat(invalidHeartbeat),
	)

	if err := testutil.CheckGolden(
		"testdata/golden/crawl-observability-contract.golden",
		snapshot.Bytes(),
	); err != nil {
		t.Fatalf(
			"crawl observability golden error = %v",
			err,
		)
	}
}

func validObservabilityCrawlConfig() discovery.CrawlConfig {
	return discovery.CrawlConfig{
		MaxDepth:                     2,
		MaxPages:                     32,
		MaxPageBytes:                 1024,
		RequestDelay:                 time.Second,
		RedirectLimit:                5,
		PageTimeout:                  10 * time.Second,
		MaxAutomaticPromotionsPerRun: 4,
	}
}

func validObservabilityPageAttempt() discovery.PageAttempt {
	return discovery.PageAttempt{
		Sequence:        1,
		RequestedURL:    "https://example.com/path",
		FinalURL:        "https://example.com/final",
		Depth:           0,
		StartedAt:       time.Date(2026, time.September, 18, 12, 0, 0, 0, time.UTC),
		Duration:        125 * time.Millisecond,
		ResponseBytes:   512,
		ContentType:     "text/html",
		RedirectCount:   0,
		RobotsDecision:  discovery.RobotsAllowed,
		Outcome:         discovery.PageComplete,
		FailureCategory: retry.CategoryNone,
		URLsFound:       3,
		URLsEnqueued:    2,
	}
}

func validObservabilityHeartbeat(
	t *testing.T,
) ServiceHeartbeat {
	t.Helper()

	startedAt := time.Date(
		2026,
		time.September,
		18,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	return ServiceHeartbeat{
		Service:    "discovery",
		InstanceID: "discovery-1",
		State:      "running",
		CurrentOrigin: mustStoreOrigin(
			t,
			"https://example.com",
		),
		Message:   "crawling",
		StartedAt: startedAt,
		UpdatedAt: startedAt.Add(
			time.Minute,
		),
	}
}

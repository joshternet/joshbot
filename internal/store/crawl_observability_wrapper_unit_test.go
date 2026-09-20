package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
)

func TestBeginCrawlValidationWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	var nilStore *DiscoveryStore

	runID, err := nilStore.BeginCrawl(
		ctx,
		origin.Origin{},
		discovery.CrawlConfig{},
	)

	if runID != 0 {
		t.Fatalf(
			"BeginCrawl() run ID = %d, want 0",
			runID,
		)
	}

	if !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Fatalf(
			"BeginCrawl() error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}

	store := observabilityWrapperStore()

	runID, err = store.BeginCrawl(
		ctx,
		origin.Origin{},
		validObservabilityCrawlConfig(),
	)

	if runID != 0 {
		t.Fatalf(
			"BeginCrawl(zero source) run ID = %d, want 0",
			runID,
		)
	}

	if !errors.Is(
		err,
		errInvalidCrawlRun,
	) {
		t.Fatalf(
			"BeginCrawl(zero source) error = %v, want %v",
			err,
			errInvalidCrawlRun,
		)
	}

	runID, err = store.BeginCrawl(
		ctx,
		mustStoreOrigin(
			t,
			"https://example.com",
		),
		discovery.CrawlConfig{},
	)

	if runID != 0 {
		t.Fatalf(
			"BeginCrawl(invalid config) run ID = %d, want 0",
			runID,
		)
	}

	if !errors.Is(
		err,
		errInvalidCrawlRun,
	) {
		t.Fatalf(
			"BeginCrawl(invalid config) error = %v, want %v",
			err,
			errInvalidCrawlRun,
		)
	}
}

func TestRecordPageAttemptValidationWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	var nilStore *DiscoveryStore

	if err := nilStore.RecordPageAttempt(
		ctx,
		1,
		validObservabilityPageAttempt(),
	); !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Fatalf(
			"RecordPageAttempt() error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}

	store := observabilityWrapperStore()

	if err := store.RecordPageAttempt(
		ctx,
		0,
		validObservabilityPageAttempt(),
	); !errors.Is(
		err,
		errInvalidPageAttempt,
	) {
		t.Fatalf(
			"RecordPageAttempt(zero run) error = %v, want %v",
			err,
			errInvalidPageAttempt,
		)
	}

	if err := store.RecordPageAttempt(
		ctx,
		1,
		discovery.PageAttempt{},
	); !errors.Is(
		err,
		errInvalidPageAttempt,
	) {
		t.Fatalf(
			"RecordPageAttempt(invalid attempt) error = %v, want %v",
			err,
			errInvalidPageAttempt,
		)
	}
}

func TestFinishCrawlWrapperWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	var nilStore *DiscoveryStore

	err := nilStore.FinishCrawl(
		ctx,
		1,
		discovery.CrawlResult{},
		discovery.CrawlRunComplete,
		"",
	)

	if !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Fatalf(
			"FinishCrawl() error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}
}

func TestFinishCrawlSummaryValidationWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	store := observabilityWrapperStore()

	validResult := discovery.CrawlResult{}

	tests := []struct {
		name              string
		runID             discovery.CrawlRunID
		result            discovery.CrawlResult
		outcome           discovery.CrawlRunOutcome
		stopReason        string
		frontierRemaining int
	}{
		{
			name:              "zero run",
			runID:             0,
			result:            validResult,
			outcome:           discovery.CrawlRunComplete,
			stopReason:        "",
			frontierRemaining: 0,
		},
		{
			name:              "invalid outcome",
			runID:             1,
			result:            validResult,
			outcome:           discovery.CrawlRunOutcome("invalid"),
			stopReason:        "",
			frontierRemaining: 0,
		},
		{
			name:              "stop reason too long",
			runID:             1,
			result:            validResult,
			outcome:           discovery.CrawlRunComplete,
			stopReason:        strings.Repeat("x", 129),
			frontierRemaining: 0,
		},
		{
			name:  "negative pages attempted",
			runID: 1,
			result: discovery.CrawlResult{
				PagesAttempted: -1,
			},
			outcome:           discovery.CrawlRunComplete,
			stopReason:        "",
			frontierRemaining: 0,
		},
		{
			name:  "negative pages parsed",
			runID: 1,
			result: discovery.CrawlResult{
				PagesParsed: -1,
			},
			outcome:           discovery.CrawlRunComplete,
			stopReason:        "",
			frontierRemaining: 0,
		},
		{
			name:  "negative candidates",
			runID: 1,
			result: discovery.CrawlResult{
				CandidatesDiscovered: -1,
			},
			outcome:           discovery.CrawlRunComplete,
			stopReason:        "",
			frontierRemaining: 0,
		},
		{
			name:              "negative frontier",
			runID:             1,
			result:            validResult,
			outcome:           discovery.CrawlRunComplete,
			stopReason:        "",
			frontierRemaining: -1,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				err := store.FinishCrawlSummary(
					ctx,
					test.runID,
					test.result,
					test.outcome,
					test.stopReason,
					test.frontierRemaining,
				)

				if !errors.Is(
					err,
					errInvalidCrawlRun,
				) {
					t.Fatalf(
						"FinishCrawlSummary() error = %v, want %v",
						err,
						errInvalidCrawlRun,
					)
				}
			},
		)
	}

	var nilStore *DiscoveryStore

	if err := nilStore.FinishCrawlSummary(
		ctx,
		1,
		validResult,
		discovery.CrawlRunComplete,
		"",
		0,
	); !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Fatalf(
			"nil FinishCrawlSummary() error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}
}

func TestPurgeCrawlTelemetryValidationWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	var nilStore *DiscoveryStore

	count, err := nilStore.PurgeCrawlTelemetry(
		ctx,
		time.Hour,
	)

	if count != 0 {
		t.Fatalf(
			"PurgeCrawlTelemetry() count = %d, want 0",
			count,
		)
	}

	if !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Fatalf(
			"PurgeCrawlTelemetry() error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}

	store := observabilityWrapperStore()

	for _, retention := range []time.Duration{
		0,
		-time.Nanosecond,
	} {
		count, err := store.PurgeCrawlTelemetry(
			ctx,
			retention,
		)

		if count != 0 {
			t.Fatalf(
				"PurgeCrawlTelemetry(%v) count = %d, want 0",
				retention,
				count,
			)
		}

		if !errors.Is(
			err,
			errInvalidRetention,
		) {
			t.Fatalf(
				"PurgeCrawlTelemetry(%v) error = %v, want %v",
				retention,
				err,
				errInvalidRetention,
			)
		}
	}
}

func TestCrawlControlValidationWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	var nilStore *DiscoveryStore

	control, err := nilStore.CrawlControl(ctx)

	if control != (CrawlControl{}) {
		t.Fatalf(
			"CrawlControl() = %#v, want zero",
			control,
		)
	}

	if !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Fatalf(
			"CrawlControl() error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}
}

func TestDiscoveryPausedWrapperWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	var nilStore *DiscoveryStore

	paused, err := nilStore.DiscoveryPaused(ctx)

	if paused {
		t.Fatal(
			"DiscoveryPaused() = true, want false",
		)
	}

	if !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Fatalf(
			"DiscoveryPaused() error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}
}

func TestUpsertServiceHeartbeatValidationWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	var nilStore *DiscoveryStore

	if err := nilStore.UpsertServiceHeartbeat(
		ctx,
		validObservabilityHeartbeat(t),
	); !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Fatalf(
			"UpsertServiceHeartbeat() error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}

	store := observabilityWrapperStore()

	if err := store.UpsertServiceHeartbeat(
		ctx,
		ServiceHeartbeat{},
	); !errors.Is(
		err,
		errInvalidServiceState,
	) {
		t.Fatalf(
			"UpsertServiceHeartbeat(invalid) error = %v, want %v",
			err,
			errInvalidServiceState,
		)
	}
}

func observabilityWrapperStore() *DiscoveryStore {
	return &DiscoveryStore{
		pool:  new(pgxpool.Pool),
		clock: databaseQueueClock{},
	}
}

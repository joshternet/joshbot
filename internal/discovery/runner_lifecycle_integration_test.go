package discovery_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

type discoveryRunnerIntegrationClaim struct {
	source origin.Origin
	found  bool
	err    error
}

type discoveryRunnerIntegrationCrawlStore struct {
	claims []discoveryRunnerIntegrationClaim
	index  int

	paused   bool
	pauseErr error

	completedCategories []retry.Category
	completedRetryAfter []time.Duration
	completeErr         error
}

func (store *discoveryRunnerIntegrationCrawlStore) ClaimDiscoverySourceLease(
	_ context.Context,
	_ time.Duration,
	leaseDuration time.Duration,
) (discovery.CrawlSourceLease, bool, error) {
	if store.index >= len(store.claims) {
		return discovery.CrawlSourceLease{}, false, nil
	}

	claim := store.claims[store.index]
	store.index++

	if claim.err != nil {
		return discovery.CrawlSourceLease{}, false, claim.err
	}

	if !claim.found {
		return discovery.CrawlSourceLease{}, false, nil
	}

	claimedAt := time.Unix(1_800_000_000, 0).UTC()

	return discovery.CrawlSourceLease{
		Origin:     claim.source,
		Generation: int64(store.index),
		ClaimedAt:  claimedAt,
		ExpiresAt:  claimedAt.Add(leaseDuration),
	}, true, nil
}

func (store *discoveryRunnerIntegrationCrawlStore) RenewDiscoverySourceLease(
	_ context.Context,
	lease discovery.CrawlSourceLease,
	leaseDuration time.Duration,
) (discovery.CrawlSourceLease, error) {
	lease.ExpiresAt = lease.ExpiresAt.Add(leaseDuration)

	return lease, nil
}

func (store *discoveryRunnerIntegrationCrawlStore) DiscoveryPaused(
	context.Context,
) (bool, error) {
	if store.pauseErr != nil {
		return false, store.pauseErr
	}

	return store.paused, nil
}

func (store *discoveryRunnerIntegrationCrawlStore) CompleteDiscoverySourceLeaseRetry(
	_ context.Context,
	_ discovery.CrawlSourceLease,
	category retry.Category,
	retryAfter time.Duration,
) error {
	store.completedCategories = append(
		store.completedCategories,
		category,
	)
	store.completedRetryAfter = append(
		store.completedRetryAfter,
		retryAfter,
	)

	return store.completeErr
}

type discoveryRunnerIntegrationCrawler struct {
	crawl func(
		context.Context,
		origin.Origin,
	) (discovery.CrawlResult, error)
}

func (crawler discoveryRunnerIntegrationCrawler) Crawl(
	ctx context.Context,
	source origin.Origin,
) (discovery.CrawlResult, error) {
	return crawler.crawl(ctx, source)
}

type discoveryRunnerIntegrationEvent struct {
	state   string
	source  origin.Origin
	message string
}

type discoveryRunnerIntegrationObserver struct {
	events chan discoveryRunnerIntegrationEvent
}

func (observer *discoveryRunnerIntegrationObserver) Observe(
	state string,
	source origin.Origin,
	message string,
) {
	observer.events <- discoveryRunnerIntegrationEvent{
		state:   state,
		source:  source,
		message: message,
	}
}

func TestDiscoveryRunnerIntegrationCrawlRunnerPersistsRetryFailure(
	t *testing.T,
) {
	source := mustDiscoveryRunnerIntegrationOrigin(
		t,
		"https://example.com",
	)
	crawlFailure := errors.New(
		"integration crawl failure",
	)

	store := &discoveryRunnerIntegrationCrawlStore{
		claims: []discoveryRunnerIntegrationClaim{
			{
				source: source,
				found:  true,
			},
		},
	}

	crawler := discoveryRunnerIntegrationCrawler{
		crawl: func(
			context.Context,
			origin.Origin,
		) (discovery.CrawlResult, error) {
			return discovery.CrawlResult{
				Source:          source,
				PagesAttempted:  1,
				FailureCategory: retry.CategoryHTTP429,
				RetryAfter:      time.Hour,
			}, crawlFailure
		},
	}

	runner, err := discovery.NewCrawlRunner(
		store,
		crawler,
		discovery.CrawlRunnerConfig{
			DiscoveryInterval: time.Hour,
			PollInterval:      time.Hour,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewCrawlRunner() error = %v",
			err,
		)
	}

	observer := &discoveryRunnerIntegrationObserver{
		events: make(
			chan discoveryRunnerIntegrationEvent,
			4,
		),
	}
	runner.SetLifecycleObserver(observer)

	report, err := runner.RunOnce(
		context.Background(),
	)
	if !errors.Is(err, crawlFailure) {
		t.Fatalf(
			"RunOnce() error = %v, want %v",
			err,
			crawlFailure,
		)
	}

	if !report.Worked || report.Source != source {
		t.Errorf(
			"RunOnce() report = %#v, want worked source %q",
			report,
			source.String(),
		)
	}

	if len(store.completedCategories) != 1 ||
		store.completedCategories[0] != retry.CategoryHTTP429 {
		t.Fatalf(
			"completion categories = %#v, want [%q]",
			store.completedCategories,
			retry.CategoryHTTP429,
		)
	}

	if len(store.completedRetryAfter) != 1 ||
		store.completedRetryAfter[0] != time.Hour {
		t.Fatalf(
			"completion retry-after = %#v, want [1h]",
			store.completedRetryAfter,
		)
	}

	running := receiveDiscoveryRunnerIntegrationEvent(
		t,
		observer.events,
	)
	failed := receiveDiscoveryRunnerIntegrationEvent(
		t,
		observer.events,
	)

	if running.state != "running" ||
		running.source != source {
		t.Errorf(
			"running event = %#v",
			running,
		)
	}

	if failed.state != "failed" ||
		failed.source != source ||
		failed.message != "crawl_failed" {
		t.Errorf(
			"failed event = %#v",
			failed,
		)
	}
}

func TestDiscoveryRunnerIntegrationCrawlRunnerReportsPauseAndCancellation(
	t *testing.T,
) {
	store := &discoveryRunnerIntegrationCrawlStore{
		paused: true,
	}

	crawler := discoveryRunnerIntegrationCrawler{
		crawl: func(
			context.Context,
			origin.Origin,
		) (discovery.CrawlResult, error) {
			t.Fatal(
				"crawler called without a claimed source",
			)
			return discovery.CrawlResult{}, nil
		},
	}

	runner, err := discovery.NewCrawlRunner(
		store,
		crawler,
		discovery.CrawlRunnerConfig{
			DiscoveryInterval: time.Hour,
			PollInterval:      time.Hour,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewCrawlRunner() error = %v",
			err,
		)
	}

	observer := &discoveryRunnerIntegrationObserver{
		events: make(
			chan discoveryRunnerIntegrationEvent,
			4,
		),
	}
	runner.SetLifecycleObserver(observer)

	report, err := runner.RunOnce(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"RunOnce() error = %v",
			err,
		)
	}

	if report != (discovery.CrawlReport{}) {
		t.Errorf(
			"RunOnce() report = %#v, want zero",
			report,
		)
	}

	paused := receiveDiscoveryRunnerIntegrationEvent(
		t,
		observer.events,
	)
	if paused.state != "paused" {
		t.Fatalf(
			"lifecycle event = %#v, want paused",
			paused,
		)
	}

	store.paused = false

	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	runResult := make(chan error, 1)
	go func() {
		runResult <- runner.Run(ctx)
	}()

	idle := receiveDiscoveryRunnerIntegrationEvent(
		t,
		observer.events,
	)
	if idle.state != "idle" {
		cancel()
		t.Fatalf(
			"Run() lifecycle event = %#v, want idle",
			idle,
		)
	}

	cancel()

	select {
	case err := <-runResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf(
				"Run() error = %v, want context.Canceled",
				err,
			)
		}
	case <-time.After(2 * time.Second):
		t.Fatal(
			"Run() did not stop after cancellation",
		)
	}
}

func receiveDiscoveryRunnerIntegrationEvent(
	t *testing.T,
	events <-chan discoveryRunnerIntegrationEvent,
) discoveryRunnerIntegrationEvent {
	t.Helper()

	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal(
			"discovery lifecycle event was not observed",
		)
		return discoveryRunnerIntegrationEvent{}
	}
}

func mustDiscoveryRunnerIntegrationOrigin(
	t *testing.T,
	raw string,
) origin.Origin {
	t.Helper()

	source, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf(
			"origin.Parse(%q) error = %v",
			raw,
			err,
		)
	}

	return source
}

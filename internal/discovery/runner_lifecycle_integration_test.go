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

func (store *discoveryRunnerIntegrationCrawlStore) ClaimDiscoverySource(
	context.Context,
	time.Duration,
) (origin.Origin, bool, error) {
	if store.index >= len(store.claims) {
		return origin.Origin{}, false, nil
	}

	claim := store.claims[store.index]
	store.index++

	return claim.source, claim.found, claim.err
}

func (store *discoveryRunnerIntegrationCrawlStore) DiscoveryPaused(
	context.Context,
) (bool, error) {
	if store.pauseErr != nil {
		return false, store.pauseErr
	}

	return store.paused, nil
}

func (store *discoveryRunnerIntegrationCrawlStore) CompleteDiscoverySourceRetry(
	_ context.Context,
	_ origin.Origin,
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

type discoveryLegacyIntegrationStore struct {
	source origin.Origin
	found  bool

	recordedSource     origin.Origin
	recordedCandidates []discovery.Candidate
	accepted           int
}

func (store *discoveryLegacyIntegrationStore) ClaimDiscoverySource(
	context.Context,
	time.Duration,
) (origin.Origin, bool, error) {
	if !store.found {
		return origin.Origin{}, false, nil
	}

	store.found = false
	return store.source, true, nil
}

func (store *discoveryLegacyIntegrationStore) RecordDiscovery(
	_ context.Context,
	source origin.Origin,
	candidates []discovery.Candidate,
) (discovery.RecordResult, error) {
	store.recordedSource = source
	store.recordedCandidates = append(
		[]discovery.Candidate(nil),
		candidates...,
	)

	return discovery.RecordResult{
		Accepted: store.accepted,
	}, nil
}

type discoveryLegacyIntegrationDiscoverer struct {
	discover func(
		context.Context,
		origin.Origin,
	) (discovery.Result, error)
}

func (discoverer discoveryLegacyIntegrationDiscoverer) Discover(
	ctx context.Context,
	source origin.Origin,
) (discovery.Result, error) {
	return discoverer.discover(ctx, source)
}

func TestDiscoveryRunnerIntegrationLegacyRunnerPersistsCandidates(
	t *testing.T,
) {
	source := mustDiscoveryRunnerIntegrationOrigin(
		t,
		"https://example.com",
	)
	candidateOrigin := mustDiscoveryRunnerIntegrationOrigin(
		t,
		"https://candidate.example",
	)

	store := &discoveryLegacyIntegrationStore{
		source:   source,
		found:    true,
		accepted: 1,
	}

	discoverer := discoveryLegacyIntegrationDiscoverer{
		discover: func(
			context.Context,
			origin.Origin,
		) (discovery.Result, error) {
			return discovery.Result{
				Source: source,
				Status: discovery.StatusComplete,
				Candidates: []discovery.Candidate{
					{
						Origin: candidateOrigin,
						Kind:   discovery.KindLink,
					},
				},
			}, nil
		},
	}

	runner, err := discovery.NewRunner(
		store,
		discoverer,
		discovery.Config{
			DiscoveryInterval: time.Hour,
			PollInterval:      time.Hour,
			PageTimeout:       time.Second,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewRunner() error = %v",
			err,
		)
	}

	report, err := runner.RunOnce(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"RunOnce() error = %v",
			err,
		)
	}

	if !report.Worked ||
		report.Source != source ||
		report.Status != discovery.StatusComplete ||
		report.Candidates != 1 ||
		report.Accepted != 1 {
		t.Errorf(
			"RunOnce() report = %#v",
			report,
		)
	}

	if store.recordedSource != source {
		t.Errorf(
			"recorded source = %q, want %q",
			store.recordedSource.String(),
			source.String(),
		)
	}

	if len(store.recordedCandidates) != 1 ||
		store.recordedCandidates[0].Origin != candidateOrigin {
		t.Errorf(
			"recorded candidates = %#v",
			store.recordedCandidates,
		)
	}
}

func TestDiscoveryRunnerIntegrationLegacyRunnerTimesOutAndCancels(
	t *testing.T,
) {
	source := mustDiscoveryRunnerIntegrationOrigin(
		t,
		"https://timeout.example",
	)

	store := &discoveryLegacyIntegrationStore{
		source: source,
		found:  true,
	}

	discoverer := discoveryLegacyIntegrationDiscoverer{
		discover: func(
			ctx context.Context,
			source origin.Origin,
		) (discovery.Result, error) {
			<-ctx.Done()
			return discovery.Result{
				Source: source,
			}, ctx.Err()
		},
	}

	runner, err := discovery.NewRunner(
		store,
		discoverer,
		discovery.Config{
			DiscoveryInterval: time.Hour,
			PollInterval:      time.Hour,
			PageTimeout:       20 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewRunner() error = %v",
			err,
		)
	}

	report, err := runner.RunOnce(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"RunOnce() error = %v",
			err,
		)
	}

	if !report.Worked ||
		report.Source != source ||
		report.Status != discovery.StatusUnavailable {
		t.Errorf(
			"timeout report = %#v",
			report,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	runResult := make(chan error, 1)
	go func() {
		runResult <- runner.Run(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
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

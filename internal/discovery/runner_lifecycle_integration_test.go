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

	renewErr                 error
	renewWaitForCancellation bool
	renewStarted             chan struct{}
	renewed                  chan discovery.CrawlSourceLease
	renewCalls               int
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
	ctx context.Context,
	lease discovery.CrawlSourceLease,
	leaseDuration time.Duration,
) (discovery.CrawlSourceLease, error) {
	store.renewCalls++

	if store.renewStarted != nil {
		select {
		case store.renewStarted <- struct{}{}:
		default:
		}
	}

	if store.renewWaitForCancellation {
		<-ctx.Done()

		return lease, ctx.Err()
	}

	if store.renewErr != nil {
		return lease, store.renewErr
	}

	lease.ExpiresAt = lease.ExpiresAt.Add(
		leaseDuration,
	)

	if store.renewed != nil {
		select {
		case store.renewed <- lease:
		default:
		}
	}

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

func TestDiscoveryRunnerIntegrationConstructorAndValidationBoundaries(
	t *testing.T,
) {
	validStore := &discoveryRunnerIntegrationCrawlStore{}

	validCrawler := discoveryRunnerIntegrationCrawler{
		crawl: func(
			context.Context,
			origin.Origin,
		) (discovery.CrawlResult, error) {
			return discovery.CrawlResult{}, nil
		},
	}

	validConfig := discovery.CrawlRunnerConfig{
		DiscoveryInterval: time.Hour,
		PollInterval:      time.Hour,
	}

	tests := []struct {
		name    string
		store   discovery.CrawlSourceStore
		crawler discovery.SourceCrawler
		config  discovery.CrawlRunnerConfig
	}{
		{
			name:    "nil store",
			crawler: validCrawler,
			config:  validConfig,
		},
		{
			name:   "nil crawler",
			store:  validStore,
			config: validConfig,
		},
		{
			name:    "invalid configuration",
			store:   validStore,
			crawler: validCrawler,
			config:  discovery.CrawlRunnerConfig{},
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				runner, err := discovery.NewCrawlRunner(
					test.store,
					test.crawler,
					test.config,
				)

				if err == nil {
					t.Fatal(
						"NewCrawlRunner() error = nil",
					)
				}

				if runner != nil {
					t.Errorf(
						"NewCrawlRunner() runner = %#v, want nil",
						runner,
					)
				}
			},
		)
	}

	var nilRunner *discovery.CrawlRunner

	if _, err := nilRunner.RunOnce(
		context.Background(),
	); err == nil {
		t.Fatal(
			"nil CrawlRunner.RunOnce() error = nil",
		)
	}

	runner, err := discovery.NewCrawlRunner(
		validStore,
		validCrawler,
		validConfig,
	)
	if err != nil {
		t.Fatalf(
			"NewCrawlRunner() error = %v",
			err,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if _, err := runner.RunOnce(ctx); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Errorf(
			"RunOnce(canceled) error = %v, want context.Canceled",
			err,
		)
	}

	if err := runner.Run(ctx); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Errorf(
			"Run(canceled) error = %v, want context.Canceled",
			err,
		)
	}
}

func TestDiscoveryRunnerIntegrationRunOnceFailureBoundaries(
	t *testing.T,
) {
	source := mustDiscoveryRunnerIntegrationOrigin(
		t,
		"https://example.com",
	)

	config := discovery.CrawlRunnerConfig{
		DiscoveryInterval: time.Hour,
		PollInterval:      time.Hour,
	}

	t.Run(
		"claim failure",
		func(t *testing.T) {
			claimErr := errors.New(
				"integration claim failure",
			)

			store := &discoveryRunnerIntegrationCrawlStore{
				claims: []discoveryRunnerIntegrationClaim{
					{err: claimErr},
				},
			}

			crawler := discoveryRunnerIntegrationCrawler{
				crawl: func(
					context.Context,
					origin.Origin,
				) (discovery.CrawlResult, error) {
					t.Fatal(
						"crawler called after claim failure",
					)

					return discovery.CrawlResult{}, nil
				},
			}

			runner, err := discovery.NewCrawlRunner(
				store,
				crawler,
				config,
			)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := runner.RunOnce(
				context.Background(),
			); !errors.Is(err, claimErr) {
				t.Errorf(
					"RunOnce() error = %v, want %v",
					err,
					claimErr,
				)
			}
		},
	)

	crawlErr := errors.New(
		"integration crawl failure",
	)

	tests := []struct {
		name           string
		result         discovery.CrawlResult
		completeErr    error
		wantCategory   retry.Category
		wantRetryAfter time.Duration
		wantErr        error
	}{
		{
			name:         "missing failure category becomes processor",
			result:       discovery.CrawlResult{},
			wantCategory: retry.CategoryProcessor,
			wantErr:      crawlErr,
		},
		{
			name: "partial success clears transient failure",
			result: discovery.CrawlResult{
				PagesAttempted:  2,
				PagesParsed:     1,
				FailureCategory: retry.CategoryHTTP5xx,
				RetryAfter:      time.Hour,
			},
			wantCategory: retry.CategoryNone,
			wantErr:      crawlErr,
		},
		{
			name: "failed crawl completion failure",
			result: discovery.CrawlResult{
				PagesAttempted:  1,
				FailureCategory: retry.CategoryHTTP429,
				RetryAfter:      time.Hour,
			},
			completeErr: errors.New(
				"integration failed completion failure",
			),
			wantCategory:   retry.CategoryHTTP429,
			wantRetryAfter: time.Hour,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				store := &discoveryRunnerIntegrationCrawlStore{
					claims: []discoveryRunnerIntegrationClaim{
						{
							source: source,
							found:  true,
						},
					},
					completeErr: test.completeErr,
				}

				crawler := discoveryRunnerIntegrationCrawler{
					crawl: func(
						context.Context,
						origin.Origin,
					) (discovery.CrawlResult, error) {
						return test.result, crawlErr
					},
				}

				runner, err := discovery.NewCrawlRunner(
					store,
					crawler,
					config,
				)
				if err != nil {
					t.Fatal(err)
				}

				_, runErr := runner.RunOnce(
					context.Background(),
				)

				expectedErr := test.wantErr
				if test.completeErr != nil {
					expectedErr = test.completeErr
				}

				if !errors.Is(
					runErr,
					expectedErr,
				) {
					t.Errorf(
						"RunOnce() error = %v, want %v",
						runErr,
						expectedErr,
					)
				}

				if len(
					store.completedCategories,
				) != 1 ||
					store.completedCategories[0] !=
						test.wantCategory {
					t.Errorf(
						"completion categories = %#v, want [%q]",
						store.completedCategories,
						test.wantCategory,
					)
				}

				if len(
					store.completedRetryAfter,
				) != 1 ||
					store.completedRetryAfter[0] !=
						test.wantRetryAfter {
					t.Errorf(
						"completion retry-after = %#v, want [%v]",
						store.completedRetryAfter,
						test.wantRetryAfter,
					)
				}
			},
		)
	}

	t.Run(
		"successful crawl completion failure",
		func(t *testing.T) {
			completeErr := errors.New(
				"integration completion failure",
			)

			store := &discoveryRunnerIntegrationCrawlStore{
				claims: []discoveryRunnerIntegrationClaim{
					{
						source: source,
						found:  true,
					},
				},
				completeErr: completeErr,
			}

			crawler := discoveryRunnerIntegrationCrawler{
				crawl: func(
					context.Context,
					origin.Origin,
				) (discovery.CrawlResult, error) {
					return discovery.CrawlResult{
						PagesAttempted: 1,
						PagesParsed:    1,
					}, nil
				},
			}

			runner, err := discovery.NewCrawlRunner(
				store,
				crawler,
				config,
			)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := runner.RunOnce(
				context.Background(),
			); !errors.Is(err, completeErr) {
				t.Errorf(
					"RunOnce() error = %v, want %v",
					err,
					completeErr,
				)
			}
		},
	)

	t.Run(
		"parent cancellation wins over crawl failure",
		func(t *testing.T) {
			store := &discoveryRunnerIntegrationCrawlStore{
				claims: []discoveryRunnerIntegrationClaim{
					{
						source: source,
						found:  true,
					},
				},
			}

			ctx, cancel := context.WithCancel(
				context.Background(),
			)

			crawler := discoveryRunnerIntegrationCrawler{
				crawl: func(
					context.Context,
					origin.Origin,
				) (discovery.CrawlResult, error) {
					cancel()

					return discovery.CrawlResult{},
						crawlErr
				},
			}

			runner, err := discovery.NewCrawlRunner(
				store,
				crawler,
				config,
			)
			if err != nil {
				t.Fatal(err)
			}

			report, err := runner.RunOnce(ctx)
			if !errors.Is(
				err,
				context.Canceled,
			) {
				t.Errorf(
					"RunOnce() error = %v, want context.Canceled",
					err,
				)
			}

			if !report.Worked ||
				report.Source != source {
				t.Errorf(
					"RunOnce() report = %#v",
					report,
				)
			}
		},
	)
}

func TestDiscoveryRunnerIntegrationLeaseRenewalBoundaries(
	t *testing.T,
) {
	source := mustDiscoveryRunnerIntegrationOrigin(
		t,
		"https://example.com",
	)

	t.Run(
		"successful renewal",
		func(t *testing.T) {
			renewed := make(
				chan discovery.CrawlSourceLease,
				1,
			)

			store := &discoveryRunnerIntegrationCrawlStore{
				claims: []discoveryRunnerIntegrationClaim{
					{
						source: source,
						found:  true,
					},
				},
				renewed: renewed,
			}

			crawler := discoveryRunnerIntegrationCrawler{
				crawl: func(
					ctx context.Context,
					_ origin.Origin,
				) (discovery.CrawlResult, error) {
					select {
					case <-renewed:
						return discovery.CrawlResult{
							PagesAttempted: 1,
							PagesParsed:    1,
						}, nil

					case <-ctx.Done():
						return discovery.CrawlResult{},
							ctx.Err()

					case <-time.After(
						2 * time.Second,
					):
						return discovery.CrawlResult{},
							errors.New(
								"renewal was not observed",
							)
					}
				},
			}

			runner, err := discovery.NewCrawlRunner(
				store,
				crawler,
				discovery.CrawlRunnerConfig{
					DiscoveryInterval: time.Hour,
					PollInterval:      time.Hour,
					LeaseDuration:     time.Nanosecond,
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := runner.RunOnce(
				context.Background(),
			); err != nil {
				t.Fatalf(
					"RunOnce() error = %v",
					err,
				)
			}

			if store.renewCalls == 0 {
				t.Error(
					"RenewDiscoverySourceLease() calls = 0",
				)
			}
		},
	)

	t.Run(
		"renewal failure cancels crawl",
		func(t *testing.T) {
			renewErr := errors.New(
				"integration renewal failure",
			)

			store := &discoveryRunnerIntegrationCrawlStore{
				claims: []discoveryRunnerIntegrationClaim{
					{
						source: source,
						found:  true,
					},
				},
				renewErr: renewErr,
			}

			crawler := discoveryRunnerIntegrationCrawler{
				crawl: func(
					ctx context.Context,
					_ origin.Origin,
				) (discovery.CrawlResult, error) {
					<-ctx.Done()

					return discovery.CrawlResult{},
						ctx.Err()
				},
			}

			runner, err := discovery.NewCrawlRunner(
				store,
				crawler,
				discovery.CrawlRunnerConfig{
					DiscoveryInterval: time.Hour,
					PollInterval:      time.Hour,
					LeaseDuration:     10 * time.Millisecond,
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := runner.RunOnce(
				context.Background(),
			); !errors.Is(err, renewErr) {
				t.Errorf(
					"RunOnce() error = %v, want %v",
					err,
					renewErr,
				)
			}
		},
	)

	t.Run(
		"renewal canceled after crawl completes",
		func(t *testing.T) {
			renewStarted := make(
				chan struct{},
				1,
			)

			store := &discoveryRunnerIntegrationCrawlStore{
				claims: []discoveryRunnerIntegrationClaim{
					{
						source: source,
						found:  true,
					},
				},
				renewWaitForCancellation: true,
				renewStarted:             renewStarted,
			}

			crawler := discoveryRunnerIntegrationCrawler{
				crawl: func(
					ctx context.Context,
					_ origin.Origin,
				) (discovery.CrawlResult, error) {
					select {
					case <-renewStarted:
						return discovery.CrawlResult{
							PagesAttempted: 1,
							PagesParsed:    1,
						}, nil

					case <-ctx.Done():
						return discovery.CrawlResult{},
							ctx.Err()

					case <-time.After(
						2 * time.Second,
					):
						return discovery.CrawlResult{},
							errors.New(
								"renewal did not start",
							)
					}
				},
			}

			runner, err := discovery.NewCrawlRunner(
				store,
				crawler,
				discovery.CrawlRunnerConfig{
					DiscoveryInterval: time.Hour,
					PollInterval:      time.Hour,
					LeaseDuration:     10 * time.Millisecond,
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := runner.RunOnce(
				context.Background(),
			); err != nil {
				t.Fatalf(
					"RunOnce() error = %v",
					err,
				)
			}

			if store.renewCalls == 0 {
				t.Error(
					"RenewDiscoverySourceLease() calls = 0",
				)
			}
		},
	)
}

func TestDiscoveryRunnerIntegrationPauseControlFailure(
	t *testing.T,
) {
	pauseErr := errors.New(
		"integration pause control failure",
	)

	store := &discoveryRunnerIntegrationCrawlStore{
		pauseErr: pauseErr,
	}

	crawler := discoveryRunnerIntegrationCrawler{
		crawl: func(
			context.Context,
			origin.Origin,
		) (discovery.CrawlResult, error) {
			t.Fatal(
				"crawler called without claimed source",
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
		t.Fatal(err)
	}

	observer := &discoveryRunnerIntegrationObserver{
		events: make(
			chan discoveryRunnerIntegrationEvent,
			2,
		),
	}
	runner.SetLifecycleObserver(observer)

	if _, err := runner.RunOnce(
		context.Background(),
	); !errors.Is(err, pauseErr) {
		t.Errorf(
			"RunOnce() error = %v, want %v",
			err,
			pauseErr,
		)
	}

	event := receiveDiscoveryRunnerIntegrationEvent(
		t,
		observer.events,
	)

	if event.state != "failed" ||
		event.message != "control_read_failed" {
		t.Errorf(
			"lifecycle event = %#v",
			event,
		)
	}
}

func TestDiscoveryRunnerIntegrationRunLoopBoundaries(
	t *testing.T,
) {
	first := mustDiscoveryRunnerIntegrationOrigin(
		t,
		"https://first.example",
	)
	second := mustDiscoveryRunnerIntegrationOrigin(
		t,
		"https://second.example",
	)

	t.Run(
		"claimed failure continues immediately",
		func(t *testing.T) {
			store := &discoveryRunnerIntegrationCrawlStore{
				claims: []discoveryRunnerIntegrationClaim{
					{
						source: first,
						found:  true,
					},
					{
						source: second,
						found:  true,
					},
				},
			}

			ctx, cancel := context.WithTimeout(
				context.Background(),
				2*time.Second,
			)
			defer cancel()

			crawlCalls := 0
			firstErr := errors.New(
				"integration claimed crawl failure",
			)

			crawler := discoveryRunnerIntegrationCrawler{
				crawl: func(
					context.Context,
					origin.Origin,
				) (discovery.CrawlResult, error) {
					crawlCalls++

					if crawlCalls == 1 {
						return discovery.CrawlResult{
							FailureCategory: retry.CategoryProcessor,
						}, firstErr
					}

					cancel()

					return discovery.CrawlResult{},
						errors.New(
							"integration stop after claimed retry",
						)
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
				t.Fatal(err)
			}

			err = runner.Run(ctx)
			if !errors.Is(
				err,
				context.Canceled,
			) {
				t.Errorf(
					"Run() error = %v, want context.Canceled",
					err,
				)
			}

			if crawlCalls != 2 {
				t.Errorf(
					"Crawl() calls = %d, want 2",
					crawlCalls,
				)
			}
		},
	)

	t.Run(
		"claim failure waits then retries",
		func(t *testing.T) {
			claimErr := errors.New(
				"integration transient claim failure",
			)

			store := &discoveryRunnerIntegrationCrawlStore{
				claims: []discoveryRunnerIntegrationClaim{
					{err: claimErr},
					{
						source: second,
						found:  true,
					},
				},
			}

			ctx, cancel := context.WithCancel(
				context.Background(),
			)

			crawlCalls := 0
			crawler := discoveryRunnerIntegrationCrawler{
				crawl: func(
					context.Context,
					origin.Origin,
				) (discovery.CrawlResult, error) {
					crawlCalls++
					cancel()

					return discovery.CrawlResult{},
						errors.New(
							"integration stop after claim retry",
						)
				},
			}

			runner, err := discovery.NewCrawlRunner(
				store,
				crawler,
				discovery.CrawlRunnerConfig{
					DiscoveryInterval: time.Hour,
					PollInterval:      time.Millisecond,
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			err = runner.Run(ctx)
			if !errors.Is(
				err,
				context.Canceled,
			) {
				t.Errorf(
					"Run() error = %v, want context.Canceled",
					err,
				)
			}

			if crawlCalls != 1 {
				t.Errorf(
					"Crawl() calls = %d, want 1",
					crawlCalls,
				)
			}

			if store.index != 2 {
				t.Errorf(
					"claim attempts = %d, want 2",
					store.index,
				)
			}
		},
	)

	t.Run(
		"successful work continues immediately",
		func(t *testing.T) {
			store := &discoveryRunnerIntegrationCrawlStore{
				claims: []discoveryRunnerIntegrationClaim{
					{
						source: first,
						found:  true,
					},
					{
						source: second,
						found:  true,
					},
				},
			}

			ctx, cancel := context.WithTimeout(
				context.Background(),
				2*time.Second,
			)
			defer cancel()

			crawlCalls := 0

			crawler := discoveryRunnerIntegrationCrawler{
				crawl: func(
					context.Context,
					origin.Origin,
				) (discovery.CrawlResult, error) {
					crawlCalls++

					if crawlCalls == 1 {
						return discovery.CrawlResult{
							PagesAttempted: 1,
							PagesParsed:    1,
						}, nil
					}

					cancel()

					return discovery.CrawlResult{},
						errors.New(
							"integration stop after successful work",
						)
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
				t.Fatal(err)
			}

			err = runner.Run(ctx)
			if !errors.Is(
				err,
				context.Canceled,
			) {
				t.Errorf(
					"Run() error = %v, want context.Canceled",
					err,
				)
			}

			if crawlCalls != 2 {
				t.Errorf(
					"Crawl() calls = %d, want 2",
					crawlCalls,
				)
			}
		},
	)

	t.Run(
		"idle poll expires then retries",
		func(t *testing.T) {
			store := &discoveryRunnerIntegrationCrawlStore{
				claims: []discoveryRunnerIntegrationClaim{
					{},
					{
						source: second,
						found:  true,
					},
				},
			}

			ctx, cancel := context.WithCancel(
				context.Background(),
			)

			crawlCalls := 0
			crawler := discoveryRunnerIntegrationCrawler{
				crawl: func(
					context.Context,
					origin.Origin,
				) (discovery.CrawlResult, error) {
					crawlCalls++
					cancel()

					return discovery.CrawlResult{},
						errors.New(
							"integration stop after idle poll",
						)
				},
			}

			runner, err := discovery.NewCrawlRunner(
				store,
				crawler,
				discovery.CrawlRunnerConfig{
					DiscoveryInterval: time.Hour,
					PollInterval:      time.Millisecond,
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			err = runner.Run(ctx)
			if !errors.Is(
				err,
				context.Canceled,
			) {
				t.Errorf(
					"Run() error = %v, want context.Canceled",
					err,
				)
			}

			if crawlCalls != 1 {
				t.Errorf(
					"Crawl() calls = %d, want 1",
					crawlCalls,
				)
			}

			if store.index != 2 {
				t.Errorf(
					"claim attempts = %d, want 2",
					store.index,
				)
			}
		},
	)
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

package discovery

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

type fakeLeaseCrawlSourceClaim struct {
	lease CrawlSourceLease
	found bool
	err   error
}

type fakeLeaseRenewResult struct {
	lease CrawlSourceLease
	err   error
}

type fakeLeaseCompletion struct {
	lease      CrawlSourceLease
	category   retry.Category
	retryAfter time.Duration
}

type fakeLeaseCrawlSourceStore struct {
	*fakeCrawlSourceStore

	leaseClaims        []fakeLeaseCrawlSourceClaim
	claimIntervals     []time.Duration
	claimLeaseDuration []time.Duration

	renewResults   []fakeLeaseRenewResult
	renewLeases    []CrawlSourceLease
	renewDurations []time.Duration
	renewCalls     chan struct{}
	renewFunc      func(
		context.Context,
		CrawlSourceLease,
		time.Duration,
	) (CrawlSourceLease, error)

	completions []fakeLeaseCompletion
	completeErr error

	paused   bool
	pauseErr error
}

func newFakeLeaseCrawlSourceStore() *fakeLeaseCrawlSourceStore {
	return &fakeLeaseCrawlSourceStore{
		fakeCrawlSourceStore: &fakeCrawlSourceStore{},
	}
}

func (store *fakeLeaseCrawlSourceStore) ClaimDiscoverySourceLease(
	_ context.Context,
	interval time.Duration,
	leaseDuration time.Duration,
) (CrawlSourceLease, bool, error) {
	store.claimIntervals = append(
		store.claimIntervals,
		interval,
	)
	store.claimLeaseDuration = append(
		store.claimLeaseDuration,
		leaseDuration,
	)

	if len(store.leaseClaims) == 0 {
		return CrawlSourceLease{}, false, nil
	}

	claim := store.leaseClaims[0]
	store.leaseClaims = store.leaseClaims[1:]

	return claim.lease, claim.found, claim.err
}

func (store *fakeLeaseCrawlSourceStore) RenewDiscoverySourceLease(
	ctx context.Context,
	lease CrawlSourceLease,
	leaseDuration time.Duration,
) (CrawlSourceLease, error) {
	store.renewLeases = append(
		store.renewLeases,
		lease,
	)
	store.renewDurations = append(
		store.renewDurations,
		leaseDuration,
	)

	var (
		renewed CrawlSourceLease
		err     error
	)

	switch {
	case store.renewFunc != nil:
		renewed, err = store.renewFunc(
			ctx,
			lease,
			leaseDuration,
		)

	case len(store.renewResults) > 0:
		result := store.renewResults[0]
		store.renewResults =
			store.renewResults[1:]
		renewed = result.lease
		err = result.err

	default:
		renewed = lease
		renewed.ExpiresAt =
			lease.ExpiresAt.Add(
				leaseDuration,
			)
	}

	if store.renewCalls != nil {
		select {
		case store.renewCalls <- struct{}{}:
		default:
		}
	}

	return renewed, err
}

func (store *fakeLeaseCrawlSourceStore) CompleteDiscoverySourceLeaseRetry(
	_ context.Context,
	lease CrawlSourceLease,
	category retry.Category,
	retryAfter time.Duration,
) error {
	store.completions = append(
		store.completions,
		fakeLeaseCompletion{
			lease:      lease,
			category:   category,
			retryAfter: retryAfter,
		},
	)

	return store.completeErr
}

func (store *fakeLeaseCrawlSourceStore) DiscoveryPaused(
	context.Context,
) (bool, error) {
	return store.paused, store.pauseErr
}

func TestCrawlRunnerUsesDiscoverySourceLease(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	lease := testCrawlSourceLease(
		source,
	)

	store := newFakeLeaseCrawlSourceStore()
	store.leaseClaims = []fakeLeaseCrawlSourceClaim{
		{
			lease: lease,
			found: true,
		},
	}

	crawler := &fakeSourceCrawler{
		results: []fakeSourceCrawlResult{
			{
				result: CrawlResult{
					Source:               source,
					PagesAttempted:       4,
					PagesParsed:          3,
					CandidatesDiscovered: 7,
					BudgetExhausted:      true,
				},
			},
		},
	}

	config := testCrawlRunnerConfig()
	config.LeaseDuration = time.Hour

	runner, err := NewCrawlRunner(
		store,
		crawler,
		config,
	)
	if err != nil {
		t.Fatal(err)
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

	wantReport := CrawlReport{
		Worked:               true,
		Source:               source,
		PagesAttempted:       4,
		PagesParsed:          3,
		CandidatesDiscovered: 7,
		BudgetExhausted:      true,
	}

	if report != wantReport {
		t.Fatalf(
			"RunOnce() report = %#v, want %#v",
			report,
			wantReport,
		)
	}

	if !reflect.DeepEqual(
		store.claimIntervals,
		[]time.Duration{
			config.DiscoveryInterval,
		},
	) {
		t.Fatalf(
			"claim intervals = %#v",
			store.claimIntervals,
		)
	}

	if !reflect.DeepEqual(
		store.claimLeaseDuration,
		[]time.Duration{
			config.LeaseDuration,
		},
	) {
		t.Fatalf(
			"claim lease durations = %#v",
			store.claimLeaseDuration,
		)
	}

	if len(store.intervals) != 0 {
		t.Fatalf(
			"legacy claims = %#v, want none",
			store.intervals,
		)
	}

	wantCompletion := fakeLeaseCompletion{
		lease:      lease,
		category:   retry.CategoryNone,
		retryAfter: 0,
	}

	if !reflect.DeepEqual(
		store.completions,
		[]fakeLeaseCompletion{
			wantCompletion,
		},
	) {
		t.Fatalf(
			"completions = %#v, want %#v",
			store.completions,
			wantCompletion,
		)
	}
}

func TestCrawlRunnerLeaseClaimStates(
	t *testing.T,
) {
	t.Run(
		"claim failure",
		func(t *testing.T) {
			claimErr := errors.New(
				"test lease claim failure",
			)

			store :=
				newFakeLeaseCrawlSourceStore()
			store.leaseClaims =
				[]fakeLeaseCrawlSourceClaim{
					{
						err: claimErr,
					},
				}

			runner, err := NewCrawlRunner(
				store,
				&fakeSourceCrawler{},
				testCrawlRunnerConfig(),
			)
			if err != nil {
				t.Fatal(err)
			}

			report, err := runner.RunOnce(
				context.Background(),
			)

			if !errors.Is(err, claimErr) ||
				!strings.Contains(
					err.Error(),
					"claim crawl source",
				) {
				t.Fatalf(
					"RunOnce() error = %v",
					err,
				)
			}

			if report != (CrawlReport{}) {
				t.Fatalf(
					"RunOnce() report = %#v, want zero",
					report,
				)
			}
		},
	)

	t.Run(
		"paused",
		func(t *testing.T) {
			store :=
				newFakeLeaseCrawlSourceStore()
			store.paused = true

			runner, err := NewCrawlRunner(
				store,
				&fakeSourceCrawler{},
				testCrawlRunnerConfig(),
			)
			if err != nil {
				t.Fatal(err)
			}

			observer :=
				&recordingLifecycleObserver{}
			runner.SetLifecycleObserver(
				observer,
			)

			report, err := runner.RunOnce(
				context.Background(),
			)
			if err != nil {
				t.Fatal(err)
			}

			if report != (CrawlReport{}) {
				t.Fatalf(
					"RunOnce() report = %#v, want zero",
					report,
				)
			}

			if !reflect.DeepEqual(
				observer.states,
				[]string{"paused"},
			) {
				t.Fatalf(
					"states = %#v, want paused",
					observer.states,
				)
			}
		},
	)

	t.Run(
		"control failure",
		func(t *testing.T) {
			controlErr := errors.New(
				"test lease control failure",
			)

			store :=
				newFakeLeaseCrawlSourceStore()
			store.pauseErr = controlErr

			runner, err := NewCrawlRunner(
				store,
				&fakeSourceCrawler{},
				testCrawlRunnerConfig(),
			)
			if err != nil {
				t.Fatal(err)
			}

			_, err = runner.RunOnce(
				context.Background(),
			)

			if !errors.Is(
				err,
				controlErr,
			) {
				t.Fatalf(
					"RunOnce() error = %v, want %v",
					err,
					controlErr,
				)
			}
		},
	)
}

func TestCrawlRunnerLeaseCrawlFailureCompletion(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	crawlErr := errors.New(
		"test lease crawl failure",
	)

	tests := []struct {
		name           string
		result         CrawlResult
		wantCategory   retry.Category
		wantRetryAfter time.Duration
	}{
		{
			name: "processor fallback",
			result: CrawlResult{
				Source: source,
			},
			wantCategory: retry.CategoryProcessor,
		},
		{
			name: "partial success resets failure",
			result: CrawlResult{
				Source:          source,
				PagesAttempted:  2,
				PagesParsed:     1,
				FailureCategory: retry.CategoryHTTP5xx,
				RetryAfter:      time.Hour,
			},
			wantCategory: retry.CategoryNone,
		},
		{
			name: "typed transient failure",
			result: CrawlResult{
				Source:          source,
				PagesAttempted:  1,
				FailureCategory: retry.CategoryHTTP429,
				RetryAfter:      time.Hour,
			},
			wantCategory:   retry.CategoryHTTP429,
			wantRetryAfter: time.Hour,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				lease := testCrawlSourceLease(
					source,
				)

				store :=
					newFakeLeaseCrawlSourceStore()
				store.leaseClaims =
					[]fakeLeaseCrawlSourceClaim{
						{
							lease: lease,
							found: true,
						},
					}

				crawler := &fakeSourceCrawler{
					results: []fakeSourceCrawlResult{
						{
							result: test.result,
							err:    crawlErr,
						},
					},
				}

				config :=
					testCrawlRunnerConfig()
				config.LeaseDuration =
					time.Hour

				runner, err :=
					NewCrawlRunner(
						store,
						crawler,
						config,
					)
				if err != nil {
					t.Fatal(err)
				}

				_, err = runner.RunOnce(
					context.Background(),
				)

				if !errors.Is(
					err,
					crawlErr,
				) {
					t.Fatalf(
						"RunOnce() error = %v, want %v",
						err,
						crawlErr,
					)
				}

				want := []fakeLeaseCompletion{
					{
						lease:      lease,
						category:   test.wantCategory,
						retryAfter: test.wantRetryAfter,
					},
				}

				if !reflect.DeepEqual(
					store.completions,
					want,
				) {
					t.Fatalf(
						"completions = %#v, want %#v",
						store.completions,
						want,
					)
				}
			},
		)
	}
}

func TestCrawlRunnerLeaseCompletionFailures(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	completionErr := errors.New(
		"test lease completion failure",
	)

	t.Run(
		"successful crawl",
		func(t *testing.T) {
			lease := testCrawlSourceLease(
				source,
			)

			store :=
				newFakeLeaseCrawlSourceStore()
			store.leaseClaims =
				[]fakeLeaseCrawlSourceClaim{
					{
						lease: lease,
						found: true,
					},
				}
			store.completeErr =
				completionErr

			config :=
				testCrawlRunnerConfig()
			config.LeaseDuration =
				time.Hour

			runner, err := NewCrawlRunner(
				store,
				&fakeSourceCrawler{},
				config,
			)
			if err != nil {
				t.Fatal(err)
			}

			_, err = runner.RunOnce(
				context.Background(),
			)

			if !errors.Is(
				err,
				completionErr,
			) ||
				!strings.Contains(
					err.Error(),
					"complete crawl source",
				) {
				t.Fatalf(
					"RunOnce() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"failed crawl",
		func(t *testing.T) {
			lease := testCrawlSourceLease(
				source,
			)

			store :=
				newFakeLeaseCrawlSourceStore()
			store.leaseClaims =
				[]fakeLeaseCrawlSourceClaim{
					{
						lease: lease,
						found: true,
					},
				}
			store.completeErr =
				completionErr

			crawlErr := errors.New(
				"test crawl failure",
			)

			crawler := &fakeSourceCrawler{
				results: []fakeSourceCrawlResult{
					{
						err: crawlErr,
					},
				},
			}

			config :=
				testCrawlRunnerConfig()
			config.LeaseDuration =
				time.Hour

			runner, err := NewCrawlRunner(
				store,
				crawler,
				config,
			)
			if err != nil {
				t.Fatal(err)
			}

			_, err = runner.RunOnce(
				context.Background(),
			)

			if !errors.Is(
				err,
				completionErr,
			) ||
				!strings.Contains(
					err.Error(),
					"complete failed crawl source",
				) {
				t.Fatalf(
					"RunOnce() error = %v",
					err,
				)
			}
		},
	)
}

func TestCrawlRunnerLeaseRenewalUsesRenewedLease(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	lease := testCrawlSourceLease(
		source,
	)

	renewed := lease
	renewed.ExpiresAt =
		lease.ExpiresAt.Add(time.Hour)

	store :=
		newFakeLeaseCrawlSourceStore()
	store.leaseClaims =
		[]fakeLeaseCrawlSourceClaim{
			{
				lease: lease,
				found: true,
			},
		}
	store.renewResults =
		[]fakeLeaseRenewResult{
			{
				lease: renewed,
			},
		}
	store.renewCalls =
		make(chan struct{}, 1)

	crawler := sourceCrawlerFunc(
		func(
			ctx context.Context,
			crawled origin.Origin,
		) (CrawlResult, error) {
			if crawled != source {
				return CrawlResult{},
					errors.New(
						"unexpected crawl source",
					)
			}

			select {
			case <-store.renewCalls:
				return CrawlResult{
					Source:         source,
					PagesAttempted: 1,
					PagesParsed:    1,
				}, nil

			case <-ctx.Done():
				return CrawlResult{},
					ctx.Err()

			case <-time.After(
				2 * time.Second,
			):
				return CrawlResult{},
					errors.New(
						"timed out waiting for lease renewal",
					)
			}
		},
	)

	config := testCrawlRunnerConfig()
	config.LeaseDuration =
		20 * time.Millisecond

	runner, err := NewCrawlRunner(
		store,
		crawler,
		config,
	)
	if err != nil {
		t.Fatal(err)
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
		report.PagesParsed != 1 {
		t.Fatalf(
			"RunOnce() report = %#v",
			report,
		)
	}

	if len(store.renewLeases) != 1 ||
		store.renewLeases[0] != lease {
		t.Fatalf(
			"renew leases = %#v, want [%#v]",
			store.renewLeases,
			lease,
		)
	}

	if !reflect.DeepEqual(
		store.renewDurations,
		[]time.Duration{
			config.LeaseDuration,
		},
	) {
		t.Fatalf(
			"renew durations = %#v",
			store.renewDurations,
		)
	}

	if len(store.completions) != 1 ||
		store.completions[0].lease !=
			renewed {
		t.Fatalf(
			"completion lease = %#v, want %#v",
			store.completions,
			renewed,
		)
	}
}

func TestCrawlRunnerLeaseRenewalFailureCancelsCrawl(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	lease := testCrawlSourceLease(
		source,
	)
	renewErr := errors.New(
		"test discovery lease renewal failure",
	)

	store :=
		newFakeLeaseCrawlSourceStore()
	store.leaseClaims =
		[]fakeLeaseCrawlSourceClaim{
			{
				lease: lease,
				found: true,
			},
		}
	store.renewResults =
		[]fakeLeaseRenewResult{
			{
				err: renewErr,
			},
		}

	crawler := sourceCrawlerFunc(
		func(
			ctx context.Context,
			_ origin.Origin,
		) (CrawlResult, error) {
			<-ctx.Done()

			return CrawlResult{},
				ctx.Err()
		},
	)

	config := testCrawlRunnerConfig()
	config.LeaseDuration =
		20 * time.Millisecond

	runner, err := NewCrawlRunner(
		store,
		crawler,
		config,
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = runner.RunOnce(
		context.Background(),
	)

	if !errors.Is(err, renewErr) ||
		!strings.Contains(
			err.Error(),
			"renew crawl source lease",
		) {
		t.Fatalf(
			"RunOnce() error = %v",
			err,
		)
	}

	if len(store.completions) != 0 {
		t.Fatalf(
			"completions = %#v, want none",
			store.completions,
		)
	}
}

func TestCrawlRunnerLeaseParentCancellationDoesNotComplete(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	lease := testCrawlSourceLease(
		source,
	)

	store :=
		newFakeLeaseCrawlSourceStore()
	store.leaseClaims =
		[]fakeLeaseCrawlSourceClaim{
			{
				lease: lease,
				found: true,
			},
		}

	ctx, cancel :=
		context.WithCancel(
			context.Background(),
		)

	crawler := sourceCrawlerFunc(
		func(
			context.Context,
			origin.Origin,
		) (CrawlResult, error) {
			cancel()

			return CrawlResult{},
				errors.New(
					"secondary crawl failure",
				)
		},
	)

	config := testCrawlRunnerConfig()
	config.LeaseDuration = time.Hour

	runner, err := NewCrawlRunner(
		store,
		crawler,
		config,
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = runner.RunOnce(ctx)

	if !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf(
			"RunOnce() error = %v, want context.Canceled",
			err,
		)
	}

	if len(store.completions) != 0 {
		t.Fatalf(
			"completions = %#v, want none",
			store.completions,
		)
	}
}

func TestCrawlRunnerLeaseRenewalIgnoresShutdownError(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	lease := testCrawlSourceLease(
		source,
	)

	store :=
		newFakeLeaseCrawlSourceStore()

	ctx, cancel :=
		context.WithCancel(
			context.Background(),
		)
	defer cancel()

	store.renewFunc = func(
		context.Context,
		CrawlSourceLease,
		time.Duration,
	) (CrawlSourceLease, error) {
		cancel()

		return CrawlSourceLease{},
			context.Canceled
	}

	results := make(
		chan crawlLeaseRenewalResult,
		1,
	)

	runner := &CrawlRunner{}

	go runner.renewCrawlSourceLease(
		ctx,
		func() {},
		store,
		lease,
		time.Nanosecond,
		results,
	)

	select {
	case result := <-results:
		if result.err != nil {
			t.Fatalf(
				"renewal result error = %v, want nil",
				result.err,
			)
		}

		if result.lease != lease {
			t.Fatalf(
				"renewal result lease = %#v, want %#v",
				result.lease,
				lease,
			)
		}

	case <-time.After(
		2 * time.Second,
	):
		t.Fatal(
			"timed out waiting for renewal shutdown",
		)
	}
}

func TestCrawlRunnerLeaseTiming(
	t *testing.T,
) {
	config := CrawlRunnerConfig{}

	if got := config.leaseDuration(); got !=
		defaultCrawlSourceLeaseDuration {
		t.Fatalf(
			"leaseDuration() = %v, want %v",
			got,
			defaultCrawlSourceLeaseDuration,
		)
	}

	config.LeaseDuration =
		17 * time.Minute

	if got := config.leaseDuration(); got != config.LeaseDuration {
		t.Fatalf(
			"leaseDuration() = %v, want %v",
			got,
			config.LeaseDuration,
		)
	}

	if got :=
		discoveryLeaseRenewInterval(
			10 * time.Minute,
		); got != 5*time.Minute {
		t.Fatalf(
			"discoveryLeaseRenewInterval() = %v, want 5m",
			got,
		)
	}

	if got :=
		discoveryLeaseRenewInterval(
			time.Nanosecond,
		); got != time.Nanosecond {
		t.Fatalf(
			"discoveryLeaseRenewInterval(1ns) = %v, want 1ns",
			got,
		)
	}

	valid := testCrawlRunnerConfig()
	valid.LeaseDuration = time.Minute

	if !validCrawlRunnerConfig(valid) {
		t.Fatal(
			"validCrawlRunnerConfig() = false for positive lease duration",
		)
	}

	valid.LeaseDuration = -time.Second

	if validCrawlRunnerConfig(valid) {
		t.Fatal(
			"validCrawlRunnerConfig() = true for negative lease duration",
		)
	}
}

func testCrawlSourceLease(
	source origin.Origin,
) CrawlSourceLease {
	claimedAt := time.Date(
		2026,
		time.September,
		19,
		22,
		0,
		0,
		0,
		time.UTC,
	)

	return CrawlSourceLease{
		Origin:     source,
		Generation: 7,
		ClaimedAt:  claimedAt,
		ExpiresAt: claimedAt.Add(
			time.Hour,
		),
	}
}

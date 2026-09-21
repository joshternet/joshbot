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

func TestCrawlRunnerProcessesOneSource(t *testing.T) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	config := testCrawlRunnerConfig()

	store := &fakeCrawlSourceStore{
		claims: []fakeCrawlSourceClaim{
			{
				source: source,
				found:  true,
			},
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

	runner, err := NewCrawlRunner(
		store,
		crawler,
		config,
	)
	if err != nil {
		t.Fatalf(
			"NewCrawlRunner() error = %v, want nil",
			err,
		)
	}

	report, err := runner.RunOnce(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"RunOnce() error = %v, want nil",
			err,
		)
	}

	want := CrawlReport{
		Worked:               true,
		Source:               source,
		PagesAttempted:       4,
		PagesParsed:          3,
		CandidatesDiscovered: 7,
		BudgetExhausted:      true,
	}
	if report != want {
		t.Errorf(
			"RunOnce() report = %#v, want %#v",
			report,
			want,
		)
	}

	if !reflect.DeepEqual(
		store.intervals,
		[]time.Duration{
			config.DiscoveryInterval,
		},
	) {
		t.Errorf(
			"claim intervals = %#v, want [%v]",
			store.intervals,
			config.DiscoveryInterval,
		)
	}

	if !reflect.DeepEqual(
		crawler.sources,
		[]origin.Origin{source},
	) {
		t.Errorf(
			"crawler sources = %#v, want [%v]",
			crawler.sources,
			source,
		)
	}
}

func TestCrawlRunnerCompletesDurableRetryState(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	crawlError := errors.New("crawl failed")
	for _, test := range []struct {
		name          string
		crawlError    error
		completeError error
		wantCategory  retry.Category
	}{
		{name: "success", wantCategory: retry.CategoryNone},
		{name: "crawl failure", crawlError: crawlError, wantCategory: retry.CategoryProcessor},
		{name: "completion failure", completeError: errors.New("complete failed"), wantCategory: retry.CategoryNone},
		{
			name:          "failed crawl completion failure",
			crawlError:    crawlError,
			completeError: errors.New("complete failed"),
			wantCategory:  retry.CategoryProcessor,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := &fakeCrawlSourceStore{claims: []fakeCrawlSourceClaim{{
				source: source, found: true,
			}}}
			store := &retryingCrawlSourceStore{
				fakeCrawlSourceStore: base,
				completeError:        test.completeError,
			}
			crawler := &fakeSourceCrawler{results: []fakeSourceCrawlResult{{
				result: CrawlResult{Source: source}, err: test.crawlError,
			}}}
			runner, err := NewCrawlRunner(store, crawler, testCrawlRunnerConfig())
			if err != nil {
				t.Fatal(err)
			}
			_, err = runner.RunOnce(context.Background())
			if test.crawlError == nil && test.completeError == nil && err != nil {
				t.Fatalf("RunOnce() error = %v", err)
			}
			if (test.crawlError != nil || test.completeError != nil) && err == nil {
				t.Fatal("RunOnce() error = nil")
			}
			if len(store.categories) != 1 || store.categories[0] != test.wantCategory {
				t.Fatalf("completion categories = %#v", store.categories)
			}
		})
	}
}

func TestCrawlRunnerPersistsTypedCrawlResultFailure(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	base := &fakeCrawlSourceStore{claims: []fakeCrawlSourceClaim{{
		source: source, found: true,
	}}}
	store := &retryingCrawlSourceStore{fakeCrawlSourceStore: base}
	crawler := &fakeSourceCrawler{results: []fakeSourceCrawlResult{{
		result: CrawlResult{
			Source:          source,
			PagesAttempted:  1,
			FailureCategory: retry.CategoryHTTP429,
			RetryAfter:      time.Hour,
		},
	}}}
	runner, err := NewCrawlRunner(store, crawler, testCrawlRunnerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.categories) != 1 ||
		store.categories[0] != retry.CategoryHTTP429 ||
		len(store.retryAfter) != 1 ||
		store.retryAfter[0] != time.Hour {
		t.Fatalf("completion = %#v, %#v", store.categories, store.retryAfter)
	}
}

func TestCrawlRunnerDoesNotPersistTransientFailureAfterPartialSuccess(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	base := &fakeCrawlSourceStore{claims: []fakeCrawlSourceClaim{{
		source: source, found: true,
	}}}
	store := &retryingCrawlSourceStore{fakeCrawlSourceStore: base}
	crawler := &fakeSourceCrawler{results: []fakeSourceCrawlResult{{
		result: CrawlResult{
			Source:          source,
			PagesAttempted:  2,
			PagesParsed:     1,
			FailureCategory: retry.CategoryHTTP5xx,
		},
		err: errors.New("late persistence failure"),
	}}}
	runner, err := NewCrawlRunner(store, crawler, testCrawlRunnerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce() error = nil")
	}
	if len(store.categories) != 1 || store.categories[0] != retry.CategoryNone {
		t.Fatalf("completion categories = %#v", store.categories)
	}
}

func TestCrawlRunnerContinuesAfterProcessorFailure(t *testing.T) {
	stopError := errors.New("stop")
	waiter := &sequenceCrawlRunnerWaiter{errors: []error{nil, stopError}}
	runner, err := newCrawlRunner(
		&fakeCrawlSourceStore{claims: []fakeCrawlSourceClaim{{
			err: errors.New("temporary store failure"),
		}}},
		&fakeSourceCrawler{},
		testCrawlRunnerConfig(),
		waiter,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background()); !errors.Is(err, stopError) {
		t.Fatalf("Run() error = %v", err)
	}
	want := []time.Duration{
		testCrawlRunnerConfig().PollInterval,
		testCrawlRunnerConfig().PollInterval,
	}
	if !reflect.DeepEqual(waiter.durations, want) {
		t.Fatalf("wait durations = %v, want %v", waiter.durations, want)
	}
}

func TestCrawlRunnerClaimedFailureDoesNotParkUnrelatedSource(t *testing.T) {
	first := mustDiscoveryOrigin(t, "https://first.example")
	second := mustDiscoveryOrigin(t, "https://second.example")
	stopError := errors.New("stop")

	store := &fakeCrawlSourceStore{claims: []fakeCrawlSourceClaim{
		{source: first, found: true},
		{source: second, found: true},
	}}
	crawler := &fakeSourceCrawler{
		results: []fakeSourceCrawlResult{
			{err: errors.New("crawl failed after claim")},
			{result: CrawlResult{PagesAttempted: 1, PagesParsed: 1}},
		},
	}
	waiter := &sequenceCrawlRunnerWaiter{errors: []error{stopError}}

	runner, err := newCrawlRunner(
		store,
		crawler,
		testCrawlRunnerConfig(),
		waiter,
	)
	if err != nil {
		t.Fatal(err)
	}

	err = runner.Run(context.Background())
	if !errors.Is(err, stopError) {
		t.Fatalf("Run() error = %v, want stop", err)
	}

	if len(crawler.sources) != 2 {
		t.Fatalf("Crawl() calls = %d, want 2", len(crawler.sources))
	}
	if crawler.sources[0] != first || crawler.sources[1] != second {
		t.Fatalf("Crawl() sources = %#v, want [%v %v]", crawler.sources, first, second)
	}
	for _, delay := range waiter.durations {
		if delay >= 5*time.Minute {
			t.Fatalf(
				"wait durations = %v include durable source schedule",
				waiter.durations,
			)
		}
	}
	if len(waiter.durations) != 1 ||
		waiter.durations[0] != testCrawlRunnerConfig().PollInterval {
		t.Fatalf(
			"wait durations = %v, want single PollInterval after both sources",
			waiter.durations,
		)
	}
}

func TestCrawlRunnerRunReturnsParentCancellationFromFailure(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	ctx, cancel := context.WithCancel(context.Background())
	crawler := sourceCrawlerFunc(func(context.Context, origin.Origin) (CrawlResult, error) {
		cancel()
		return CrawlResult{}, errors.New("canceled")
	})
	runner, err := NewCrawlRunner(
		&fakeCrawlSourceStore{claims: []fakeCrawlSourceClaim{{
			source: source, found: true,
		}}},
		crawler,
		testCrawlRunnerConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want canceled", err)
	}
}

func TestCrawlRunnerDoesNothingWithoutSource(
	t *testing.T,
) {
	store := &fakeCrawlSourceStore{}
	crawler := &fakeSourceCrawler{}

	runner, err := NewCrawlRunner(
		store,
		crawler,
		testCrawlRunnerConfig(),
	)
	if err != nil {
		t.Fatalf(
			"NewCrawlRunner() error = %v, want nil",
			err,
		)
	}

	report, err := runner.RunOnce(
		context.Background(),
	)
	if err != nil {
		t.Fatalf(
			"RunOnce() error = %v, want nil",
			err,
		)
	}

	if report != (CrawlReport{}) {
		t.Errorf(
			"RunOnce() report = %#v, want zero",
			report,
		)
	}

	if len(crawler.sources) != 0 {
		t.Errorf(
			"crawler sources = %#v, want none",
			crawler.sources,
		)
	}
}

func TestCrawlRunnerReturnsClaimFailure(
	t *testing.T,
) {
	claimErr := errors.New(
		"test crawl source claim failure",
	)
	runner, err := NewCrawlRunner(
		&fakeCrawlSourceStore{
			claims: []fakeCrawlSourceClaim{
				{err: claimErr},
			},
		},
		&fakeSourceCrawler{},
		testCrawlRunnerConfig(),
	)
	if err != nil {
		t.Fatalf(
			"NewCrawlRunner() error = %v, want nil",
			err,
		)
	}

	report, err := runner.RunOnce(
		context.Background(),
	)
	if !errors.Is(err, claimErr) {
		t.Errorf(
			"RunOnce() error = %v, want %v",
			err,
			claimErr,
		)
	}

	if !strings.Contains(
		err.Error(),
		"claim crawl source",
	) {
		t.Errorf(
			"RunOnce() error = %v, want claim context",
			err,
		)
	}

	if report != (CrawlReport{}) {
		t.Errorf(
			"RunOnce() report = %#v, want zero",
			report,
		)
	}
}

func TestCrawlRunnerReturnsFatalCrawlFailure(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	crawlErr := errors.New(
		"test candidate persistence failure",
	)

	runner, err := NewCrawlRunner(
		&fakeCrawlSourceStore{
			claims: []fakeCrawlSourceClaim{
				{
					source: source,
					found:  true,
				},
			},
		},
		&fakeSourceCrawler{
			results: []fakeSourceCrawlResult{
				{err: crawlErr},
			},
		},
		testCrawlRunnerConfig(),
	)
	if err != nil {
		t.Fatalf(
			"NewCrawlRunner() error = %v, want nil",
			err,
		)
	}

	report, err := runner.RunOnce(
		context.Background(),
	)
	if !errors.Is(err, crawlErr) {
		t.Errorf(
			"RunOnce() error = %v, want %v",
			err,
			crawlErr,
		)
	}

	if !strings.Contains(
		err.Error(),
		"crawl source",
	) {
		t.Errorf(
			"RunOnce() error = %v, want crawl context",
			err,
		)
	}

	want := CrawlReport{
		Worked: true,
		Source: source,
	}
	if report != want {
		t.Errorf(
			"RunOnce() report = %#v, want %#v",
			report,
			want,
		)
	}
}

func TestCrawlRunnerReturnsParentCancellation(
	t *testing.T,
) {
	source := mustDiscoveryOrigin(
		t,
		"https://example.com",
	)
	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	runner, err := NewCrawlRunner(
		&fakeCrawlSourceStore{
			claims: []fakeCrawlSourceClaim{
				{
					source: source,
					found:  true,
				},
			},
		},
		sourceCrawlerFunc(
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
		),
		testCrawlRunnerConfig(),
	)
	if err != nil {
		t.Fatalf(
			"NewCrawlRunner() error = %v, want nil",
			err,
		)
	}

	report, err := runner.RunOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf(
			"RunOnce() error = %v, want context.Canceled",
			err,
		)
	}

	want := CrawlReport{
		Worked: true,
		Source: source,
	}
	if report != want {
		t.Errorf(
			"RunOnce() report = %#v, want %#v",
			report,
			want,
		)
	}
}

func TestCrawlRunnerProcessesAvailableSourcesBeforeWaiting(
	t *testing.T,
) {
	first := mustDiscoveryOrigin(
		t,
		"https://alpha.example",
	)
	second := mustDiscoveryOrigin(
		t,
		"https://bravo.example",
	)
	waitErr := errors.New(
		"test stop after idle wait",
	)
	config := testCrawlRunnerConfig()

	store := &fakeCrawlSourceStore{
		claims: []fakeCrawlSourceClaim{
			{
				source: first,
				found:  true,
			},
			{
				source: second,
				found:  true,
			},
			{},
		},
	}
	crawler := &fakeSourceCrawler{
		results: []fakeSourceCrawlResult{
			{},
			{},
		},
	}
	waiter := &fakeCrawlRunnerWaiter{
		err: waitErr,
	}

	runner, err := newCrawlRunner(
		store,
		crawler,
		config,
		waiter,
	)
	if err != nil {
		t.Fatalf(
			"newCrawlRunner() error = %v, want nil",
			err,
		)
	}

	err = runner.Run(context.Background())
	if !errors.Is(err, waitErr) {
		t.Errorf(
			"Run() error = %v, want %v",
			err,
			waitErr,
		)
	}

	wantSources := []origin.Origin{
		first,
		second,
	}
	if !reflect.DeepEqual(
		crawler.sources,
		wantSources,
	) {
		t.Errorf(
			"crawler sources = %#v, want %#v",
			crawler.sources,
			wantSources,
		)
	}

	if !reflect.DeepEqual(
		waiter.durations,
		[]time.Duration{
			config.PollInterval,
		},
	) {
		t.Errorf(
			"wait durations = %#v, want [%v]",
			waiter.durations,
			config.PollInterval,
		)
	}
}

func TestCrawlRunnerRunReturnsRunOnceFailure(
	t *testing.T,
) {
	claimErr := errors.New(
		"test claim failure",
	)

	stopErr := errors.New("stop")
	waiter := &fakeCrawlRunnerWaiter{err: stopErr}
	runner, err := newCrawlRunner(
		&fakeCrawlSourceStore{
			claims: []fakeCrawlSourceClaim{
				{err: claimErr},
			},
		},
		&fakeSourceCrawler{},
		testCrawlRunnerConfig(),
		waiter,
	)
	if err != nil {
		t.Fatalf(
			"NewCrawlRunner() error = %v, want nil",
			err,
		)
	}

	err = runner.Run(context.Background())
	if !errors.Is(err, stopErr) {
		t.Errorf(
			"Run() error = %v, want %v",
			err,
			stopErr,
		)
	}
	if !reflect.DeepEqual(
		waiter.durations,
		[]time.Duration{testCrawlRunnerConfig().PollInterval},
	) {
		t.Errorf(
			"retry durations = %v, want [%v]",
			waiter.durations,
			testCrawlRunnerConfig().PollInterval,
		)
	}
}

func TestNewCrawlRunnerValidatesDependenciesAndConfiguration(
	t *testing.T,
) {
	validStore := &fakeCrawlSourceStore{}
	validCrawler := &fakeSourceCrawler{}
	validConfig := testCrawlRunnerConfig()
	validWaiter := &fakeCrawlRunnerWaiter{}

	tests := []struct {
		name    string
		store   CrawlSourceStore
		crawler SourceCrawler
		config  CrawlRunnerConfig
		waiter  crawlRunnerWaiter
		want    error
	}{
		{
			name:    "nil store",
			crawler: validCrawler,
			config:  validConfig,
			waiter:  validWaiter,
			want:    errCrawlSourceStoreUnavailable,
		},
		{
			name:   "nil crawler",
			store:  validStore,
			config: validConfig,
			waiter: validWaiter,
			want:   errSourceCrawlerUnavailable,
		},
		{
			name:    "zero discovery interval",
			store:   validStore,
			crawler: validCrawler,
			config: CrawlRunnerConfig{
				PollInterval: time.Second,
			},
			waiter: validWaiter,
			want:   errInvalidCrawlRunnerConfig,
		},
		{
			name:    "zero poll interval",
			store:   validStore,
			crawler: validCrawler,
			config: CrawlRunnerConfig{
				DiscoveryInterval: time.Hour,
			},
			waiter: validWaiter,
			want:   errInvalidCrawlRunnerConfig,
		},
		{
			name:    "nil waiter",
			store:   validStore,
			crawler: validCrawler,
			config:  validConfig,
			want:    errCrawlRunnerWaiterUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner, err := newCrawlRunner(
				test.store,
				test.crawler,
				test.config,
				test.waiter,
			)
			if !errors.Is(err, test.want) {
				t.Errorf(
					"newCrawlRunner() error = %v, want %v",
					err,
					test.want,
				)
			}

			if runner != nil {
				t.Errorf(
					"newCrawlRunner() runner = %#v, want nil",
					runner,
				)
			}
		})
	}

	runner, err := NewCrawlRunner(
		validStore,
		validCrawler,
		validConfig,
	)
	if err != nil {
		t.Fatalf(
			"NewCrawlRunner() error = %v, want nil",
			err,
		)
	}

	if runner == nil {
		t.Fatal(
			"NewCrawlRunner() runner = nil",
		)
	}
}

func TestCrawlRunnerValidatesStoredState(
	t *testing.T,
) {
	validStore := &fakeCrawlSourceStore{}
	validCrawler := &fakeSourceCrawler{}
	validConfig := testCrawlRunnerConfig()
	validWaiter := &fakeCrawlRunnerWaiter{}

	var nilRunner *CrawlRunner
	if _, err := nilRunner.RunOnce(
		context.Background(),
	); !errors.Is(
		err,
		errCrawlRunnerUnavailable,
	) {
		t.Errorf(
			"nil runner error = %v, want unavailable",
			err,
		)
	}

	runner := &CrawlRunner{
		store:   validStore,
		crawler: validCrawler,
		config:  validConfig,
		waiter:  validWaiter,
	}

	canceled, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if _, err := runner.RunOnce(canceled); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Errorf(
			"canceled context error = %v, want context.Canceled",
			err,
		)
	}

	broken := *runner
	broken.store = nil
	if _, err := broken.RunOnce(
		context.Background(),
	); !errors.Is(
		err,
		errCrawlSourceStoreUnavailable,
	) {
		t.Errorf(
			"nil stored store error = %v",
			err,
		)
	}

	broken = *runner
	broken.crawler = nil
	if _, err := broken.RunOnce(
		context.Background(),
	); !errors.Is(
		err,
		errSourceCrawlerUnavailable,
	) {
		t.Errorf(
			"nil stored crawler error = %v",
			err,
		)
	}

	broken = *runner
	broken.config = CrawlRunnerConfig{}
	if _, err := broken.RunOnce(
		context.Background(),
	); !errors.Is(
		err,
		errInvalidCrawlRunnerConfig,
	) {
		t.Errorf(
			"invalid stored configuration error = %v",
			err,
		)
	}

	broken = *runner
	broken.waiter = nil
	if err := broken.Run(
		context.Background(),
	); !errors.Is(
		err,
		errCrawlRunnerWaiterUnavailable,
	) {
		t.Errorf(
			"nil stored waiter error = %v",
			err,
		)
	}
}

func TestCrawlRunnerConfigurationValidation(
	t *testing.T,
) {
	if !validCrawlRunnerConfig(
		testCrawlRunnerConfig(),
	) {
		t.Error(
			"validCrawlRunnerConfig(valid) = false",
		)
	}

	for _, config := range []CrawlRunnerConfig{
		{},
		{
			DiscoveryInterval: -time.Second,
			PollInterval:      time.Second,
		},
		{
			DiscoveryInterval: time.Second,
			PollInterval:      -time.Second,
		},
	} {
		if validCrawlRunnerConfig(config) {
			t.Errorf(
				"validCrawlRunnerConfig(%#v) = true",
				config,
			)
		}
	}
}

func TestTimerCrawlRunnerWaiter(t *testing.T) {
	waiter := timerCrawlRunnerWaiter{}

	if err := waiter.Wait(
		context.Background(),
		0,
	); err != nil {
		t.Errorf(
			"immediate Wait() error = %v, want nil",
			err,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := waiter.Wait(
		ctx,
		time.Hour,
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Errorf(
			"canceled Wait() error = %v, want context.Canceled",
			err,
		)
	}
}

func TestTimerWaitStrategy(t *testing.T) {
	waiter := timerWaitStrategy{}

	if err := waiter.Wait(
		context.Background(),
		0,
	); err != nil {
		t.Errorf(
			"immediate Wait() error = %v, want nil",
			err,
		)
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := waiter.Wait(
		ctx,
		time.Hour,
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Errorf(
			"canceled Wait() error = %v, want context.Canceled",
			err,
		)
	}
}

type fakeCrawlSourceClaim struct {
	source origin.Origin
	found  bool
	err    error
}

type recordingLifecycleObserver struct {
	states  []string
	sources []origin.Origin
}

func (observer *recordingLifecycleObserver) Observe(
	state string,
	source origin.Origin,
	_ string,
) {
	observer.states = append(observer.states, state)
	observer.sources = append(observer.sources, source)
}

func TestCrawlRunnerObservesClaimedLifecycle(t *testing.T) {
	source := mustDiscoveryOrigin(t, "https://example.com")
	store := &fakeCrawlSourceStore{claims: []fakeCrawlSourceClaim{{
		source: source, found: true,
	}}}
	runner, err := NewCrawlRunner(store, &fakeSourceCrawler{}, CrawlRunnerConfig{
		DiscoveryInterval: time.Minute,
		PollInterval:      time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	observer := &recordingLifecycleObserver{}
	runner.SetLifecycleObserver(observer)
	report, err := runner.RunOnce(context.Background())
	if err != nil || !report.Worked {
		t.Fatalf("RunOnce() = %#v, %v", report, err)
	}
	if len(observer.states) != 2 || observer.states[0] != "running" ||
		observer.states[1] != "idle" ||
		observer.sources[0] != source {
		t.Errorf("observations = %#v %#v", observer.states, observer.sources)
	}
	var missing *CrawlRunner
	missing.SetLifecycleObserver(observer)
}

type pausableCrawlSourceStore struct {
	*fakeCrawlSourceStore
	paused bool
	err    error
}

func (store *pausableCrawlSourceStore) DiscoveryPaused(context.Context) (bool, error) {
	return store.paused, store.err
}

func TestCrawlRunnerObservesPausedAndControlFailure(t *testing.T) {
	for _, test := range []struct {
		name      string
		paused    bool
		err       error
		wantState string
		wantErr   bool
	}{
		{name: "paused", paused: true, wantState: "paused"},
		{name: "idle", wantState: "idle"},
		{name: "control failure", err: errors.New("control unavailable"), wantState: "failed", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			storage := &pausableCrawlSourceStore{
				fakeCrawlSourceStore: &fakeCrawlSourceStore{},
				paused:               test.paused,
				err:                  test.err,
			}
			runner, err := NewCrawlRunner(storage, &fakeSourceCrawler{}, testCrawlRunnerConfig())
			if err != nil {
				t.Fatal(err)
			}
			observer := &recordingLifecycleObserver{}
			runner.SetLifecycleObserver(observer)
			_, runErr := runner.RunOnce(context.Background())
			if (runErr != nil) != test.wantErr {
				t.Errorf("RunOnce() error = %v, wantErr %v", runErr, test.wantErr)
			}
			if len(observer.states) != 1 || observer.states[0] != test.wantState {
				t.Errorf("states = %#v, want %q", observer.states, test.wantState)
			}
		})
	}
}

type fakeCrawlSourceStore struct {
	claims         []fakeCrawlSourceClaim
	intervals      []time.Duration
	leaseDurations []time.Duration
	generation     int64
	categories     []retry.Category
	retryAfter     []time.Duration
	completeError  error
	renewError     error
}

type retryingCrawlSourceStore struct {
	*fakeCrawlSourceStore
	completeError error
	categories    []retry.Category
	retryAfter    []time.Duration
}

func (store *retryingCrawlSourceStore) CompleteDiscoverySourceLeaseRetry(
	_ context.Context,
	_ CrawlSourceLease,
	category retry.Category,
	retryAfter time.Duration,
) error {
	store.categories = append(store.categories, category)
	store.retryAfter = append(store.retryAfter, retryAfter)
	return store.completeError
}

func (store *fakeCrawlSourceStore) ClaimDiscoverySourceLease(
	_ context.Context,
	interval time.Duration,
	leaseDuration time.Duration,
) (CrawlSourceLease, bool, error) {
	store.intervals = append(
		store.intervals,
		interval,
	)
	store.leaseDurations = append(
		store.leaseDurations,
		leaseDuration,
	)

	if len(store.claims) == 0 {
		return CrawlSourceLease{}, false, nil
	}

	claim := store.claims[0]
	store.claims = store.claims[1:]

	if claim.err != nil {
		return CrawlSourceLease{}, false, claim.err
	}

	if !claim.found {
		return CrawlSourceLease{}, false, nil
	}

	store.generation++

	claimedAt := time.Unix(
		1_800_000_000+store.generation,
		0,
	).UTC()

	return CrawlSourceLease{
		Origin:     claim.source,
		Generation: store.generation,
		ClaimedAt:  claimedAt,
		ExpiresAt:  claimedAt.Add(leaseDuration),
	}, true, nil
}

func (store *fakeCrawlSourceStore) RenewDiscoverySourceLease(
	_ context.Context,
	lease CrawlSourceLease,
	leaseDuration time.Duration,
) (CrawlSourceLease, error) {
	if store.renewError != nil {
		return CrawlSourceLease{}, store.renewError
	}

	lease.ExpiresAt = lease.ExpiresAt.Add(
		leaseDuration,
	)

	return lease, nil
}

func (store *fakeCrawlSourceStore) CompleteDiscoverySourceLeaseRetry(
	_ context.Context,
	_ CrawlSourceLease,
	category retry.Category,
	retryAfter time.Duration,
) error {
	store.categories = append(
		store.categories,
		category,
	)
	store.retryAfter = append(
		store.retryAfter,
		retryAfter,
	)

	return store.completeError
}

type fakeSourceCrawlResult struct {
	result CrawlResult
	err    error
}

type fakeSourceCrawler struct {
	results []fakeSourceCrawlResult
	sources []origin.Origin
}

func (crawler *fakeSourceCrawler) Crawl(
	_ context.Context,
	source origin.Origin,
) (CrawlResult, error) {
	crawler.sources = append(
		crawler.sources,
		source,
	)

	if len(crawler.results) == 0 {
		return CrawlResult{
			Source: source,
		}, nil
	}

	result := crawler.results[0]
	crawler.results = crawler.results[1:]

	return result.result, result.err
}

type sourceCrawlerFunc func(
	context.Context,
	origin.Origin,
) (CrawlResult, error)

func (function sourceCrawlerFunc) Crawl(
	ctx context.Context,
	source origin.Origin,
) (CrawlResult, error) {
	return function(ctx, source)
}

type fakeCrawlRunnerWaiter struct {
	durations []time.Duration
	err       error
}

type sequenceCrawlRunnerWaiter struct {
	durations []time.Duration
	errors    []error
}

func (waiter *sequenceCrawlRunnerWaiter) Wait(
	_ context.Context,
	duration time.Duration,
) error {
	waiter.durations = append(waiter.durations, duration)
	err := waiter.errors[0]
	waiter.errors = waiter.errors[1:]
	return err
}

func (waiter *fakeCrawlRunnerWaiter) Wait(
	_ context.Context,
	duration time.Duration,
) error {
	waiter.durations = append(
		waiter.durations,
		duration,
	)

	return waiter.err
}

func testCrawlRunnerConfig() CrawlRunnerConfig {
	return CrawlRunnerConfig{
		DiscoveryInterval: 24 * time.Hour,
		PollInterval:      time.Minute,
	}
}

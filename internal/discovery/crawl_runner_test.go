package discovery

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
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

	err = runner.Run(context.Background())
	if !errors.Is(err, claimErr) {
		t.Errorf(
			"Run() error = %v, want %v",
			err,
			claimErr,
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

	if _, err := runner.RunOnce(nil); !errors.Is(
		err,
		errInvalidContext,
	) {
		t.Errorf(
			"nil context error = %v, want invalid context",
			err,
		)
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

type fakeCrawlSourceClaim struct {
	source origin.Origin
	found  bool
	err    error
}

type fakeCrawlSourceStore struct {
	claims    []fakeCrawlSourceClaim
	intervals []time.Duration
}

func (store *fakeCrawlSourceStore) ClaimDiscoverySource(
	_ context.Context,
	interval time.Duration,
) (origin.Origin, bool, error) {
	store.intervals = append(
		store.intervals,
		interval,
	)

	if len(store.claims) == 0 {
		return origin.Origin{}, false, nil
	}

	claim := store.claims[0]
	store.claims = store.claims[1:]

	return claim.source, claim.found, claim.err
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

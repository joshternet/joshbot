package discovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

type crawlRunnerFailureIntegrationStore struct {
	claimErr error
}

func (store *crawlRunnerFailureIntegrationStore) ClaimDiscoverySourceLease(
	context.Context,
	time.Duration,
	time.Duration,
) (CrawlSourceLease, bool, error) {
	if store.claimErr != nil {
		return CrawlSourceLease{}, false, store.claimErr
	}

	return CrawlSourceLease{}, false, nil
}

func (*crawlRunnerFailureIntegrationStore) RenewDiscoverySourceLease(
	context.Context,
	CrawlSourceLease,
	time.Duration,
) (CrawlSourceLease, error) {
	return CrawlSourceLease{}, nil
}

func (*crawlRunnerFailureIntegrationStore) CompleteDiscoverySourceLeaseRetry(
	context.Context,
	CrawlSourceLease,
	retry.Category,
	time.Duration,
) error {
	return nil
}

type crawlRunnerFailureIntegrationCrawler struct{}

func (crawlRunnerFailureIntegrationCrawler) Crawl(
	context.Context,
	origin.Origin,
) (CrawlResult, error) {
	return CrawlResult{}, nil
}

type crawlRunnerFailureIntegrationWaiter struct {
	err error
}

func (waiter crawlRunnerFailureIntegrationWaiter) Wait(
	context.Context,
	time.Duration,
) error {
	return waiter.err
}

func TestCrawlRunnerFailureIntegrationPrivateValidationBoundaries(
	t *testing.T,
) {
	store := &crawlRunnerFailureIntegrationStore{}
	crawler := crawlRunnerFailureIntegrationCrawler{}
	config := CrawlRunnerConfig{
		DiscoveryInterval: time.Hour,
		PollInterval:      time.Second,
	}
	waiter := crawlRunnerFailureIntegrationWaiter{}

	t.Run(
		"missing injected waiter",
		func(t *testing.T) {
			runner, err := newCrawlRunner(
				store,
				crawler,
				config,
				nil,
			)

			if !errors.Is(
				err,
				errCrawlRunnerWaiterUnavailable,
			) {
				t.Errorf(
					"newCrawlRunner() error = %v, want %v",
					err,
					errCrawlRunnerWaiterUnavailable,
				)
			}

			if runner != nil {
				t.Errorf(
					"newCrawlRunner() runner = %#v, want nil",
					runner,
				)
			}
		},
	)

	runner, err := newCrawlRunner(
		store,
		crawler,
		config,
		waiter,
	)
	if err != nil {
		t.Fatalf(
			"newCrawlRunner() error = %v",
			err,
		)
	}

	tests := []struct {
		name   string
		change func(*CrawlRunner)
		run    func(*CrawlRunner) error
		want   error
	}{
		{
			name: "missing stored store",
			change: func(candidate *CrawlRunner) {
				candidate.store = nil
			},
			run: func(candidate *CrawlRunner) error {
				_, err := candidate.RunOnce(
					context.Background(),
				)

				return err
			},
			want: errCrawlSourceStoreUnavailable,
		},
		{
			name: "missing stored crawler",
			change: func(candidate *CrawlRunner) {
				candidate.crawler = nil
			},
			run: func(candidate *CrawlRunner) error {
				_, err := candidate.RunOnce(
					context.Background(),
				)

				return err
			},
			want: errSourceCrawlerUnavailable,
		},
		{
			name: "invalid stored configuration",
			change: func(candidate *CrawlRunner) {
				candidate.config = CrawlRunnerConfig{}
			},
			run: func(candidate *CrawlRunner) error {
				_, err := candidate.RunOnce(
					context.Background(),
				)

				return err
			},
			want: errInvalidCrawlRunnerConfig,
		},
		{
			name: "missing stored waiter",
			change: func(candidate *CrawlRunner) {
				candidate.waiter = nil
			},
			run: func(candidate *CrawlRunner) error {
				return candidate.Run(
					context.Background(),
				)
			},
			want: errCrawlRunnerWaiterUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				candidate := *runner
				test.change(&candidate)

				if err := test.run(
					&candidate,
				); !errors.Is(err, test.want) {
					t.Errorf(
						"runner error = %v, want %v",
						err,
						test.want,
					)
				}
			},
		)
	}
}

func TestCrawlRunnerFailureIntegrationRunReturnsWaitFailure(
	t *testing.T,
) {
	claimErr := errors.New(
		"integration claim failure",
	)
	waitErr := errors.New(
		"integration waiter failure",
	)

	runner, err := newCrawlRunner(
		&crawlRunnerFailureIntegrationStore{
			claimErr: claimErr,
		},
		crawlRunnerFailureIntegrationCrawler{},
		CrawlRunnerConfig{
			DiscoveryInterval: time.Hour,
			PollInterval:      time.Second,
		},
		crawlRunnerFailureIntegrationWaiter{
			err: waitErr,
		},
	)
	if err != nil {
		t.Fatalf(
			"newCrawlRunner() error = %v",
			err,
		)
	}

	if err := runner.Run(
		context.Background(),
	); !errors.Is(err, waitErr) {
		t.Errorf(
			"Run() error = %v, want %v",
			err,
			waitErr,
		)
	}
}

package discovery

import "testing"

func TestCrawlRunnerLeaseIntegrationCoverage(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "uses discovery source lease",
			run:  TestCrawlRunnerUsesDiscoverySourceLease,
		},
		{
			name: "claim states",
			run:  TestCrawlRunnerLeaseClaimStates,
		},
		{
			name: "crawl failure completion",
			run:  TestCrawlRunnerLeaseCrawlFailureCompletion,
		},
		{
			name: "completion failures",
			run:  TestCrawlRunnerLeaseCompletionFailures,
		},
		{
			name: "renewal uses renewed lease",
			run:  TestCrawlRunnerLeaseRenewalUsesRenewedLease,
		},
		{
			name: "renewal failure cancels crawl",
			run:  TestCrawlRunnerLeaseRenewalFailureCancelsCrawl,
		},
		{
			name: "parent cancellation does not complete",
			run:  TestCrawlRunnerLeaseParentCancellationDoesNotComplete,
		},
		{
			name: "renewal ignores shutdown error",
			run:  TestCrawlRunnerLeaseRenewalIgnoresShutdownError,
		},
		{
			name: "lease timing",
			run:  TestCrawlRunnerLeaseTiming,
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

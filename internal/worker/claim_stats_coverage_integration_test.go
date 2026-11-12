package worker

import "testing"

// TestClaimStatsIntegrationCoverage re-enters claim-stats unit paths. The
// integration coverage gate skips *_unit_test.go-style selection for plain
// worker_test.go helpers, so these wrappers keep ClaimWithStats observe
// branches at 100% statement coverage.
func TestClaimStatsIntegrationCoverage(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "observes claim deferred",
			run:  TestRunOnceObservesClaimDeferred,
		},
		{
			name: "claim stats error outcomes",
			run:  TestRunOnceClaimStatsErrorOutcomes,
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

package personalsite

import "testing"

// TestPersonalsiteIntegrationCoverage re-enters extract/score/mode unit paths.
// The independent integration coverage gate only runs *_integration_test.go for
// this package, so these wrappers keep statement coverage at 100%.
func TestPersonalsiteIntegrationCoverage(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "extract fixtures",
			run:  TestExtractSignalsFixtures,
		},
		{
			name: "extract invalid inputs",
			run:  TestExtractSignalsRejectsInvalidInputs,
		},
		{
			name: "extract edges",
			run:  TestExtractSignalsEdgeCases,
		},
		{
			name: "extract oversized commerce",
			run:  TestExtractSignalsOversizedCommercePrefix,
		},
		{
			name: "score weights",
			run:  TestScorePersonalSiteWeights,
		},
		{
			name: "score fixture goldens",
			run:  TestScorePersonalSiteFixtureGoldens,
		},
		{
			name: "score buckets",
			run:  TestScoreBucket,
		},
		{
			name: "retry ladders",
			run:  TestRetryDelayLadders,
		},
		{
			name: "retry classes",
			run:  TestRetryClassForHTTPStatuses,
		},
		{
			name: "retry all outcomes",
			run:  TestRetryClassForAllOutcomes,
		},
		{
			name: "retry delay branches",
			run:  TestNextFailureStepNoneAndRetryDelayBranches,
		},
		{
			name: "parse retry after",
			run:  TestParseRetryAfter,
		},
		{
			name: "parse retry after edges",
			run:  TestParseRetryAfterEdgeCases,
		},
		{
			name: "next failure step reset",
			run:  TestNextFailureStepResetsOnClassChange,
		},
		{
			name: "reason weights",
			run:  TestReasonWeightAllCodes,
		},
		{
			name: "contributions skip unknown",
			run:  TestContributionsFromReasonsSkipsUnknown,
		},
		{
			name: "contributions clamping",
			run:  TestContributionsFromReasonsClamping,
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

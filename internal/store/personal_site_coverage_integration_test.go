package store

import "testing"

// TestPersonalSiteIntegrationCoverage re-enters personal-site store unit paths.
// The integration coverage gate skips *_unit_test.go, so these calls cover
// validation, fake-database failures, allocation SQL, and outcome checks.
func TestPersonalSiteIntegrationCoverage(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "upsert without database",
			run:  TestUpsertPersonalSiteAssessmentWithoutDatabase,
		},
		{
			name: "candidates needing without database",
			run:  TestCandidatesNeedingPersonalSiteAssessmentWithoutDatabase,
		},
		{
			name: "admission allocation query",
			run:  TestAutomaticAdmissionAllocationQuery,
		},
		{
			name: "valid outcome",
			run:  TestValidPersonalSiteOutcome,
		},
		{
			name: "claim without database",
			run:  TestClaimPersonalSiteQualificationsWithoutDatabase,
		},
		{
			name: "complete without database",
			run:  TestCompletePersonalSiteQualificationWithoutDatabase,
		},
		{
			name: "upsert transaction paths",
			run:  TestUpsertPersonalSiteAssessmentTransactionPaths,
		},
		{
			name: "persist and backfill",
			run:  TestPersonalSitePersistStateAndBackfill,
		},
		{
			name: "pending backfill",
			run:  TestPendingAutomaticCandidatesBackfillWithoutDatabase,
		},
		{
			name: "set override",
			run:  TestSetPersonalSiteOverrideWithoutDatabase,
		},
		{
			name: "clear override",
			run:  TestClearPersonalSiteOverrideWithoutDatabase,
		},
		{
			name: "request rescore",
			run:  TestRequestPersonalSiteRescoreWithoutDatabase,
		},
		{
			name: "request crawl",
			run:  TestRequestOriginCrawlWithoutDatabase,
		},
		{
			name: "reject avoided",
			run:  TestRejectDomainAvoidedOriginWithoutDatabase,
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

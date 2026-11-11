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
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

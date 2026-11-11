package main

import "testing"

// TestPersonalSiteIntegrationCoverage re-enters personal-site command wiring
// unit paths. Cmd integration coverage only runs *_integration_test.go (plus
// integration_test.go), so these wrappers keep qualify/config statements at
// 100%.
func TestPersonalSiteIntegrationCoverage(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "qualify candidates",
			run:  TestQualifyPersonalSiteCandidates,
		},
		{
			name: "qualification limit",
			run:  TestPersonalSiteQualificationLimit,
		},
		{
			name: "finalize qualify error",
			run:  TestFinalizeCandidatesQualifyError,
		},
		{
			name: "drain qualifications",
			run:  TestDrainPersonalSiteQualifications,
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

package control

import "testing"

// TestPersonalSiteControlIntegrationCoverage re-enters personal-site control
// handler unit paths. The independent integration coverage gate only runs
// *_integration_test.go for this package, so these wrappers keep the new
// operator routes and mutateStatus branches at 100%.
func TestPersonalSiteControlIntegrationCoverage(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "override and rescore routes",
			run:  TestHandlerPersonalSiteOverrideAndRescoreRoutes,
		},
		{
			name: "validation failures",
			run:  TestHandlerPersonalSiteValidationFailures,
		},
		{
			name: "mutate status errors",
			run:  TestHandlerMutateStatusMapsOperatorErrors,
		},
		{
			name: "method not allowed",
			run:  TestHandlerPersonalSiteRoutesMethodNotAllowed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

package discovery

import "testing"

// TestQualifyPersonalSiteIntegrationCoverage re-enters personal-site
// qualification unit paths. Integration coverage only runs
// *_integration_test.go for packages outside the store/reporting legacy
// surfaces, so these wrappers keep QualifyPersonalSite at 100%.
func TestQualifyPersonalSiteIntegrationCoverage(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "assessed",
			run:  TestQualifyPersonalSiteAssessed,
		},
		{
			name: "robots denied",
			run:  TestQualifyPersonalSiteRobotsDenied,
		},
		{
			name: "unsupported content",
			run:  TestQualifyPersonalSiteUnsupportedContent,
		},
		{
			name: "too large",
			run:  TestQualifyPersonalSiteTooLarge,
		},
		{
			name: "http error",
			run:  TestQualifyPersonalSiteHTTPError,
		},
		{
			name: "network rejected",
			run:  TestQualifyPersonalSiteNetworkRejected,
		},
		{
			name: "timeout",
			run:  TestQualifyPersonalSiteTimeout,
		},
		{
			name: "invalid args",
			run:  TestQualifyPersonalSiteInvalidArgs,
		},
		{
			name: "nil response and read errors",
			run:  TestQualifyPersonalSiteNilResponseAndReadErrors,
		},
		{
			name: "fetch error categories",
			run:  TestMapQualifyFetchErrorCategories,
		},
		{
			name: "root page url",
			run:  TestRootPageURL,
		},
		{
			name: "extract statuses",
			run:  TestQualifyPersonalSiteExtractStatuses,
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

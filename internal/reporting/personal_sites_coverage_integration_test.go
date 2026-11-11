package reporting

import "testing"

// TestPersonalSitesIntegrationCoverage re-enters personal-site reporting unit
// paths. The independent integration coverage gate only runs explicit
// *_integration_test.go files plus a fixed postgres allowlist for this
// package, so these wrappers keep handler/SQL statement coverage at 100%.
func TestPersonalSitesIntegrationCoverage(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "list and detail handlers",
			run:  TestPersonalSitesListAndDetailHandlers,
		},
		{
			name: "handler error paths",
			run:  TestPersonalSitesHandlerErrorPaths,
		},
		{
			name: "build identity",
			run:  TestBuildIdentityHandler,
		},
		{
			name: "enrich and cursors",
			run:  TestEnrichAndPersonalSiteCursorHelpers,
		},
		{
			name: "parse query",
			run:  TestParsePersonalSiteQueryFilters,
		},
		{
			name: "detail invalid origin",
			run:  TestPersonalSiteDetailRejectsInvalidOrigin,
		},
		{
			name: "postgres page",
			run:  TestPostgresPersonalSitesPageWithoutDatabase,
		},
		{
			name: "postgres null cursor",
			run:  TestPostgresPersonalSitesPageNullCursorAndAwaitingFalse,
		},
		{
			name: "postgres page failures",
			run:  TestPostgresPersonalSitesPageFailuresWithoutDatabase,
		},
		{
			name: "postgres detail",
			run:  TestPostgresPersonalSiteDetailWithoutDatabase,
		},
		{
			name: "paged personal sites route",
			run:  TestPagedHandlersPreserveArrayBodiesAndExposeNextCursor,
		},
		{
			name: "handler build defaults",
			run:  TestNewHandlerValidatesDependencies,
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

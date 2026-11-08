package store

import "testing"

// TestPendingAutomaticCandidatesIntegration re-enters the deferred-probe and
// regex-exclusion unit coverage. The integration gate skips *_unit_test.go
// unless an *_integration_test.go Test calls into those paths.
func TestPendingAutomaticCandidatesIntegration(t *testing.T) {
	t.Run(
		"without database",
		TestPendingAutomaticCandidatesWithoutDatabase,
	)
	t.Run(
		"regex exclude",
		TestRemoveRegexExcludedOriginsWithoutDatabase,
	)
}

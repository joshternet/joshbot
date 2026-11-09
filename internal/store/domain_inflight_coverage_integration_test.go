package store

import "testing"

// TestDomainInflightIntegrationCoverage re-enters the domain-inflight and
// related queue unit paths. The integration coverage gate skips *_unit_test.go,
// so these calls cover acquire/extend/release failures, busy-neighborhood
// claim skips, discovery upsert races, and renew/complete domain-loss mapping.
func TestDomainInflightIntegrationCoverage(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "domain key without neighborhood",
			run:  TestDomainInflightKeyWithoutNeighborhood,
		},
		{
			name: "acquire without database",
			run:  TestAcquireDomainInflightWithoutDatabase,
		},
		{
			name: "extend without database",
			run:  TestExtendDomainInflightWithoutDatabase,
		},
		{
			name: "release without database",
			run:  TestReleaseDomainInflightWithoutDatabase,
		},
		{
			name: "queue claim domain branches",
			run:  TestQueueClaimDomainInflightBranchesWithoutDatabase,
		},
		{
			name: "queue renew domain branches",
			run:  TestQueueRenewDomainInflightBranchesWithoutDatabase,
		},
		{
			name: "discovery claim domain branches",
			run:  TestClaimDiscoveryDomainInflightBranchesWithoutDatabase,
		},
		{
			name: "discovery renew domain branches",
			run:  TestRenewDiscoveryDomainInflightBranchesWithoutDatabase,
		},
		{
			name: "discovery complete domain release failure",
			run:  TestCompleteDiscoveryReleaseDomainFailureWithoutDatabase,
		},
		{
			name: "queue claim without database",
			run:  TestQueueClaimWithoutDatabase,
		},
		{
			name: "queue renew without database",
			run:  TestRenewWithoutDatabase,
		},
		{
			name: "queue reschedule without database",
			run:  TestRescheduleWithoutDatabase,
		},
		{
			name: "queue abandon without database",
			run:  TestAbandonVerificationWithoutDatabase,
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

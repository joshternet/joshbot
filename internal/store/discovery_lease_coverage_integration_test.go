package store

import "testing"

func TestDiscoveryLeaseIntegrationCoverage(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "claim",
			run:  TestClaimDiscoverySourceLeaseWithoutDatabase,
		},
		{
			name: "renew",
			run:  TestRenewDiscoverySourceLeaseWithoutDatabase,
		},
		{
			name: "completion",
			run:  TestCompleteDiscoverySourceLeaseWithoutDatabase,
		},
		{
			name: "validation",
			run:  TestValidDiscoverySourceLease,
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

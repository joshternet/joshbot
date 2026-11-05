package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
)

type fakeDiscoverySourceLeaseBackend struct {
	drainErr error
	claimErr error
	lease    discovery.CrawlSourceLease
	found    bool

	drained bool
	claimed bool
	owner   string
}

func (backend *fakeDiscoverySourceLeaseBackend) SchedulePendingDiscoveryProbes(
	context.Context,
) (int, error) {
	backend.drained = true
	return 0, backend.drainErr
}

func (backend *fakeDiscoverySourceLeaseBackend) ClaimDiscoverySourceLease(
	_ context.Context,
	owner string,
	_ time.Duration,
	_ time.Duration,
) (discovery.CrawlSourceLease, bool, error) {
	backend.claimed = true
	backend.owner = owner
	return backend.lease, backend.found, backend.claimErr
}

func TestOwnedDiscoveryRuntimeStoreClaimDiscoverySourceLeaseDrainsProbes(
	t *testing.T,
) {
	ctx := context.Background()
	source := mustParseOrigin(t, "https://source.example")
	backend := &fakeDiscoverySourceLeaseBackend{
		lease: discovery.CrawlSourceLease{
			Origin: source,
		},
		found: true,
	}
	runtimeStore := &ownedDiscoveryRuntimeStore{
		leaseOwner:   "discovery-1",
		leaseBackend: backend,
	}

	lease, found, err := runtimeStore.ClaimDiscoverySourceLease(
		ctx,
		time.Hour,
		time.Minute,
	)
	if err != nil {
		t.Fatalf(
			"ClaimDiscoverySourceLease() error = %v",
			err,
		)
	}
	if !found || lease.Origin != source {
		t.Fatalf(
			"ClaimDiscoverySourceLease() = %#v, %v",
			lease,
			found,
		)
	}
	if !backend.drained || !backend.claimed {
		t.Fatalf(
			"drain/claim = %v/%v, want true/true",
			backend.drained,
			backend.claimed,
		)
	}
	if backend.owner != "discovery-1" {
		t.Fatalf(
			"claim owner = %q, want discovery-1",
			backend.owner,
		)
	}
}

func TestOwnedDiscoveryRuntimeStoreClaimDiscoverySourceLeaseDrainFailure(
	t *testing.T,
) {
	drainErr := errors.New("test drain failure")
	backend := &fakeDiscoverySourceLeaseBackend{
		drainErr: drainErr,
	}
	runtimeStore := &ownedDiscoveryRuntimeStore{
		leaseOwner:   "discovery-1",
		leaseBackend: backend,
	}

	lease, found, err := runtimeStore.ClaimDiscoverySourceLease(
		context.Background(),
		time.Hour,
		time.Minute,
	)
	if !errors.Is(err, drainErr) || found || lease.Origin.String() != "" {
		t.Fatalf(
			"ClaimDiscoverySourceLease() = %#v, %v, %v",
			lease,
			found,
			err,
		)
	}
	if !backend.drained || backend.claimed {
		t.Fatalf(
			"drain/claim = %v/%v, want true/false",
			backend.drained,
			backend.claimed,
		)
	}
}

func mustParseOrigin(t *testing.T, raw string) origin.Origin {
	t.Helper()
	parsed, err := origin.Parse(raw)
	if err != nil {
		t.Fatalf("origin.Parse(%q) error = %v", raw, err)
	}
	return parsed
}

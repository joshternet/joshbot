package main

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
	"github.com/joshternet/joshbot/internal/store"
)

func TestRuntimeCandidateSinkAdmitsOnlyAllAddressPublicCandidates(t *testing.T) {
	safe := mustRuntimeOrigin(t, "https://safe.example")
	mixed := mustRuntimeOrigin(t, "https://mixed.example")
	unsafe := mustRuntimeOrigin(t, "https://unsafe.example")
	candidateStore := &fakeAutomaticAdmissionStore{
		pending: []discovery.Candidate{
			{Origin: mixed, Kind: discovery.KindLink},
			{Origin: safe, Kind: discovery.KindLink},
			{Origin: unsafe, Kind: discovery.KindLink},
		},
	}
	resolver := admissionResolver{
		addresses: map[string][]netip.Addr{
			safe.Hostname(): {
				netip.MustParseAddr("93.184.216.34"),
				netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946"),
			},
			mixed.Hostname(): {
				netip.MustParseAddr("93.184.216.34"),
				netip.MustParseAddr("127.0.0.1"),
			},
			unsafe.Hostname(): {
				netip.MustParseAddr("10.0.0.1"),
			},
		},
	}
	sink := runtimeCandidateSink{
		store:    candidateStore,
		resolver: resolver,
	}

	if err := sink.FinalizeCandidates(context.Background(), 41); err != nil {
		t.Fatalf("FinalizeCandidates() error = %v", err)
	}
	want := []discovery.Candidate{
		{Origin: safe, Kind: discovery.KindLink},
	}
	if !reflect.DeepEqual(candidateStore.admitted, want) {
		t.Fatalf("admitted candidates = %#v, want %#v", candidateStore.admitted, want)
	}
	if candidateStore.runID != 41 {
		t.Fatalf("admission run ID = %d, want 41", candidateStore.runID)
	}
	if candidateStore.pendingRunID != 41 {
		t.Fatalf("pending run ID = %d, want 41", candidateStore.pendingRunID)
	}
}

func TestRuntimeCandidateSinkPropagatesAdmissionStoreFailures(t *testing.T) {
	testError := errors.New("admission failure")
	tests := []struct {
		name  string
		store *fakeAutomaticAdmissionStore
	}{
		{
			name:  "pending candidates",
			store: &fakeAutomaticAdmissionStore{pendingErr: testError},
		},
		{
			name: "atomic admission",
			store: &fakeAutomaticAdmissionStore{
				pending:  []discovery.Candidate{{Origin: mustRuntimeOrigin(t, "https://safe.example"), Kind: discovery.KindLink}},
				admitErr: testError,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sink := runtimeCandidateSink{
				store: test.store,
				resolver: admissionResolver{addresses: map[string][]netip.Addr{
					"safe.example": {netip.MustParseAddr("93.184.216.34")},
				}},
			}
			if err := sink.FinalizeCandidates(context.Background(), 1); !errors.Is(err, testError) {
				t.Fatalf("FinalizeCandidates() error = %v, want %v", err, testError)
			}
		})
	}
}

func TestRuntimeCandidateSinkWithoutAdmissionStoreIsNoOp(t *testing.T) {
	sink := runtimeCandidateSink{store: &fakeDiscoveryCandidateStore{}}
	if err := sink.FinalizeCandidates(context.Background(), 1); err != nil {
		t.Fatalf("FinalizeCandidates() error = %v, want nil", err)
	}
}

func TestRuntimeCandidateSinkRetriesAndPersistsTypedDNSFailure(t *testing.T) {
	candidate := discovery.Candidate{
		Origin: mustRuntimeOrigin(t, "https://temporary.example"),
		Kind:   discovery.KindLink,
	}
	base := &fakeAutomaticAdmissionStore{pending: []discovery.Candidate{candidate}}
	retryingStore := &retryAutomaticAdmissionStore{fakeAutomaticAdmissionStore: base}
	resolver := &failingAdmissionResolver{err: errors.New("temporary DNS failure")}
	sink := runtimeCandidateSink{store: retryingStore, resolver: resolver}

	if err := sink.FinalizeCandidates(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if resolver.calls != retry.MaxAttemptsPerCycle {
		t.Fatalf("resolver calls = %d, want %d", resolver.calls, retry.MaxAttemptsPerCycle)
	}
	if len(retryingStore.results) != 1 ||
		retryingStore.results[0].FailureCategory != retry.CategoryDNS {
		t.Fatalf("admission results = %#v", retryingStore.results)
	}
}

func TestRuntimeCandidateSinkRecordsDurableRunAttribution(t *testing.T) {
	source := mustRuntimeOrigin(t, "https://source.example")
	candidate := discovery.Candidate{
		Origin: mustRuntimeOrigin(t, "https://candidate.example"),
		Kind:   discovery.KindLink,
	}
	candidateStore := &fakeAutomaticAdmissionStore{}
	sink := runtimeCandidateSink{store: candidateStore}
	if err := sink.RecordCandidatesForRun(
		context.Background(),
		52,
		source,
		[]discovery.Candidate{candidate},
	); err != nil {
		t.Fatal(err)
	}
	if candidateStore.recordRunID != 52 ||
		candidateStore.recordSource != source ||
		!reflect.DeepEqual(candidateStore.recorded, []discovery.Candidate{candidate}) {
		t.Fatalf(
			"recorded attribution = %d %v %#v",
			candidateStore.recordRunID,
			candidateStore.recordSource,
			candidateStore.recorded,
		)
	}
}

type admissionResolver struct {
	addresses map[string][]netip.Addr
}

type failingAdmissionResolver struct {
	calls int
	err   error
}

func (resolver *failingAdmissionResolver) LookupNetIP(
	context.Context,
	string,
	string,
) ([]netip.Addr, error) {
	resolver.calls++
	return nil, resolver.err
}

func (resolver admissionResolver) LookupNetIP(
	_ context.Context,
	_ string,
	host string,
) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), resolver.addresses[host]...), nil
}

type fakeAutomaticAdmissionStore struct {
	pending      []discovery.Candidate
	admitted     []discovery.Candidate
	runID        discovery.CrawlRunID
	pendingRunID discovery.CrawlRunID
	pendingErr   error
	admitErr     error
	recordRunID  discovery.CrawlRunID
	recordSource origin.Origin
	recorded     []discovery.Candidate
}

type retryAutomaticAdmissionStore struct {
	*fakeAutomaticAdmissionStore
	results []store.AutomaticCandidateResult
}

func (retryStore *retryAutomaticAdmissionStore) CompleteAutomaticCandidates(
	_ context.Context,
	runID discovery.CrawlRunID,
	results []store.AutomaticCandidateResult,
) error {
	retryStore.runID = runID
	retryStore.results = append([]store.AutomaticCandidateResult(nil), results...)
	return retryStore.admitErr
}

func (store *fakeAutomaticAdmissionStore) RecordDiscovery(
	context.Context,
	origin.Origin,
	[]discovery.Candidate,
) (discovery.RecordResult, error) {
	return discovery.RecordResult{}, nil
}

func (store *fakeAutomaticAdmissionStore) RecordDiscoveryForRun(
	_ context.Context,
	runID discovery.CrawlRunID,
	source origin.Origin,
	candidates []discovery.Candidate,
) (discovery.RecordResult, error) {
	store.recordRunID = runID
	store.recordSource = source
	store.recorded = append([]discovery.Candidate(nil), candidates...)
	return discovery.RecordResult{Accepted: len(candidates)}, nil
}

func (store *fakeAutomaticAdmissionStore) PendingAutomaticCandidates(
	_ context.Context,
	runID discovery.CrawlRunID,
) ([]discovery.Candidate, error) {
	store.pendingRunID = runID
	return append([]discovery.Candidate(nil), store.pending...), store.pendingErr
}

func (store *fakeAutomaticAdmissionStore) AdmitAutomaticCandidates(
	_ context.Context,
	runID discovery.CrawlRunID,
	candidates []discovery.Candidate,
) error {
	store.runID = runID
	store.admitted = append([]discovery.Candidate(nil), candidates...)
	return store.admitErr
}

func mustRuntimeOrigin(t *testing.T, raw string) origin.Origin {
	t.Helper()
	parsed, err := origin.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

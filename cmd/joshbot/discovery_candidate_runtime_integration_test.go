package main

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/retry"
)

func TestDiscoveryCandidateRuntimeIntegrationRecordingBoundaries(
	t *testing.T,
) {
	source := mustRuntimeOrigin(
		t,
		"https://source.example",
	)
	candidate := discovery.Candidate{
		Origin: mustRuntimeOrigin(
			t,
			"https://candidate.example",
		),
		Kind: discovery.KindLink,
	}
	candidates := []discovery.Candidate{
		candidate,
	}

	t.Run("record candidates", func(t *testing.T) {
		candidateStore :=
			&fakeDiscoveryCandidateStore{
				result: discovery.RecordResult{
					Accepted: 1,
				},
			}

		err := (runtimeCandidateSink{
			store: candidateStore,
		}).RecordCandidates(
			context.Background(),
			source,
			candidates,
		)
		if err != nil {
			t.Fatalf(
				"RecordCandidates() error = %v, want nil",
				err,
			)
		}

		if candidateStore.source != source {
			t.Errorf(
				"recorded source = %v, want %v",
				candidateStore.source,
				source,
			)
		}

		if !reflect.DeepEqual(
			candidateStore.candidates,
			candidates,
		) {
			t.Errorf(
				"recorded candidates = %#v, want %#v",
				candidateStore.candidates,
				candidates,
			)
		}
	})

	t.Run("record failure", func(t *testing.T) {
		recordErr := errors.New(
			"integration record discovery failure",
		)
		candidateStore :=
			&fakeDiscoveryCandidateStore{
				err: recordErr,
			}

		err := (runtimeCandidateSink{
			store: candidateStore,
		}).RecordCandidates(
			context.Background(),
			source,
			candidates,
		)

		if !errors.Is(err, recordErr) {
			t.Errorf(
				"RecordCandidates() error = %v, want %v",
				err,
				recordErr,
			)
		}
	})

	t.Run("run attribution fallback", func(t *testing.T) {
		candidateStore :=
			&fakeDiscoveryCandidateStore{}

		err := (runtimeCandidateSink{
			store: candidateStore,
		}).RecordCandidatesForRun(
			context.Background(),
			41,
			source,
			candidates,
		)
		if err != nil {
			t.Fatalf(
				"RecordCandidatesForRun() fallback error = %v",
				err,
			)
		}

		if candidateStore.source != source ||
			!reflect.DeepEqual(
				candidateStore.candidates,
				candidates,
			) {
			t.Errorf(
				"fallback recording = %v %#v, want %v %#v",
				candidateStore.source,
				candidateStore.candidates,
				source,
				candidates,
			)
		}
	})

	t.Run("durable run attribution", func(t *testing.T) {
		candidateStore :=
			&fakeAutomaticAdmissionStore{}

		err := (runtimeCandidateSink{
			store: candidateStore,
		}).RecordCandidatesForRun(
			context.Background(),
			52,
			source,
			candidates,
		)
		if err != nil {
			t.Fatalf(
				"RecordCandidatesForRun() error = %v",
				err,
			)
		}

		if candidateStore.recordRunID != 52 {
			t.Errorf(
				"record run ID = %d, want 52",
				candidateStore.recordRunID,
			)
		}

		if candidateStore.recordSource != source {
			t.Errorf(
				"record source = %v, want %v",
				candidateStore.recordSource,
				source,
			)
		}

		if !reflect.DeepEqual(
			candidateStore.recorded,
			candidates,
		) {
			t.Errorf(
				"recorded candidates = %#v, want %#v",
				candidateStore.recorded,
				candidates,
			)
		}
	})
}

func TestDiscoveryCandidateRuntimeIntegrationFinalizationBoundaries(
	t *testing.T,
) {
	t.Run("store without automatic admission", func(t *testing.T) {
		sink := runtimeCandidateSink{
			store: &fakeDiscoveryCandidateStore{},
		}

		if err := sink.FinalizeCandidates(
			context.Background(),
			1,
		); err != nil {
			t.Fatalf(
				"FinalizeCandidates() error = %v, want nil",
				err,
			)
		}
	})

	t.Run("pending candidates failure", func(t *testing.T) {
		pendingErr := errors.New(
			"integration pending candidates failure",
		)
		sink := runtimeCandidateSink{
			store: &fakeAutomaticAdmissionStore{
				pendingErr: pendingErr,
			},
		}

		err := sink.FinalizeCandidates(
			context.Background(),
			2,
		)

		if !errors.Is(err, pendingErr) {
			t.Errorf(
				"FinalizeCandidates() error = %v, want %v",
				err,
				pendingErr,
			)
		}
	})

	t.Run("legacy automatic admission", func(t *testing.T) {
		safe := mustRuntimeOrigin(
			t,
			"https://safe.example",
		)
		mixed := mustRuntimeOrigin(
			t,
			"https://mixed.example",
		)
		unsafe := mustRuntimeOrigin(
			t,
			"https://unsafe.example",
		)

		candidateStore :=
			&fakeAutomaticAdmissionStore{
				pending: []discovery.Candidate{
					{
						Origin: mixed,
						Kind:   discovery.KindLink,
					},
					{
						Origin: safe,
						Kind:   discovery.KindLink,
					},
					{
						Origin: unsafe,
						Kind:   discovery.KindLink,
					},
				},
			}

		resolver := admissionResolver{
			addresses: map[string][]netip.Addr{
				safe.Hostname(): {
					netip.MustParseAddr(
						"93.184.216.34",
					),
				},
				mixed.Hostname(): {
					netip.MustParseAddr(
						"93.184.216.34",
					),
					netip.MustParseAddr(
						"127.0.0.1",
					),
				},
				unsafe.Hostname(): {
					netip.MustParseAddr(
						"10.0.0.1",
					),
				},
			},
		}

		err := (runtimeCandidateSink{
			store:    candidateStore,
			resolver: resolver,
		}).FinalizeCandidates(
			context.Background(),
			41,
		)
		if err != nil {
			t.Fatalf(
				"FinalizeCandidates() error = %v",
				err,
			)
		}

		want := []discovery.Candidate{
			{
				Origin: safe,
				Kind:   discovery.KindLink,
			},
		}

		if !reflect.DeepEqual(
			candidateStore.admitted,
			want,
		) {
			t.Errorf(
				"admitted candidates = %#v, want %#v",
				candidateStore.admitted,
				want,
			)
		}

		if candidateStore.pendingRunID != 41 {
			t.Errorf(
				"pending run ID = %d, want 41",
				candidateStore.pendingRunID,
			)
		}

		if candidateStore.runID != 41 {
			t.Errorf(
				"admission run ID = %d, want 41",
				candidateStore.runID,
			)
		}
	})

	t.Run("retry aware completion", func(t *testing.T) {
		candidate := discovery.Candidate{
			Origin: mustRuntimeOrigin(
				t,
				"https://temporary.example",
			),
			Kind: discovery.KindLink,
		}

		base := &fakeAutomaticAdmissionStore{
			pending: []discovery.Candidate{
				candidate,
			},
		}
		candidateStore :=
			&retryAutomaticAdmissionStore{
				fakeAutomaticAdmissionStore: base,
			}
		resolver := &failingAdmissionResolver{
			err: errors.New(
				"temporary DNS failure",
			),
		}

		err := (runtimeCandidateSink{
			store:    candidateStore,
			resolver: resolver,
		}).FinalizeCandidates(
			context.Background(),
			77,
		)
		if err != nil {
			t.Fatalf(
				"FinalizeCandidates() error = %v",
				err,
			)
		}

		if resolver.calls !=
			retry.MaxAttemptsPerCycle {
			t.Errorf(
				"resolver calls = %d, want %d",
				resolver.calls,
				retry.MaxAttemptsPerCycle,
			)
		}

		if candidateStore.runID != 77 {
			t.Errorf(
				"completion run ID = %d, want 77",
				candidateStore.runID,
			)
		}

		if len(candidateStore.results) != 1 {
			t.Fatalf(
				"completion results = %#v, want one result",
				candidateStore.results,
			)
		}

		if candidateStore.results[0].FailureCategory !=
			retry.CategoryDNS {
			t.Errorf(
				"failure category = %v, want %v",
				candidateStore.results[0].FailureCategory,
				retry.CategoryDNS,
			)
		}
	})
}

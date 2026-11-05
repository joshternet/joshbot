package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/discovery"
)

func TestSchedulePendingDiscoveryProbesWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := time.Date(
		2026,
		time.November,
		4,
		21,
		0,
		0,
		0,
		time.UTC,
	)

	t.Run("unavailable store", func(t *testing.T) {
		var store *DiscoveryStore
		scheduled, err := store.SchedulePendingDiscoveryProbes(ctx)
		if scheduled != 0 || !errors.Is(err, errDiscoveryStoreUnavailable) {
			t.Fatalf(
				"SchedulePendingDiscoveryProbes() = %d, %v",
				scheduled,
				err,
			)
		}
	})

	t.Run("query failure", func(t *testing.T) {
		testErr := errors.New("test pending probe query failure")
		database := &discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{err: testErr},
				},
			},
		}
		store := discoveryPolicyStore(database)
		scheduled, err := store.SchedulePendingDiscoveryProbes(ctx)
		if scheduled != 0 ||
			!errors.Is(err, testErr) ||
			!strings.Contains(err.Error(), "list pending discovery probes") {
			t.Fatalf(
				"SchedulePendingDiscoveryProbes() = %d, %v",
				scheduled,
				err,
			)
		}
	})

	t.Run("scan failure", func(t *testing.T) {
		testErr := errors.New("test pending probe scan failure")
		database := &discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{err: testErr},
							},
						},
					},
				},
			},
		}
		store := discoveryPolicyStore(database)
		scheduled, err := store.SchedulePendingDiscoveryProbes(ctx)
		if scheduled != 0 ||
			!errors.Is(err, testErr) ||
			!strings.Contains(err.Error(), "scan pending discovery probe") {
			t.Fatalf(
				"SchedulePendingDiscoveryProbes() = %d, %v",
				scheduled,
				err,
			)
		}
	})

	t.Run("invalid origin", func(t *testing.T) {
		database := &discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{values: []any{"not a url"}},
							},
						},
					},
				},
			},
		}
		store := discoveryPolicyStore(database)
		scheduled, err := store.SchedulePendingDiscoveryProbes(ctx)
		if scheduled != 0 ||
			err == nil ||
			!strings.Contains(err.Error(), "pending discovery probe origin") {
			t.Fatalf(
				"SchedulePendingDiscoveryProbes() = %d, %v",
				scheduled,
				err,
			)
		}
	})

	t.Run("rows iteration failure", func(t *testing.T) {
		testErr := errors.New("test pending probe rows failure")
		database := &discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{
							err: testErr,
						},
					},
				},
			},
		}
		store := discoveryPolicyStore(database)
		scheduled, err := store.SchedulePendingDiscoveryProbes(ctx)
		if scheduled != 0 ||
			!errors.Is(err, testErr) ||
			!strings.Contains(err.Error(), "iterate pending discovery probes") {
			t.Fatalf(
				"SchedulePendingDiscoveryProbes() = %d, %v",
				scheduled,
				err,
			)
		}
	})

	t.Run("no pending candidates", func(t *testing.T) {
		database := &discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{rows: &discoveryUnitRows{}},
				},
			},
		}
		store := discoveryPolicyStore(database)
		scheduled, err := store.SchedulePendingDiscoveryProbes(ctx)
		if scheduled != 0 || err != nil {
			t.Fatalf(
				"SchedulePendingDiscoveryProbes() = %d, %v",
				scheduled,
				err,
			)
		}
	})

	t.Run("transaction clock failure", func(t *testing.T) {
		clockErr := errors.New("test probe drain clock failure")
		tx := &discoveryPolicyUnitTx{}
		database := &discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{
									values: []any{
										"https://friend.example",
									},
								},
							},
						},
					},
					{rows: &discoveryUnitRows{}},
				},
			},
			beginTx: tx,
		}
		store := &DiscoveryStore{
			pool:  database,
			clock: claimDiscoveryClock{err: clockErr},
		}
		scheduled, err := store.SchedulePendingDiscoveryProbes(ctx)
		if scheduled != 0 ||
			!errors.Is(err, clockErr) ||
			!strings.Contains(err.Error(), "read discovery probe drain clock") {
			t.Fatalf(
				"SchedulePendingDiscoveryProbes() = %d, %v",
				scheduled,
				err,
			)
		}
	})

	t.Run("schedules pending probes", func(t *testing.T) {
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{
				{tag: pgconn.NewCommandTag("SELECT 1")},
				{tag: pgconn.NewCommandTag("INSERT 0 1")},
			},
		}
		database := &discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{
									values: []any{
										"https://friend.example",
									},
								},
							},
						},
					},
					{rows: &discoveryUnitRows{}},
				},
			},
			beginTx: tx,
		}
		store := &DiscoveryStore{
			pool:  database,
			clock: claimDiscoveryClock{now: now},
		}
		scheduled, err := store.SchedulePendingDiscoveryProbes(ctx)
		if err != nil {
			t.Fatalf(
				"SchedulePendingDiscoveryProbes() error = %v",
				err,
			)
		}
		if scheduled != 1 {
			t.Fatalf(
				"SchedulePendingDiscoveryProbes() scheduled = %d, want 1",
				scheduled,
			)
		}
	})

	t.Run("excluded pending candidates", func(t *testing.T) {
		database := &discoveryPolicyUnitDatabase{
			discoveryUnitDatabase: &discoveryUnitDatabase{
				queryResults: []discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{
							rows: []discoveryUnitRow{
								{
									values: []any{
										"https://writer.blogspot.com",
									},
								},
							},
						},
					},
					{rows: &discoveryUnitRows{}},
				},
			},
		}
		store := discoveryPolicyStore(database)
		store.automatic.ExcludedHostSuffixes = "blogspot.com"
		scheduled, err := store.SchedulePendingDiscoveryProbes(ctx)
		if scheduled != 0 || err != nil {
			t.Fatalf(
				"SchedulePendingDiscoveryProbes() = %d, %v",
				scheduled,
				err,
			)
		}
	})
}

func TestScheduleProbeCandidatesCountedUsesProvidedAvailability(
	t *testing.T,
) {
	ctx := context.Background()
	availableAt := time.Date(
		2026,
		time.November,
		4,
		22,
		0,
		0,
		0,
		time.UTC,
	)
	tx := &discoveryPolicyUnitTx{
		execResults: []discoveryUnitExecResult{
			{tag: pgconn.NewCommandTag("SELECT 1")},
			{tag: pgconn.NewCommandTag("INSERT 0 1")},
		},
	}
	database := discoveryPolicyDatabase(tx)
	database.queryResults = []discoveryUnitQueryResult{
		{rows: &discoveryUnitRows{}},
	}
	store := discoveryPolicyStore(database)

	scheduled, err := store.scheduleProbeCandidatesCounted(
		ctx,
		[]discovery.Candidate{{
			Origin: mustStoreOrigin(t, "https://friend.example"),
			Kind:   discovery.KindLink,
		}},
		availableAt,
		false,
	)
	if err != nil {
		t.Fatalf(
			"scheduleProbeCandidatesCounted() error = %v",
			err,
		)
	}
	if scheduled != 1 {
		t.Fatalf(
			"scheduleProbeCandidatesCounted() scheduled = %d, want 1",
			scheduled,
		)
	}
}

// Integration coverage selection matches files that mention newStoreTestPool.

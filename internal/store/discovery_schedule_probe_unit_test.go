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

func TestScheduleProbeCandidatesWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	availableAt := time.Date(
		2026,
		time.September,
		18,
		19,
		0,
		0,
		0,
		time.UTC,
	)

	candidate := discovery.Candidate{
		Origin: mustStoreOrigin(
			t,
			"https://example.com",
		),
		Kind: discovery.KindLink,
	}

	t.Run(
		"exclusion read failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test exclusion read failure",
			)

			database := &discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{
					queryResults: []discoveryUnitQueryResult{
						{
							err: testErr,
						},
					},
				},
			}

			store := discoveryPolicyStore(
				database,
			)

			err := store.scheduleProbeCandidates(
				ctx,
				[]discovery.Candidate{
					candidate,
				},
				availableAt,
			)

			if !errors.Is(
				err,
				testErr,
			) {
				t.Fatalf(
					"scheduleProbeCandidates() error = %v, want %v",
					err,
					testErr,
				)
			}
		},
	)

	t.Run(
		"all candidates excluded",
		func(t *testing.T) {
			database := &discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{
					queryResults: []discoveryUnitQueryResult{
						{
							rows: &discoveryUnitRows{},
						},
					},
				},
			}

			store := discoveryPolicyStore(
				database,
			)
			store.automatic.ExcludedHostSuffixes =
				"example.com"

			err := store.scheduleProbeCandidates(
				ctx,
				[]discovery.Candidate{
					candidate,
				},
				availableAt,
			)
			if err != nil {
				t.Fatalf(
					"scheduleProbeCandidates() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"begin failure",
		func(t *testing.T) {
			database := &discoveryPolicyUnitDatabase{
				discoveryUnitDatabase: &discoveryUnitDatabase{
					queryResults: []discoveryUnitQueryResult{
						{
							rows: &discoveryUnitRows{},
						},
					},
				},
				beginErr: errDiscoveryPolicyBegin,
			}

			store := discoveryPolicyStore(
				database,
			)

			err := store.scheduleProbeCandidates(
				ctx,
				[]discovery.Candidate{
					candidate,
				},
				availableAt,
			)

			if !errors.Is(
				err,
				errDiscoveryPolicyBegin,
			) {
				t.Fatalf(
					"scheduleProbeCandidates() error = %v, want %v",
					err,
					errDiscoveryPolicyBegin,
				)
			}
		},
	)

	t.Run(
		"advisory lock failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test probe admission lock failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						err: testErr,
					},
				},
			}

			database := discoveryPolicyDatabase(
				tx,
			)
			database.queryResults =
				[]discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{},
					},
				}

			store := discoveryPolicyStore(
				database,
			)

			err := store.scheduleProbeCandidates(
				ctx,
				[]discovery.Candidate{
					candidate,
				},
				availableAt,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: lock probe admission",
				) {
				t.Fatalf(
					"scheduleProbeCandidates() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"probe insert failure",
		func(t *testing.T) {
			testErr := errors.New(
				"test probe insert failure",
			)

			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"SELECT 1",
						),
					},
					{
						err: testErr,
					},
				},
			}

			database := discoveryPolicyDatabase(
				tx,
			)
			database.queryResults =
				[]discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{},
					},
				}

			store := discoveryPolicyStore(
				database,
			)

			err := store.scheduleProbeCandidates(
				ctx,
				[]discovery.Candidate{
					candidate,
				},
				availableAt,
			)

			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"store: admit discovery probes",
				) {
				t.Fatalf(
					"scheduleProbeCandidates() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"success",
		func(t *testing.T) {
			tx := &discoveryPolicyUnitTx{
				execResults: []discoveryUnitExecResult{
					{
						tag: pgconn.NewCommandTag(
							"SELECT 1",
						),
					},
					{
						tag: pgconn.NewCommandTag(
							"INSERT 0 1",
						),
					},
				},
			}

			database := discoveryPolicyDatabase(
				tx,
			)
			database.queryResults =
				[]discoveryUnitQueryResult{
					{
						rows: &discoveryUnitRows{},
					},
				}

			store := discoveryPolicyStore(
				database,
			)

			err := store.scheduleProbeCandidates(
				ctx,
				[]discovery.Candidate{
					candidate,
				},
				availableAt,
			)
			if err != nil {
				t.Fatalf(
					"scheduleProbeCandidates() error = %v",
					err,
				)
			}
		},
	)
}

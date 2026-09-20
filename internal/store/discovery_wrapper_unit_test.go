package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
)

func TestRecordDiscoveryWrapperWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	var nilStore *DiscoveryStore

	result, err := nilStore.RecordDiscovery(
		ctx,
		origin.Origin{},
		nil,
	)

	if result != (discovery.RecordResult{}) {
		t.Fatalf(
			"RecordDiscovery() result = %#v, want zero",
			result,
		)
	}

	if !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Fatalf(
			"RecordDiscovery() error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}
}

func TestRecordDiscoveryForRunWrapperWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()

	store := discoveryWrapperStore()

	result, err := store.RecordDiscoveryForRun(
		ctx,
		0,
		mustStoreOrigin(
			t,
			"https://source.example",
		),
		nil,
	)

	if result != (discovery.RecordResult{}) {
		t.Fatalf(
			"RecordDiscoveryForRun(invalid run) result = %#v, want zero",
			result,
		)
	}

	if !errors.Is(
		err,
		errInvalidCrawlRun,
	) {
		t.Fatalf(
			"RecordDiscoveryForRun(invalid run) error = %v, want %v",
			err,
			errInvalidCrawlRun,
		)
	}

	var nilStore *DiscoveryStore

	result, err = nilStore.RecordDiscoveryForRun(
		ctx,
		1,
		mustStoreOrigin(
			t,
			"https://source.example",
		),
		nil,
	)

	if result != (discovery.RecordResult{}) {
		t.Fatalf(
			"RecordDiscoveryForRun() result = %#v, want zero",
			result,
		)
	}

	if !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Fatalf(
			"RecordDiscoveryForRun() error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}
}

func TestRecordDiscoveryPreTransactionPathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	store := discoveryWrapperStore()
	source := mustStoreOrigin(
		t,
		"https://source.example",
	)

	t.Run(
		"empty batch",
		func(t *testing.T) {
			result, err := store.RecordDiscovery(
				ctx,
				source,
				nil,
			)
			if err != nil {
				t.Fatalf(
					"RecordDiscovery() error = %v",
					err,
				)
			}

			if result != (discovery.RecordResult{}) {
				t.Fatalf(
					"RecordDiscovery() result = %#v, want zero",
					result,
				)
			}
		},
	)

	t.Run(
		"empty batch for run",
		func(t *testing.T) {
			result, err := store.RecordDiscoveryForRun(
				ctx,
				1,
				source,
				nil,
			)
			if err != nil {
				t.Fatalf(
					"RecordDiscoveryForRun() error = %v",
					err,
				)
			}

			if result != (discovery.RecordResult{}) {
				t.Fatalf(
					"RecordDiscoveryForRun() result = %#v, want zero",
					result,
				)
			}
		},
	)

	t.Run(
		"invalid source",
		func(t *testing.T) {
			result, err := store.RecordDiscovery(
				ctx,
				origin.Origin{},
				nil,
			)

			if result != (discovery.RecordResult{}) {
				t.Fatalf(
					"RecordDiscovery() result = %#v, want zero",
					result,
				)
			}

			if !errors.Is(
				err,
				errInvalidOrigin,
			) {
				t.Fatalf(
					"RecordDiscovery() error = %v, want %v",
					err,
					errInvalidOrigin,
				)
			}
		},
	)

	t.Run(
		"invalid candidate",
		func(t *testing.T) {
			result, err := store.RecordDiscovery(
				ctx,
				source,
				[]discovery.Candidate{
					{
						Origin: mustStoreOrigin(
							t,
							"https://candidate.example",
						),
						Kind: discovery.Kind(255),
					},
				},
			)

			if result != (discovery.RecordResult{}) {
				t.Fatalf(
					"RecordDiscovery() result = %#v, want zero",
					result,
				)
			}

			if !errors.Is(
				err,
				errInvalidDiscoveryCandidate,
			) {
				t.Fatalf(
					"RecordDiscovery() error = %v, want %v",
					err,
					errInvalidDiscoveryCandidate,
				)
			}
		},
	)

	t.Run(
		"source candidate",
		func(t *testing.T) {
			result, err := store.RecordDiscovery(
				ctx,
				source,
				[]discovery.Candidate{
					{
						Origin: source,
						Kind:   discovery.KindLink,
					},
				},
			)

			if result != (discovery.RecordResult{}) {
				t.Fatalf(
					"RecordDiscovery() result = %#v, want zero",
					result,
				)
			}

			if !errors.Is(
				err,
				errInvalidDiscoveryCandidate,
			) {
				t.Fatalf(
					"RecordDiscovery() error = %v, want %v",
					err,
					errInvalidDiscoveryCandidate,
				)
			}
		},
	)
}

func discoveryWrapperStore() *DiscoveryStore {
	return &DiscoveryStore{
		pool:  new(pgxpool.Pool),
		clock: databaseQueueClock{},
	}
}

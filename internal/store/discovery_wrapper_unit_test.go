package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/origin"
	"github.com/joshternet/joshbot/internal/retry"
)

func TestCompleteDiscoverySourceWrapperWithoutDatabase(
	t *testing.T,
) {
	var store *DiscoveryStore

	err := store.CompleteDiscoverySource(
		context.Background(),
		origin.Origin{},
		retry.CategoryNone,
	)

	if !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Fatalf(
			"CompleteDiscoverySource() error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}
}

func TestCompleteDiscoverySourceRetryValidationWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	source := mustStoreOrigin(
		t,
		"https://example.com",
	)

	var nilStore *DiscoveryStore

	if err := nilStore.CompleteDiscoverySourceRetry(
		ctx,
		source,
		retry.CategoryNone,
		0,
	); !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Fatalf(
			"nil CompleteDiscoverySourceRetry() error = %v, want %v",
			err,
			errDiscoveryStoreUnavailable,
		)
	}

	store := discoveryWrapperStore()

	if err := store.CompleteDiscoverySourceRetry(
		ctx,
		origin.Origin{},
		retry.CategoryNone,
		0,
	); !errors.Is(
		err,
		errInvalidOrigin,
	) {
		t.Fatalf(
			"CompleteDiscoverySourceRetry(zero origin) error = %v, want %v",
			err,
			errInvalidOrigin,
		)
	}

	if err := store.CompleteDiscoverySourceRetry(
		ctx,
		source,
		retry.Category("invalid"),
		0,
	); !errors.Is(
		err,
		errInvalidDiscoveryCandidate,
	) {
		t.Fatalf(
			"CompleteDiscoverySourceRetry(invalid category) error = %v, want %v",
			err,
			errInvalidDiscoveryCandidate,
		)
	}

	if err := store.CompleteDiscoverySourceRetry(
		ctx,
		source,
		retry.CategoryDNS,
		-time.Nanosecond,
	); !errors.Is(
		err,
		errInvalidDiscoveryCandidate,
	) {
		t.Fatalf(
			"CompleteDiscoverySourceRetry(negative retry) error = %v, want %v",
			err,
			errInvalidDiscoveryCandidate,
		)
	}

	if err := store.CompleteDiscoverySourceRetry(
		ctx,
		source,
		retry.CategoryDNS,
		retry.MaxDelay+time.Nanosecond,
	); !errors.Is(
		err,
		errInvalidDiscoveryCandidate,
	) {
		t.Fatalf(
			"CompleteDiscoverySourceRetry(excessive retry) error = %v, want %v",
			err,
			errInvalidDiscoveryCandidate,
		)
	}
}

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

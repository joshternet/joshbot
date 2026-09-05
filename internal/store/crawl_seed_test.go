package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshternet/joshbot/internal/declaration"
	"github.com/joshternet/joshbot/internal/origin"
)

func TestCrawlSeedAddsUnverifiedOriginWithoutSideEffects(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	source := mustStoreOrigin(
		t,
		"https://seed.example",
	)

	crawlStore, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v, want nil",
			err,
		)
	}

	if err := crawlStore.AddCrawlSeed(
		ctx,
		source,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v, want nil",
			err,
		)
	}

	var (
		seeded          bool
		lastAttemptedAt *time.Time
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				seeded,
				last_attempted_at
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		source.String(),
	).Scan(
		&seeded,
		&lastAttemptedAt,
	)
	if err != nil {
		t.Fatalf(
			"query crawl seed: %v",
			err,
		)
	}

	if !seeded {
		t.Error(
			"seeded = false, want true",
		)
	}

	if lastAttemptedAt != nil {
		t.Errorf(
			"last_attempted_at = %v, want nil",
			lastAttemptedAt,
		)
	}

	var (
		originCount      int
		observationCount int
		queueCount       int
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				(
					SELECT count(*)
					FROM origins
					WHERE origin = $1
				),
				(
					SELECT count(*)
					FROM verification_observations
					WHERE origin = $1
				),
				(
					SELECT count(*)
					FROM verification_queue
					WHERE origin = $1
				)
		`,
		source.String(),
	).Scan(
		&originCount,
		&observationCount,
		&queueCount,
	)
	if err != nil {
		t.Fatalf(
			"query crawl-seed side effects: %v",
			err,
		)
	}

	if originCount != 0 {
		t.Errorf(
			"verification origin count = %d, want 0",
			originCount,
		)
	}

	if observationCount != 0 {
		t.Errorf(
			"verification observation count = %d, want 0",
			observationCount,
		)
	}

	if queueCount != 0 {
		t.Errorf(
			"verification queue count = %d, want 0",
			queueCount,
		)
	}

	seeds, err := crawlStore.CrawlSeeds(ctx)
	if err != nil {
		t.Fatalf(
			"CrawlSeeds() error = %v, want nil",
			err,
		)
	}

	want := []origin.Origin{source}
	if !reflect.DeepEqual(seeds, want) {
		t.Errorf(
			"CrawlSeeds() = %#v, want %#v",
			seeds,
			want,
		)
	}
}

func TestCrawlSeedAddIsIdempotentAndListIsSorted(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	first := mustStoreOrigin(
		t,
		"https://a.example",
	)
	second := mustStoreOrigin(
		t,
		"https://z.example",
	)
	unseeded := mustStoreOrigin(
		t,
		"https://unseeded.example",
	)

	crawlStore, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v, want nil",
			err,
		)
	}

	for _, source := range []origin.Origin{
		second,
		first,
		second,
	} {
		if err := crawlStore.AddCrawlSeed(
			ctx,
			source,
		); err != nil {
			t.Fatalf(
				"AddCrawlSeed(%q) error = %v, want nil",
				source,
				err,
			)
		}
	}

	_, err = pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_state (
				source_origin,
				last_attempted_at,
				seeded
			)
			VALUES (
				$1,
				clock_timestamp(),
				false
			)
		`,
		unseeded.String(),
	)
	if err != nil {
		t.Fatalf(
			"insert unseeded source state: %v",
			err,
		)
	}

	seeds, err := crawlStore.CrawlSeeds(ctx)
	if err != nil {
		t.Fatalf(
			"CrawlSeeds() error = %v, want nil",
			err,
		)
	}

	want := []origin.Origin{
		first,
		second,
	}
	if !reflect.DeepEqual(seeds, want) {
		t.Errorf(
			"CrawlSeeds() = %#v, want %#v",
			seeds,
			want,
		)
	}

	var seededCount int
	err = pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM discovery_source_state
			WHERE seeded
		`,
	).Scan(&seededCount)
	if err != nil {
		t.Fatalf(
			"count crawl seeds: %v",
			err,
		)
	}

	if seededCount != 2 {
		t.Errorf(
			"seeded source count = %d, want 2",
			seededCount,
		)
	}
}

func TestCrawlSeedAddPreservesExistingAttempt(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	source := mustStoreOrigin(
		t,
		"https://verified.example",
	)
	attemptedAt := queueTestTime()

	seedDiscoveryTestObservation(
		t,
		pool,
		source,
		attemptedAt.Add(-time.Hour),
		declaration.OutcomeValid,
		declaration.IdentityUndeclared,
	)

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_state (
				source_origin,
				last_attempted_at,
				seeded
			)
			VALUES ($1, $2, false)
		`,
		source.String(),
		attemptedAt,
	)
	if err != nil {
		t.Fatalf(
			"insert existing source state: %v",
			err,
		)
	}

	crawlStore, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v, want nil",
			err,
		)
	}

	if err := crawlStore.AddCrawlSeed(
		ctx,
		source,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v, want nil",
			err,
		)
	}

	var (
		gotAttemptedAt time.Time
		seeded         bool
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				last_attempted_at,
				seeded
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		source.String(),
	).Scan(
		&gotAttemptedAt,
		&seeded,
	)
	if err != nil {
		t.Fatalf(
			"query existing crawl source: %v",
			err,
		)
	}

	if !gotAttemptedAt.Equal(attemptedAt) {
		t.Errorf(
			"last_attempted_at = %v, want %v",
			gotAttemptedAt,
			attemptedAt,
		)
	}

	if !seeded {
		t.Error(
			"seeded = false, want true",
		)
	}
}

func TestCrawlSeedIsClaimableWithoutVerification(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	source := mustStoreOrigin(
		t,
		"https://seed.example",
	)
	claimedAt := queueTestTime()

	crawlStore := newDiscoveryTestStore(
		t,
		pool,
		claimedAt,
	)

	if err := crawlStore.AddCrawlSeed(
		ctx,
		source,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v, want nil",
			err,
		)
	}

	claimed, found, err :=
		crawlStore.ClaimDiscoverySource(
			ctx,
			time.Hour,
		)
	if err != nil {
		t.Fatalf(
			"ClaimDiscoverySource() error = %v, want nil",
			err,
		)
	}

	if !found {
		t.Fatal(
			"ClaimDiscoverySource() found = false, want true",
		)
	}

	if claimed != source {
		t.Errorf(
			"ClaimDiscoverySource() = %q, want %q",
			claimed,
			source,
		)
	}

	var (
		lastAttemptedAt time.Time
		seeded          bool
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				last_attempted_at,
				seeded
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		source.String(),
	).Scan(
		&lastAttemptedAt,
		&seeded,
	)
	if err != nil {
		t.Fatalf(
			"query claimed crawl seed: %v",
			err,
		)
	}

	if !lastAttemptedAt.Equal(claimedAt) {
		t.Errorf(
			"last_attempted_at = %v, want %v",
			lastAttemptedAt,
			claimedAt,
		)
	}

	if !seeded {
		t.Error(
			"seeded = false after claim, want true",
		)
	}
}

func TestCrawlSeedRemoveDisablesUnverifiedSource(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	source := mustStoreOrigin(
		t,
		"https://seed.example",
	)
	now := queueTestTime()

	crawlStore := newDiscoveryTestStore(
		t,
		pool,
		now,
	)

	if err := crawlStore.AddCrawlSeed(
		ctx,
		source,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v, want nil",
			err,
		)
	}

	if err := crawlStore.RemoveCrawlSeed(
		ctx,
		source,
	); err != nil {
		t.Fatalf(
			"RemoveCrawlSeed() error = %v, want nil",
			err,
		)
	}

	var seeded bool
	err := pool.QueryRow(
		ctx,
		`
			SELECT seeded
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		source.String(),
	).Scan(&seeded)
	if err != nil {
		t.Fatalf(
			"query removed crawl seed: %v",
			err,
		)
	}

	if seeded {
		t.Error(
			"seeded = true, want false",
		)
	}

	seeds, err := crawlStore.CrawlSeeds(ctx)
	if err != nil {
		t.Fatalf(
			"CrawlSeeds() error = %v, want nil",
			err,
		)
	}

	if len(seeds) != 0 {
		t.Errorf(
			"CrawlSeeds() = %#v, want empty",
			seeds,
		)
	}

	claimed, found, err :=
		crawlStore.ClaimDiscoverySource(
			ctx,
			time.Hour,
		)
	if err != nil {
		t.Fatalf(
			"ClaimDiscoverySource() error = %v, want nil",
			err,
		)
	}

	if found || claimed != (origin.Origin{}) {
		t.Errorf(
			"ClaimDiscoverySource() = %q, %v, want zero, false",
			claimed,
			found,
		)
	}
}

func TestCrawlSeedRemovalPreservesVerifiedEligibilityAndState(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newCrawlSourceMigrationTestPool(t)
	source := mustStoreOrigin(
		t,
		"https://verified.example",
	)
	candidate := mustStoreOrigin(
		t,
		"https://candidate.example",
	)
	observedAt := queueTestTime()
	claimedAt := observedAt.Add(time.Hour)

	seedDiscoveryTestObservation(
		t,
		pool,
		source,
		observedAt,
		declaration.OutcomeValid,
		declaration.IdentityAffirmed,
	)

	crawlStore := newDiscoveryTestStore(
		t,
		pool,
		claimedAt,
	)

	if err := crawlStore.AddCrawlSeed(
		ctx,
		source,
	); err != nil {
		t.Fatalf(
			"AddCrawlSeed() error = %v, want nil",
			err,
		)
	}

	_, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_candidates (
				origin,
				first_discovered_at,
				last_discovered_at
			)
			VALUES ($1, $2, $2)
		`,
		candidate.String(),
		observedAt,
	)
	if err != nil {
		t.Fatalf(
			"insert discovery candidate: %v",
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		`
			INSERT INTO discovery_edges (
				source_origin,
				candidate_origin,
				kind,
				first_discovered_at,
				last_discovered_at
			)
			VALUES (
				$1,
				$2,
				'link',
				$3,
				$3
			)
		`,
		source.String(),
		candidate.String(),
		observedAt,
	)
	if err != nil {
		t.Fatalf(
			"insert discovery provenance: %v",
			err,
		)
	}

	_, err = pool.Exec(
		ctx,
		`
			INSERT INTO verification_queue (
				origin,
				available_at,
				mode
			)
			VALUES ($1, $2, 'recurring')
		`,
		source.String(),
		observedAt,
	)
	if err != nil {
		t.Fatalf(
			"insert verification queue state: %v",
			err,
		)
	}

	if err := crawlStore.RemoveCrawlSeed(
		ctx,
		source,
	); err != nil {
		t.Fatalf(
			"RemoveCrawlSeed() error = %v, want nil",
			err,
		)
	}

	var (
		observationCount int
		edgeCount        int
		queueCount       int
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				(
					SELECT count(*)
					FROM verification_observations
					WHERE origin = $1
				),
				(
					SELECT count(*)
					FROM discovery_edges
					WHERE source_origin = $1
				),
				(
					SELECT count(*)
					FROM verification_queue
					WHERE origin = $1
				)
		`,
		source.String(),
	).Scan(
		&observationCount,
		&edgeCount,
		&queueCount,
	)
	if err != nil {
		t.Fatalf(
			"query state after seed removal: %v",
			err,
		)
	}

	if observationCount != 1 {
		t.Errorf(
			"observation count = %d, want 1",
			observationCount,
		)
	}

	if edgeCount != 1 {
		t.Errorf(
			"discovery edge count = %d, want 1",
			edgeCount,
		)
	}

	if queueCount != 1 {
		t.Errorf(
			"verification queue count = %d, want 1",
			queueCount,
		)
	}

	seeds, err := crawlStore.CrawlSeeds(ctx)
	if err != nil {
		t.Fatalf(
			"CrawlSeeds() error = %v, want nil",
			err,
		)
	}

	if len(seeds) != 0 {
		t.Errorf(
			"CrawlSeeds() = %#v, want empty",
			seeds,
		)
	}

	claimed, found, err :=
		crawlStore.ClaimDiscoverySource(
			ctx,
			time.Hour,
		)
	if err != nil {
		t.Fatalf(
			"ClaimDiscoverySource() error = %v, want nil",
			err,
		)
	}

	if !found || claimed != source {
		t.Errorf(
			"ClaimDiscoverySource() = %q, %v, want %q, true",
			claimed,
			found,
			source,
		)
	}

	var (
		seeded          bool
		lastAttemptedAt time.Time
	)
	err = pool.QueryRow(
		ctx,
		`
			SELECT
				seeded,
				last_attempted_at
			FROM discovery_source_state
			WHERE source_origin = $1
		`,
		source.String(),
	).Scan(
		&seeded,
		&lastAttemptedAt,
	)
	if err != nil {
		t.Fatalf(
			"query verified source after claim: %v",
			err,
		)
	}

	if seeded {
		t.Error(
			"seeded = true, want false",
		)
	}

	if !lastAttemptedAt.Equal(claimedAt) {
		t.Errorf(
			"last_attempted_at = %v, want %v",
			lastAttemptedAt,
			claimedAt,
		)
	}
}

func TestCrawlSeedOperationsValidateInputs(
	t *testing.T,
) {
	source := mustStoreOrigin(
		t,
		"https://seed.example",
	)

	var nilStore *DiscoveryStore

	if err := nilStore.AddCrawlSeed(
		context.Background(),
		source,
	); !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Errorf(
			"nil AddCrawlSeed() error = %v, want errDiscoveryStoreUnavailable",
			err,
		)
	}

	if err := nilStore.RemoveCrawlSeed(
		context.Background(),
		source,
	); !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Errorf(
			"nil RemoveCrawlSeed() error = %v, want errDiscoveryStoreUnavailable",
			err,
		)
	}

	if _, err := nilStore.CrawlSeeds(
		context.Background(),
	); !errors.Is(
		err,
		errDiscoveryStoreUnavailable,
	) {
		t.Errorf(
			"nil CrawlSeeds() error = %v, want errDiscoveryStoreUnavailable",
			err,
		)
	}

	pool := newCrawlSourceMigrationTestPool(t)
	crawlStore, err := NewDiscoveryStore(pool)
	if err != nil {
		t.Fatalf(
			"NewDiscoveryStore() error = %v, want nil",
			err,
		)
	}

	var nilContext context.Context

	if err := crawlStore.AddCrawlSeed(
		nilContext,
		source,
	); !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"AddCrawlSeed(nil context) error = %v, want errInvalidContext",
			err,
		)
	}

	if err := crawlStore.RemoveCrawlSeed(
		nilContext,
		source,
	); !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"RemoveCrawlSeed(nil context) error = %v, want errInvalidContext",
			err,
		)
	}

	if _, err := crawlStore.CrawlSeeds(
		nilContext,
	); !errors.Is(err, errInvalidContext) {
		t.Errorf(
			"CrawlSeeds(nil context) error = %v, want errInvalidContext",
			err,
		)
	}

	canceledContext, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if err := crawlStore.AddCrawlSeed(
		canceledContext,
		source,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"AddCrawlSeed(canceled context) error = %v, want context.Canceled",
			err,
		)
	}

	if err := crawlStore.RemoveCrawlSeed(
		canceledContext,
		source,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"RemoveCrawlSeed(canceled context) error = %v, want context.Canceled",
			err,
		)
	}

	if _, err := crawlStore.CrawlSeeds(
		canceledContext,
	); !errors.Is(err, context.Canceled) {
		t.Errorf(
			"CrawlSeeds(canceled context) error = %v, want context.Canceled",
			err,
		)
	}

	if err := crawlStore.AddCrawlSeed(
		context.Background(),
		origin.Origin{},
	); !errors.Is(err, errInvalidOrigin) {
		t.Errorf(
			"AddCrawlSeed(zero origin) error = %v, want errInvalidOrigin",
			err,
		)
	}

	if err := crawlStore.RemoveCrawlSeed(
		context.Background(),
		origin.Origin{},
	); !errors.Is(err, errInvalidOrigin) {
		t.Errorf(
			"RemoveCrawlSeed(zero origin) error = %v, want errInvalidOrigin",
			err,
		)
	}
}

func TestCrawlSeedOperationsReturnDatabaseFailures(
	t *testing.T,
) {
	t.Run("unavailable table", func(t *testing.T) {
		ctx := context.Background()
		pool := newCrawlSourceMigrationTestPool(t)
		source := mustStoreOrigin(
			t,
			"https://seed.example",
		)

		crawlStore, err := NewDiscoveryStore(pool)
		if err != nil {
			t.Fatalf(
				"NewDiscoveryStore() error = %v, want nil",
				err,
			)
		}

		_, err = pool.Exec(
			ctx,
			"DROP TABLE discovery_source_state CASCADE",
		)
		if err != nil {
			t.Fatalf(
				"drop discovery source state: %v",
				err,
			)
		}

		if err := crawlStore.AddCrawlSeed(
			ctx,
			source,
		); err == nil {
			t.Error(
				"AddCrawlSeed() error = nil, want non-nil",
			)
		}

		if err := crawlStore.RemoveCrawlSeed(
			ctx,
			source,
		); err == nil {
			t.Error(
				"RemoveCrawlSeed() error = nil, want non-nil",
			)
		}

		if _, err := crawlStore.CrawlSeeds(
			ctx,
		); err == nil {
			t.Error(
				"CrawlSeeds() error = nil, want non-nil",
			)
		}
	})

	t.Run("invalid stored seed", func(t *testing.T) {
		ctx := context.Background()
		pool := newCrawlSourceMigrationTestPool(t)

		_, err := pool.Exec(
			ctx,
			`
				INSERT INTO discovery_source_state (
					source_origin,
					seeded
				)
				VALUES (
					'not-an-origin',
					true
				)
			`,
		)
		if err != nil {
			t.Fatalf(
				"insert invalid stored seed: %v",
				err,
			)
		}

		crawlStore, err := NewDiscoveryStore(pool)
		if err != nil {
			t.Fatalf(
				"NewDiscoveryStore() error = %v, want nil",
				err,
			)
		}

		if _, err := crawlStore.CrawlSeeds(
			ctx,
		); err == nil {
			t.Error(
				"CrawlSeeds() error = nil, want non-nil",
			)
		}
	})
}

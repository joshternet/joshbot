package store

import (
	"context"
	"testing"
	"testing/fstest"
	"time"
)

const participationMetadataMigrationVersion int64 = 21

func TestParticipationMetadataMigrationBackfillsDurableFacts(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	if err := migrate(
		ctx,
		pool,
		participationMetadataMigrationsBefore(t),
	); err != nil {
		t.Fatalf(
			"migrate through version %d: %v",
			participationMetadataMigrationVersion-1,
			err,
		)
	}

	base := time.Date(
		2026,
		time.September,
		20,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	const (
		participatingOrigin = "https://participating.example"
		neverOrigin         = "https://never-participating.example"
		equalOrigin         = "https://equal-timestamps.example"
	)

	for _, item := range []struct {
		origin          string
		firstObservedAt time.Time
	}{
		{
			origin:          participatingOrigin,
			firstObservedAt: base,
		},
		{
			origin:          neverOrigin,
			firstObservedAt: base.Add(time.Minute),
		},
		{
			origin:          equalOrigin,
			firstObservedAt: base.Add(2 * time.Minute),
		},
	} {
		_, err := pool.Exec(
			ctx,
			`
				INSERT INTO origins (
					origin,
					first_observed_at
				)
				VALUES ($1, $2)
			`,
			item.origin,
			item.firstObservedAt,
		)
		if err != nil {
			t.Fatalf(
				"insert origin %q: %v",
				item.origin,
				err,
			)
		}
	}

	insertObservation := func(
		origin string,
		observedAt time.Time,
		outcome string,
		version any,
		identity any,
	) {
		t.Helper()

		_, err := pool.Exec(
			ctx,
			`
				INSERT INTO verification_observations (
					origin,
					observed_at,
					outcome,
					version,
					identity
				)
				VALUES ($1, $2, $3, $4, $5)
			`,
			origin,
			observedAt,
			outcome,
			version,
			identity,
		)
		if err != nil {
			t.Fatalf(
				"insert observation for %q: %v",
				origin,
				err,
			)
		}
	}

	firstParticipatedAt := base.Add(time.Hour)
	latestParticipatingCheckAt := base.Add(
		3 * time.Hour,
	)

	insertObservation(
		participatingOrigin,
		base.Add(30*time.Minute),
		"absent",
		nil,
		nil,
	)
	insertObservation(
		participatingOrigin,
		firstParticipatedAt,
		"valid",
		1,
		"affirmed",
	)
	insertObservation(
		participatingOrigin,
		base.Add(2*time.Hour),
		"valid",
		1,
		"declined",
	)
	insertObservation(
		participatingOrigin,
		latestParticipatingCheckAt,
		"unavailable",
		nil,
		nil,
	)

	latestNeverCheckAt := base.Add(
		4 * time.Hour,
	)

	insertObservation(
		neverOrigin,
		base.Add(90*time.Minute),
		"absent",
		nil,
		nil,
	)
	insertObservation(
		neverOrigin,
		latestNeverCheckAt,
		"unavailable",
		nil,
		nil,
	)

	equalObservedAt := base.Add(
		5 * time.Hour,
	)

	insertObservation(
		equalOrigin,
		equalObservedAt,
		"valid",
		1,
		"affirmed",
	)
	insertObservation(
		equalOrigin,
		equalObservedAt,
		"valid",
		1,
		"declined",
	)
	insertObservation(
		equalOrigin,
		equalObservedAt,
		"absent",
		nil,
		nil,
	)

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf(
			"Migrate() participation metadata error = %v",
			err,
		)
	}

	t.Run(
		"participating origin",
		func(t *testing.T) {
			var (
				gotFirstParticipatedAt *time.Time
				gotInitialVersion      *int
				gotInitialIdentity     *string
				gotLatestCheckAt       *time.Time
				gotLatestOutcome       *string
			)

			err := pool.QueryRow(
				ctx,
				`
					SELECT
						first_participated_at,
						initial_declaration_version,
						initial_declaration_identity,
						latest_declaration_check_at,
						latest_declaration_check_outcome
					FROM origins
					WHERE origin = $1
				`,
				participatingOrigin,
			).Scan(
				&gotFirstParticipatedAt,
				&gotInitialVersion,
				&gotInitialIdentity,
				&gotLatestCheckAt,
				&gotLatestOutcome,
			)
			if err != nil {
				t.Fatalf(
					"query participating metadata: %v",
					err,
				)
			}

			assertParticipationMetadataTime(
				t,
				"first participated at",
				gotFirstParticipatedAt,
				firstParticipatedAt,
			)

			if gotInitialVersion == nil {
				t.Fatal(
					"initial declaration version = nil, want 1",
				)
			}
			if *gotInitialVersion != 1 {
				t.Errorf(
					"initial declaration version = %d, want 1",
					*gotInitialVersion,
				)
			}

			if gotInitialIdentity == nil {
				t.Fatal(
					"initial declaration identity = nil, want affirmed",
				)
			}
			if *gotInitialIdentity != "affirmed" {
				t.Errorf(
					"initial declaration identity = %q, want affirmed",
					*gotInitialIdentity,
				)
			}

			assertParticipationMetadataTime(
				t,
				"latest declaration check at",
				gotLatestCheckAt,
				latestParticipatingCheckAt,
			)

			if gotLatestOutcome == nil {
				t.Fatal(
					"latest declaration check outcome = nil, want unavailable",
				)
			}
			if *gotLatestOutcome != "unavailable" {
				t.Errorf(
					"latest declaration check outcome = %q, want unavailable",
					*gotLatestOutcome,
				)
			}
		},
	)

	t.Run(
		"never participating origin",
		func(t *testing.T) {
			var (
				gotFirstParticipatedAt *time.Time
				gotInitialVersion      *int
				gotInitialIdentity     *string
				gotLatestCheckAt       *time.Time
				gotLatestOutcome       *string
			)

			err := pool.QueryRow(
				ctx,
				`
					SELECT
						first_participated_at,
						initial_declaration_version,
						initial_declaration_identity,
						latest_declaration_check_at,
						latest_declaration_check_outcome
					FROM origins
					WHERE origin = $1
				`,
				neverOrigin,
			).Scan(
				&gotFirstParticipatedAt,
				&gotInitialVersion,
				&gotInitialIdentity,
				&gotLatestCheckAt,
				&gotLatestOutcome,
			)
			if err != nil {
				t.Fatalf(
					"query never-participating metadata: %v",
					err,
				)
			}

			if gotFirstParticipatedAt != nil {
				t.Errorf(
					"first participated at = %v, want nil",
					*gotFirstParticipatedAt,
				)
			}

			if gotInitialVersion != nil {
				t.Errorf(
					"initial declaration version = %d, want nil",
					*gotInitialVersion,
				)
			}

			if gotInitialIdentity != nil {
				t.Errorf(
					"initial declaration identity = %q, want nil",
					*gotInitialIdentity,
				)
			}

			assertParticipationMetadataTime(
				t,
				"latest declaration check at",
				gotLatestCheckAt,
				latestNeverCheckAt,
			)

			if gotLatestOutcome == nil {
				t.Fatal(
					"latest declaration check outcome = nil, want unavailable",
				)
			}
			if *gotLatestOutcome != "unavailable" {
				t.Errorf(
					"latest declaration check outcome = %q, want unavailable",
					*gotLatestOutcome,
				)
			}
		},
	)

	t.Run(
		"equal timestamps use insertion order",
		func(t *testing.T) {
			var (
				gotFirstParticipatedAt *time.Time
				gotInitialVersion      *int
				gotInitialIdentity     *string
				gotLatestCheckAt       *time.Time
				gotLatestOutcome       *string
			)

			err := pool.QueryRow(
				ctx,
				`
					SELECT
						first_participated_at,
						initial_declaration_version,
						initial_declaration_identity,
						latest_declaration_check_at,
						latest_declaration_check_outcome
					FROM origins
					WHERE origin = $1
				`,
				equalOrigin,
			).Scan(
				&gotFirstParticipatedAt,
				&gotInitialVersion,
				&gotInitialIdentity,
				&gotLatestCheckAt,
				&gotLatestOutcome,
			)
			if err != nil {
				t.Fatalf(
					"query equal-timestamp metadata: %v",
					err,
				)
			}

			assertParticipationMetadataTime(
				t,
				"first participated at",
				gotFirstParticipatedAt,
				equalObservedAt,
			)

			if gotInitialVersion == nil {
				t.Fatal(
					"initial declaration version = nil, want 1",
				)
			}
			if *gotInitialVersion != 1 {
				t.Errorf(
					"initial declaration version = %d, want 1",
					*gotInitialVersion,
				)
			}

			if gotInitialIdentity == nil {
				t.Fatal(
					"initial declaration identity = nil, want affirmed",
				)
			}
			if *gotInitialIdentity != "affirmed" {
				t.Errorf(
					"initial declaration identity = %q, want affirmed",
					*gotInitialIdentity,
				)
			}

			assertParticipationMetadataTime(
				t,
				"latest declaration check at",
				gotLatestCheckAt,
				equalObservedAt,
			)

			if gotLatestOutcome == nil {
				t.Fatal(
					"latest declaration check outcome = nil, want absent",
				)
			}
			if *gotLatestOutcome != "absent" {
				t.Errorf(
					"latest declaration check outcome = %q, want absent",
					*gotLatestOutcome,
				)
			}
		},
	)
}

func TestParticipationMetadataMigrationAddsIntegrityConstraints(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf(
			"Migrate() error = %v, want nil",
			err,
		)
	}

	for _, constraint := range []string{
		"origins_initial_participation_check",
		"origins_latest_declaration_check_check",
	} {
		var exists bool

		err := pool.QueryRow(
			ctx,
			`
				SELECT EXISTS (
					SELECT 1
					FROM pg_constraint
					WHERE conrelid = 'origins'::regclass
						AND conname = $1
				)
			`,
			constraint,
		).Scan(&exists)
		if err != nil {
			t.Fatalf(
				"query constraint %q: %v",
				constraint,
				err,
			)
		}

		if !exists {
			t.Errorf(
				"constraint %q does not exist",
				constraint,
			)
		}
	}

	base := time.Date(
		2026,
		time.September,
		20,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	firstParticipatedAt := base.Add(time.Hour)
	latestCheckAt := base.Add(2 * time.Hour)

	tests := []struct {
		name                string
		origin              string
		firstParticipatedAt any
		version             any
		identity            any
		latestCheckAt       any
		latestOutcome       any
	}{
		{
			name:                "initial declaration without participation time",
			origin:              "https://missing-first-time.example",
			firstParticipatedAt: nil,
			version:             1,
			identity:            "affirmed",
			latestCheckAt:       latestCheckAt,
			latestOutcome:       "valid",
		},
		{
			name:                "participation missing initial version",
			origin:              "https://missing-version.example",
			firstParticipatedAt: firstParticipatedAt,
			version:             nil,
			identity:            "affirmed",
			latestCheckAt:       latestCheckAt,
			latestOutcome:       "valid",
		},
		{
			name:                "participation missing initial identity",
			origin:              "https://missing-identity.example",
			firstParticipatedAt: firstParticipatedAt,
			version:             1,
			identity:            nil,
			latestCheckAt:       latestCheckAt,
			latestOutcome:       "valid",
		},
		{
			name:                "unsupported initial version",
			origin:              "https://unsupported-version.example",
			firstParticipatedAt: firstParticipatedAt,
			version:             2,
			identity:            "affirmed",
			latestCheckAt:       latestCheckAt,
			latestOutcome:       "valid",
		},
		{
			name:                "invalid initial identity",
			origin:              "https://invalid-identity.example",
			firstParticipatedAt: firstParticipatedAt,
			version:             1,
			identity:            "maybe",
			latestCheckAt:       latestCheckAt,
			latestOutcome:       "valid",
		},
		{
			name:                "first participation after latest check",
			origin:              "https://reversed-times.example",
			firstParticipatedAt: latestCheckAt,
			version:             1,
			identity:            "affirmed",
			latestCheckAt:       firstParticipatedAt,
			latestOutcome:       "valid",
		},
		{
			name:                "participant without latest check",
			origin:              "https://missing-latest.example",
			firstParticipatedAt: firstParticipatedAt,
			version:             1,
			identity:            "affirmed",
			latestCheckAt:       nil,
			latestOutcome:       nil,
		},
		{
			name:                "latest check time without outcome",
			origin:              "https://missing-outcome.example",
			firstParticipatedAt: nil,
			version:             nil,
			identity:            nil,
			latestCheckAt:       latestCheckAt,
			latestOutcome:       nil,
		},
		{
			name:                "latest check outcome without time",
			origin:              "https://missing-check-time.example",
			firstParticipatedAt: nil,
			version:             nil,
			identity:            nil,
			latestCheckAt:       nil,
			latestOutcome:       "absent",
		},
		{
			name:                "invalid latest outcome",
			origin:              "https://invalid-outcome.example",
			firstParticipatedAt: nil,
			version:             nil,
			identity:            nil,
			latestCheckAt:       latestCheckAt,
			latestOutcome:       "mystery",
		},
		{
			name:                "valid latest check without participation",
			origin:              "https://valid-without-participation.example",
			firstParticipatedAt: nil,
			version:             nil,
			identity:            nil,
			latestCheckAt:       latestCheckAt,
			latestOutcome:       "valid",
		},
	}

	for _, test := range tests {
		t.Run(
			test.name,
			func(t *testing.T) {
				_, err := pool.Exec(
					ctx,
					`
						INSERT INTO origins (
							origin,
							first_observed_at,
							first_participated_at,
							initial_declaration_version,
							initial_declaration_identity,
							latest_declaration_check_at,
							latest_declaration_check_outcome
						)
						VALUES (
							$1,
							$2,
							$3,
							$4,
							$5,
							$6,
							$7
						)
					`,
					test.origin,
					base,
					test.firstParticipatedAt,
					test.version,
					test.identity,
					test.latestCheckAt,
					test.latestOutcome,
				)
				if err == nil {
					t.Error(
						"constraint violation error = nil, want non-nil",
					)
				}
			},
		)
	}
}

func participationMetadataMigrationsBefore(
	t *testing.T,
) fstest.MapFS {
	t.Helper()

	migrations, err := loadMigrations(
		embeddedMigrations,
	)
	if err != nil {
		t.Fatalf(
			"load embedded migrations: %v",
			err,
		)
	}

	source := make(fstest.MapFS)

	for _, migration := range migrations {
		if migration.version >=
			participationMetadataMigrationVersion {
			continue
		}

		data := make(
			[]byte,
			len(migration.data),
		)
		copy(data, migration.data)

		source["migrations/"+migration.name] =
			&fstest.MapFile{
				Data: data,
			}
	}

	if len(source) == 0 {
		t.Fatal(
			"no migrations found before participation metadata migration",
		)
	}

	return source
}

func assertParticipationMetadataTime(
	t *testing.T,
	name string,
	got *time.Time,
	want time.Time,
) {
	t.Helper()

	if got == nil {
		t.Fatalf(
			"%s = nil, want %v",
			name,
			want,
		)
	}

	if !got.Equal(want) {
		t.Errorf(
			"%s = %v, want %v",
			name,
			*got,
			want,
		)
	}
}

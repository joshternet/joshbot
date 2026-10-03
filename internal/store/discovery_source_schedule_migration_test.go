package store

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestDiscoverySourceScheduleMigrationBackfillsActiveWork(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	migrations, err := loadMigrations(
		embeddedMigrations,
	)
	if err != nil {
		t.Fatalf(
			"load migrations: %v",
			err,
		)
	}

	for _, migration := range migrations {
		if migration.version >= 23 {
			continue
		}

		if _, err := pool.Exec(
			ctx,
			string(migration.data),
			pgx.QueryExecModeSimpleProtocol,
		); err != nil {
			t.Fatalf(
				"apply migration %q: %v",
				migration.name,
				err,
			)
		}
	}

	before := time.Date(
		2026,
		time.September,
		29,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	terminalAt := before.Add(2 * time.Hour)
	rediscoveredAt := terminalAt.Add(2 * time.Hour)

	if _, err := pool.Exec(
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
			VALUES
				(
					'https://valid-only.example',
					$1,
					$1,
					1,
					'affirmed',
					$2,
					'valid'
				),
				(
					'https://auto-terminal.example',
					$1,
					NULL,
					NULL,
					NULL,
					$2,
					'absent'
				),
				(
					'https://auto-rediscovered.example',
					$1,
					NULL,
					NULL,
					NULL,
					$2,
					'absent'
				),
				(
					'https://prior-terminal.example',
					$1,
					$1,
					1,
					'affirmed',
					$2,
					'absent'
				),
				(
					'https://prior-rediscovered.example',
					$1,
					$1,
					1,
					'affirmed',
					$2,
					'absent'
				)
		`,
		before,
		terminalAt,
	); err != nil {
		t.Fatalf(
			"insert migration origins: %v",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO verification_observations (
				origin,
				observed_at,
				outcome,
				version,
				identity
			)
			VALUES
				(
					'https://valid-only.example',
					$2,
					'valid',
					1,
					'affirmed'
				),
				(
					'https://auto-terminal.example',
					$2,
					'absent',
					NULL,
					NULL
				),
				(
					'https://auto-rediscovered.example',
					$2,
					'absent',
					NULL,
					NULL
				),
				(
					'https://prior-terminal.example',
					$1,
					'valid',
					1,
					'affirmed'
				),
				(
					'https://prior-terminal.example',
					$2,
					'absent',
					NULL,
					NULL
				),
				(
					'https://prior-rediscovered.example',
					$1,
					'valid',
					1,
					'affirmed'
				),
				(
					'https://prior-rediscovered.example',
					$2,
					'absent',
					NULL,
					NULL
				)
		`,
		before,
		terminalAt,
	); err != nil {
		t.Fatalf(
			"insert migration observations: %v",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_state (
				source_origin,
				seeded,
				automatically_discovered
			)
			VALUES
				(
					'https://seed.example',
					true,
					false
				),
				(
					'https://auto-new.example',
					false,
					true
				),
				(
					'https://auto-terminal.example',
					false,
					true
				),
				(
					'https://auto-rediscovered.example',
					false,
					true
				)
		`,
	); err != nil {
		t.Fatalf(
			"insert migration source state: %v",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_candidates (
				origin,
				first_discovered_at,
				last_discovered_at
			)
			VALUES
				(
					'https://auto-terminal.example',
					$1,
					$1
				),
				(
					'https://auto-rediscovered.example',
					$1,
					$2
				),
				(
					'https://prior-terminal.example',
					$1,
					$1
				),
				(
					'https://prior-rediscovered.example',
					$1,
					$2
				)
		`,
		before,
		rediscoveredAt,
	); err != nil {
		t.Fatalf(
			"insert migration candidates: %v",
			err,
		)
	}

	applyRawStoreMigration(
		t,
		ctx,
		pool,
		"migrations/0023_discovery_source_schedule.sql",
	)

	rows, err := pool.Query(
		ctx,
		`
			SELECT source_origin
			FROM discovery_source_schedule
			ORDER BY source_origin
		`,
	)
	if err != nil {
		t.Fatalf(
			"query migrated schedule: %v",
			err,
		)
	}
	defer rows.Close()

	var scheduled []string
	for rows.Next() {
		var rawOrigin string
		if err := rows.Scan(
			&rawOrigin,
		); err != nil {
			t.Fatalf(
				"scan migrated schedule: %v",
				err,
			)
		}

		scheduled = append(
			scheduled,
			rawOrigin,
		)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf(
			"iterate migrated schedule: %v",
			err,
		)
	}

	want := []string{
		"https://auto-new.example",
		"https://auto-rediscovered.example",
		"https://prior-rediscovered.example",
		"https://seed.example",
		"https://valid-only.example",
	}

	if !slices.Equal(
		scheduled,
		want,
	) {
		t.Fatalf(
			"migrated schedule = %#v, want %#v",
			scheduled,
			want,
		)
	}

	var knownButUnscheduled int
	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM origins AS stored_origin
			WHERE stored_origin.origin IN (
				'https://auto-terminal.example',
				'https://prior-terminal.example'
			)
				AND NOT EXISTS (
					SELECT 1
					FROM discovery_source_schedule AS schedule
					WHERE schedule.source_origin =
						stored_origin.origin
				)
		`,
	).Scan(
		&knownButUnscheduled,
	); err != nil {
		t.Fatalf(
			"count durable unscheduled sources: %v",
			err,
		)
	}

	if knownButUnscheduled != 2 {
		t.Fatalf(
			"known unscheduled source count = %d, want 2",
			knownButUnscheduled,
		)
	}

	var priorParticipantStateCount int
	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM discovery_source_state
			WHERE source_origin =
				'https://prior-rediscovered.example'
		`,
	).Scan(
		&priorParticipantStateCount,
	); err != nil {
		t.Fatalf(
			"count prior participant source state: %v",
			err,
		)
	}

	if priorParticipantStateCount != 0 {
		t.Fatalf(
			"prior participant source state count = %d, want 0",
			priorParticipantStateCount,
		)
	}
}

func TestDiscoverySourceScheduleSchemaContract(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_schedule (
				source_origin
			)
			VALUES ('')
		`,
	); err == nil {
		t.Fatal(
			"empty scheduled origin was accepted",
		)
	}

	const scheduleOnlyOrigin = "https://schedule-only.example"

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_schedule (
				source_origin
			)
			VALUES ($1)
		`,
		scheduleOnlyOrigin,
	); err != nil {
		t.Fatalf(
			"insert schedule-only origin: %v",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_source_schedule (
				source_origin
			)
			VALUES ($1)
		`,
		scheduleOnlyOrigin,
	); err == nil {
		t.Fatal(
			"duplicate scheduled origin was accepted",
		)
	}
}

func TestDiscoverySourceScheduleRuntimePrivileges(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newStoreTestPool(t)

	tests := []struct {
		role     string
		selectOK bool
		insertOK bool
		updateOK bool
		deleteOK bool
	}{
		{
			role:     "joshbot_app",
			selectOK: true,
			insertOK: true,
			updateOK: true,
			deleteOK: true,
		},
		{
			role:     "joshbot_reporter",
			selectOK: true,
		},
	}

	for _, test := range tests {
		t.Run(test.role, func(t *testing.T) {
			var exists bool
			if err := pool.QueryRow(
				ctx,
				`
					SELECT EXISTS (
						SELECT 1
						FROM pg_roles
						WHERE rolname = $1
					)
				`,
				test.role,
			).Scan(
				&exists,
			); err != nil {
				t.Fatalf(
					"query role existence: %v",
					err,
				)
			}

			if !exists {
				t.Skip(
					"runtime role is not present in this test database",
				)
			}

			var (
				selectOK bool
				insertOK bool
				updateOK bool
				deleteOK bool
			)

			if err := pool.QueryRow(
				ctx,
				`
					SELECT
						has_table_privilege(
							$1,
							'discovery_source_schedule',
							'SELECT'
						),
						has_table_privilege(
							$1,
							'discovery_source_schedule',
							'INSERT'
						),
						has_table_privilege(
							$1,
							'discovery_source_schedule',
							'UPDATE'
						),
						has_table_privilege(
							$1,
							'discovery_source_schedule',
							'DELETE'
						)
				`,
				test.role,
			).Scan(
				&selectOK,
				&insertOK,
				&updateOK,
				&deleteOK,
			); err != nil {
				t.Fatalf(
					"query table privileges: %v",
					err,
				)
			}

			if selectOK != test.selectOK ||
				insertOK != test.insertOK ||
				updateOK != test.updateOK ||
				deleteOK != test.deleteOK {
				t.Fatalf(
					"privileges = select:%v insert:%v update:%v delete:%v, want select:%v insert:%v update:%v delete:%v",
					selectOK,
					insertOK,
					updateOK,
					deleteOK,
					test.selectOK,
					test.insertOK,
					test.updateOK,
					test.deleteOK,
				)
			}
		})
	}
}

func TestDiscoverySourceScheduleMigrationDoesNotReactivateParticipantWithoutAuthoritativeObservation(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	migrations, err := loadMigrations(
		embeddedMigrations,
	)
	if err != nil {
		t.Fatalf(
			"load migrations: %v",
			err,
		)
	}

	for _, migration := range migrations {
		if migration.version >= 23 {
			continue
		}

		if _, err := pool.Exec(
			ctx,
			string(migration.data),
			pgx.QueryExecModeSimpleProtocol,
		); err != nil {
			t.Fatalf(
				"apply migration %q: %v",
				migration.name,
				err,
			)
		}
	}

	participatedAt := time.Date(
		2026,
		time.September,
		29,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	rediscoveredAt := participatedAt.Add(
		time.Hour,
	)

	const rawOrigin = "https://participant-no-authoritative.example"

	if _, err := pool.Exec(
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
				$2,
				1,
				'affirmed',
				$2,
				'unavailable'
			)
		`,
		rawOrigin,
		participatedAt,
	); err != nil {
		t.Fatalf(
			"insert former participant: %v",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		`
			INSERT INTO discovery_candidates (
				origin,
				first_discovered_at,
				last_discovered_at
			)
			VALUES (
				$1,
				$2,
				$2
			)
		`,
		rawOrigin,
		rediscoveredAt,
	); err != nil {
		t.Fatalf(
			"insert rediscovery evidence: %v",
			err,
		)
	}

	applyRawStoreMigration(
		t,
		ctx,
		pool,
		"migrations/0023_discovery_source_schedule.sql",
	)

	var scheduled bool
	if err := pool.QueryRow(
		ctx,
		`
			SELECT EXISTS (
				SELECT 1
				FROM discovery_source_schedule
				WHERE source_origin = $1
			)
		`,
		rawOrigin,
	).Scan(
		&scheduled,
	); err != nil {
		t.Fatalf(
			"query migrated schedule: %v",
			err,
		)
	}

	if scheduled {
		t.Fatal(
			"migration scheduled former participant without authoritative observation",
		)
	}
}

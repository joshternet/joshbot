package store

import (
	"context"
	"testing"
	"testing/fstest"
	"time"
)

const historyRetentionMigrationVersion int64 = 22

func TestHistoryRetentionMigrationBackfillsReprobeState(
	t *testing.T,
) {
	ctx := context.Background()
	pool := newEmptyStoreTestPool(t)

	if err := migrate(
		ctx,
		pool,
		historyRetentionMigrationsBefore(t),
	); err != nil {
		t.Fatalf(
			"migrate through version %d: %v",
			historyRetentionMigrationVersion-1,
			err,
		)
	}

	base := time.Date(
		2026,
		time.September,
		28,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	const (
		missedOrigin = "https://missed.example"
		cleanOrigin  = "https://clean.example"
	)

	for _, rawOrigin := range []string{
		missedOrigin,
		cleanOrigin,
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
			rawOrigin,
			base,
		)
		if err != nil {
			t.Fatalf(
				"insert origin %q: %v",
				rawOrigin,
				err,
			)
		}
	}

	insertObservation := func(
		rawOrigin string,
		offset time.Duration,
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
			rawOrigin,
			base.Add(offset),
			outcome,
			version,
			identity,
		)
		if err != nil {
			t.Fatalf(
				"insert %q observation for %q: %v",
				outcome,
				rawOrigin,
				err,
			)
		}
	}

	insertObservation(
		missedOrigin,
		time.Minute,
		"absent",
		nil,
		nil,
	)
	insertObservation(
		missedOrigin,
		2*time.Minute,
		"invalid",
		nil,
		nil,
	)
	insertObservation(
		missedOrigin,
		3*time.Minute,
		"unsupported_version",
		nil,
		nil,
	)
	insertObservation(
		missedOrigin,
		4*time.Minute,
		"robots_denied",
		nil,
		nil,
	)
	insertObservation(
		missedOrigin,
		5*time.Minute,
		"cross_origin_redirect",
		nil,
		nil,
	)
	insertObservation(
		missedOrigin,
		6*time.Minute,
		"valid",
		1,
		"affirmed",
	)
	insertObservation(
		missedOrigin,
		7*time.Minute,
		"unavailable",
		nil,
		nil,
	)

	insertObservation(
		cleanOrigin,
		time.Minute,
		"valid",
		1,
		"declined",
	)
	insertObservation(
		cleanOrigin,
		2*time.Minute,
		"unavailable",
		nil,
		nil,
	)

	if err := Migrate(
		ctx,
		pool,
	); err != nil {
		t.Fatalf(
			"Migrate() history retention error = %v",
			err,
		)
	}

	tests := []struct {
		origin string
		want   int64
	}{
		{
			origin: missedOrigin,
			want:   5,
		},
		{
			origin: cleanOrigin,
			want:   0,
		},
	}

	for _, test := range tests {
		var got int64

		err := pool.QueryRow(
			ctx,
			`
				SELECT miss_count
				FROM verification_reprobe_state
				WHERE origin = $1
			`,
			test.origin,
		).Scan(&got)
		if err != nil {
			t.Fatalf(
				"read reprobe state for %q: %v",
				test.origin,
				err,
			)
		}

		if got != test.want {
			t.Errorf(
				"miss count for %q = %d, want %d",
				test.origin,
				got,
				test.want,
			)
		}
	}

	var stateCount int

	if err := pool.QueryRow(
		ctx,
		`
			SELECT count(*)
			FROM verification_reprobe_state
		`,
	).Scan(&stateCount); err != nil {
		t.Fatalf(
			"count reprobe state rows: %v",
			err,
		)
	}

	if stateCount != 2 {
		t.Errorf(
			"reprobe state rows = %d, want 2",
			stateCount,
		)
	}

	_, err := pool.Exec(
		ctx,
		`
			UPDATE verification_reprobe_state
			SET miss_count = -1
			WHERE origin = $1
		`,
		missedOrigin,
	)
	if err == nil {
		t.Error(
			"negative reprobe miss count error = nil, want non-nil",
		)
	}
}

func historyRetentionMigrationsBefore(
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

	source := make(
		fstest.MapFS,
	)

	for _, migration := range migrations {
		if migration.version >=
			historyRetentionMigrationVersion {
			continue
		}

		data := make(
			[]byte,
			len(migration.data),
		)
		copy(
			data,
			migration.data,
		)

		source["migrations/"+migration.name] = &fstest.MapFile{
			Data: data,
		}
	}

	if len(source) == 0 {
		t.Fatal(
			"no migrations found before history retention migration",
		)
	}

	return source
}

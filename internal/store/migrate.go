package store

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationAdvisoryLockKey int64 = 0x4a6f7368426f74

var (
	// ErrMigrationDrift means an applied migration no longer matches its
	// authoritative embedded file.
	ErrMigrationDrift = errors.New(
		"store: migration drift",
	)

	errMigrationSourceUnavailable = errors.New(
		"store: migration source unavailable",
	)
	errInvalidMigrationSet = errors.New(
		"store: invalid migration set",
	)
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

type migration struct {
	version  int64
	name     string
	checksum string
	data     []byte
}

type appliedMigration struct {
	version   int64
	name      string
	checksum  string
	appliedAt time.Time
}

// Migrate applies every pending authoritative store migration.
//
// Migration execution is serialized with a transaction-level PostgreSQL
// advisory lock. Applied migration checksums must continue to match the
// embedded migration bytes.
func Migrate(
	ctx context.Context,
	pool *pgxpool.Pool,
) error {
	return migrate(
		ctx,
		pool,
		embeddedMigrations,
	)
}

func migrate(
	ctx context.Context,
	pool *pgxpool.Pool,
	source fs.FS,
) error {
	if ctx == nil {
		return errInvalidContext
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if pool == nil {
		return errPoolUnavailable
	}

	if source == nil {
		return errMigrationSourceUnavailable
	}

	migrations, err := loadMigrations(source)
	if err != nil {
		return err
	}

	err = pgx.BeginFunc(
		ctx,
		pool,
		func(tx pgx.Tx) error {
			return runMigrations(
				ctx,
				tx,
				migrations,
			)
		},
	)
	if err != nil {
		return fmt.Errorf(
			"store: migrate: %w",
			err,
		)
	}

	return nil
}

func loadMigrations(
	source fs.FS,
) ([]migration, error) {
	names, _ := fs.Glob(
		source,
		"migrations/*.sql",
	)
	if len(names) == 0 {
		return nil, fmt.Errorf(
			"%w: no migrations",
			errInvalidMigrationSet,
		)
	}

	migrations := make(
		[]migration,
		0,
		len(names),
	)
	seenVersions := make(
		map[int64]struct{},
		len(names),
	)

	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return nil, err
		}

		if _, exists := seenVersions[version]; exists {
			return nil, fmt.Errorf(
				"%w: duplicate version %d",
				errInvalidMigrationSet,
				version,
			)
		}

		data, err := fs.ReadFile(source, name)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: read %q",
				errInvalidMigrationSet,
				path.Base(name),
			)
		}

		if len(data) == 0 {
			return nil, fmt.Errorf(
				"%w: empty migration %q",
				errInvalidMigrationSet,
				path.Base(name),
			)
		}

		digest := sha256.Sum256(data)
		migrations = append(
			migrations,
			migration{
				version: version,
				name:    path.Base(name),
				checksum: hex.EncodeToString(
					digest[:],
				),
				data: data,
			},
		)
		seenVersions[version] = struct{}{}
	}

	sort.Slice(
		migrations,
		func(left int, right int) bool {
			return migrations[left].version <
				migrations[right].version
		},
	)

	return migrations, nil
}

func migrationVersion(name string) (int64, error) {
	base := path.Base(name)
	separator := strings.IndexByte(base, '_')
	if separator <= 0 ||
		!strings.HasSuffix(base, ".sql") {
		return 0, fmt.Errorf(
			"%w: migration name %q",
			errInvalidMigrationSet,
			base,
		)
	}

	version, err := strconv.ParseInt(
		base[:separator],
		10,
		64,
	)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf(
			"%w: migration version %q",
			errInvalidMigrationSet,
			base,
		)
	}

	return version, nil
}

func runMigrations(
	ctx context.Context,
	tx pgx.Tx,
	migrations []migration,
) error {
	setupStatements := []string{
		`
			SELECT pg_advisory_xact_lock($1)
		`,
		`
			CREATE TABLE IF NOT EXISTS schema_migrations (
				version BIGINT PRIMARY KEY,
				name TEXT NOT NULL UNIQUE,
				checksum TEXT NOT NULL,
				applied_at TIMESTAMPTZ NOT NULL
			)
		`,
	}

	for index, statement := range setupStatements {
		var (
			arguments []any
			mode      pgx.QueryExecMode
		)

		if index == 0 {
			arguments = []any{
				migrationAdvisoryLockKey,
			}
		} else {
			mode = pgx.QueryExecModeSimpleProtocol
			arguments = []any{mode}
		}

		if _, err := tx.Exec(
			ctx,
			statement,
			arguments...,
		); err != nil {
			return err
		}
	}

	rows, err := tx.Query(
		ctx,
		`
			SELECT
				version,
				name,
				checksum,
				applied_at
			FROM schema_migrations
			ORDER BY version
		`,
	)
	if err != nil {
		return err
	}

	applied, err := pgx.CollectRows(
		rows,
		func(
			row pgx.CollectableRow,
		) (appliedMigration, error) {
			var migration appliedMigration
			err := row.Scan(
				&migration.version,
				&migration.name,
				&migration.checksum,
				&migration.appliedAt,
			)

			return migration, err
		},
	)
	if err != nil {
		return err
	}

	current := make(
		map[int64]migration,
		len(migrations),
	)
	for _, migration := range migrations {
		current[migration.version] = migration
	}

	appliedByVersion := make(
		map[int64]appliedMigration,
		len(applied),
	)
	for _, recorded := range applied {
		expected, exists := current[recorded.version]
		if !exists ||
			recorded.name != expected.name ||
			recorded.checksum != expected.checksum {
			return fmt.Errorf(
				"%w: version %d",
				ErrMigrationDrift,
				recorded.version,
			)
		}

		appliedByVersion[recorded.version] = recorded
	}

	for _, migration := range migrations {
		if _, exists :=
			appliedByVersion[migration.version]; exists {
			continue
		}

		if _, err := tx.Exec(
			ctx,
			string(migration.data),
			pgx.QueryExecModeSimpleProtocol,
		); err != nil {
			return fmt.Errorf(
				"apply migration %q: %w",
				migration.name,
				err,
			)
		}

		if _, err := tx.Exec(
			ctx,
			`
				INSERT INTO schema_migrations (
					version,
					name,
					checksum,
					applied_at
				)
				VALUES (
					$1,
					$2,
					$3,
					clock_timestamp()
				)
			`,
			migration.version,
			migration.name,
			migration.checksum,
		); err != nil {
			return fmt.Errorf(
				"record migration %q: %w",
				migration.name,
				err,
			)
		}
	}

	return nil
}

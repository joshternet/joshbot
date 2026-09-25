package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/origin"
)

type storedCrawlBlockState struct {
	rawOrigin       string
	operatorBlocked bool
	crawlBlocked    bool
}

func setExactCrawlBlockTransaction(
	ctx context.Context,
	tx pgx.Tx,
	source origin.Origin,
	blocked bool,
	config AutomaticCrawlConfig,
) error {
	var seeded, automatic bool
	if err := tx.QueryRow(ctx, `
		INSERT INTO discovery_source_state (
			source_origin, operator_blocked, crawl_blocked
		) VALUES ($1, $2, $2)
		ON CONFLICT (source_origin) DO UPDATE
		SET
			operator_blocked = EXCLUDED.operator_blocked,
			crawl_blocked = EXCLUDED.crawl_blocked
		RETURNING seeded, automatically_discovered
	`, source.String(), blocked).Scan(&seeded, &automatic); err != nil {
		return fmt.Errorf("store exact crawl block: %w", err)
	}
	effectiveBlocked := blocked
	if automatic && !seeded {
		exclusions, err := automaticExclusionsTransaction(ctx, tx, config)
		if err != nil {
			return err
		}
		effectiveBlocked = effectiveBlocked ||
			exclusions.excludes(source.Hostname())
	}
	if _, err := tx.Exec(ctx, `
		UPDATE discovery_source_state
		SET crawl_blocked = $2
		WHERE source_origin = $1
	`, source.String(), effectiveBlocked); err != nil {
		return fmt.Errorf("update effective crawl block: %w", err)
	}
	if effectiveBlocked {
		if _, err := tx.Exec(ctx, `
			DELETE FROM verification_queue
			WHERE origin = $1
				AND mode IN ('probe', 'reprobe')
		`, source.String()); err != nil {
			return fmt.Errorf("remove blocked origin probe: %w", err)
		}
	}
	return nil
}

func recomputeAutomaticCrawlBlocksTransaction(
	ctx context.Context,
	tx pgx.Tx,
	config AutomaticCrawlConfig,
) (int, error) {
	exclusions, err := automaticExclusionsTransaction(ctx, tx, config)
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `
		SELECT source_origin, operator_blocked, crawl_blocked
		FROM discovery_source_state
		WHERE automatically_discovered AND NOT seeded
		ORDER BY source_origin
		FOR UPDATE
	`)
	if err != nil {
		return 0, fmt.Errorf("list automatic crawl blocks: %w", err)
	}
	states, err := pgx.CollectRows(
		rows,
		func(row pgx.CollectableRow) (storedCrawlBlockState, error) {
			var state storedCrawlBlockState
			err := row.Scan(
				&state.rawOrigin,
				&state.operatorBlocked,
				&state.crawlBlocked,
			)
			return state, err
		},
	)
	if err != nil {
		return 0, fmt.Errorf("collect automatic crawl blocks: %w", err)
	}
	changed := 0
	blockedOrigins := make([]string, 0, len(states))
	for _, state := range states {
		source, err := origin.Parse(state.rawOrigin)
		if err != nil {
			return 0, fmt.Errorf("invalid automatic crawl source: %w", err)
		}
		effectiveBlocked := state.operatorBlocked ||
			exclusions.excludes(source.Hostname())
		if effectiveBlocked {
			blockedOrigins = append(blockedOrigins, state.rawOrigin)
		}
		if effectiveBlocked == state.crawlBlocked {
			continue
		}
		if _, err := tx.Exec(ctx, `
			UPDATE discovery_source_state
			SET crawl_blocked = $2
			WHERE source_origin = $1
		`, state.rawOrigin, effectiveBlocked); err != nil {
			return 0, fmt.Errorf("update automatic crawl block: %w", err)
		}
		changed++
	}
	if len(blockedOrigins) > 0 {
		if _, err := tx.Exec(ctx, `
			DELETE FROM verification_queue
			WHERE origin = ANY($1::text[])
				AND mode IN ('probe', 'reprobe')
		`, blockedOrigins); err != nil {
			return 0, fmt.Errorf("remove blocked automatic probes: %w", err)
		}
	}
	return changed, nil
}

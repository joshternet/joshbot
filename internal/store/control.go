package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joshternet/joshbot/internal/control"
	"github.com/joshternet/joshbot/internal/origin"
)

var errControlStoreUnavailable = errors.New("store: control store is unavailable")

// ControlStore persists operator control mutations.
type ControlStore struct {
	pool *pgxpool.Pool
}

// NewControlStore constructs a control adapter using a caller-owned pool.
func NewControlStore(pool *pgxpool.Pool) (*ControlStore, error) {
	if pool == nil {
		return nil, errPoolUnavailable
	}
	return &ControlStore{pool: pool}, nil
}

// SetProcessorPaused updates one processor and appends its success audit in
// the same transaction.
func (s *ControlStore) SetProcessorPaused(
	ctx context.Context,
	processor string,
	paused bool,
	audit control.Audit,
) error {
	column := ""
	switch processor {
	case "discovery":
		column = "discovery_paused"
	case "verification":
		column = "verification_paused"
	default:
		return errInvalidServiceState
	}
	expectedAction := "processor.resume"
	if paused {
		expectedAction = "processor.pause"
	}
	if !controlAuditMatches(audit, expectedAction, processor) {
		return errors.New("store: processor control audit is inconsistent")
	}
	return s.mutate(ctx, audit, func(tx pgx.Tx) error {
		result, err := tx.Exec(
			ctx,
			"UPDATE crawl_control SET "+column+
				" = $1, updated_at = statement_timestamp() WHERE singleton",
			paused,
		)
		if err != nil {
			return fmt.Errorf("set processor state: %w", err)
		}
		if result.RowsAffected() != 1 {
			return errors.New("set processor state: singleton unavailable")
		}
		return nil
	})
}

// AddDomainAvoid inserts a domain policy, reconciles matching automatic
// non-seed sources, removes their probes, and atomically appends the audit.
func (s *ControlStore) AddDomainAvoid(
	ctx context.Context,
	pattern string,
	audit control.Audit,
) error {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" || strings.Contains(pattern, ",") ||
		!validExcludedHostSuffixes(pattern) {
		return errors.New("store: crawl domain avoid rule is invalid")
	}
	if !controlAuditMatches(audit, "domain-avoid.add", pattern) {
		return errors.New("store: domain avoid audit is inconsistent")
	}
	return s.mutate(ctx, audit, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO crawl_domain_avoid_rules (pattern)
			VALUES ($1)
			ON CONFLICT (pattern) DO NOTHING
		`, pattern); err != nil {
			return fmt.Errorf("add domain avoid rule: %w", err)
		}
		if _, err := recomputeAutomaticCrawlBlocksTransaction(
			ctx,
			tx,
			AutomaticCrawlConfig{},
		); err != nil {
			return fmt.Errorf("reconcile domain avoid rule: %w", err)
		}
		return nil
	})
}

// RemoveDomainAvoid removes a domain policy and atomically appends the audit.
func (s *ControlStore) RemoveDomainAvoid(
	ctx context.Context,
	pattern string,
	audit control.Audit,
) error {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" || strings.Contains(pattern, ",") ||
		!validExcludedHostSuffixes(pattern) {
		return errors.New("store: crawl domain avoid rule is invalid")
	}
	if !controlAuditMatches(audit, "domain-avoid.remove", pattern) {
		return errors.New("store: domain avoid audit is inconsistent")
	}
	return s.mutate(ctx, audit, func(tx pgx.Tx) error {
		if _, err := tx.Exec(
			ctx,
			"DELETE FROM crawl_domain_avoid_rules WHERE pattern = $1",
			pattern,
		); err != nil {
			return fmt.Errorf("remove domain avoid rule: %w", err)
		}
		if _, err := recomputeAutomaticCrawlBlocksTransaction(
			ctx,
			tx,
			AutomaticCrawlConfig{},
		); err != nil {
			return fmt.Errorf("reconcile removed domain avoid rule: %w", err)
		}
		return nil
	})
}

// SetOriginBlocked applies explicit origin policy and atomically appends the
// audit. Allowing an origin does not override matching domain avoid policy.
func (s *ControlStore) SetOriginBlocked(
	ctx context.Context,
	rawOrigin string,
	blocked bool,
	audit control.Audit,
) error {
	source, err := origin.Parse(rawOrigin)
	if err != nil || source.String() != rawOrigin {
		return errInvalidOrigin
	}
	expectedAction := "origin.allow"
	if blocked {
		expectedAction = "origin.block"
	}
	if !controlAuditMatches(audit, expectedAction, rawOrigin) {
		return errors.New("store: origin control audit is inconsistent")
	}
	return s.mutate(ctx, audit, func(tx pgx.Tx) error {
		if err := setExactCrawlBlockTransaction(
			ctx,
			tx,
			source,
			blocked,
			AutomaticCrawlConfig{},
		); err != nil {
			return fmt.Errorf("set origin block: %w", err)
		}
		return nil
	})
}

// RecordRejected durably appends a failed authenticated attempt.
func (s *ControlStore) RecordRejected(
	ctx context.Context,
	audit control.Audit,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}
	if audit.Result != control.ResultRejected {
		return errors.New("store: rejected audit result is invalid")
	}
	if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return insertControlAudit(ctx, tx, audit)
	}); err != nil {
		return fmt.Errorf("store: record rejected control audit: %w", err)
	}
	return nil
}

func (s *ControlStore) mutate(
	ctx context.Context,
	audit control.Audit,
	mutation func(pgx.Tx) error,
) error {
	if err := s.validate(ctx); err != nil {
		return err
	}
	if mutation == nil || audit.Result != control.ResultSuccess {
		return errors.New("store: successful control audit is invalid")
	}
	if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := mutation(tx); err != nil {
			return err
		}
		return insertControlAudit(ctx, tx, audit)
	}); err != nil {
		return fmt.Errorf("store: apply control mutation: %w", err)
	}
	return nil
}

func (s *ControlStore) validate(ctx context.Context) error {
	if s == nil {
		return errControlStoreUnavailable
	}
	if ctx == nil {
		return errInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.pool == nil {
		return errPoolUnavailable
	}
	return nil
}

func insertControlAudit(
	ctx context.Context,
	tx pgx.Tx,
	audit control.Audit,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO operator_audit_events (
			occurred_at, action, target, caller, actor, result, reason
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, audit.OccurredAt, audit.Action, audit.Target, audit.Caller,
		audit.Actor, audit.Result, audit.Reason)
	if err != nil {
		return fmt.Errorf("insert operator audit: %w", err)
	}
	return nil
}

func controlAuditMatches(
	audit control.Audit,
	action string,
	target string,
) bool {
	return audit.Action == action && audit.Target == target
}

var _ control.Commander = (*ControlStore)(nil)

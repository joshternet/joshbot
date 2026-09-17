package reporting

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Audits returns recent immutable operator events ordered newest first.
func (reader *PostgresReader) Audits(
	ctx context.Context,
	limit int,
) ([]AuditEvent, error) {
	if !validReaderLimit(limit) {
		return nil, errInvalidLimit
	}
	rows, err := reader.pool.Query(ctx, auditSelect+`
		ORDER BY occurred_at DESC, id DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("reporting: read audits: %w", err)
	}
	items, err := collectAudits(rows)
	if err != nil {
		return nil, fmt.Errorf("reporting: collect audits: %w", err)
	}
	return items, nil
}

func (reader *PostgresReader) AuditsPage(
	ctx context.Context,
	query auditQuery,
) (auditPage, error) {
	fetchLimit, err := pageLimit(query.Limit)
	if err != nil {
		return auditPage{}, err
	}
	var occurredAt *time.Time
	var id int64
	if query.Cursor != nil {
		occurredAt = &query.Cursor.OccurredAt
		id = query.Cursor.ID
	}
	rows, err := reader.pool.Query(ctx, auditSelect+`
		WHERE (
			$1::timestamptz IS NULL
			OR (occurred_at, id) < ($1, $2::bigint)
		)
		ORDER BY occurred_at DESC, id DESC
		LIMIT $3
	`, occurredAt, id, fetchLimit)
	if err != nil {
		return auditPage{}, fmt.Errorf("reporting: read audit page: %w", err)
	}
	items, err := collectAudits(rows)
	if err != nil {
		return auditPage{}, fmt.Errorf("reporting: collect audit page: %w", err)
	}
	return trimAuditPage(items, query.Limit), nil
}

const auditSelect = `
	SELECT id, occurred_at, action, target, caller, actor, result, reason
	FROM operator_audit_events
`

func collectAudits(rows pgx.Rows) ([]AuditEvent, error) {
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (AuditEvent, error) {
		var event AuditEvent
		err := row.Scan(
			&event.ID,
			&event.OccurredAt,
			&event.Action,
			&event.Target,
			&event.Caller,
			&event.Actor,
			&event.Result,
			&event.Reason,
		)
		return event, err
	})
}

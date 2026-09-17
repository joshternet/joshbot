package reporting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPostgresReaderAuditPagination(t *testing.T) {
	ctx := context.Background()
	pool := newReportingTestPool(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	for index := 1; index <= 3; index++ {
		if _, err := pool.Exec(ctx, `
			INSERT INTO operator_audit_events
				(occurred_at, action, target, caller, actor, result, reason)
			VALUES ($1, $2, 'discovery', 'integration', 'operator', 'success', '')
		`, at, "action-"+string(rune('0'+index))); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := NewPostgresReader(pool, PostgresConfig{MaxPendingProbes: 1})
	if err != nil {
		t.Fatal(err)
	}
	first, err := reader.AuditsPage(ctx, auditQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" ||
		first.Items[0].ID <= first.Items[1].ID {
		t.Fatalf("first audit page = %#v", first)
	}
	cursor, err := decodeAuditCursor(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reader.AuditsPage(ctx, auditQuery{Limit: 2, Cursor: cursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.NextCursor != "" ||
		second.Items[0].ID >= first.Items[1].ID {
		t.Fatalf("second audit page = %#v", second)
	}
	audits, err := reader.Audits(ctx, 3)
	if err != nil || len(audits) != 3 {
		t.Fatalf("Audits() = %#v, %v", audits, err)
	}
	metrics, err := reader.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.CrawlRuns != 0 || metrics.Candidates != 0 {
		t.Errorf("empty durable metrics = %#v", metrics)
	}
}

func TestPostgresReaderAuditScanFailureAndReporterPrivileges(t *testing.T) {
	ctx := context.Background()
	pool := newReportingTestPool(t)
	if _, err := pool.Exec(ctx, `
		INSERT INTO operator_audit_events
			(occurred_at, action, target, caller, actor, result)
		VALUES (
			TIMESTAMPTZ 'infinity', 'pause', 'discovery',
			'integration', 'operator', 'success'
		)
	`); err != nil {
		t.Fatal(err)
	}
	reader, err := NewPostgresReader(pool, PostgresConfig{MaxPendingProbes: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = reader.Audits(ctx, 1)
	var scanError pgx.ScanArgError
	if !errors.As(err, &scanError) ||
		scanError.ColumnIndex != 1 ||
		scanError.FieldName != "occurred_at" ||
		!strings.HasPrefix(err.Error(), "reporting: collect audits:") {
		t.Fatalf("Audits() scan error = %#v, %v", scanError, err)
	}

	var roleExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_roles WHERE rolname = 'joshbot_reporter'
		)
	`).Scan(&roleExists); err != nil {
		t.Fatal(err)
	}
	if !roleExists {
		t.Skip("joshbot_reporter role is not installed in the test database")
	}
	var canSelect, canInsert, canUpdate, canDelete bool
	if err := pool.QueryRow(ctx, `
		SELECT
			has_table_privilege('joshbot_reporter', 'operator_audit_events', 'SELECT'),
			has_table_privilege('joshbot_reporter', 'operator_audit_events', 'INSERT'),
			has_table_privilege('joshbot_reporter', 'operator_audit_events', 'UPDATE'),
			has_table_privilege('joshbot_reporter', 'operator_audit_events', 'DELETE')
	`).Scan(&canSelect, &canInsert, &canUpdate, &canDelete); err != nil {
		t.Fatal(err)
	}
	if !canSelect || canInsert || canUpdate || canDelete {
		t.Errorf(
			"reporter audit privileges = select:%t insert:%t update:%t delete:%t",
			canSelect, canInsert, canUpdate, canDelete,
		)
	}
}

func TestPostgresReaderAuditPageReturnsQueryFailure(t *testing.T) {
	pool := newReportingTestPool(t)
	reader, err := NewPostgresReader(
		pool,
		PostgresConfig{MaxPendingProbes: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if _, err := reader.AuditsPage(
		context.Background(),
		auditQuery{
			Limit: 1,
			Cursor: &auditCursor{
				OccurredAt: time.Now().UTC(),
				ID:         1,
			},
		},
	); err == nil || !strings.Contains(err.Error(), "reporting: read audit page") {
		t.Fatalf("AuditsPage() query error = %v", err)
	}
}

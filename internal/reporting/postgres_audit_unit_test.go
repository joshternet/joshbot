package reporting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPostgresReaderAuditsWithoutDatabase(
	t *testing.T,
) {
	now := time.Date(
		2026,
		time.September,
		18,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{
				{
					rows: newFakeRows(
						unitAuditValues(
							2,
							now,
						),
						unitAuditValues(
							1,
							now.Add(-time.Minute),
						),
					),
				},
			},
		},
		PostgresConfig{
			MaxPendingProbes: 1,
		},
	)

	audits, err := reader.Audits(
		context.Background(),
		2,
	)
	if err != nil {
		t.Fatalf(
			"Audits() error = %v",
			err,
		)
	}

	if len(audits) != 2 {
		t.Fatalf(
			"Audits() length = %d, want 2",
			len(audits),
		)
	}

	if audits[0].ID != 2 ||
		audits[0].Action != "pause" ||
		audits[0].Target != "discovery" ||
		audits[0].Caller != "control" ||
		audits[0].Actor != "operator" ||
		audits[0].Result != "success" ||
		audits[0].Reason != "maintenance" {
		t.Fatalf(
			"Audits()[0] = %#v",
			audits[0],
		)
	}
}

func TestPostgresReaderAuditsFailuresWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test audit failure",
	)

	t.Run(
		"invalid limit",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			if _, err := reader.Audits(
				context.Background(),
				0,
			); !errors.Is(
				err,
				errInvalidLimit,
			) {
				t.Fatalf(
					"Audits(0) error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"query",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{
					queryResults: []fakeQueryResult{
						{
							err: testErr,
						},
					},
				},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			_, err := reader.Audits(
				context.Background(),
				1,
			)
			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"reporting: read audits",
				) {
				t.Fatalf(
					"Audits() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"collect",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{
					queryResults: []fakeQueryResult{
						{
							rows: newFakeRows(
								[]any{
									int64(1),
								},
							),
						},
					},
				},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			_, err := reader.Audits(
				context.Background(),
				1,
			)
			if err == nil ||
				!strings.Contains(
					err.Error(),
					"reporting: collect audits",
				) {
				t.Fatalf(
					"Audits() error = %v",
					err,
				)
			}
		},
	)
}

func TestPostgresReaderAuditsPageWithoutDatabase(
	t *testing.T,
) {
	now := time.Date(
		2026,
		time.September,
		18,
		12,
		0,
		0,
		0,
		time.UTC,
	)

	t.Run(
		"first page",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{
					queryResults: []fakeQueryResult{
						{
							rows: newFakeRows(
								unitAuditValues(
									3,
									now,
								),
								unitAuditValues(
									2,
									now.Add(
										-time.Minute,
									),
								),
							),
						},
					},
				},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			page, err := reader.AuditsPage(
				context.Background(),
				auditQuery{
					Limit: 1,
				},
			)
			if err != nil {
				t.Fatalf(
					"AuditsPage() error = %v",
					err,
				)
			}

			if len(page.Items) != 1 ||
				page.Items[0].ID != 3 ||
				page.NextCursor == "" {
				t.Fatalf(
					"AuditsPage() = %#v",
					page,
				)
			}
		},
	)

	t.Run(
		"cursor",
		func(t *testing.T) {
			cursorTime := now.Add(
				-time.Minute,
			)

			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{
					queryResults: []fakeQueryResult{
						{
							rows: newFakeRows(
								unitAuditValues(
									1,
									cursorTime.Add(
										-time.Minute,
									),
								),
							),
						},
					},
				},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			page, err := reader.AuditsPage(
				context.Background(),
				auditQuery{
					Limit: 1,
					Cursor: &auditCursor{
						OccurredAt: cursorTime,
						ID:         2,
					},
				},
			)
			if err != nil {
				t.Fatalf(
					"AuditsPage(cursor) error = %v",
					err,
				)
			}

			if len(page.Items) != 1 ||
				page.Items[0].ID != 1 ||
				page.NextCursor != "" {
				t.Fatalf(
					"AuditsPage(cursor) = %#v",
					page,
				)
			}
		},
	)
}

func TestPostgresReaderAuditsPageFailuresWithoutDatabase(
	t *testing.T,
) {
	testErr := errors.New(
		"test audit page failure",
	)

	t.Run(
		"invalid limit",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			if _, err := reader.AuditsPage(
				context.Background(),
				auditQuery{
					Limit: 0,
				},
			); !errors.Is(
				err,
				errInvalidLimit,
			) {
				t.Fatalf(
					"AuditsPage(0) error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"query",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{
					queryResults: []fakeQueryResult{
						{
							err: testErr,
						},
					},
				},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			_, err := reader.AuditsPage(
				context.Background(),
				auditQuery{
					Limit: 1,
				},
			)
			if !errors.Is(
				err,
				testErr,
			) ||
				!strings.Contains(
					err.Error(),
					"reporting: read audit page",
				) {
				t.Fatalf(
					"AuditsPage() error = %v",
					err,
				)
			}
		},
	)

	t.Run(
		"collect",
		func(t *testing.T) {
			reader := mustUnitPostgresReader(
				t,
				&fakeQueryer{
					queryResults: []fakeQueryResult{
						{
							rows: newFakeRows(
								[]any{
									int64(1),
								},
							),
						},
					},
				},
				PostgresConfig{
					MaxPendingProbes: 1,
				},
			)

			_, err := reader.AuditsPage(
				context.Background(),
				auditQuery{
					Limit: 1,
				},
			)
			if err == nil ||
				!strings.Contains(
					err.Error(),
					"reporting: collect audit page",
				) {
				t.Fatalf(
					"AuditsPage() error = %v",
					err,
				)
			}
		},
	)
}

func unitAuditValues(
	id int64,
	occurredAt time.Time,
) []any {
	return []any{
		id,
		occurredAt,
		"pause",
		"discovery",
		"control",
		"operator",
		"success",
		"maintenance",
	}
}

package reporting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joshternet/joshbot/internal/personalsite"
)

func personalSiteRowValues(
	origin string,
	score int,
	reasons []byte,
) []any {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	outcome := "assessed"
	version := personalsite.AssessmentVersion
	return []any{
		origin,
		&outcome,
		&score,
		&score,
		&version,
		&now,
		false,
		reasons,
		(*time.Time)(nil),
		&now,
		0,
		(*int)(nil),
		(*string)(nil),
		(*int)(nil),
		(*string)(nil),
		(*string)(nil),
		(*time.Time)(nil),
		int64(0),
		false,
		"",
		"unscheduled",
		true,
	}
}

func TestPostgresPersonalSitesPageWithoutDatabase(t *testing.T) {
	t.Parallel()

	score := 60
	awaiting := true
	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{{
				rows: newFakeRows(
					personalSiteRowValues(
						"https://a.example",
						score,
						[]byte(`["person_schema"]`),
					),
					personalSiteRowValues(
						"https://b.example",
						score-1,
						nil,
					),
				),
			}},
		},
		PostgresConfig{MaxPendingProbes: 1},
	)

	page, err := reader.PersonalSitesPage(
		context.Background(),
		personalSiteQuery{
			Limit:    1,
			Origin:   "https://a.example",
			Outcome:  "assessed",
			Awaiting: &awaiting,
			Cursor: &personalSiteCursor{
				HasScore:       true,
				EffectiveScore: &score,
				Origin:         "https://cursor.example",
			},
		},
	)
	if err != nil {
		t.Fatalf("PersonalSitesPage() error = %v", err)
	}
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("page = %#v", page)
	}
	if page.Items[0].Origin != "https://a.example" {
		t.Fatalf("item = %#v", page.Items[0])
	}
}

func TestPostgresPersonalSitesPageNullCursorAndAwaitingFalse(
	t *testing.T,
) {
	t.Parallel()

	awaiting := false
	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			queryResults: []fakeQueryResult{{
				rows: newFakeRows(
					personalSiteRowValues(
						"https://null.example",
						40,
						[]byte(`[]`),
					),
				),
			}},
		},
		PostgresConfig{MaxPendingProbes: 1},
	)

	page, err := reader.PersonalSitesPage(
		context.Background(),
		personalSiteQuery{
			Limit:    10,
			Awaiting: &awaiting,
			Cursor: &personalSiteCursor{
				HasScore: false,
				Origin:   "https://before.example",
			},
		},
	)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(page.Items) != 1 || page.NextCursor != "" {
		t.Fatalf("page = %#v", page)
	}
}

func TestPostgresPersonalSitesPageFailuresWithoutDatabase(
	t *testing.T,
) {
	t.Parallel()

	t.Run("invalid limit", func(t *testing.T) {
		t.Parallel()
		reader := mustUnitPostgresReader(
			t,
			&fakeQueryer{},
			PostgresConfig{MaxPendingProbes: 1},
		)
		_, err := reader.PersonalSitesPage(
			context.Background(),
			personalSiteQuery{Limit: 0},
		)
		if err == nil {
			t.Fatal("error = nil")
		}
	})

	t.Run("query error", func(t *testing.T) {
		t.Parallel()
		testErr := errors.New("list failed")
		reader := mustUnitPostgresReader(
			t,
			&fakeQueryer{
				queryResults: []fakeQueryResult{{err: testErr}},
			},
			PostgresConfig{MaxPendingProbes: 1},
		)
		_, err := reader.PersonalSitesPage(
			context.Background(),
			personalSiteQuery{Limit: 5},
		)
		if err == nil || !strings.Contains(err.Error(), "list personal sites") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("collect error", func(t *testing.T) {
		t.Parallel()
		reader := mustUnitPostgresReader(
			t,
			&fakeQueryer{
				queryResults: []fakeQueryResult{{
					rows: newFakeRows([]any{"too-few"}),
				}},
			},
			PostgresConfig{MaxPendingProbes: 1},
		)
		_, err := reader.PersonalSitesPage(
			context.Background(),
			personalSiteQuery{Limit: 5},
		)
		if err == nil {
			t.Fatal("error = nil")
		}
	})
}

func TestPostgresPersonalSiteDetailWithoutDatabase(t *testing.T) {
	t.Parallel()

	reader := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			rowResults: []fakeRow{{
				values: personalSiteRowValues(
					"https://detail.example",
					70,
					[]byte(`["rel_me"]`),
				),
			}},
		},
		PostgresConfig{MaxPendingProbes: 1},
	)

	site, found, err := reader.PersonalSite(
		context.Background(),
		"https://detail.example",
	)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if site.Origin != "https://detail.example" {
		t.Fatalf("site = %#v", site)
	}

	missing := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			rowResults: []fakeRow{{err: pgx.ErrNoRows}},
		},
		PostgresConfig{MaxPendingProbes: 1},
	)
	_, found, err = missing.PersonalSite(
		context.Background(),
		"https://missing.example",
	)
	if err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}

	_, _, err = reader.PersonalSite(
		context.Background(),
		"ftp://bad.example",
	)
	if !errors.Is(err, errInvalidOriginFilter) {
		t.Fatalf("invalid origin err = %v", err)
	}

	failing := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			rowResults: []fakeRow{{err: errors.New("read failed")}},
		},
		PostgresConfig{MaxPendingProbes: 1},
	)
	_, _, err = failing.PersonalSite(
		context.Background(),
		"https://detail.example",
	)
	if err == nil {
		t.Fatal("error = nil")
	}

	badJSON := mustUnitPostgresReader(
		t,
		&fakeQueryer{
			rowResults: []fakeRow{{
				values: personalSiteRowValues(
					"https://detail.example",
					70,
					[]byte(`{`),
				),
			}},
		},
		PostgresConfig{MaxPendingProbes: 1},
	)
	_, _, err = badJSON.PersonalSite(
		context.Background(),
		"https://detail.example",
	)
	if err == nil {
		t.Fatal("bad json accepted")
	}
}

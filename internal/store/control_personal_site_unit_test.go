package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/control"
	"github.com/joshternet/joshbot/internal/personalsite"
)

func TestSetPersonalSiteOverrideWithoutDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	audit := controlUnitAudit(
		"personal-site.override.set",
		"https://personal.example",
		control.ResultSuccess,
	)

	t.Run("validation", func(t *testing.T) {
		t.Parallel()
		var store *ControlStore
		if err := store.SetPersonalSiteOverride(
			ctx,
			"https://personal.example",
			50,
			0,
			audit,
		); !errors.Is(err, errControlStoreUnavailable) {
			t.Fatalf("nil store = %v", err)
		}

		unit := mustControlUnitStore(t, &controlUnitTx{})
		if err := unit.SetPersonalSiteOverride(
			ctx,
			"ftp://bad.example",
			50,
			0,
			audit,
		); !errors.Is(err, errInvalidOrigin) {
			t.Fatalf("bad origin = %v", err)
		}
		if err := unit.SetPersonalSiteOverride(
			ctx,
			"https://personal.example",
			101,
			0,
			audit,
		); err == nil {
			t.Fatal("bad score accepted")
		}
		badAudit := audit
		badAudit.Reason = ""
		if err := unit.SetPersonalSiteOverride(
			ctx,
			"https://personal.example",
			50,
			0,
			badAudit,
		); err == nil {
			t.Fatal("empty reason accepted")
		}
		badAudit = audit
		badAudit.Action = "wrong"
		if err := unit.SetPersonalSiteOverride(
			ctx,
			"https://personal.example",
			50,
			0,
			badAudit,
		); err == nil {
			t.Fatal("bad audit accepted")
		}
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		outcome := string(personalsite.QualificationAssessed)
		version := personalsite.AssessmentVersion
		score := 40
		tx := &controlUnitTx{
			rowResults: []storeFakeRow{{
				values: []any{int64(0), false, &outcome, &version, &score},
			}},
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("UPDATE 1")},
				{tag: pgconn.NewCommandTag("INSERT 0 1")},
			},
		}
		store := mustControlUnitStore(t, tx)
		if err := store.SetPersonalSiteOverride(
			ctx,
			"https://personal.example",
			77,
			0,
			audit,
		); err != nil {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		t.Parallel()
		tx := &controlUnitTx{
			rowResults: []storeFakeRow{{err: pgx.ErrNoRows}},
		}
		store := mustControlUnitStore(t, tx)
		err := store.SetPersonalSiteOverride(
			ctx,
			"https://personal.example",
			50,
			0,
			audit,
		)
		if !errors.Is(err, ErrPersonalSiteCandidateUnknown) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("blocked_and_conflict", func(t *testing.T) {
		t.Parallel()
		outcome := string(personalsite.QualificationAssessed)
		version := personalsite.AssessmentVersion
		score := 40
		tx := &controlUnitTx{
			rowResults: []storeFakeRow{{
				values: []any{int64(0), true, &outcome, &version, &score},
			}},
		}
		store := mustControlUnitStore(t, tx)
		err := store.SetPersonalSiteOverride(
			ctx,
			"https://personal.example",
			50,
			0,
			audit,
		)
		if !errors.Is(err, ErrPersonalSiteCandidateBlocked) {
			t.Fatalf("blocked = %v", err)
		}

		tx = &controlUnitTx{
			rowResults: []storeFakeRow{{
				values: []any{int64(0), false, (*string)(nil), (*int)(nil), (*int)(nil)},
			}},
		}
		store = mustControlUnitStore(t, tx)
		err = store.SetPersonalSiteOverride(
			ctx,
			"https://personal.example",
			50,
			0,
			audit,
		)
		if !errors.Is(err, ErrPersonalSiteCandidateBlocked) {
			t.Fatalf("unassessed = %v", err)
		}

		tx = &controlUnitTx{
			rowResults: []storeFakeRow{{
				values: []any{int64(2), false, &outcome, &version, &score},
			}},
		}
		store = mustControlUnitStore(t, tx)
		err = store.SetPersonalSiteOverride(
			ctx,
			"https://personal.example",
			50,
			1,
			audit,
		)
		if !errors.Is(err, ErrPersonalSiteOverrideConflict) {
			t.Fatalf("revision conflict = %v", err)
		}

		tx = &controlUnitTx{
			rowResults: []storeFakeRow{{
				values: []any{int64(1), false, &outcome, &version, &score},
			}},
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("UPDATE 0")},
			},
		}
		store = mustControlUnitStore(t, tx)
		err = store.SetPersonalSiteOverride(
			ctx,
			"https://personal.example",
			50,
			1,
			audit,
		)
		if !errors.Is(err, ErrPersonalSiteOverrideConflict) {
			t.Fatalf("update conflict = %v", err)
		}
	})

	t.Run("load_and_exec_errors", func(t *testing.T) {
		t.Parallel()
		tx := &controlUnitTx{
			rowResults: []storeFakeRow{{err: errors.New("load failed")}},
		}
		store := mustControlUnitStore(t, tx)
		err := store.SetPersonalSiteOverride(
			ctx,
			"https://personal.example",
			50,
			0,
			audit,
		)
		if err == nil {
			t.Fatal("load error = nil")
		}

		outcome := string(personalsite.QualificationAssessed)
		version := personalsite.AssessmentVersion
		score := 40
		tx = &controlUnitTx{
			rowResults: []storeFakeRow{{
				values: []any{int64(0), false, &outcome, &version, &score},
			}},
			execResults: []controlUnitExecResult{
				{err: errors.New("update failed")},
			},
		}
		store = mustControlUnitStore(t, tx)
		err = store.SetPersonalSiteOverride(
			ctx,
			"https://personal.example",
			50,
			0,
			audit,
		)
		if err == nil {
			t.Fatal("exec error = nil")
		}
	})
}

func TestClearPersonalSiteOverrideWithoutDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	audit := controlUnitAudit(
		"personal-site.override.clear",
		"https://personal.example",
		control.ResultSuccess,
	)

	t.Run("validation", func(t *testing.T) {
		t.Parallel()
		var store *ControlStore
		if err := store.ClearPersonalSiteOverride(
			ctx,
			"https://personal.example",
			1,
			audit,
		); !errors.Is(err, errControlStoreUnavailable) {
			t.Fatalf("nil = %v", err)
		}
		unit := mustControlUnitStore(t, &controlUnitTx{})
		if err := unit.ClearPersonalSiteOverride(
			ctx,
			"ftp://bad.example",
			1,
			audit,
		); !errors.Is(err, errInvalidOrigin) {
			t.Fatalf("origin = %v", err)
		}
		if err := unit.ClearPersonalSiteOverride(
			ctx,
			"https://personal.example",
			0,
			audit,
		); !errors.Is(err, ErrPersonalSiteOverrideConflict) {
			t.Fatalf("revision = %v", err)
		}
		badAudit := audit
		badAudit.Action = "wrong"
		if err := unit.ClearPersonalSiteOverride(
			ctx,
			"https://personal.example",
			1,
			badAudit,
		); err == nil {
			t.Fatal("bad audit accepted")
		}
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		tx := &controlUnitTx{
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("UPDATE 1")},
				{tag: pgconn.NewCommandTag("INSERT 0 1")},
			},
		}
		store := mustControlUnitStore(t, tx)
		if err := store.ClearPersonalSiteOverride(
			ctx,
			"https://personal.example",
			2,
			audit,
		); err != nil {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("missing_and_conflict", func(t *testing.T) {
		t.Parallel()
		tx := &controlUnitTx{
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("UPDATE 0")},
			},
			rowResults: []storeFakeRow{{values: []any{false}}},
		}
		store := mustControlUnitStore(t, tx)
		err := store.ClearPersonalSiteOverride(
			ctx,
			"https://personal.example",
			2,
			audit,
		)
		if !errors.Is(err, ErrPersonalSiteCandidateUnknown) {
			t.Fatalf("missing = %v", err)
		}

		tx = &controlUnitTx{
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("UPDATE 0")},
			},
			rowResults: []storeFakeRow{{values: []any{true}}},
		}
		store = mustControlUnitStore(t, tx)
		err = store.ClearPersonalSiteOverride(
			ctx,
			"https://personal.example",
			2,
			audit,
		)
		if !errors.Is(err, ErrPersonalSiteOverrideConflict) {
			t.Fatalf("conflict = %v", err)
		}
	})

	t.Run("exec_and_exists_errors", func(t *testing.T) {
		t.Parallel()
		tx := &controlUnitTx{
			execResults: []controlUnitExecResult{
				{err: errors.New("clear failed")},
			},
		}
		store := mustControlUnitStore(t, tx)
		if err := store.ClearPersonalSiteOverride(
			ctx,
			"https://personal.example",
			2,
			audit,
		); err == nil {
			t.Fatal("exec error = nil")
		}

		tx = &controlUnitTx{
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("UPDATE 0")},
			},
			rowResults: []storeFakeRow{{err: errors.New("exists failed")}},
		}
		store = mustControlUnitStore(t, tx)
		if err := store.ClearPersonalSiteOverride(
			ctx,
			"https://personal.example",
			2,
			audit,
		); err == nil {
			t.Fatal("exists error = nil")
		}
	})
}

func TestRequestPersonalSiteRescoreWithoutDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	audit := controlUnitAudit(
		"personal-site.rescore.request",
		"https://personal.example",
		control.ResultSuccess,
	)

	t.Run("validation", func(t *testing.T) {
		t.Parallel()
		var store *ControlStore
		if _, err := store.RequestPersonalSiteRescore(
			ctx,
			"https://personal.example",
			audit,
		); !errors.Is(err, errControlStoreUnavailable) {
			t.Fatalf("nil = %v", err)
		}
		unit := mustControlUnitStore(t, &controlUnitTx{})
		if _, err := unit.RequestPersonalSiteRescore(
			ctx,
			"ftp://bad.example",
			audit,
		); !errors.Is(err, errInvalidOrigin) {
			t.Fatalf("origin = %v", err)
		}
		badAudit := audit
		badAudit.Action = "wrong"
		if _, err := unit.RequestPersonalSiteRescore(
			ctx,
			"https://personal.example",
			badAudit,
		); err == nil {
			t.Fatal("bad audit accepted")
		}
	})

	t.Run("success_statuses", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
		tx := &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{{
				values: []any{true, false, (*time.Time)(nil), (*time.Time)(nil), now},
			}},
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("UPDATE 1")},
				{tag: pgconn.NewCommandTag("INSERT 0 1")},
			},
		}
		store := mustControlUnitStore(t, tx)
		status, err := store.RequestPersonalSiteRescore(
			ctx,
			"https://personal.example",
			audit,
		)
		if err != nil || status != controlMutationOK {
			t.Fatalf("status=%q err=%v", status, err)
		}

		pending := now.Add(-time.Minute)
		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{{
				values: []any{true, false, &pending, (*time.Time)(nil), now},
			}},
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("INSERT 0 1")},
			},
		}
		store = mustControlUnitStore(t, tx)
		status, err = store.RequestPersonalSiteRescore(
			ctx,
			"https://personal.example",
			audit,
		)
		if err != nil || status != controlMutationAlreadyPending {
			t.Fatalf("pending status=%q err=%v", status, err)
		}

		active := now.Add(time.Minute)
		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{{
				values: []any{true, false, (*time.Time)(nil), &active, now},
			}},
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("INSERT 0 1")},
			},
		}
		store = mustControlUnitStore(t, tx)
		status, err = store.RequestPersonalSiteRescore(
			ctx,
			"https://personal.example",
			audit,
		)
		if err != nil || status != controlMutationActive {
			t.Fatalf("active status=%q err=%v", status, err)
		}

		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{{
				values: []any{true, false, (*time.Time)(nil), (*time.Time)(nil), now},
			}},
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("UPDATE 0")},
				{tag: pgconn.NewCommandTag("INSERT 0 1")},
			},
		}
		store = mustControlUnitStore(t, tx)
		status, err = store.RequestPersonalSiteRescore(
			ctx,
			"https://personal.example",
			audit,
		)
		if err != nil || status != controlMutationAlreadyPending {
			t.Fatalf("race status=%q err=%v", status, err)
		}
	})

	t.Run("errors", func(t *testing.T) {
		t.Parallel()
		tx := &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{{err: pgx.ErrNoRows}},
		}
		store := mustControlUnitStore(t, tx)
		_, err := store.RequestPersonalSiteRescore(
			ctx,
			"https://personal.example",
			audit,
		)
		if !errors.Is(err, ErrPersonalSiteCandidateUnknown) {
			t.Fatalf("missing = %v", err)
		}

		now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{{
				values: []any{true, true, (*time.Time)(nil), (*time.Time)(nil), now},
			}},
		}
		store = mustControlUnitStore(t, tx)
		_, err = store.RequestPersonalSiteRescore(
			ctx,
			"https://personal.example",
			audit,
		)
		if !errors.Is(err, ErrPersonalSiteCandidateBlocked) {
			t.Fatalf("blocked = %v", err)
		}

		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows([][]any{{"personal.example"}}...)},
			},
		}
		store = mustControlUnitStore(t, tx)
		store.automatic.ExcludedHostSuffixes = "personal.example"
		_, err = store.RequestPersonalSiteRescore(
			ctx,
			"https://personal.example",
			audit,
		)
		if !errors.Is(err, ErrPersonalSiteCandidateBlocked) {
			t.Fatalf("avoided = %v", err)
		}

		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{err: errors.New("avoid query failed")},
			},
		}
		store = mustControlUnitStore(t, tx)
		_, err = store.RequestPersonalSiteRescore(
			ctx,
			"https://personal.example",
			audit,
		)
		if err == nil {
			t.Fatal("avoid error = nil")
		}

		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{{err: errors.New("load failed")}},
		}
		store = mustControlUnitStore(t, tx)
		_, err = store.RequestPersonalSiteRescore(
			ctx,
			"https://personal.example",
			audit,
		)
		if err == nil || !strings.Contains(err.Error(), "load personal site rescore") {
			t.Fatalf("load = %v", err)
		}

		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{{
				values: []any{true, false, (*time.Time)(nil), (*time.Time)(nil), now},
			}},
			execResults: []controlUnitExecResult{
				{err: errors.New("update failed")},
			},
		}
		store = mustControlUnitStore(t, tx)
		_, err = store.RequestPersonalSiteRescore(
			ctx,
			"https://personal.example",
			audit,
		)
		if err == nil {
			t.Fatal("update error = nil")
		}
	})
}

func TestRequestOriginCrawlWithoutDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	audit := controlUnitAudit(
		"origin.crawl.request",
		"https://personal.example",
		control.ResultSuccess,
	)
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

	t.Run("validation", func(t *testing.T) {
		t.Parallel()
		var store *ControlStore
		if _, err := store.RequestOriginCrawl(
			ctx,
			"https://personal.example",
			audit,
		); !errors.Is(err, errControlStoreUnavailable) {
			t.Fatalf("nil = %v", err)
		}
		unit := mustControlUnitStore(t, &controlUnitTx{})
		if _, err := unit.RequestOriginCrawl(
			ctx,
			"ftp://bad.example",
			audit,
		); !errors.Is(err, errInvalidOrigin) {
			t.Fatalf("origin = %v", err)
		}
		badAudit := audit
		badAudit.Action = "wrong"
		if _, err := unit.RequestOriginCrawl(
			ctx,
			"https://personal.example",
			badAudit,
		); err == nil {
			t.Fatal("bad audit accepted")
		}
	})

	t.Run("success_and_status", func(t *testing.T) {
		t.Parallel()
		tx := &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{
				{values: []any{now}},
				{values: []any{false, (*time.Time)(nil)}},
			},
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("SELECT 1")},
				{tag: pgconn.NewCommandTag("INSERT 0 1")},
				{tag: pgconn.NewCommandTag("UPDATE 1")},
				{tag: pgconn.NewCommandTag("INSERT 0 1")},
			},
		}
		store := mustControlUnitStore(t, tx)
		status, err := store.RequestOriginCrawl(
			ctx,
			"https://personal.example",
			audit,
		)
		if err != nil || status != controlMutationOK {
			t.Fatalf("status=%q err=%v", status, err)
		}

		active := now.Add(time.Minute)
		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{
				{values: []any{now}},
				{values: []any{false, &active}},
			},
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("INSERT 0 1")},
			},
		}
		store = mustControlUnitStore(t, tx)
		status, err = store.RequestOriginCrawl(
			ctx,
			"https://personal.example",
			audit,
		)
		if err != nil || status != controlMutationActive {
			t.Fatalf("active status=%q err=%v", status, err)
		}

		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{
				{values: []any{now}},
				{values: []any{false, (*time.Time)(nil)}},
			},
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("SELECT 1")},
				{tag: pgconn.NewCommandTag("INSERT 0 1")},
				{tag: pgconn.NewCommandTag("UPDATE 0")},
				{tag: pgconn.NewCommandTag("INSERT 0 1")},
			},
		}
		store = mustControlUnitStore(t, tx)
		status, err = store.RequestOriginCrawl(
			ctx,
			"https://personal.example",
			audit,
		)
		if err != nil || status != controlMutationAlreadyPending {
			t.Fatalf("pending status=%q err=%v", status, err)
		}
	})

	t.Run("errors", func(t *testing.T) {
		t.Parallel()
		tx := &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows([]any{"personal.example"})},
			},
		}
		store := mustControlUnitStore(t, tx)
		_, err := store.RequestOriginCrawl(
			ctx,
			"https://personal.example",
			audit,
		)
		if !errors.Is(err, ErrPersonalSiteCandidateBlocked) {
			t.Fatalf("avoided = %v", err)
		}

		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{
				{values: []any{now}},
				{err: pgx.ErrNoRows},
			},
		}
		store = mustControlUnitStore(t, tx)
		_, err = store.RequestOriginCrawl(
			ctx,
			"https://personal.example",
			audit,
		)
		if !errors.Is(err, ErrOriginCrawlUnknown) {
			t.Fatalf("unknown = %v", err)
		}

		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{
				{values: []any{now}},
				{values: []any{true, (*time.Time)(nil)}},
			},
		}
		store = mustControlUnitStore(t, tx)
		_, err = store.RequestOriginCrawl(
			ctx,
			"https://personal.example",
			audit,
		)
		if !errors.Is(err, ErrOriginCrawlBlocked) {
			t.Fatalf("blocked = %v", err)
		}

		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{
				{err: errors.New("clock failed")},
			},
		}
		store = mustControlUnitStore(t, tx)
		_, err = store.RequestOriginCrawl(
			ctx,
			"https://personal.example",
			audit,
		)
		if err == nil || !strings.Contains(err.Error(), "crawl request clock") {
			t.Fatalf("clock = %v", err)
		}

		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{
				{values: []any{now}},
				{err: errors.New("load failed")},
			},
		}
		store = mustControlUnitStore(t, tx)
		_, err = store.RequestOriginCrawl(
			ctx,
			"https://personal.example",
			audit,
		)
		if err == nil || !strings.Contains(err.Error(), "load crawl source") {
			t.Fatalf("load = %v", err)
		}

		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{
				{values: []any{now}},
				{values: []any{false, (*time.Time)(nil)}},
			},
			execResults: []controlUnitExecResult{
				{err: errors.New("lock failed")},
			},
		}
		// negative advisory key path uses discoveryScheduleLockErr on policy tx;
		// for controlUnitTx, first Exec is the advisory lock via lockDiscoveryScheduleOrigin
		// which uses pg_advisory_xact_lock - controlUnitTx just returns scripted exec.
		store = mustControlUnitStore(t, tx)
		_, err = store.RequestOriginCrawl(
			ctx,
			"https://personal.example",
			audit,
		)
		if err == nil {
			t.Fatal("lock error = nil")
		}

		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{
				{values: []any{now}},
				{values: []any{false, (*time.Time)(nil)}},
			},
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("SELECT 1")},
				{err: errors.New("schedule failed")},
			},
		}
		store = mustControlUnitStore(t, tx)
		_, err = store.RequestOriginCrawl(
			ctx,
			"https://personal.example",
			audit,
		)
		if err == nil || !strings.Contains(err.Error(), "ensure crawl schedule") {
			t.Fatalf("schedule = %v", err)
		}

		tx = &controlUnitTx{
			queryResults: []storeFakeQueryResult{
				{rows: newStoreFakeRows()},
			},
			rowResults: []storeFakeRow{
				{values: []any{now}},
				{values: []any{false, (*time.Time)(nil)}},
			},
			execResults: []controlUnitExecResult{
				{tag: pgconn.NewCommandTag("SELECT 1")},
				{tag: pgconn.NewCommandTag("INSERT 0 1")},
				{err: errors.New("nudge failed")},
			},
		}
		store = mustControlUnitStore(t, tx)
		_, err = store.RequestOriginCrawl(
			ctx,
			"https://personal.example",
			audit,
		)
		if err == nil || !strings.Contains(err.Error(), "nudge crawl source") {
			t.Fatalf("nudge = %v", err)
		}
	})
}

func TestRejectDomainAvoidedOriginWithoutDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	candidate := mustStoreOrigin(t, "https://blocked.example")

	tx := &controlUnitTx{
		queryResults: []storeFakeQueryResult{
			{err: errors.New("avoid failed")},
		},
	}
	if err := rejectDomainAvoidedOrigin(
		ctx,
		tx,
		candidate,
		AutomaticCrawlConfig{},
	); err == nil {
		t.Fatal("error = nil")
	}

	tx = &controlUnitTx{
		queryResults: []storeFakeQueryResult{
			{rows: newStoreFakeRows([][]any{{"blocked.example"}}...)},
		},
	}
	if err := rejectDomainAvoidedOrigin(
		ctx,
		tx,
		candidate,
		AutomaticCrawlConfig{},
	); !errors.Is(err, ErrPersonalSiteCandidateBlocked) {
		t.Fatalf("error = %v", err)
	}

	tx = &controlUnitTx{
		queryResults: []storeFakeQueryResult{
			{rows: newStoreFakeRows()},
		},
	}
	if err := rejectDomainAvoidedOrigin(
		ctx,
		tx,
		candidate,
		AutomaticCrawlConfig{},
	); err != nil {
		t.Fatalf("error = %v", err)
	}
}

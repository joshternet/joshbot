package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/joshternet/joshbot/internal/discovery"
	"github.com/joshternet/joshbot/internal/retry"
)

type completeAutomaticClock struct {
	now time.Time
	err error
}

func (clock completeAutomaticClock) NowTransaction(
	context.Context,
	pgx.Tx,
) (time.Time, error) {
	return clock.now, clock.err
}

func TestCompleteAutomaticCandidatesDatabasePathsWithoutDatabase(
	t *testing.T,
) {
	ctx := context.Background()
	now := time.Date(2026, time.September, 18, 21, 0, 0, 0, time.UTC)
	candidateOrigin := mustStoreOrigin(t, "https://candidate.example")
	candidate := discovery.Candidate{
		Origin: candidateOrigin,
		Kind:   discovery.KindLink,
	}
	successResult := AutomaticCandidateResult{Candidate: candidate}

	t.Run("advisory lock failure", func(t *testing.T) {
		testErr := errors.New("test automatic admission lock failure")
		tx := &discoveryPolicyUnitTx{
			execResults: []discoveryUnitExecResult{{err: testErr}},
		}
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(
			t,
			err,
			testErr,
			"store: lock automatic admission",
		)
	})

	t.Run("unknown run", func(t *testing.T) {
		tx := completeAutomaticBaseTx(1, 0, 0, nil)
		tx.rowResults[0] = discoveryUnitRow{err: pgx.ErrNoRows}
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		if !errors.Is(err, errUnknownCrawlRun) {
			t.Fatalf(
				"CompleteAutomaticCandidates() error = %v, want %v",
				err,
				errUnknownCrawlRun,
			)
		}
	})

	t.Run("run read failure", func(t *testing.T) {
		testErr := errors.New("test automatic admission run read failure")
		tx := completeAutomaticBaseTx(1, 0, 0, nil)
		tx.rowResults[0] = discoveryUnitRow{err: testErr}
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(t, err, testErr, "read automatic admission run")
	})

	t.Run("exclusion read failure", func(t *testing.T) {
		testErr := errors.New("test automatic admission exclusion failure")
		tx := completeAutomaticBaseTx(1, 0, 0, nil)
		tx.queryResults[0] = discoveryUnitQueryResult{err: testErr}
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(
			t,
			err,
			testErr,
			"store: read crawl domain avoid rules",
		)
	})

	t.Run("pending probe count failure", func(t *testing.T) {
		testErr := errors.New("test pending probe count failure")
		tx := completeAutomaticBaseTx(1, 0, 0, nil)
		tx.rowResults[1] = discoveryUnitRow{err: testErr}
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(t, err, testErr, "count pending probes")
	})

	t.Run("clock failure", func(t *testing.T) {
		testErr := errors.New("test automatic admission clock failure")
		tx := completeAutomaticBaseTx(1, 0, 0, nil)
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{err: testErr},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(t, err, testErr, "store: read discovery clock")
	})

	t.Run("batch read failure", func(t *testing.T) {
		testErr := errors.New("test automatic admission batch failure")
		tx := completeAutomaticBaseTx(1, 0, 0, nil)
		tx.rowResults[2] = discoveryUnitRow{err: testErr}
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(
			t,
			err,
			testErr,
			"collect automatic admission batch",
		)
	})

	t.Run("invalid batch origin", func(t *testing.T) {
		tx := completeAutomaticBaseTx(1, 0, 0, []string{"ftp://example.com"})
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		if err == nil || !strings.Contains(
			err.Error(),
			"invalid automatic admission candidate",
		) {
			t.Fatalf("CompleteAutomaticCandidates() error = %v", err)
		}
	})

	t.Run("transient streak read failure", func(t *testing.T) {
		testErr := errors.New("test admission retry streak read failure")
		tx := completeAutomaticBaseTx(
			1,
			0,
			0,
			[]string{candidateOrigin.String()},
		)
		tx.rowResults = append(tx.rowResults, discoveryUnitRow{err: testErr})
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{{
				Candidate:       candidate,
				FailureCategory: retry.CategoryDNS,
			}},
		)
		assertCompleteAutomaticError(t, err, testErr, "read admission retry streak")
	})

	t.Run("transient candidate update failure", func(t *testing.T) {
		testErr := errors.New("test transient candidate update failure")
		tx := completeAutomaticBaseTx(
			1,
			0,
			0,
			[]string{candidateOrigin.String()},
		)
		tx.rowResults = append(tx.rowResults, discoveryUnitRow{values: []any{2}})
		tx.execResults = append(tx.execResults, discoveryUnitExecResult{err: testErr})
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{{
				Candidate:       candidate,
				FailureCategory: retry.CategoryDNS,
			}},
		)
		assertCompleteAutomaticError(
			t,
			err,
			testErr,
			"defer transient admission failure",
		)
	})

	t.Run("transient outcome update failure", func(t *testing.T) {
		testErr := errors.New("test transient outcome update failure")
		tx := completeAutomaticBaseTx(
			1,
			0,
			0,
			[]string{candidateOrigin.String()},
		)
		tx.rowResults = append(tx.rowResults, discoveryUnitRow{values: []any{2}})
		tx.execResults = append(
			tx.execResults,
			completeAutomaticSuccessExec(),
			discoveryUnitExecResult{err: testErr},
		)
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{{
				Candidate:       candidate,
				FailureCategory: retry.CategoryDNS,
			}},
		)
		assertCompleteAutomaticError(
			t,
			err,
			testErr,
			"record deferred admission outcome",
		)
	})

	t.Run("network rejected update failure", func(t *testing.T) {
		testErr := errors.New("test network rejected update failure")
		tx := completeAutomaticBaseTx(
			1,
			0,
			0,
			[]string{candidateOrigin.String()},
		)
		tx.execResults = append(tx.execResults, discoveryUnitExecResult{err: testErr})
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{{
				Candidate:       candidate,
				FailureCategory: retry.CategoryUnsafeAddress,
			}},
		)
		assertCompleteAutomaticError(
			t,
			err,
			testErr,
			"record network-rejected admission",
		)
	})

	t.Run("reset retry streak failure", func(t *testing.T) {
		testErr := errors.New("test retry streak reset failure")
		tx := completeAutomaticBaseTx(
			1,
			0,
			0,
			[]string{candidateOrigin.String()},
		)
		tx.execResults = append(tx.execResults, discoveryUnitExecResult{err: testErr})
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(t, err, testErr, "reset admission retry streak")
	})

	t.Run("admission state read failure", func(t *testing.T) {
		testErr := errors.New("test admission state read failure")
		tx := completeAutomaticBaseTx(
			1,
			0,
			0,
			[]string{candidateOrigin.String()},
		)
		tx.execResults = append(tx.execResults, completeAutomaticSuccessExec())
		tx.rowResults = append(tx.rowResults, discoveryUnitRow{err: testErr})
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(t, err, testErr, "read candidate admission state")
	})

	t.Run("policy deferred update failure", func(t *testing.T) {
		testErr := errors.New("test policy deferred update failure")
		tx := completeAutomaticBaseTx(
			1,
			0,
			0,
			[]string{candidateOrigin.String()},
		)
		tx.execResults = append(
			tx.execResults,
			completeAutomaticSuccessExec(),
			discoveryUnitExecResult{err: testErr},
		)
		tx.rowResults = append(
			tx.rowResults,
			discoveryUnitRow{values: []any{false, true, false}},
		)
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(
			t,
			err,
			testErr,
			"record policy-deferred admission",
		)
	})

	t.Run("existing source update failure", func(t *testing.T) {
		testErr := errors.New("test existing source update failure")
		tx := completeAutomaticBaseTx(
			1,
			0,
			0,
			[]string{candidateOrigin.String()},
		)
		tx.execResults = append(
			tx.execResults,
			completeAutomaticSuccessExec(),
			discoveryUnitExecResult{err: testErr},
		)
		tx.rowResults = append(
			tx.rowResults,
			discoveryUnitRow{values: []any{true, false, false}},
		)
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(
			t,
			err,
			testErr,
			"record existing automatic source",
		)
	})

	t.Run("probe scheduling failure", func(t *testing.T) {
		testErr := errors.New("test automatic probe scheduling failure")
		tx := completeAutomaticBaseTx(
			1,
			0,
			0,
			[]string{candidateOrigin.String()},
		)
		tx.execResults = append(
			tx.execResults,
			completeAutomaticSuccessExec(),
			discoveryUnitExecResult{err: testErr},
		)
		tx.rowResults = append(
			tx.rowResults,
			discoveryUnitRow{values: []any{false, false, false}},
		)
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(
			t,
			err,
			testErr,
			"schedule automatic candidate probe",
		)
	})

	t.Run("capacity deferred update failure", func(t *testing.T) {
		testErr := errors.New("test capacity deferred update failure")
		tx := completeAutomaticBaseTx(
			0,
			0,
			0,
			[]string{candidateOrigin.String()},
		)
		tx.execResults = append(
			tx.execResults,
			completeAutomaticSuccessExec(),
			completeAutomaticSuccessExec(),
			discoveryUnitExecResult{err: testErr},
		)
		tx.rowResults = append(
			tx.rowResults,
			discoveryUnitRow{values: []any{false, false, false}},
		)
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(
			t,
			err,
			testErr,
			"record capacity-deferred admission",
		)
	})

	t.Run("promotion insert failure", func(t *testing.T) {
		testErr := errors.New("test automatic source promotion failure")
		tx := completeAutomaticBaseTx(
			1,
			0,
			0,
			[]string{candidateOrigin.String()},
		)
		tx.execResults = append(
			tx.execResults,
			completeAutomaticSuccessExec(),
			completeAutomaticSuccessExec(),
			discoveryUnitExecResult{err: testErr},
		)
		tx.rowResults = append(
			tx.rowResults,
			discoveryUnitRow{values: []any{false, false, false}},
		)
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(t, err, testErr, "promote automatic candidate")
	})

	t.Run("promotion outcome failure", func(t *testing.T) {
		testErr := errors.New("test promoted outcome failure")
		tx := completeAutomaticBaseTx(
			1,
			0,
			0,
			[]string{candidateOrigin.String()},
		)
		tx.execResults = append(
			tx.execResults,
			completeAutomaticSuccessExec(),
			completeAutomaticSuccessExec(),
			completeAutomaticSuccessExec(),
			discoveryUnitExecResult{err: testErr},
		)
		tx.rowResults = append(
			tx.rowResults,
			discoveryUnitRow{values: []any{false, false, false}},
		)
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(t, err, testErr, "record promoted admission")
	})

	t.Run("promotion count update failure", func(t *testing.T) {
		testErr := errors.New("test promotion count update failure")
		tx := completeAutomaticBaseTx(
			1,
			0,
			0,
			[]string{candidateOrigin.String()},
		)
		tx.execResults = append(
			tx.execResults,
			completeAutomaticSuccessExec(),
			completeAutomaticSuccessExec(),
			completeAutomaticSuccessExec(),
			completeAutomaticSuccessExec(),
			discoveryUnitExecResult{err: testErr},
		)
		tx.rowResults = append(
			tx.rowResults,
			discoveryUnitRow{values: []any{false, false, false}},
		)
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(
			t,
			err,
			testErr,
			"update automatic promotion count",
		)
	})

	t.Run("promotion telemetry update failure", func(t *testing.T) {
		testErr := errors.New("test promotion telemetry update failure")
		tx := completeAutomaticBaseTx(
			1,
			0,
			0,
			[]string{candidateOrigin.String()},
		)
		tx.execResults = append(
			tx.execResults,
			completeAutomaticSuccessExec(),
			completeAutomaticSuccessExec(),
			completeAutomaticSuccessExec(),
			completeAutomaticSuccessExec(),
			completeAutomaticSuccessExec(),
			discoveryUnitExecResult{err: testErr},
		)
		tx.rowResults = append(
			tx.rowResults,
			discoveryUnitRow{values: []any{false, false, false}},
		)
		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{successResult},
		)
		assertCompleteAutomaticError(
			t,
			err,
			testErr,
			"update original-run promotion telemetry",
		)
	})

	t.Run("mixed outcomes success", func(t *testing.T) {
		transientOrigin := mustStoreOrigin(t, "https://transient.example")
		unsafeOrigin := mustStoreOrigin(t, "https://unsafe.example")
		policyOrigin := mustStoreOrigin(t, "https://policy.example")
		existingOrigin := mustStoreOrigin(t, "https://existing.example")
		capacityOrigin := mustStoreOrigin(t, "https://capacity.example")
		promotedOrigin := candidateOrigin

		tx := completeAutomaticBaseTx(
			1,
			0,
			0,
			[]string{
				transientOrigin.String(),
				unsafeOrigin.String(),
				policyOrigin.String(),
				existingOrigin.String(),
				promotedOrigin.String(),
				capacityOrigin.String(),
			},
		)
		tx.rowResults = append(
			tx.rowResults,
			discoveryUnitRow{values: []any{1}},
			discoveryUnitRow{values: []any{false, true, false}},
			discoveryUnitRow{values: []any{true, false, false}},
			discoveryUnitRow{values: []any{false, false, false}},
			discoveryUnitRow{values: []any{false, false, true}},
		)
		for index := 0; index < 15; index++ {
			tx.execResults = append(
				tx.execResults,
				completeAutomaticSuccessExec(),
			)
		}

		err := callCompleteAutomatic(
			t,
			ctx,
			tx,
			completeAutomaticClock{now: now},
			[]AutomaticCandidateResult{
				{
					Candidate: discovery.Candidate{
						Origin: transientOrigin,
						Kind:   discovery.KindLink,
					},
					FailureCategory: retry.CategoryDNS,
				},
				{
					Candidate: discovery.Candidate{
						Origin: unsafeOrigin,
						Kind:   discovery.KindLink,
					},
					FailureCategory: retry.CategoryUnsafeAddress,
				},
				{
					Candidate: discovery.Candidate{
						Origin: policyOrigin,
						Kind:   discovery.KindLink,
					},
				},
				{
					Candidate: discovery.Candidate{
						Origin: existingOrigin,
						Kind:   discovery.KindLink,
					},
				},
				{
					Candidate: discovery.Candidate{
						Origin: promotedOrigin,
						Kind:   discovery.KindLink,
					},
				},
				{
					Candidate: discovery.Candidate{
						Origin: capacityOrigin,
						Kind:   discovery.KindLink,
					},
				},
			},
		)
		if err != nil {
			t.Fatalf(
				"CompleteAutomaticCandidates() error = %v",
				err,
			)
		}
	})
}

func callCompleteAutomatic(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	clock transactionQueueClock,
	results []AutomaticCandidateResult,
) error {
	t.Helper()

	store := completeAutomaticStore(t, tx, clock)
	return store.CompleteAutomaticCandidates(ctx, 1, results)
}

func assertCompleteAutomaticError(
	t *testing.T,
	err error,
	want error,
	text string,
) {
	t.Helper()

	if !errors.Is(err, want) || !strings.Contains(err.Error(), text) {
		t.Fatalf(
			"CompleteAutomaticCandidates() error = %v",
			err,
		)
	}
}

func completeAutomaticSuccessExec() discoveryUnitExecResult {
	return discoveryUnitExecResult{
		tag: pgconn.NewCommandTag("UPDATE 1"),
	}
}

func completeAutomaticBaseTx(
	maxPromotions int,
	promotions int,
	pendingProbes int,
	batch []string,
) *discoveryPolicyUnitTx {
	return &discoveryPolicyUnitTx{
		execResults: []discoveryUnitExecResult{
			{
				tag: pgconn.NewCommandTag("SELECT 1"),
			},
		},
		rowResults: []discoveryUnitRow{
			{values: []any{maxPromotions, promotions}},
			{values: []any{pendingProbes}},
			{values: []any{batch}},
		},
		queryResults: []discoveryUnitQueryResult{
			{rows: &discoveryUnitRows{}},
		},
	}
}

func completeAutomaticStore(
	t *testing.T,
	tx pgx.Tx,
	clock transactionQueueClock,
) *DiscoveryStore {
	t.Helper()

	store, err := newDiscoveryStoreWithConfig(
		discoveryPolicyDatabase(tx),
		clock,
		AutomaticCrawlConfig{Enabled: true},
	)
	if err != nil {
		t.Fatalf(
			"newDiscoveryStoreWithConfig() error = %v",
			err,
		)
	}

	return store
}

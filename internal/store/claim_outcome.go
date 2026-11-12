package store

// Goal & Constraints: Distinguish final claim outcomes from diagnostic skip
// counters without changing the public Claim (Lease, bool, error) contract.
// Busy/locked skips must not count as claim failures or mutate retry state.

const claimCandidateBatchSize = 16

// claimMaxCandidatesExamined bounds one claim transaction's candidate scan.
// Tests may lower it to exercise deferred budget exhaustion.
var claimMaxCandidatesExamined = 256

// ClaimOutcome is the final result of one claim attempt.
type ClaimOutcome string

const (
	// ClaimOutcomeSuccess means work was claimed.
	ClaimOutcomeSuccess ClaimOutcome = "success"
	// ClaimOutcomeNoWork means no claimable candidates and no temporary contention.
	ClaimOutcomeNoWork ClaimOutcome = "no_work"
	// ClaimOutcomeDeferred means budget exhausted or only temporary unavailability.
	ClaimOutcomeDeferred ClaimOutcome = "deferred"
	// ClaimOutcomeError means a genuine database or protocol failure.
	ClaimOutcomeError ClaimOutcome = "error"
)

// ClaimStats captures claim diagnostics for metrics and tests.
type ClaimStats struct {
	Outcome            ClaimOutcome
	BusySkips          int
	LockedSkips        int
	IneligibleSkips    int
	CandidatesExamined int
	LockTimeouts       int
}

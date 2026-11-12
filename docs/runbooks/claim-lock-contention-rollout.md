# Claim Lock Contention Rollout

Goal & Constraints: Ship the fail-fast neighborhood claim protocol without
weakening durable domain exclusivity, discovery’s global claim lock, or
neighborhood politeness. Authorize deploy separately after local/test evidence.

## Pre-deploy checks

- [ ] `go test ./internal/store/ ./internal/worker/` green locally against
      PostgreSQL test database
- [ ] Contention tests pass:
      `TestSameNeighborhoodClaimsDoNotBlockOnUpsert`,
      `TestClaimFindsWorkBehindBusyNeighborhoodBatch`,
      `TestClaimReportsDeferredForBusyOnlyQueue`,
      `TestClaimDistinguishesLockedRowFromIneligibleCandidate`
- [ ] Staging or canary workers show claim errors dropping while due work drains
- [ ] Heartbeats show `claim_deferred` idle messages under contention, not a
      surge of `claim_failed`

## Deploy

1. Deploy build to one verification worker first (canary).
2. Watch for 15–30 minutes: due-work trend, claim failures, live
   `domain_inflight_leases`, and absence of deadlock/lock_timeout noise on
   renew/complete.
3. Expand to remaining workers, then discovery if the same binary is shared.

## Rollback

1. Redeploy the previous known-good worker/discovery image/binary.
2. Confirm claim path returns to prior behavior; no schema migration is required
   for this change, so rollback is binary-only.
3. If canary alone is bad, stop that instance and leave peers on the prior build.

## Do not

- Commit, push, merge, or deploy without explicit authorization
- Change production pause flags or mutate live queue rows as part of this fix
- Broaden `lock_timeout` beyond acquisition-scoped `set_config(..., true)`

# Legacy terminal-miss reprobe cleanup

**Authorization:** Do not run this procedure in production without independent
explicit authorization. Deploying the application change that stops creating
delayed reprobes does **not** authorize backlog deletion. `joshbot migrate`
must not apply this SQL.

**Goal:** Retire legacy `verification_queue` rows with `mode = 'reprobe'` that
remain from the old timer ladder, without deleting recurring work, active
leases, observations, candidates, edges, or provenance.

## Preconditions

1. Application build with terminal-miss queue removal and admission freshness
   (including robots-denied admission suppression) is already deployed.
2. Verification pause state is known (`crawl_control.verification_paused`).
3. Capture preflight counts (save the output):

```sql
SELECT mode, count(*) AS rows
FROM verification_queue
GROUP BY mode
ORDER BY mode;

SELECT
  count(*) FILTER (
    WHERE lease_owner IS NULL
  ) AS unleased,
  count(*) FILTER (
    WHERE lease_owner IS NOT NULL
      AND lease_expires_at > clock_timestamp()
  ) AS active_lease,
  count(*) FILTER (
    WHERE lease_owner IS NOT NULL
      AND (
        lease_expires_at IS NULL
        OR lease_expires_at <= clock_timestamp()
      )
  ) AS expired_lease,
  min(available_at) AS oldest_available_at,
  max(available_at) AS newest_available_at
FROM verification_queue
WHERE mode = 'reprobe';
```

## Eligible rows

Delete only `mode = 'reprobe'` rows that are:

- unleased (`lease_owner IS NULL`), or
- expired (`lease_expires_at IS NOT NULL AND lease_expires_at <= now()`).

Expired rows may still show a non-null `lease_owner` until reclaim/complete.
Those are eligible once expiry is confirmed.

Never delete:

- `mode = 'recurring'`
- actively leased reprobes (`lease_owner IS NOT NULL AND lease_expires_at > now()`)
- observations, candidates, edges, origins, audit history

## Cleanup statement

Run in bounded batches if lock duration is a concern. The statement also clears
matching `domain_inflight_leases` for deleted generations so domain exclusivity
does not linger after expired legacy work is retired.

```sql
WITH deleted AS (
  DELETE FROM verification_queue AS queued
  WHERE queued.ctid IN (
    SELECT queued.ctid
    FROM verification_queue AS queued
    WHERE queued.mode = 'reprobe'
      AND (
        queued.lease_owner IS NULL
        OR (
          queued.lease_expires_at IS NOT NULL
          AND queued.lease_expires_at <= clock_timestamp()
        )
      )
    ORDER BY queued.origin
    LIMIT 5000
  )
  RETURNING
    queued.origin,
    queued.lease_generation
)
DELETE FROM domain_inflight_leases AS inflight
USING deleted
WHERE inflight.holder_origin = deleted.origin
  AND inflight.lease_generation = deleted.lease_generation;
```

Repeat until the batch deletes zero queue rows. The procedure is idempotent.

## Residual active leases

1. Leave actively leased reprobes alone.
2. Existing claim/complete/abandon paths remove them under the new completion
   policy (terminal miss deletes the row) or return them to an expired/unleased
   state.
3. After leases expire or complete, run the same cleanup again under a separate
   authorization if residuals remain.

**Done when:** `SELECT count(*) FROM verification_queue WHERE mode = 'reprobe'`
is `0` on consecutive checks after sustained discovery activity (or only
documented intentional residuals remain).

## Post-cleanup validation

```sql
SELECT mode, count(*) FROM verification_queue GROUP BY mode ORDER BY mode;

SELECT count(*) AS residual_reprobes
FROM verification_queue
WHERE mode = 'reprobe';

SELECT count(*) AS due_recurring
FROM verification_queue
WHERE mode = 'recurring'
  AND available_at <= clock_timestamp();
```

Confirm:

1. Unleased and expired reprobes are gone.
2. Recurring and probe work remain healthy.
3. Terminal-miss origins are not re-queued without
   `last_discovered_at > observed_at`.
4. Domain-inflight slots are not stuck for cleaned origins.

## Rollback limits

Restoring an older application image can recreate delayed reprobes on new
terminal completions. Deleted queue rows are not reconstructed by rollback.
Historical observations and discovery provenance remain intact.

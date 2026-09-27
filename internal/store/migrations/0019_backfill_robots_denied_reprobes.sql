-- Restore historical robots-denied verification work that left the queue
-- before robots_denied became a reprobe outcome.
--
-- Only origins whose latest observation is robots_denied are eligible.
-- Existing queue rows are preserved.
--
-- Historical work is admitted gradually over fourteen days so recovery does
-- not displace fresh discovery. The first return is never scheduled earlier
-- than twelve hours after the robots-denied observation.

WITH latest AS (
    SELECT DISTINCT ON (origin)
        origin,
        observed_at,
        outcome
    FROM verification_observations
    ORDER BY
        origin,
        observed_at DESC,
        id DESC
),
candidates AS (
    SELECT
        latest.origin,
        latest.observed_at,
        row_number() OVER (
            ORDER BY
                latest.observed_at ASC,
                latest.origin ASC
        ) AS position,
        count(*) OVER () AS total
    FROM latest
    WHERE latest.outcome = 'robots_denied'
      AND NOT EXISTS (
          SELECT 1
          FROM verification_queue
          WHERE verification_queue.origin = latest.origin
      )
),
scheduled AS (
    SELECT
        origin,
        GREATEST(
            observed_at + interval '12 hours',
            statement_timestamp()
                + interval '14 days'
                * (
                    (position - 1)::double precision
                    / GREATEST(total, 1)::double precision
                )
        ) AS available_at
    FROM candidates
)
INSERT INTO verification_queue (
    origin,
    available_at,
    mode
)
SELECT
    origin,
    available_at,
    'reprobe'
FROM scheduled
ON CONFLICT (origin) DO NOTHING;
CREATE INDEX verification_queue_claim_priority_idx
    ON verification_queue (
        (CASE
            WHEN mode = 'probe' THEN 0
            ELSE 1
        END),
        available_at,
        origin
    );

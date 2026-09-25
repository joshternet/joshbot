-- Restore verification work that was removed before reprobe queue semantics
-- were introduced.
--
-- Before reprobes existed, terminal non-participation outcomes such as
-- absent, invalid, unsupported_version, and cross_origin_redirect caused
-- one-shot probe work to be removed from verification_queue. Those origins
-- therefore had no path to notice a declaration published later unless they
-- happened to be rediscovered or explicitly scheduled by an operator.
--
-- Only the latest observation for each origin determines eligibility for this
-- repair. Origins whose latest result is unavailable, robots_denied, valid,
-- or any other outcome are intentionally left unchanged.
--
-- Historical work is admitted gradually over fourteen days instead of making
-- the entire backlog immediately eligible. Oldest observations are scheduled
-- first. Once one of these origins completes verification, normal reprobe
-- scheduling takes over.
--
-- ON CONFLICT protects against an origin being independently scheduled or
-- rediscovered while this migration is running.

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
    WHERE latest.outcome IN (
        'absent',
        'invalid',
        'unsupported_version',
        'cross_origin_redirect'
    )
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
            observed_at + interval '24 hours',
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

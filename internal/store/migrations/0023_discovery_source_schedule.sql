CREATE TABLE discovery_source_schedule (
    source_origin TEXT PRIMARY KEY,

    CONSTRAINT discovery_source_schedule_origin_check
        CHECK (source_origin <> '')
);

WITH effective_verification AS (
    SELECT DISTINCT ON (origin)
        origin,
        observed_at,
        outcome
    FROM verification_observations
    WHERE outcome IN (
        'valid',
        'absent',
        'invalid',
        'unsupported_version',
        'cross_origin_redirect'
    )
    ORDER BY
        origin,
        observed_at DESC,
        id DESC
),
scheduled_sources AS (
    SELECT
        state.source_origin
    FROM discovery_source_state AS state
    LEFT JOIN effective_verification AS effective
        ON effective.origin = state.source_origin
    LEFT JOIN discovery_candidates AS candidate
        ON candidate.origin = state.source_origin
    WHERE
        state.seeded
        OR (
            state.automatically_discovered
            AND (
                effective.origin IS NULL
                OR candidate.last_discovered_at >
                    effective.observed_at
            )
        )

    UNION

    SELECT
        effective.origin
    FROM effective_verification AS effective
    WHERE effective.outcome = 'valid'

    UNION

    SELECT
        stored_origin.origin
    FROM origins AS stored_origin
    JOIN discovery_candidates AS candidate
        ON candidate.origin = stored_origin.origin
    JOIN effective_verification AS effective
        ON effective.origin = stored_origin.origin
    WHERE stored_origin.first_participated_at IS NOT NULL
        AND effective.outcome <> 'valid'
        AND candidate.last_discovered_at >
            effective.observed_at
)
INSERT INTO discovery_source_schedule (
    source_origin
)
SELECT source_origin
FROM scheduled_sources;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_roles
        WHERE rolname = 'joshbot_app'
    ) THEN
        GRANT DELETE
            ON TABLE discovery_source_schedule
            TO joshbot_app;
    END IF;
END
$$;

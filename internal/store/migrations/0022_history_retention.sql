CREATE TABLE verification_reprobe_state (
    origin TEXT PRIMARY KEY
        REFERENCES origins (origin)
        ON DELETE CASCADE,
    miss_count BIGINT NOT NULL DEFAULT 0,

    CONSTRAINT verification_reprobe_state_miss_count_check
        CHECK (miss_count >= 0)
);

INSERT INTO verification_reprobe_state (
    origin,
    miss_count
)
SELECT
    stored_origin.origin,
    count(observation.id) FILTER (
        WHERE observation.outcome IN (
            'absent',
            'invalid',
            'unsupported_version',
            'robots_denied',
            'cross_origin_redirect'
        )
    )
FROM origins AS stored_origin
LEFT JOIN verification_observations AS observation
    ON observation.origin = stored_origin.origin
GROUP BY stored_origin.origin;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_roles
        WHERE rolname = 'joshbot_app'
    ) THEN
        GRANT DELETE
            ON TABLE
                verification_observations,
                verification_queue_events,
                crawl_service_heartbeats
            TO joshbot_app;
    END IF;
END
$$;

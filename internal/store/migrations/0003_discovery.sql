ALTER TABLE verification_queue
    ADD COLUMN mode TEXT NOT NULL DEFAULT 'recurring';

ALTER TABLE verification_queue
    ADD CONSTRAINT verification_queue_mode_check
    CHECK (
        mode IN (
            'recurring',
            'probe'
        )
    );

CREATE TABLE discovery_source_state (
    source_origin TEXT PRIMARY KEY
        REFERENCES origins (origin),
    last_attempted_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX discovery_source_state_attempt_order_idx
    ON discovery_source_state (
        last_attempted_at,
        source_origin
    );

CREATE TABLE discovery_candidates (
    origin TEXT PRIMARY KEY,
    first_discovered_at TIMESTAMPTZ NOT NULL,
    last_discovered_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT discovery_candidates_origin_check
        CHECK (origin <> ''),

    CONSTRAINT discovery_candidates_time_order_check
        CHECK (
            first_discovered_at <= last_discovered_at
        )
);

CREATE TABLE discovery_edges (
    source_origin TEXT NOT NULL
        REFERENCES origins (origin),
    candidate_origin TEXT NOT NULL
        REFERENCES discovery_candidates (origin),
    kind TEXT NOT NULL,
    first_discovered_at TIMESTAMPTZ NOT NULL,
    last_discovered_at TIMESTAMPTZ NOT NULL,

    PRIMARY KEY (
        source_origin,
        candidate_origin,
        kind
    ),

    CONSTRAINT discovery_edges_kind_check
        CHECK (
            kind IN (
                'link',
                'redirect'
            )
        ),

    CONSTRAINT discovery_edges_distinct_origins_check
        CHECK (
            source_origin <> candidate_origin
        ),

    CONSTRAINT discovery_edges_time_order_check
        CHECK (
            first_discovered_at <= last_discovered_at
        )
);

CREATE INDEX discovery_edges_candidate_idx
    ON discovery_edges (
        candidate_origin,
        source_origin,
        kind
    );

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_roles
        WHERE rolname = 'joshbot_app'
    ) THEN
        GRANT DELETE
            ON TABLE verification_queue
            TO joshbot_app;
    END IF;
END
$$;
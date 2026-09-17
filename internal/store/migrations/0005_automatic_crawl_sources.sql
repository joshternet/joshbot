ALTER TABLE discovery_source_state
    ADD COLUMN automatically_discovered BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE discovery_source_state
    ADD COLUMN crawl_blocked BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX discovery_source_state_automatic_attempt_idx
    ON discovery_source_state (
        last_attempted_at,
        source_origin
    )
    WHERE automatically_discovered
        AND NOT crawl_blocked;

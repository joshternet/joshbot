ALTER TABLE discovery_source_state
    DROP CONSTRAINT discovery_source_state_source_origin_fkey;

ALTER TABLE discovery_source_state
    ALTER COLUMN last_attempted_at DROP NOT NULL;

ALTER TABLE discovery_source_state
    ADD COLUMN seeded BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE discovery_source_state
    ADD CONSTRAINT discovery_source_state_origin_check
    CHECK (source_origin <> '');

ALTER TABLE discovery_edges
    DROP CONSTRAINT discovery_edges_source_origin_fkey;

ALTER TABLE discovery_edges
    ADD CONSTRAINT discovery_edges_source_origin_check
    CHECK (source_origin <> '');
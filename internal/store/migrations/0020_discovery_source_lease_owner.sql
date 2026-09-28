ALTER TABLE discovery_source_state
    ADD COLUMN lease_owner TEXT;

ALTER TABLE discovery_source_state
    ADD CONSTRAINT discovery_source_state_lease_owner_check
    CHECK (
        lease_owner IS NULL
        OR char_length(lease_owner) BETWEEN 1 AND 128
    ),
    ADD CONSTRAINT discovery_source_state_lease_owner_state_check
    CHECK (
        lease_owner IS NULL
        OR lease_expires_at IS NOT NULL
    );

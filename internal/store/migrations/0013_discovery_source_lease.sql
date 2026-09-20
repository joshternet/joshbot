ALTER TABLE discovery_source_state
    ADD COLUMN lease_generation BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN lease_expires_at TIMESTAMPTZ,
    ADD COLUMN last_claimed_at TIMESTAMPTZ;

ALTER TABLE discovery_source_state
    ADD CONSTRAINT discovery_source_state_lease_generation_check
    CHECK (
        lease_generation >= 0
    );

ALTER TABLE discovery_source_state
    ADD CONSTRAINT discovery_source_state_lease_state_check
    CHECK (
        (
            lease_generation = 0
            AND last_claimed_at IS NULL
            AND lease_expires_at IS NULL
        )
        OR (
            lease_generation > 0
            AND last_claimed_at IS NOT NULL
            AND (
                lease_expires_at IS NULL
                OR lease_expires_at > last_claimed_at
            )
        )
    );

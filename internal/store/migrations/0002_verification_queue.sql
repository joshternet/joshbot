CREATE TABLE verification_queue (
    origin TEXT PRIMARY KEY,
    available_at TIMESTAMPTZ NOT NULL,
    lease_generation BIGINT NOT NULL DEFAULT 0,
    lease_owner TEXT,
    lease_expires_at TIMESTAMPTZ,
    last_claimed_at TIMESTAMPTZ,

    CONSTRAINT verification_queue_origin_check
        CHECK (origin <> ''),

    CONSTRAINT verification_queue_generation_check
        CHECK (lease_generation >= 0),

    CONSTRAINT verification_queue_lease_pair_check
        CHECK (
            (
                lease_owner IS NULL
                AND lease_expires_at IS NULL
            )
            OR (
                lease_owner IS NOT NULL
                AND lease_expires_at IS NOT NULL
            )
        ),

    CONSTRAINT verification_queue_worker_check
        CHECK (
            lease_owner IS NULL
            OR char_length(lease_owner) BETWEEN 1 AND 128
        ),

    CONSTRAINT verification_queue_active_lease_check
        CHECK (
            lease_owner IS NULL
            OR (
                last_claimed_at IS NOT NULL
                AND lease_expires_at > last_claimed_at
            )
        )
);

CREATE INDEX verification_queue_claim_order_idx
    ON verification_queue (
        available_at,
        origin
    );

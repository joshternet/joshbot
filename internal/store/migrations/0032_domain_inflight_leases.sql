-- Goal: one in-flight origin per registrable-domain neighborhood across
-- verification and discovery claims. Constraints: no denormalized keys on
-- queue tables, no sweeper, and DELETE for joshbot_app so completion can free
-- the domain immediately.
CREATE TABLE domain_inflight_leases (
    domain_key TEXT PRIMARY KEY,
    holder_origin TEXT NOT NULL,
    lease_owner TEXT NOT NULL,
    lease_generation BIGINT NOT NULL,
    claimed_at TIMESTAMPTZ NOT NULL,
    lease_expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT domain_inflight_leases_lease_generation_check
        CHECK (lease_generation > 0),
    CONSTRAINT domain_inflight_leases_lease_owner_check
        CHECK (lease_owner <> ''),
    CONSTRAINT domain_inflight_leases_holder_origin_check
        CHECK (holder_origin <> ''),
    CONSTRAINT domain_inflight_leases_domain_key_check
        CHECK (domain_key <> ''),
    CONSTRAINT domain_inflight_leases_lease_window_check
        CHECK (lease_expires_at > claimed_at)
);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_roles WHERE rolname = 'joshbot_app'
    ) THEN
        GRANT DELETE ON TABLE domain_inflight_leases TO joshbot_app;
    END IF;
END
$$;

ALTER TABLE discovery_source_state
    ADD COLUMN operator_blocked BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE discovery_source_state
SET operator_blocked = crawl_blocked
WHERE crawl_blocked;

ALTER TABLE discovery_source_state
    ADD CONSTRAINT discovery_source_state_effective_block_check
    CHECK (NOT operator_blocked OR crawl_blocked);

CREATE TABLE operator_audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    action TEXT NOT NULL,
    target TEXT NOT NULL,
    caller TEXT NOT NULL,
    actor TEXT NOT NULL,
    result TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    CONSTRAINT operator_audit_events_action_check CHECK (
        octet_length(action) BETWEEN 1 AND 128
    ),
    CONSTRAINT operator_audit_events_target_check CHECK (
        octet_length(target) <= 2048
    ),
    CONSTRAINT operator_audit_events_caller_check CHECK (
        octet_length(caller) BETWEEN 1 AND 256
    ),
    CONSTRAINT operator_audit_events_actor_check CHECK (
        octet_length(actor) BETWEEN 1 AND 128
    ),
    CONSTRAINT operator_audit_events_result_check CHECK (
        result IN ('success', 'rejected')
    ),
    CONSTRAINT operator_audit_events_reason_check CHECK (
        octet_length(reason) <= 512
    )
);

CREATE INDEX operator_audit_events_occurred_idx
    ON operator_audit_events (occurred_at DESC, id DESC);

CREATE FUNCTION prevent_operator_audit_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'operator audit events are append-only';
END;
$$;

CREATE TRIGGER operator_audit_events_append_only
BEFORE UPDATE OR DELETE ON operator_audit_events
FOR EACH ROW EXECUTE FUNCTION prevent_operator_audit_mutation();

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_roles WHERE rolname = 'joshbot_app'
    ) THEN
        REVOKE ALL ON TABLE operator_audit_events FROM joshbot_app;
        REVOKE ALL ON SEQUENCE operator_audit_events_id_seq FROM joshbot_app;
    END IF;

    IF EXISTS (
        SELECT 1 FROM pg_roles WHERE rolname = 'joshbot_reporter'
    ) THEN
        REVOKE ALL ON TABLE operator_audit_events FROM joshbot_reporter;
        REVOKE ALL ON SEQUENCE operator_audit_events_id_seq FROM joshbot_reporter;
    END IF;

    IF EXISTS (
        SELECT 1 FROM pg_roles WHERE rolname = 'joshbot_operator'
    ) THEN
        GRANT SELECT (singleton) ON TABLE crawl_control TO joshbot_operator;
        GRANT UPDATE (
            discovery_paused,
            verification_paused,
            updated_at
        ) ON TABLE crawl_control TO joshbot_operator;
        GRANT SELECT (pattern), INSERT (pattern), DELETE
            ON TABLE crawl_domain_avoid_rules
            TO joshbot_operator;
        GRANT SELECT (
            source_origin,
            seeded,
            automatically_discovered,
            operator_blocked,
            crawl_blocked
        ) ON TABLE discovery_source_state TO joshbot_operator;
        GRANT INSERT (source_origin, operator_blocked, crawl_blocked)
            ON TABLE discovery_source_state TO joshbot_operator;
        GRANT UPDATE (operator_blocked, crawl_blocked)
            ON TABLE discovery_source_state
            TO joshbot_operator;
        GRANT SELECT (origin, mode), DELETE
            ON TABLE verification_queue TO joshbot_operator;
        GRANT INSERT ON TABLE verification_queue_events TO joshbot_operator;
        GRANT USAGE, SELECT ON SEQUENCE verification_queue_events_id_seq
            TO joshbot_operator;
        GRANT INSERT ON TABLE operator_audit_events TO joshbot_operator;
        GRANT USAGE, SELECT ON SEQUENCE operator_audit_events_id_seq
            TO joshbot_operator;
    END IF;
END
$$;

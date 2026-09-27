ALTER TABLE crawl_control
    ADD COLUMN automatic_expansion_paused BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE crawl_run_automatic_admission_batches
    DROP CONSTRAINT crawl_run_automatic_admission_batches_outcome_check,
    ADD CONSTRAINT crawl_run_automatic_admission_batches_outcome_check
    CHECK (
        outcome IN (
            'pending',
            'promoted',
            'capacity_deferred',
            'policy_deferred',
            'retry_deferred',
            'expansion_paused',
            'network_rejected',
            'existing'
        )
    );

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_roles
        WHERE rolname = 'joshbot_operator'
    ) THEN
        GRANT UPDATE (
            automatic_expansion_paused,
            updated_at
        )
        ON TABLE crawl_control
        TO joshbot_operator;
    END IF;
END
$$;

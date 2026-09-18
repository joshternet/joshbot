ALTER TABLE verification_queue
    ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN last_failure_category TEXT,
    ADD COLUMN next_attempt_at TIMESTAMPTZ;

ALTER TABLE verification_queue
    ADD CONSTRAINT verification_queue_retry_state_check
    CHECK (
        consecutive_failures >= 0
        AND (
            (consecutive_failures = 0
                AND last_failure_category IS NULL
                AND next_attempt_at IS NULL)
            OR
            (consecutive_failures > 0
                AND last_failure_category IN (
                    'dns', 'transport', 'timeout', 'robots_temporary',
                    'http_408', 'http_429', 'http_5xx',
                    'declaration_unavailable', 'processor', 'store'
                )
                AND next_attempt_at IS NOT NULL
                AND next_attempt_at = available_at)
        )
    );

ALTER TABLE discovery_candidates
    ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN last_failure_category TEXT,
    ADD COLUMN next_attempt_at TIMESTAMPTZ;

ALTER TABLE discovery_candidates
    ADD CONSTRAINT discovery_candidates_retry_state_check
    CHECK (
        consecutive_failures >= 0
        AND (
            (consecutive_failures = 0
                AND last_failure_category IS NULL
                AND next_attempt_at IS NULL)
            OR
            (consecutive_failures > 0
                AND last_failure_category IN (
                    'dns', 'transport', 'timeout', 'robots_temporary',
                    'http_408', 'http_429', 'http_5xx',
                    'declaration_unavailable', 'processor', 'store'
                )
                AND next_attempt_at IS NOT NULL)
        )
    );

ALTER TABLE discovery_source_state
    ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN last_failure_category TEXT,
    ADD COLUMN next_attempt_at TIMESTAMPTZ;

ALTER TABLE discovery_source_state
    ADD CONSTRAINT discovery_source_state_retry_state_check
    CHECK (
        consecutive_failures >= 0
        AND (
            (consecutive_failures = 0
                AND last_failure_category IS NULL
                AND next_attempt_at IS NULL)
            OR
            (consecutive_failures > 0
                AND last_failure_category IN (
                    'dns', 'transport', 'timeout', 'robots_temporary',
                    'http_408', 'http_429', 'http_5xx',
                    'declaration_unavailable', 'processor', 'store'
                )
                AND next_attempt_at IS NOT NULL)
        )
    );

ALTER TABLE crawl_run_automatic_admission_batches
    ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN last_failure_category TEXT,
    ADD COLUMN next_attempt_at TIMESTAMPTZ,
    DROP CONSTRAINT crawl_run_automatic_admission_batches_outcome_check,
    ADD CONSTRAINT crawl_run_automatic_admission_batches_outcome_check
    CHECK (
        outcome IN (
            'pending',
            'promoted',
            'capacity_deferred',
            'policy_deferred',
            'retry_deferred',
            'network_rejected',
            'existing'
        )
    );

ALTER TABLE crawl_run_automatic_admission_batches
    ADD CONSTRAINT crawl_run_automatic_admission_batches_retry_state_check
    CHECK (
        consecutive_failures >= 0
        AND (
            (outcome <> 'retry_deferred'
                AND consecutive_failures = 0
                AND last_failure_category IS NULL
                AND next_attempt_at IS NULL)
            OR
            (outcome = 'retry_deferred'
                AND consecutive_failures > 0
                AND last_failure_category IN (
                    'dns', 'transport', 'timeout', 'robots_temporary',
                    'http_408', 'http_429', 'http_5xx',
                    'declaration_unavailable', 'processor', 'store'
                )
                AND next_attempt_at IS NOT NULL)
        )
    );

CREATE INDEX verification_queue_retry_due_idx
    ON verification_queue (next_attempt_at, origin)
    WHERE next_attempt_at IS NOT NULL;

CREATE INDEX discovery_candidates_retry_due_idx
    ON discovery_candidates (next_attempt_at, origin)
    WHERE next_attempt_at IS NOT NULL;

CREATE INDEX discovery_source_state_retry_due_idx
    ON discovery_source_state (next_attempt_at, source_origin)
    WHERE next_attempt_at IS NOT NULL;

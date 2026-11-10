-- Goal: record failure_category on verification observations and allow
-- discovery_candidates cooldown after non-transient probe removals so
-- admission cannot immediately re-flood the same origin.
-- Inputs: existing observations (category NULL) and candidate retry rows.
-- Acceptance: observations accept the full typed category set; candidates
-- may hold non-transient last_failure_category with next_attempt_at.

ALTER TABLE verification_observations
    ADD COLUMN failure_category TEXT;

ALTER TABLE verification_observations
    ADD CONSTRAINT verification_observations_failure_category_check
    CHECK (
        failure_category IS NULL
        OR failure_category IN (
            'dns', 'transport', 'timeout', 'robots_temporary',
            'http_408', 'http_429', 'http_4xx', 'http_5xx',
            'declaration_unavailable', 'processor', 'store',
            'unsafe_address', 'policy_blocked', 'robots_denied',
            'malformed_origin', 'unsupported_origin', 'oversized_content',
            'unsupported_content', 'budget_exhausted',
            'declaration_invalid', 'declaration_unsupported',
            'cross_origin_redirect', 'declaration_absent'
        )
    );

ALTER TABLE discovery_candidates
    DROP CONSTRAINT discovery_candidates_retry_state_check;

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
                    'http_408', 'http_429', 'http_4xx', 'http_5xx',
                    'declaration_unavailable', 'processor', 'store',
                    'unsafe_address', 'policy_blocked', 'robots_denied',
                    'malformed_origin', 'unsupported_origin',
                    'oversized_content', 'unsupported_content',
                    'budget_exhausted', 'declaration_invalid',
                    'declaration_unsupported', 'cross_origin_redirect',
                    'declaration_absent'
                )
                AND next_attempt_at IS NOT NULL)
        )
    );

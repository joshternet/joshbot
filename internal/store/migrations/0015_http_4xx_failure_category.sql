ALTER TABLE crawl_runs
    DROP CONSTRAINT crawl_runs_failure_category_check,
    ADD CONSTRAINT crawl_runs_failure_category_check CHECK (
        failure_category IN (
            'none', 'dns', 'transport', 'timeout', 'robots_temporary',
            'http_408', 'http_429', 'http_4xx', 'http_5xx',
            'declaration_unavailable', 'processor', 'store',
            'unsafe_address', 'policy_blocked', 'robots_denied',
            'malformed_origin', 'unsupported_origin', 'oversized_content',
            'unsupported_content', 'budget_exhausted',
            'declaration_invalid', 'declaration_unsupported',
            'cross_origin_redirect', 'declaration_absent'
        )
    );

ALTER TABLE crawl_page_attempts
    DROP CONSTRAINT crawl_page_attempts_failure_category_check,
    ADD CONSTRAINT crawl_page_attempts_failure_category_check CHECK (
        failure_category IN (
            'none', 'dns', 'transport', 'timeout', 'robots_temporary',
            'http_408', 'http_429', 'http_4xx', 'http_5xx',
            'declaration_unavailable', 'processor', 'store',
            'unsafe_address', 'policy_blocked', 'robots_denied',
            'malformed_origin', 'unsupported_origin', 'oversized_content',
            'unsupported_content', 'budget_exhausted',
            'declaration_invalid', 'declaration_unsupported',
            'cross_origin_redirect', 'declaration_absent'
        )
    );

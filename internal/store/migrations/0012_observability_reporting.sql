ALTER TABLE crawl_runs
    ADD COLUMN promotions_admitted INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN promotions_deferred INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN failure_category TEXT NOT NULL DEFAULT 'none',
    ADD COLUMN pages_blocked INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN pages_failed INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN urls_found INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN urls_enqueued INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN frontier_remaining INTEGER NOT NULL DEFAULT 0;

ALTER TABLE crawl_runs
    ADD CONSTRAINT crawl_runs_observability_counts_check CHECK (
        promotions_admitted >= 0
        AND promotions_deferred >= 0
        AND pages_blocked >= 0
        AND pages_failed >= 0
        AND urls_found >= 0
        AND urls_enqueued >= 0
        AND frontier_remaining >= 0
    ),
    ADD CONSTRAINT crawl_runs_failure_category_check CHECK (
        failure_category IN (
            'none', 'dns', 'transport', 'timeout', 'robots_temporary',
            'http_408', 'http_429', 'http_5xx', 'declaration_unavailable',
            'processor', 'store', 'unsafe_address', 'policy_blocked',
            'robots_denied', 'malformed_origin', 'unsupported_origin',
            'oversized_content', 'unsupported_content', 'budget_exhausted',
            'declaration_invalid', 'declaration_unsupported',
            'cross_origin_redirect', 'declaration_absent'
        )
    );

ALTER TABLE crawl_page_attempts
    ADD COLUMN failure_category TEXT NOT NULL DEFAULT 'none',
    ADD COLUMN urls_found INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN urls_enqueued INTEGER NOT NULL DEFAULT 0;

ALTER TABLE crawl_page_attempts
    ADD CONSTRAINT crawl_page_attempts_observability_counts_check CHECK (
        urls_found >= 0 AND urls_enqueued >= 0
    ),
    ADD CONSTRAINT crawl_page_attempts_failure_category_check CHECK (
        failure_category IN (
            'none', 'dns', 'transport', 'timeout', 'robots_temporary',
            'http_408', 'http_429', 'http_5xx', 'declaration_unavailable',
            'processor', 'store', 'unsafe_address', 'policy_blocked',
            'robots_denied', 'malformed_origin', 'unsupported_origin',
            'oversized_content', 'unsupported_content', 'budget_exhausted',
            'declaration_invalid', 'declaration_unsupported',
            'cross_origin_redirect', 'declaration_absent'
        )
    );

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_roles WHERE rolname = 'joshbot_reporter'
    ) THEN
        GRANT SELECT ON TABLE operator_audit_events TO joshbot_reporter;
    END IF;
END
$$;

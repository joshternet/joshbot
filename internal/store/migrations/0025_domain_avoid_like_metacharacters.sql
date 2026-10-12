ALTER TABLE crawl_domain_avoid_rules
    DROP CONSTRAINT crawl_domain_avoid_rules_pattern_check,
    ADD CONSTRAINT crawl_domain_avoid_rules_pattern_check CHECK (
        pattern = lower(pattern)
        AND pattern !~ '[/:@?#_%\\]'
        AND pattern !~ '^\.'
        AND pattern !~ '\.$'
        AND pattern <> ''
    );

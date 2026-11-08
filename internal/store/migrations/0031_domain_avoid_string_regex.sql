-- Goal & Constraints: Allow hostname glob strings and re: RE2 patterns in the
-- durable domain-avoid list while keeping exact-suffix and family.* rules.
-- Seed cbd-hash* and the free-blog farms hosting those spam labels.

ALTER TABLE crawl_domain_avoid_rules
    DROP CONSTRAINT crawl_domain_avoid_rules_pattern_check,
    ADD CONSTRAINT crawl_domain_avoid_rules_pattern_check CHECK (
        pattern = lower(pattern)
        AND pattern <> ''
        AND position(',' in pattern) = 0
        AND (
            (
                left(pattern, 3) <> 're:'
                AND char_length(pattern) <= 253
                AND pattern !~ '[/:@?#_%\\]'
                AND pattern !~ '^\.'
                AND pattern !~ '\.$'
                AND pattern <> '*'
            )
            OR (
                left(pattern, 3) = 're:'
                AND char_length(pattern) > 3
                AND char_length(pattern) <= 512
                AND pattern !~ '[[:cntrl:]]'
            )
        )
    );

INSERT INTO crawl_domain_avoid_rules (pattern)
VALUES
    ('cbd-hash*'),
    ('ampedpages.com'),
    ('bligblogging.com'),
    ('bloginder.com'),
    ('blogocial.com'),
    ('blogolenta.com'),
    ('blogoscience.com'),
    ('blogzet.com'),
    ('kylieblog.com'),
    ('review-blogger.com'),
    ('total-blog.com'),
    ('webdesign96.com')
ON CONFLICT (pattern) DO NOTHING;

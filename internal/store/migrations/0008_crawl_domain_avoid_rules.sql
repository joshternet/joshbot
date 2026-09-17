CREATE TABLE crawl_domain_avoid_rules (
    pattern TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    CONSTRAINT crawl_domain_avoid_rules_pattern_check CHECK (
        pattern = lower(pattern)
        AND pattern !~ '[/:@?#]'
        AND pattern !~ '^\.'
        AND pattern !~ '\.$'
        AND pattern <> ''
    )
);

INSERT INTO crawl_domain_avoid_rules (pattern) VALUES
    ('blogspot.*'),
    ('wordpress.com'),
    ('tumblr.com'),
    ('facebook.com'),
    ('instagram.com'),
    ('threads.net'),
    ('twitter.com'),
    ('x.com'),
    ('bsky.app'),
    ('mastodon.social'),
    ('linkedin.com'),
    ('tiktok.com'),
    ('youtube.com'),
    ('reddit.com'),
    ('medium.com'),
    ('substack.com'),
    ('linktr.ee'),
    ('sites.google.com');

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_roles WHERE rolname = 'joshbot_app'
    ) THEN
        GRANT DELETE ON TABLE crawl_domain_avoid_rules TO joshbot_app;
    END IF;
END
$$;

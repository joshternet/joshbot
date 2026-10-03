INSERT INTO crawl_domain_avoid_rules (pattern)
VALUES ('centerblog.net')
ON CONFLICT (pattern) DO NOTHING;

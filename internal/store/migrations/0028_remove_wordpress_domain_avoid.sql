-- Goal: WordPress sites that install the official Joshternet plugin stay
-- eligible for probes and automatic promotion.
-- Constraints: remove only wordpress.com and wordpress.org. Leave other avoid
-- rules and non-WordPress operator blocks unchanged.
DELETE FROM crawl_domain_avoid_rules
WHERE pattern IN ('wordpress.com', 'wordpress.org');

UPDATE discovery_source_state
SET
    operator_blocked = false,
    crawl_blocked = false
WHERE (operator_blocked OR crawl_blocked)
    AND lower(source_origin) ~ '^https?://([^/?#]+\.)?wordpress\.(com|org)$';

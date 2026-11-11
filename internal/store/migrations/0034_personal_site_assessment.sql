-- Goal: persist the latest private personal-site assessment on
-- discovery_candidates for crawl prioritization only.
-- Inputs: score 0-100, assessment version, assessed_at, closed outcome set,
-- and reason-code JSON only when assessed.
-- Acceptance: replace-on-write columns; unavailable != score 0; no HTML/text.

ALTER TABLE discovery_candidates
    ADD COLUMN personal_site_score SMALLINT,
    ADD COLUMN personal_site_assessment_version INTEGER,
    ADD COLUMN personal_site_assessed_at TIMESTAMPTZ,
    ADD COLUMN personal_site_qualification_outcome TEXT,
    ADD COLUMN personal_site_reason_codes JSONB;

ALTER TABLE discovery_candidates
    ADD CONSTRAINT discovery_candidates_personal_site_score_check
    CHECK (
        personal_site_score IS NULL
        OR (
            personal_site_score BETWEEN 0 AND 100
        )
    );

ALTER TABLE discovery_candidates
    ADD CONSTRAINT discovery_candidates_personal_site_assessment_check
    CHECK (
        (
            personal_site_qualification_outcome IS NULL
            AND personal_site_score IS NULL
            AND personal_site_assessment_version IS NULL
            AND personal_site_assessed_at IS NULL
            AND personal_site_reason_codes IS NULL
        )
        OR (
            personal_site_qualification_outcome IN (
                'assessed',
                'robots_denied',
                'timeout',
                'unsupported_content',
                'too_large',
                'dns_failure',
                'http_error',
                'network_rejected',
                'parser_failure',
                'unavailable'
            )
            AND personal_site_assessed_at IS NOT NULL
            AND (
                (
                    personal_site_qualification_outcome = 'assessed'
                    AND personal_site_score IS NOT NULL
                    AND personal_site_assessment_version IS NOT NULL
                    AND personal_site_reason_codes IS NOT NULL
                    AND jsonb_typeof(personal_site_reason_codes) = 'array'
                )
                OR (
                    personal_site_qualification_outcome <> 'assessed'
                    AND personal_site_score IS NULL
                    AND personal_site_assessment_version IS NULL
                    AND personal_site_reason_codes IS NULL
                )
            )
        )
    );

CREATE INDEX discovery_candidates_personal_site_score_idx
    ON discovery_candidates (
        personal_site_score DESC NULLS LAST,
        origin
    )
    WHERE personal_site_qualification_outcome = 'assessed';

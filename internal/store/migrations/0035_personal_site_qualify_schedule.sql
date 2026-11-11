-- Goal: schedule personal-site qualification separately from probe retries,
-- track truncation and claim fencing for concurrent workers.
-- Inputs: next/last attempt times, failure ladder step, retry class,
-- truncated flag, claim owner/generation/expiry.
-- Acceptance: existing rows stay non-admissible until current-version assessed;
-- historical too_large outcomes remain readable; probe next_attempt_at untouched.

ALTER TABLE discovery_candidates
    ADD COLUMN personal_site_next_attempt_at TIMESTAMPTZ,
    ADD COLUMN personal_site_last_attempt_at TIMESTAMPTZ,
    ADD COLUMN personal_site_failure_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN personal_site_retry_class TEXT,
    ADD COLUMN personal_site_truncated BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN personal_site_http_status INTEGER,
    ADD COLUMN personal_site_claim_owner TEXT,
    ADD COLUMN personal_site_claim_generation BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN personal_site_claim_expires_at TIMESTAMPTZ;

ALTER TABLE discovery_candidates
    ADD CONSTRAINT discovery_candidates_personal_site_failure_count_check
    CHECK (personal_site_failure_count >= 0);

ALTER TABLE discovery_candidates
    ADD CONSTRAINT discovery_candidates_personal_site_http_status_check
    CHECK (
        personal_site_http_status IS NULL
        OR personal_site_http_status BETWEEN 100 AND 599
    );

ALTER TABLE discovery_candidates
    ADD CONSTRAINT discovery_candidates_personal_site_retry_class_check
    CHECK (
        personal_site_retry_class IS NULL
        OR personal_site_retry_class IN (
            'transient',
            'rate_limited',
            'gone',
            'unsupported',
            'parser',
            'robots',
            'network_rejected',
            'http_other',
            'unavailable'
        )
    );

ALTER TABLE discovery_candidates
    ADD CONSTRAINT discovery_candidates_personal_site_claim_check
    CHECK (
        (
            personal_site_claim_owner IS NULL
            AND personal_site_claim_expires_at IS NULL
        )
        OR (
            personal_site_claim_owner IS NOT NULL
            AND personal_site_claim_expires_at IS NOT NULL
            AND personal_site_claim_generation > 0
        )
    );

-- Stale historical assessments remain stored but are not due for qualify until
-- version bump or explicit next_attempt; leave next_attempt NULL so due query
-- treats never-attempted and version-stale rows as eligible when other gates pass.

CREATE INDEX discovery_candidates_personal_site_qualify_due_idx
    ON discovery_candidates (
        personal_site_next_attempt_at ASC NULLS FIRST,
        origin ASC
    )
    WHERE personal_site_qualification_outcome IS DISTINCT FROM 'assessed'
       OR personal_site_assessment_version IS DISTINCT FROM 1;

CREATE INDEX discovery_candidates_personal_site_score_current_idx
    ON discovery_candidates (
        personal_site_score DESC,
        origin ASC
    )
    WHERE personal_site_qualification_outcome = 'assessed'
      AND personal_site_assessment_version = 1
      AND personal_site_score IS NOT NULL;

-- Goal: operator score overrides, rescore requests, and grants for control API.
-- Inputs: override score/reason/actor/updated_at/revision; rescore_requested_at.
-- Acceptance: calculated score untouched by overrides; revision fences concurrent
-- updates; operator can UPDATE override/rescore and nudge crawl schedule.

ALTER TABLE discovery_candidates
    ADD COLUMN personal_site_override_score SMALLINT,
    ADD COLUMN personal_site_override_reason TEXT,
    ADD COLUMN personal_site_override_actor TEXT,
    ADD COLUMN personal_site_override_updated_at TIMESTAMPTZ,
    ADD COLUMN personal_site_override_revision BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN personal_site_rescore_requested_at TIMESTAMPTZ;

ALTER TABLE discovery_candidates
    ADD CONSTRAINT discovery_candidates_personal_site_override_score_check
    CHECK (
        personal_site_override_score IS NULL
        OR personal_site_override_score BETWEEN 0 AND 100
    );

ALTER TABLE discovery_candidates
    ADD CONSTRAINT discovery_candidates_personal_site_override_check
    CHECK (
        (
            personal_site_override_score IS NULL
            AND personal_site_override_reason IS NULL
            AND personal_site_override_actor IS NULL
            AND personal_site_override_updated_at IS NULL
        )
        OR (
            personal_site_override_score IS NOT NULL
            AND personal_site_override_reason IS NOT NULL
            AND octet_length(personal_site_override_reason) BETWEEN 1 AND 512
            AND personal_site_override_actor IS NOT NULL
            AND octet_length(personal_site_override_actor) BETWEEN 1 AND 128
            AND personal_site_override_updated_at IS NOT NULL
            AND personal_site_override_revision > 0
        )
    );

ALTER TABLE discovery_candidates
    ADD CONSTRAINT discovery_candidates_personal_site_override_revision_check
    CHECK (personal_site_override_revision >= 0);

CREATE INDEX discovery_candidates_personal_site_effective_score_idx
    ON discovery_candidates (
        COALESCE(
            personal_site_override_score,
            personal_site_score
        ) DESC,
        origin ASC
    )
    WHERE personal_site_qualification_outcome = 'assessed'
      AND personal_site_assessment_version = 1
      AND personal_site_score IS NOT NULL;

CREATE INDEX discovery_candidates_personal_site_rescore_due_idx
    ON discovery_candidates (
        personal_site_rescore_requested_at ASC,
        origin ASC
    )
    WHERE personal_site_rescore_requested_at IS NOT NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_roles WHERE rolname = 'joshbot_operator'
    ) THEN
        GRANT SELECT (
            origin,
            personal_site_score,
            personal_site_override_score,
            personal_site_override_reason,
            personal_site_override_actor,
            personal_site_override_updated_at,
            personal_site_override_revision,
            personal_site_rescore_requested_at,
            personal_site_next_attempt_at,
            personal_site_qualification_outcome,
            personal_site_assessment_version,
            personal_site_claim_owner,
            personal_site_claim_expires_at
        ) ON TABLE discovery_candidates TO joshbot_operator;

        GRANT UPDATE (
            personal_site_override_score,
            personal_site_override_reason,
            personal_site_override_actor,
            personal_site_override_updated_at,
            personal_site_override_revision,
            personal_site_rescore_requested_at,
            personal_site_next_attempt_at
        ) ON TABLE discovery_candidates TO joshbot_operator;

        GRANT SELECT (source_origin)
            ON TABLE discovery_source_schedule TO joshbot_operator;
        GRANT INSERT (source_origin)
            ON TABLE discovery_source_schedule TO joshbot_operator;

        GRANT SELECT (
            source_origin,
            seeded,
            automatically_discovered,
            operator_blocked,
            crawl_blocked,
            next_attempt_at,
            lease_owner,
            lease_expires_at,
            lease_generation
        ) ON TABLE discovery_source_state TO joshbot_operator;

        GRANT UPDATE (
            next_attempt_at,
            lease_owner,
            lease_expires_at
        ) ON TABLE discovery_source_state TO joshbot_operator;

        GRANT SELECT (pattern)
            ON TABLE crawl_domain_avoid_rules TO joshbot_operator;
    END IF;
END
$$;

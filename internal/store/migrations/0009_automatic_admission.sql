ALTER TABLE crawl_runs
    ADD COLUMN max_automatic_promotions INTEGER NOT NULL DEFAULT 100;

ALTER TABLE crawl_runs
    ADD COLUMN automatic_promotions INTEGER NOT NULL DEFAULT 0;

ALTER TABLE crawl_runs
    ADD CONSTRAINT crawl_runs_automatic_promotions_check
    CHECK (
        max_automatic_promotions > 0
        AND automatic_promotions BETWEEN 0 AND max_automatic_promotions
    );

CREATE TABLE crawl_run_discovery_candidates (
    run_id BIGINT NOT NULL REFERENCES crawl_runs (id) ON DELETE CASCADE,
    candidate_origin TEXT NOT NULL
        REFERENCES discovery_candidates (origin) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    PRIMARY KEY (run_id, candidate_origin, kind),
    CONSTRAINT crawl_run_discovery_candidates_kind_check
        CHECK (kind IN ('link', 'redirect'))
);

CREATE INDEX crawl_run_discovery_candidates_origin_idx
    ON crawl_run_discovery_candidates (candidate_origin, run_id);

CREATE TABLE crawl_run_automatic_admission_batches (
    admission_run_id BIGINT NOT NULL
        REFERENCES crawl_runs (id) ON DELETE CASCADE,
    candidate_origin TEXT NOT NULL
        REFERENCES discovery_candidates (origin) ON DELETE CASCADE,
    discovered_run_id BIGINT NOT NULL
        REFERENCES crawl_runs (id) ON DELETE CASCADE,
    outcome TEXT NOT NULL DEFAULT 'pending',
    PRIMARY KEY (admission_run_id, candidate_origin),
    CONSTRAINT crawl_run_automatic_admission_batches_outcome_check
        CHECK (
            outcome IN (
                'pending',
                'promoted',
                'capacity_deferred',
                'policy_deferred',
                'network_rejected',
                'existing'
            )
        )
);

CREATE INDEX crawl_run_automatic_admission_candidate_idx
    ON crawl_run_automatic_admission_batches (
        candidate_origin,
        admission_run_id,
        outcome
    );

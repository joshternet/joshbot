CREATE TABLE discovery_candidate_admission_state (
    candidate_origin TEXT PRIMARY KEY
        REFERENCES discovery_candidates (origin) ON DELETE CASCADE,
    outcome TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    CONSTRAINT discovery_candidate_admission_state_outcome_check
        CHECK (
            outcome IN (
                'network_rejected',
                'promoted',
                'existing'
            )
        )
);

INSERT INTO discovery_candidate_admission_state (
    candidate_origin,
    outcome,
    updated_at
)
SELECT DISTINCT ON (batch.candidate_origin)
    batch.candidate_origin,
    batch.outcome,
    statement_timestamp()
FROM crawl_run_automatic_admission_batches AS batch
WHERE batch.outcome IN (
    'network_rejected',
    'promoted',
    'existing'
)
ORDER BY
    batch.candidate_origin,
    batch.admission_run_id DESC
ON CONFLICT (candidate_origin) DO NOTHING;

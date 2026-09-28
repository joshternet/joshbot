ALTER TABLE origins
    ADD COLUMN first_participated_at TIMESTAMPTZ,
    ADD COLUMN initial_declaration_version INTEGER,
    ADD COLUMN initial_declaration_identity TEXT,
    ADD COLUMN latest_declaration_check_at TIMESTAMPTZ,
    ADD COLUMN latest_declaration_check_outcome TEXT;

WITH first_valid AS (
    SELECT DISTINCT ON (origin)
        origin,
        observed_at,
        version,
        identity
    FROM verification_observations
    WHERE outcome = 'valid'
    ORDER BY
        origin,
        observed_at ASC,
        id ASC
),
latest_check AS (
    SELECT DISTINCT ON (origin)
        origin,
        observed_at,
        outcome
    FROM verification_observations
    ORDER BY
        origin,
        observed_at DESC,
        id DESC
)
UPDATE origins AS origin
SET
    first_participated_at =
        first_valid.observed_at,
    initial_declaration_version =
        first_valid.version,
    initial_declaration_identity =
        first_valid.identity,
    latest_declaration_check_at =
        latest_check.observed_at,
    latest_declaration_check_outcome =
        latest_check.outcome
FROM latest_check
LEFT JOIN first_valid
    ON first_valid.origin = latest_check.origin
WHERE origin.origin = latest_check.origin;

ALTER TABLE origins
    ADD CONSTRAINT origins_initial_participation_check
    CHECK (
        (
            first_participated_at IS NULL
            AND initial_declaration_version IS NULL
            AND initial_declaration_identity IS NULL
        )
        OR (
            first_participated_at IS NOT NULL
            AND initial_declaration_version IS NOT NULL
            AND initial_declaration_version = 1
            AND initial_declaration_identity IS NOT NULL
            AND initial_declaration_identity IN (
                'undeclared',
                'affirmed',
                'declined'
            )
            AND latest_declaration_check_at IS NOT NULL
            AND first_participated_at <=
                latest_declaration_check_at
        )
    ),
    ADD CONSTRAINT origins_latest_declaration_check_check
    CHECK (
        (
            latest_declaration_check_at IS NULL
            AND latest_declaration_check_outcome IS NULL
        )
        OR (
            latest_declaration_check_at IS NOT NULL
            AND latest_declaration_check_outcome IS NOT NULL
            AND latest_declaration_check_outcome IN (
                'valid',
                'absent',
                'invalid',
                'unsupported_version',
                'unavailable',
                'robots_denied',
                'cross_origin_redirect'
            )
            AND (
                latest_declaration_check_outcome <> 'valid'
                OR first_participated_at IS NOT NULL
            )
        )
    );

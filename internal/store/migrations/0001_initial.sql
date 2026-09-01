CREATE TABLE origins (
    origin TEXT PRIMARY KEY,
    first_observed_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE verification_observations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    origin TEXT NOT NULL REFERENCES origins (origin),
    observed_at TIMESTAMPTZ NOT NULL,
    outcome TEXT NOT NULL,
    version INTEGER,
    identity TEXT,

    CONSTRAINT verification_observations_outcome_check
        CHECK (
            outcome IN (
                'valid',
                'absent',
                'invalid',
                'unsupported_version',
                'unavailable',
                'robots_denied',
                'cross_origin_redirect'
            )
        ),

    CONSTRAINT verification_observations_identity_check
        CHECK (
            identity IS NULL
            OR identity IN (
                'undeclared',
                'affirmed',
                'declined'
            )
        ),

    CONSTRAINT verification_observations_semantics_check
        CHECK (
            (
                outcome = 'valid'
                AND version IS NOT NULL
                AND version = 1
                AND identity IS NOT NULL
            )
            OR (
                outcome <> 'valid'
                AND version IS NULL
                AND identity IS NULL
            )
        )
);

CREATE INDEX verification_observations_origin_order_idx
    ON verification_observations (
        origin,
        observed_at DESC,
        id DESC
    );

CREATE INDEX verification_observations_authoritative_order_idx
    ON verification_observations (
        origin,
        observed_at DESC,
        id DESC
    )
    WHERE outcome IN (
        'valid',
        'absent',
        'invalid',
        'unsupported_version',
        'cross_origin_redirect'
    );
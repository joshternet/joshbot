-- Goal: one shared request slot and UTC-daily byte total for the crawl fleet.
-- Constraints: a single row, no per-origin state, and no change to existing
-- crawl, queue, or lease tables.
CREATE TABLE crawl_fleet_budget (
    id SMALLINT PRIMARY KEY DEFAULT 1,
    next_request_at TIMESTAMPTZ NOT NULL,
    byte_window_started_at TIMESTAMPTZ NOT NULL,
    bytes_used BIGINT NOT NULL,
    CONSTRAINT crawl_fleet_budget_singleton_check
        CHECK (id = 1),
    CONSTRAINT crawl_fleet_budget_bytes_used_check
        CHECK (bytes_used >= 0)
);

INSERT INTO crawl_fleet_budget (
    id,
    next_request_at,
    byte_window_started_at,
    bytes_used
) VALUES (
    1,
    TIMESTAMP WITH TIME ZONE 'epoch',
    TIMESTAMP WITH TIME ZONE 'epoch',
    0
);

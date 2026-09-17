CREATE TABLE crawl_control (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE,
    discovery_paused BOOLEAN NOT NULL DEFAULT FALSE,
    verification_paused BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    CONSTRAINT crawl_control_singleton_check CHECK (singleton)
);

INSERT INTO crawl_control (singleton) VALUES (TRUE);

CREATE TABLE crawl_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_origin TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    outcome TEXT NOT NULL DEFAULT 'running',
    stop_reason TEXT NOT NULL DEFAULT '',
    pages_attempted INTEGER NOT NULL DEFAULT 0,
    pages_parsed INTEGER NOT NULL DEFAULT 0,
    candidates_discovered INTEGER NOT NULL DEFAULT 0,
    budget_exhausted BOOLEAN NOT NULL DEFAULT FALSE,
    max_depth INTEGER NOT NULL,
    max_pages INTEGER NOT NULL,
    max_page_bytes INTEGER NOT NULL,
    request_delay_milliseconds BIGINT NOT NULL,
    redirect_limit INTEGER NOT NULL,
    page_timeout_milliseconds BIGINT NOT NULL,
    CONSTRAINT crawl_runs_source_origin_check
        CHECK (source_origin ~ '^https?://[^/?#]+$'),
    CONSTRAINT crawl_runs_time_order_check
        CHECK (finished_at IS NULL OR finished_at >= started_at),
    CONSTRAINT crawl_runs_outcome_check
        CHECK (outcome IN ('running', 'complete', 'budget_exhausted', 'canceled', 'failed')),
    CONSTRAINT crawl_runs_counts_check
        CHECK (pages_attempted >= 0 AND pages_parsed >= 0 AND candidates_discovered >= 0),
    CONSTRAINT crawl_runs_limits_check
        CHECK (max_depth >= 0 AND max_pages > 0 AND max_page_bytes > 0
            AND request_delay_milliseconds >= 0 AND redirect_limit >= 0
            AND page_timeout_milliseconds > 0)
);

CREATE INDEX crawl_runs_source_started_idx
    ON crawl_runs (source_origin, started_at DESC, id DESC);

CREATE INDEX crawl_runs_started_idx
    ON crawl_runs (started_at DESC, id DESC);

CREATE TABLE crawl_page_attempts (
    run_id BIGINT NOT NULL REFERENCES crawl_runs (id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    requested_url TEXT NOT NULL,
    final_url TEXT NOT NULL,
    depth INTEGER NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    duration_milliseconds BIGINT NOT NULL,
    status_code INTEGER,
    response_bytes BIGINT NOT NULL,
    content_type TEXT NOT NULL,
    redirect_count INTEGER NOT NULL,
    robots_decision TEXT NOT NULL,
    internal_link_count INTEGER NOT NULL,
    external_link_count INTEGER NOT NULL,
    outcome TEXT NOT NULL,
    PRIMARY KEY (run_id, sequence),
    CONSTRAINT crawl_page_attempts_urls_check CHECK (
        requested_url ~ '^https?://[^?#]+$'
        AND final_url ~ '^https?://[^?#]+$'
        AND position('?' IN requested_url) = 0
        AND position('#' IN requested_url) = 0
        AND position('?' IN final_url) = 0
        AND position('#' IN final_url) = 0
    ),
    CONSTRAINT crawl_page_attempts_depth_check CHECK (depth >= 0),
    CONSTRAINT crawl_page_attempts_duration_check CHECK (duration_milliseconds >= 0),
    CONSTRAINT crawl_page_attempts_status_check CHECK (
        status_code IS NULL OR status_code BETWEEN 100 AND 599
    ),
    CONSTRAINT crawl_page_attempts_counts_check CHECK (
        response_bytes >= 0 AND redirect_count >= 0
        AND internal_link_count >= 0 AND external_link_count >= 0
    ),
    CONSTRAINT crawl_page_attempts_robots_check CHECK (
        robots_decision IN ('allowed', 'denied', 'unknown')
    ),
    CONSTRAINT crawl_page_attempts_outcome_check CHECK (
        outcome IN (
            'complete', 'redirected', 'robots_denied', 'unsupported_content',
            'too_large', 'http_error', 'timeout', 'network_error', 'invalid_response'
        )
    )
);

CREATE INDEX crawl_page_attempts_started_idx
    ON crawl_page_attempts (started_at DESC, run_id, sequence);

CREATE TABLE crawl_service_heartbeats (
    service TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    state TEXT NOT NULL,
    current_origin TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (service, instance_id),
    CONSTRAINT crawl_service_heartbeats_service_check
        CHECK (service IN ('discovery', 'worker')),
    CONSTRAINT crawl_service_heartbeats_state_check
        CHECK (state IN ('starting', 'idle', 'running', 'paused', 'stopping', 'failed')),
    CONSTRAINT crawl_service_heartbeats_origin_check
        CHECK (current_origin = '' OR current_origin ~ '^https?://[^/?#]+$'),
    CONSTRAINT crawl_service_heartbeats_time_order_check
        CHECK (updated_at >= started_at)
);

CREATE INDEX crawl_service_heartbeats_updated_idx
    ON crawl_service_heartbeats (updated_at DESC);

CREATE TABLE verification_queue_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    origin TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    event TEXT NOT NULL,
    mode TEXT NOT NULL,
    available_at TIMESTAMPTZ,
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_generation BIGINT NOT NULL DEFAULT 0,
    lease_expires_at TIMESTAMPTZ,
    CONSTRAINT verification_queue_events_event_check
        CHECK (event IN ('scheduled', 'claimed', 'renewed', 'rescheduled', 'completed', 'removed')),
    CONSTRAINT verification_queue_events_mode_check
        CHECK (mode IN ('probe', 'recurring')),
    CONSTRAINT verification_queue_events_generation_check
        CHECK (lease_generation >= 0)
);

CREATE INDEX verification_queue_events_origin_idx
    ON verification_queue_events (origin, occurred_at DESC, id DESC);

CREATE INDEX verification_queue_events_occurred_idx
    ON verification_queue_events (occurred_at DESC, id DESC);

CREATE FUNCTION record_verification_queue_event()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    queue_row verification_queue%ROWTYPE;
    queue_event TEXT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        queue_row := OLD;
        queue_event := 'removed';
    ELSE
        queue_row := NEW;
        IF TG_OP = 'INSERT' THEN
            queue_event := 'scheduled';
        ELSIF OLD.lease_owner IS NULL AND NEW.lease_owner IS NOT NULL THEN
            queue_event := 'claimed';
        ELSIF OLD.lease_owner IS NOT NULL AND NEW.lease_owner IS NULL THEN
            IF NEW.available_at IS DISTINCT FROM OLD.available_at THEN
                queue_event := 'rescheduled';
            ELSE
                queue_event := 'completed';
            END IF;
        ELSIF NEW.lease_expires_at IS DISTINCT FROM OLD.lease_expires_at THEN
            queue_event := 'renewed';
        ELSE
            queue_event := 'scheduled';
        END IF;
    END IF;

    INSERT INTO verification_queue_events (
        origin, event, mode, available_at, lease_owner,
        lease_generation, lease_expires_at
    ) VALUES (
        queue_row.origin, queue_event, queue_row.mode,
        queue_row.available_at, COALESCE(queue_row.lease_owner, ''),
        queue_row.lease_generation, queue_row.lease_expires_at
    );

    RETURN queue_row;
END;
$$;

CREATE TRIGGER verification_queue_event_trigger
AFTER INSERT OR UPDATE OR DELETE ON verification_queue
FOR EACH ROW EXECUTE FUNCTION record_verification_queue_event();

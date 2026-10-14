-- Goal: service heartbeats record the build that is actually running.
-- Constraints: existing rows stay valid with empty identity. Do not change
-- heartbeat state, origin, or timing checks.
ALTER TABLE crawl_service_heartbeats
    ADD COLUMN build_version TEXT NOT NULL DEFAULT '',
    ADD COLUMN build_commit TEXT NOT NULL DEFAULT '';

ALTER TABLE crawl_service_heartbeats
    ADD CONSTRAINT crawl_service_heartbeats_build_version_check
        CHECK (octet_length(build_version) <= 128),
    ADD CONSTRAINT crawl_service_heartbeats_build_commit_check
        CHECK (octet_length(build_commit) <= 64);

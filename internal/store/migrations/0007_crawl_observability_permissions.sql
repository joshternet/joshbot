DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_roles
        WHERE rolname = 'joshbot_app'
    ) THEN
        GRANT DELETE
            ON TABLE crawl_runs
            TO joshbot_app;
    END IF;
END
$$;

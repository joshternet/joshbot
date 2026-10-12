ALTER TABLE verification_queue_events
    DROP CONSTRAINT verification_queue_events_event_check,
    ADD CONSTRAINT verification_queue_events_event_check
    CHECK (
        event IN (
            'scheduled',
            'claimed',
            'renewed',
            'rescheduled',
            'completed',
            'abandoned',
            'removed'
        )
    );

CREATE OR REPLACE FUNCTION record_verification_queue_event()
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
            ELSIF NEW.mode IS DISTINCT FROM OLD.mode
                OR NEW.consecutive_failures IS DISTINCT FROM OLD.consecutive_failures
                OR NEW.next_attempt_at IS DISTINCT FROM OLD.next_attempt_at
                OR NEW.last_failure_category IS DISTINCT FROM OLD.last_failure_category
            THEN
                queue_event := 'completed';
            ELSE
                queue_event := 'abandoned';
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

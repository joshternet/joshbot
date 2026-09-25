ALTER TABLE verification_queue
    DROP CONSTRAINT verification_queue_mode_check,
    ADD CONSTRAINT verification_queue_mode_check
    CHECK (
        mode IN (
            'recurring',
            'probe',
            'reprobe'
        )
    );

ALTER TABLE verification_queue_events
    DROP CONSTRAINT verification_queue_events_mode_check,
    ADD CONSTRAINT verification_queue_events_mode_check
    CHECK (
        mode IN (
            'recurring',
            'probe',
            'reprobe'
        )
    );

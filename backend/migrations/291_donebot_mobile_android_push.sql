DO $$
DECLARE
    constraint_name TEXT;
BEGIN
    FOR constraint_name IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'donebot_mobile_devices'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%environment%'
    LOOP
        EXECUTE format('ALTER TABLE donebot_mobile_devices DROP CONSTRAINT %I', constraint_name);
    END LOOP;
END $$;

ALTER TABLE donebot_mobile_devices
    ADD CONSTRAINT donebot_mobile_devices_environment_check
    CHECK (environment IN ('sandbox', 'production', 'sse'));

CREATE INDEX IF NOT EXISTS donebot_mobile_outbox_device
    ON donebot_mobile_outbox(device_id, session_id, id);

-- Preserve accepted-task ownership and idempotency records in populated environments.
BEGIN;
SELECT pg_advisory_xact_lock(694208311321144027);
LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF to_regclass('donebot_media_operations') IS NOT NULL THEN
        LOCK TABLE donebot_media_operations IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM donebot_media_operations) THEN
            RAISE EXCEPTION 'DoneBot media operations exist; disable the feature or restore a verified backup instead of dropping recovery records';
        END IF;
    END IF;
END $$;
DROP TABLE IF EXISTS donebot_media_operations;
DELETE FROM schema_migrations WHERE filename = '253_donebot_media_operations.sql';
COMMIT;

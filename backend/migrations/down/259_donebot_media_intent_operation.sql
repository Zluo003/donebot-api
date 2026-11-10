BEGIN;
SELECT pg_advisory_xact_lock(694208311321144027);
LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE;
LOCK TABLE donebot_media_operations IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM donebot_media_operations WHERE native_path='/v1/donebot/media/intent') THEN
        RAISE EXCEPTION 'DoneBot media intent records exist; retain operation metadata and restore a verified backup before rollback';
    END IF;
END $$;
ALTER TABLE donebot_media_operations DROP CONSTRAINT IF EXISTS donebot_media_intent_operation_check;
ALTER TABLE donebot_media_operations DROP COLUMN IF EXISTS operation;
DELETE FROM schema_migrations WHERE filename='259_donebot_media_intent_operation.sql';
COMMIT;

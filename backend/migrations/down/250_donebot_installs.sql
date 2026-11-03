-- Apply manually before removing DoneBot code. Retire and erase credential
-- material before removing the mapping, then allow the current binary to apply
-- the up migration again if it starts later.
BEGIN;
DO $$
BEGIN
    IF to_regclass('donebot_installs') IS NOT NULL THEN
        UPDATE api_keys
        SET key = '__donebot_rollback__' || id || '__' || substr(md5(key), 1, 12),
            status = 'disabled',
            deleted_at = COALESCE(deleted_at, NOW()),
            updated_at = NOW()
        WHERE id IN (SELECT api_key_id FROM donebot_installs);
    END IF;
END $$;
DROP TABLE IF EXISTS donebot_installs;
DELETE FROM schema_migrations WHERE filename = '250_donebot_installs.sql';
COMMIT;

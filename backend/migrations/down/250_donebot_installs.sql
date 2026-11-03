-- Empty environments only. Never erase credentials or remove the mapping that
-- keeps an installation key out of ordinary key management. For live data use
-- a verified, consistent backup restore; see docs/donebot/回滚说明.md.
-- Stop every gateway instance and worker. Roll back 252, then 251, then 250.
BEGIN;
SELECT pg_advisory_xact_lock(694208311321144027);
LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM schema_migrations WHERE filename IN ('251_assets.sql', '252_donebot_storage_policies.sql'))
       OR to_regclass('assets') IS NOT NULL OR to_regclass('asset_owners') IS NOT NULL THEN
        RAISE EXCEPTION 'rollback 252 and 251 before 250; see docs/donebot/回滚说明.md';
    END IF;
    LOCK TABLE api_keys IN SHARE ROW EXCLUSIVE MODE;
    IF to_regclass('donebot_installs') IS NOT NULL THEN
        LOCK TABLE donebot_installs IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM donebot_installs) THEN
            RAISE EXCEPTION 'DoneBot installations exist; refusing to remove credential management mappings. Restore a verified backup instead; see docs/donebot/回滚说明.md';
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM api_keys WHERE key LIKE 'sk-donebot-%') THEN
        RAISE EXCEPTION 'DoneBot credentials exist without installation mappings; refusing rollback. Restore or repair the mappings from backup; see docs/donebot/回滚说明.md';
    END IF;
END $$;
DROP TABLE IF EXISTS donebot_installs;
DELETE FROM schema_migrations WHERE filename = '250_donebot_installs.sql';
COMMIT;

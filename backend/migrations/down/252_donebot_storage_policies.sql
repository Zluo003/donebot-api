-- Empty environments only. Even one asset can depend on purpose or its saved
-- storage configuration; removing either cannot be reversed by the up migration.
-- Stop every gateway instance and worker. Do not merge/delete cross-purpose
-- objects to bypass this guard. Live-data recovery: docs/donebot/回滚说明.md.
BEGIN;
SELECT pg_advisory_xact_lock(694208311321144027);
LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF to_regclass('assets') IS NOT NULL THEN
        LOCK TABLE assets IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM assets) THEN
            RAISE EXCEPTION 'assets exist; refusing to discard purpose and original storage configuration. Restore a verified backup instead; see docs/donebot/回滚说明.md';
        END IF;
    END IF;
    IF to_regclass('asset_owners') IS NOT NULL THEN
        LOCK TABLE asset_owners IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM asset_owners) THEN
            RAISE EXCEPTION 'asset owners exist; refusing storage policy rollback; see docs/donebot/回滚说明.md';
        END IF;
    END IF;
    LOCK TABLE settings IN SHARE ROW EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM settings WHERE key = 'donebot_storage_policies') THEN
        RAISE EXCEPTION 'storage policy settings exist; refusing to discard configuration. Restore a verified backup instead; see docs/donebot/回滚说明.md';
    END IF;
END $$;
DROP INDEX IF EXISTS idx_assets_purpose_created;
DROP INDEX IF EXISTS idx_assets_purpose_sha256;
ALTER TABLE IF EXISTS assets DROP COLUMN IF EXISTS storage_config;
ALTER TABLE IF EXISTS assets DROP COLUMN IF EXISTS purpose;
DO $$
BEGIN
    IF to_regclass('assets') IS NOT NULL THEN
        CREATE UNIQUE INDEX IF NOT EXISTS idx_assets_sha256 ON assets(sha256);
    END IF;
END $$;
DELETE FROM schema_migrations WHERE filename='252_donebot_storage_policies.sql';
COMMIT;

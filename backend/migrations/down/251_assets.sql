-- Empty environments only; asset metadata is required to locate objects and
-- enforce ownership. Never discard it to force a downgrade.
-- Stop every gateway instance and worker. Roll back 252 before 251.
-- Live-data recovery: docs/donebot/回滚说明.md.
BEGIN;
SELECT pg_advisory_xact_lock(694208311321144027);
LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM schema_migrations WHERE filename = '252_donebot_storage_policies.sql')
       OR EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'assets' AND column_name IN ('purpose', 'storage_config')) THEN
        RAISE EXCEPTION 'rollback 252 before 251; see docs/donebot/回滚说明.md';
    END IF;
    IF to_regclass('assets') IS NOT NULL THEN
        LOCK TABLE assets IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM assets) THEN
            RAISE EXCEPTION 'assets exist; refusing to discard asset metadata. Restore a verified backup instead; see docs/donebot/回滚说明.md';
        END IF;
    END IF;
    IF to_regclass('asset_owners') IS NOT NULL THEN
        LOCK TABLE asset_owners IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM asset_owners) THEN
            RAISE EXCEPTION 'asset owners exist; refusing to discard ownership. Restore a verified backup instead; see docs/donebot/回滚说明.md';
        END IF;
    END IF;
END $$;
DROP TABLE IF EXISTS asset_owners;
DROP TABLE IF EXISTS assets;
DELETE FROM schema_migrations WHERE filename = '251_assets.sql';
COMMIT;

BEGIN;
SELECT pg_advisory_xact_lock(694208311321144027);
LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE;
LOCK TABLE assets IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM assets WHERE purpose='donebot_output') THEN
        RAISE EXCEPTION 'donebot output assets exist; retain the purpose constraint and restore a verified backup before rollback';
    END IF;
END $$;
DROP INDEX IF EXISTS idx_assets_donebot_output_expiry;
ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_purpose_check;
ALTER TABLE assets ADD CONSTRAINT assets_purpose_check CHECK (purpose IN ('upload', 'generated'));
DELETE FROM schema_migrations WHERE filename='258_donebot_output_asset_purpose.sql';
COMMIT;

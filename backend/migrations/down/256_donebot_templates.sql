-- Stop all gateway instances before running this rollback.
-- Existing releases must be preserved by restoring a verified backup instead.
BEGIN;
SELECT pg_advisory_xact_lock(694208311321144027);
LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF to_regclass('donebot_template_releases') IS NOT NULL THEN
        LOCK TABLE donebot_template_releases IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM donebot_template_releases) THEN
            RAISE EXCEPTION 'template releases exist; refusing to discard published content. Restore a verified backup instead';
        END IF;
    END IF;
END $$;
DROP TABLE IF EXISTS donebot_template_releases;
DELETE FROM schema_migrations WHERE filename = '256_donebot_templates.sql';
COMMIT;

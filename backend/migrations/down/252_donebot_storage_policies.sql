-- Run after restoring storage policies to the original directories/buckets.
-- A collision cannot be silently discarded: retained objects may have owners.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM assets GROUP BY sha256 HAVING count(*) > 1) THEN
  RAISE EXCEPTION 'deduplicate cross-purpose assets before rolling back storage policies';
 END IF;
END $$;
DROP INDEX IF EXISTS idx_assets_purpose_created;
DROP INDEX IF EXISTS idx_assets_purpose_sha256;
ALTER TABLE assets DROP COLUMN IF EXISTS storage_config;
ALTER TABLE assets DROP COLUMN IF EXISTS purpose;
CREATE UNIQUE INDEX IF NOT EXISTS idx_assets_sha256 ON assets(sha256);
DELETE FROM settings WHERE key='donebot_storage_policies';
DELETE FROM schema_migrations WHERE filename='252_donebot_storage_policies.sql';

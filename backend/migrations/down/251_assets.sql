BEGIN;
DROP TABLE IF EXISTS asset_owners;
DROP TABLE IF EXISTS assets;
DELETE FROM schema_migrations WHERE filename = '251_assets.sql';
COMMIT;

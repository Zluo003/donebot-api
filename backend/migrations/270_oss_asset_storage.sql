ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_storage_check;
ALTER TABLE assets ADD CONSTRAINT assets_storage_check CHECK (storage IN ('local', 's3', 'oss'));

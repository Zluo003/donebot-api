ALTER TABLE assets ADD COLUMN IF NOT EXISTS purpose VARCHAR(20) NOT NULL DEFAULT 'upload' CHECK (purpose IN ('upload', 'generated'));
ALTER TABLE assets ADD COLUMN IF NOT EXISTS storage_config JSONB;
DROP INDEX IF EXISTS idx_assets_sha256;
CREATE UNIQUE INDEX IF NOT EXISTS idx_assets_purpose_sha256 ON assets(purpose,sha256);
CREATE INDEX IF NOT EXISTS idx_assets_purpose_created ON assets(purpose,created_at,id);

ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_purpose_check;
ALTER TABLE assets ADD CONSTRAINT assets_purpose_check CHECK (purpose IN ('upload', 'generated', 'donebot_output'));
CREATE INDEX IF NOT EXISTS idx_assets_donebot_output_expiry ON assets(expires_at) WHERE purpose='donebot_output';

CREATE TABLE IF NOT EXISTS assets (
    id VARCHAR(40) PRIMARY KEY,
    sha256 CHAR(64) NOT NULL,
    mime VARCHAR(100) NOT NULL,
    size BIGINT NOT NULL CHECK (size > 0),
    storage VARCHAR(20) NOT NULL CHECK (storage IN ('local', 's3')),
    object_key TEXT NOT NULL,
    url TEXT NOT NULL,
    owner_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_assets_sha256 ON assets(sha256);
CREATE INDEX IF NOT EXISTS idx_assets_expires_at ON assets(expires_at);
-- Content is global, while metadata/delete access remains owned by each key.
CREATE TABLE IF NOT EXISTS asset_owners (
    asset_id VARCHAR(40) NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    PRIMARY KEY (asset_id, api_key_id)
);
CREATE INDEX IF NOT EXISTS idx_asset_owners_api_key ON asset_owners(api_key_id);

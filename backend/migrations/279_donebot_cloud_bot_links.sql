CREATE TABLE IF NOT EXISTS donebot_cloud_bot_links (
    token_hash TEXT PRIMARY KEY,
    instance_id UUID NOT NULL REFERENCES donebot_cloud_instances(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    computer_id TEXT NOT NULL,
    token_version BIGINT NOT NULL,
    inner_token_ciphertext BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (instance_id, computer_id)
);
CREATE INDEX IF NOT EXISTS donebot_cloud_bot_links_user ON donebot_cloud_bot_links(user_id, created_at DESC);

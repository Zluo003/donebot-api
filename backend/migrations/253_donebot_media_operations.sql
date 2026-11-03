CREATE TABLE IF NOT EXISTS donebot_media_operations (
    receipt_id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    group_id BIGINT NOT NULL REFERENCES groups(id),
    installation_id UUID NOT NULL REFERENCES donebot_installs(installation_id),
    idempotency_hash CHAR(64) NOT NULL CHECK (idempotency_hash ~ '^[a-f0-9]{64}$'),
    request_fingerprint CHAR(64) NOT NULL CHECK (request_fingerprint ~ '^[a-f0-9]{64}$'),
    media_kind VARCHAR(16) NOT NULL CHECK (media_kind IN ('image', 'video', 'music', 'speech')),
    model VARCHAR(256) NOT NULL DEFAULT '',
    method VARCHAR(8) NOT NULL CHECK (method = 'POST'),
    native_path VARCHAR(100) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'dispatching'
        CHECK (status IN ('dispatching', 'accepted', 'processing', 'completed', 'failed', 'cancelled', 'refunded', 'uncertain')),
    task_id VARCHAR(512) NOT NULL DEFAULT '',
    provider_task_key VARCHAR(600) NOT NULL DEFAULT '',
    provider VARCHAR(32) NOT NULL DEFAULT '',
    usage_request_id VARCHAR(256) NOT NULL,
    estimated_cents NUMERIC(30,10) CHECK (estimated_cents >= 0),
    replay_status INTEGER CHECK (replay_status BETWEEN 200 AND 599),
    replay_content_type VARCHAR(100) NOT NULL DEFAULT '',
    replay_retry_after VARCHAR(64) NOT NULL DEFAULT '',
    replay_body BYTEA CHECK (octet_length(replay_body) <= 2097152),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, api_key_id, group_id, installation_id, idempotency_hash)
);
CREATE INDEX IF NOT EXISTS idx_donebot_media_owner_created
    ON donebot_media_operations(user_id, api_key_id, installation_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_donebot_media_task_owner
    ON donebot_media_operations(api_key_id, task_id, media_kind) WHERE task_id <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_donebot_media_provider_task_owner
    ON donebot_media_operations(api_key_id, provider_task_key) WHERE provider_task_key <> '';

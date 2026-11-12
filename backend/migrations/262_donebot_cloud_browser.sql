CREATE TABLE donebot_cloud_browser_sessions (
    token_hash TEXT PRIMARY KEY,
    instance_id UUID NOT NULL REFERENCES donebot_cloud_instances(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    origin TEXT NOT NULL,
    binding_hash TEXT NOT NULL,
    jwt_ciphertext BYTEA NOT NULL,
    phase TEXT NOT NULL CHECK (phase IN ('launch','session')),
    expires_at TIMESTAMPTZ NOT NULL,
    session_until TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX donebot_cloud_browser_user ON donebot_cloud_browser_sessions(user_id,created_at DESC);

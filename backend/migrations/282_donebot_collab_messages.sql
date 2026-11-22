CREATE TABLE IF NOT EXISTS donebot_collab_messages (
    id BIGSERIAL PRIMARY KEY,
    scope_type VARCHAR(16) NOT NULL CHECK (scope_type IN ('friend', 'team')),
    scope_key VARCHAR(64) NOT NULL,
    sender_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind VARCHAR(16) NOT NULL DEFAULT 'text' CHECK (kind IN ('text', 'file')),
    body TEXT NOT NULL DEFAULT '',
    file_id UUID UNIQUE,
    file_name VARCHAR(255) NOT NULL DEFAULT '',
    file_mime VARCHAR(128) NOT NULL DEFAULT '',
    file_size BIGINT NOT NULL DEFAULT 0 CHECK (file_size >= 0),
    file_sha256 VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_donebot_collab_messages_scope_id
    ON donebot_collab_messages(scope_type, scope_key, id DESC);

CREATE TABLE IF NOT EXISTS donebot_collab_reads (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope_type VARCHAR(16) NOT NULL CHECK (scope_type IN ('friend', 'team')),
    scope_key VARCHAR(64) NOT NULL,
    last_read_id BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, scope_type, scope_key)
);

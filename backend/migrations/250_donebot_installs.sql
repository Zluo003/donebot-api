-- Installation credentials reuse api_keys and its auth/billing chain, but are
-- only created through the DoneBot claim flow. A UUID is globally claimed by one user.
CREATE TABLE IF NOT EXISTS donebot_installs (
    id BIGSERIAL PRIMARY KEY,
    installation_id UUID NOT NULL,
    name VARCHAR(100) NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id),
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    group_id BIGINT NOT NULL REFERENCES groups(id),
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    fingerprint VARCHAR(128),
    last_seen_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (installation_id),
    UNIQUE (api_key_id)
);
CREATE INDEX IF NOT EXISTS idx_donebot_installs_status ON donebot_installs(status);
CREATE INDEX IF NOT EXISTS idx_donebot_installs_user_created ON donebot_installs(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_donebot_installs_group ON donebot_installs(group_id);

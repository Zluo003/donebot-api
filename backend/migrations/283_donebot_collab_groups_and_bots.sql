CREATE TABLE IF NOT EXISTS donebot_team_avatars (
    team_id UUID PRIMARY KEY REFERENCES donebot_teams(id) ON DELETE CASCADE,
    avatar_url TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS donebot_collab_groups (
    id UUID PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    avatar_url TEXT NOT NULL DEFAULT '',
    creator_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS donebot_collab_group_members (
    group_id UUID NOT NULL REFERENCES donebot_collab_groups(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_donebot_collab_group_members_user
    ON donebot_collab_group_members(user_id, group_id);

CREATE TABLE IF NOT EXISTS donebot_collab_chat_bots (
    id UUID PRIMARY KEY,
    scope_type VARCHAR(16) NOT NULL CHECK (scope_type IN ('team', 'group')),
    scope_key VARCHAR(64) NOT NULL,
    owner_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    installation_id UUID NOT NULL,
    local_bot_id VARCHAR(128) NOT NULL,
    bot_name VARCHAR(120) NOT NULL,
    bot_color VARCHAR(32) NOT NULL DEFAULT 'blue',
    bot_avatar_url TEXT NOT NULL DEFAULT '',
    bot_title VARCHAR(200) NOT NULL DEFAULT '',
    allow_all_members BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (scope_type, scope_key, owner_user_id, local_bot_id)
);

CREATE TABLE IF NOT EXISTS donebot_collab_chat_bot_grants (
    chat_bot_id UUID NOT NULL REFERENCES donebot_collab_chat_bots(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (chat_bot_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_donebot_collab_chat_bots_scope
    ON donebot_collab_chat_bots(scope_type, scope_key);

CREATE INDEX IF NOT EXISTS idx_donebot_collab_chat_bots_owner
    ON donebot_collab_chat_bots(owner_user_id, installation_id);

ALTER TABLE donebot_collab_messages
    DROP CONSTRAINT IF EXISTS donebot_collab_messages_scope_type_check;
ALTER TABLE donebot_collab_messages
    ADD CONSTRAINT donebot_collab_messages_scope_type_check
    CHECK (scope_type IN ('friend', 'team', 'group'));

ALTER TABLE donebot_collab_messages
    ADD COLUMN IF NOT EXISTS sender_kind VARCHAR(16) NOT NULL DEFAULT 'user';
ALTER TABLE donebot_collab_messages
    DROP CONSTRAINT IF EXISTS donebot_collab_messages_sender_kind_check;
ALTER TABLE donebot_collab_messages
    ADD CONSTRAINT donebot_collab_messages_sender_kind_check
    CHECK (sender_kind IN ('user', 'bot'));

ALTER TABLE donebot_collab_messages
    ADD COLUMN IF NOT EXISTS sender_bot_id UUID REFERENCES donebot_collab_chat_bots(id) ON DELETE SET NULL;
ALTER TABLE donebot_collab_messages
    ADD COLUMN IF NOT EXISTS sender_bot_name VARCHAR(120) NOT NULL DEFAULT '';
ALTER TABLE donebot_collab_messages
    ADD COLUMN IF NOT EXISTS sender_bot_color VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE donebot_collab_messages
    ADD COLUMN IF NOT EXISTS sender_bot_avatar_url TEXT NOT NULL DEFAULT '';

ALTER TABLE donebot_collab_reads
    DROP CONSTRAINT IF EXISTS donebot_collab_reads_scope_type_check;
ALTER TABLE donebot_collab_reads
    ADD CONSTRAINT donebot_collab_reads_scope_type_check
    CHECK (scope_type IN ('friend', 'team', 'group'));

CREATE TABLE IF NOT EXISTS donebot_collab_bot_invocations (
    id UUID PRIMARY KEY,
    chat_bot_id UUID NOT NULL REFERENCES donebot_collab_chat_bots(id) ON DELETE CASCADE,
    message_id BIGINT NOT NULL REFERENCES donebot_collab_messages(id) ON DELETE CASCADE,
    scope_type VARCHAR(16) NOT NULL CHECK (scope_type IN ('team', 'group')),
    scope_key VARCHAR(64) NOT NULL,
    owner_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    installation_id UUID NOT NULL,
    local_bot_id VARCHAR(128) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'claimed', 'done', 'failed')),
    error_text TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    UNIQUE (chat_bot_id, message_id)
);

CREATE INDEX IF NOT EXISTS idx_donebot_collab_bot_invocations_owner_status
    ON donebot_collab_bot_invocations(owner_user_id, status, created_at);

CREATE TABLE IF NOT EXISTS donebot_team_channels (
    id UUID PRIMARY KEY,
    team_id UUID NOT NULL REFERENCES donebot_teams(id) ON DELETE CASCADE,
    name VARCHAR(60) NOT NULL,
    description VARCHAR(200) NOT NULL DEFAULT '',
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    is_private BOOLEAN NOT NULL DEFAULT FALSE,
    created_by BIGINT NOT NULL REFERENCES users(id),
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_donebot_team_channels_default
    ON donebot_team_channels(team_id) WHERE is_default = TRUE;

CREATE INDEX IF NOT EXISTS idx_donebot_team_channels_team
    ON donebot_team_channels(team_id, archived_at, created_at);

CREATE TABLE IF NOT EXISTS donebot_team_channel_members (
    channel_id UUID NOT NULL REFERENCES donebot_team_channels(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_donebot_team_channel_members_user
    ON donebot_team_channel_members(user_id, channel_id);

CREATE TABLE IF NOT EXISTS donebot_team_member_profiles (
    team_id UUID NOT NULL REFERENCES donebot_teams(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    job_title VARCHAR(40) NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (team_id, user_id)
);

ALTER TABLE donebot_collab_messages
    DROP CONSTRAINT IF EXISTS donebot_collab_messages_scope_type_check;
ALTER TABLE donebot_collab_messages
    ADD CONSTRAINT donebot_collab_messages_scope_type_check
    CHECK (scope_type IN ('friend', 'team', 'group', 'channel'));

ALTER TABLE donebot_collab_reads
    DROP CONSTRAINT IF EXISTS donebot_collab_reads_scope_type_check;
ALTER TABLE donebot_collab_reads
    ADD CONSTRAINT donebot_collab_reads_scope_type_check
    CHECK (scope_type IN ('friend', 'team', 'group', 'channel'));

ALTER TABLE donebot_collab_bot_invocations
    DROP CONSTRAINT IF EXISTS donebot_collab_bot_invocations_scope_type_check;
ALTER TABLE donebot_collab_bot_invocations
    ADD CONSTRAINT donebot_collab_bot_invocations_scope_type_check
    CHECK (scope_type IN ('team', 'group', 'channel'));

CREATE TABLE IF NOT EXISTS donebot_team_tasks (
    id UUID PRIMARY KEY,
    team_id UUID NOT NULL REFERENCES donebot_teams(id) ON DELETE CASCADE,
    channel_id UUID REFERENCES donebot_team_channels(id) ON DELETE SET NULL,
    title VARCHAR(160) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL DEFAULT 'todo'
        CHECK (status IN ('todo', 'in_progress', 'in_review', 'changes_requested', 'blocked', 'done')),
    priority VARCHAR(16) NOT NULL DEFAULT 'normal'
        CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    creator_id BIGINT NOT NULL REFERENCES users(id),
    assignee_id BIGINT REFERENCES users(id),
    reviewer_id BIGINT REFERENCES users(id),
    due_at TIMESTAMPTZ,
    source_scope_type VARCHAR(16) NOT NULL DEFAULT '',
    source_target_id VARCHAR(64) NOT NULL DEFAULT '',
    source_message_id BIGINT,
    source_quote TEXT NOT NULL DEFAULT '',
    submitted_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_donebot_team_tasks_team_status
    ON donebot_team_tasks(team_id, status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_donebot_team_tasks_assignee
    ON donebot_team_tasks(team_id, assignee_id);
CREATE INDEX IF NOT EXISTS idx_donebot_team_tasks_reviewer
    ON donebot_team_tasks(team_id, reviewer_id);
CREATE INDEX IF NOT EXISTS idx_donebot_team_tasks_channel
    ON donebot_team_tasks(team_id, channel_id);

CREATE TABLE IF NOT EXISTS donebot_team_task_collaborators (
    task_id UUID NOT NULL REFERENCES donebot_team_tasks(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, user_id)
);

CREATE TABLE IF NOT EXISTS donebot_team_task_updates (
    id BIGSERIAL PRIMARY KEY,
    task_id UUID NOT NULL REFERENCES donebot_team_tasks(id) ON DELETE CASCADE,
    actor_id BIGINT NOT NULL REFERENCES users(id),
    kind VARCHAR(24) NOT NULL
        CHECK (kind IN ('comment', 'submission', 'approve', 'request_changes', 'status_change')),
    body TEXT NOT NULL DEFAULT '',
    file_id UUID UNIQUE,
    file_name VARCHAR(255) NOT NULL DEFAULT '',
    file_mime VARCHAR(128) NOT NULL DEFAULT '',
    file_size BIGINT NOT NULL DEFAULT 0 CHECK (file_size >= 0),
    file_sha256 VARCHAR(64) NOT NULL DEFAULT '',
    meta_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_donebot_team_task_updates_task
    ON donebot_team_task_updates(task_id, id ASC);

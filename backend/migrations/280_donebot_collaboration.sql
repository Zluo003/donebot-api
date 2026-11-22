CREATE TABLE IF NOT EXISTS donebot_friend_requests (
    id UUID PRIMARY KEY,
    requester_id BIGINT NOT NULL REFERENCES users(id),
    recipient_id BIGINT NOT NULL REFERENCES users(id),
    note VARCHAR(200) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','rejected','cancelled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at TIMESTAMPTZ,
    CHECK (requester_id <> recipient_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS donebot_friend_requests_pending_pair
    ON donebot_friend_requests(requester_id, recipient_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS donebot_friend_requests_recipient_pending
    ON donebot_friend_requests(recipient_id, created_at DESC) WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS donebot_friends (
    user_a BIGINT NOT NULL REFERENCES users(id),
    user_b BIGINT NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_a, user_b),
    CHECK (user_a < user_b)
);
CREATE INDEX IF NOT EXISTS donebot_friends_user_b ON donebot_friends(user_b, user_a);

CREATE TABLE IF NOT EXISTS donebot_collab_folders (
    id UUID PRIMARY KEY,
    owner_id BIGINT NOT NULL REFERENCES users(id),
    installation_id UUID NOT NULL REFERENCES donebot_installs(installation_id),
    device_name VARCHAR(100) NOT NULL,
    name VARCHAR(120) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS donebot_collab_folders_owner
    ON donebot_collab_folders(owner_id, installation_id);

CREATE TABLE IF NOT EXISTS donebot_collab_folder_grants (
    folder_id UUID NOT NULL REFERENCES donebot_collab_folders(id) ON DELETE CASCADE,
    subject_type VARCHAR(16) NOT NULL CHECK (subject_type IN ('user','team')),
    subject_id VARCHAR(64) NOT NULL,
    access VARCHAR(16) NOT NULL CHECK (access IN ('read','write')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (folder_id, subject_type, subject_id)
);
CREATE INDEX IF NOT EXISTS donebot_collab_folder_grants_subject
    ON donebot_collab_folder_grants(subject_type, subject_id);

CREATE TABLE IF NOT EXISTS donebot_collab_bots (
    id UUID PRIMARY KEY,
    owner_id BIGINT NOT NULL REFERENCES users(id),
    source_bot_id VARCHAR(128) NOT NULL DEFAULT '',
    name VARCHAR(120) NOT NULL,
    title VARCHAR(200) NOT NULL DEFAULT '',
    description VARCHAR(1000) NOT NULL DEFAULT '',
    skill_count INTEGER NOT NULL DEFAULT 0 CHECK (skill_count >= 0),
    has_soul BOOLEAN NOT NULL DEFAULT FALSE,
    package_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS donebot_collab_bots_owner
    ON donebot_collab_bots(owner_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS donebot_collab_bot_grants (
    bot_id UUID NOT NULL REFERENCES donebot_collab_bots(id) ON DELETE CASCADE,
    subject_type VARCHAR(16) NOT NULL CHECK (subject_type IN ('user','team')),
    subject_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (bot_id, subject_type, subject_id)
);
CREATE INDEX IF NOT EXISTS donebot_collab_bot_grants_subject
    ON donebot_collab_bot_grants(subject_type, subject_id);

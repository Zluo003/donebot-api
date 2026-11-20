CREATE TABLE donebot_teams (
    id UUID PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    owner_id BIGINT NOT NULL REFERENCES users(id),
    request_key UUID NOT NULL,
    state VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (state IN ('active','archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(owner_id,request_key)
);

CREATE TABLE donebot_team_members (
    team_id UUID NOT NULL REFERENCES donebot_teams(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    role VARCHAR(16) NOT NULL CHECK (role IN ('owner','admin','member')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    removed_at TIMESTAMPTZ,
    PRIMARY KEY(team_id,user_id)
);
CREATE UNIQUE INDEX donebot_team_one_owner ON donebot_team_members(team_id) WHERE role='owner' AND removed_at IS NULL;
CREATE INDEX donebot_team_member_workspaces ON donebot_team_members(user_id,team_id) WHERE removed_at IS NULL;

CREATE TABLE donebot_team_invitations (
    id UUID PRIMARY KEY,
    team_id UUID NOT NULL REFERENCES donebot_teams(id),
    email VARCHAR(320) NOT NULL,
    role VARCHAR(16) NOT NULL CHECK (role IN ('admin','member')),
    token_hash CHAR(64) NOT NULL UNIQUE,
    invited_by BIGINT NOT NULL REFERENCES users(id),
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_by BIGINT REFERENCES users(id),
    accepted_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX donebot_team_pending_invitations ON donebot_team_invitations(team_id,expires_at) WHERE accepted_at IS NULL AND revoked_at IS NULL;

CREATE TABLE donebot_team_devices (
    id UUID PRIMARY KEY,
    team_id UUID NOT NULL REFERENCES donebot_teams(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    installation_id UUID NOT NULL REFERENCES donebot_installs(installation_id),
    name VARCHAR(100) NOT NULL,
    member_version BIGINT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    last_seen TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    UNIQUE(team_id,installation_id)
);

CREATE TABLE donebot_team_cloud_subscriptions (
    id UUID PRIMARY KEY,
    team_id UUID NOT NULL REFERENCES donebot_teams(id),
    payer_id BIGINT NOT NULL REFERENCES users(id),
    state VARCHAR(16) NOT NULL CHECK (state IN ('pending','active','past_due','cancelled')),
    plan_snapshot JSONB NOT NULL,
    paid_until TIMESTAMPTZ NOT NULL,
    cycle INTEGER NOT NULL DEFAULT 1,
    cycle_price NUMERIC(20,6) NOT NULL CHECK (cycle_price > 0),
    auto_renew BOOLEAN NOT NULL DEFAULT TRUE,
    purged_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX donebot_team_one_cloud ON donebot_team_cloud_subscriptions(team_id) WHERE purged_at IS NULL;

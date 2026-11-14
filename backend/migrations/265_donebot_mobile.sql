CREATE TABLE donebot_mobile_hosts (
    installation_id UUID PRIMARY KEY REFERENCES donebot_installs(installation_id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    host_id TEXT NOT NULL UNIQUE,
    public_key TEXT NOT NULL,
    name TEXT NOT NULL,
    last_seen TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE donebot_mobile_devices (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    session_id TEXT NOT NULL,
    token_version BIGINT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    environment TEXT CHECK (environment IN ('sandbox','production')),
    push_token TEXT UNIQUE
);
CREATE TABLE donebot_mobile_grants (
    host_id UUID NOT NULL REFERENCES donebot_mobile_hosts(installation_id) ON DELETE CASCADE,
    device_id UUID NOT NULL REFERENCES donebot_mobile_devices(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL,
    acked_hash TEXT,
    PRIMARY KEY(host_id,device_id)
);
CREATE TABLE donebot_mobile_events (
    host_id UUID NOT NULL REFERENCES donebot_mobile_hosts(installation_id) ON DELETE CASCADE,
    message_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(host_id,message_id)
);
CREATE TABLE donebot_mobile_outbox (
    id BIGSERIAL PRIMARY KEY,
    host_id UUID NOT NULL REFERENCES donebot_mobile_hosts(installation_id) ON DELETE CASCADE,
    device_id UUID NOT NULL REFERENCES donebot_mobile_devices(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    payload JSONB NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX donebot_mobile_outbox_due ON donebot_mobile_outbox(next_at);

CREATE INDEX donebot_mobile_events_age ON donebot_mobile_events(created_at);

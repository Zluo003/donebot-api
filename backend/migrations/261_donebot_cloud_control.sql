ALTER TABLE donebot_cloud_subscriptions ADD COLUMN purge_locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE donebot_cloud_subscriptions ADD COLUMN purged_at TIMESTAMPTZ;
DROP INDEX donebot_cloud_one_instance_per_user;
CREATE UNIQUE INDEX donebot_cloud_one_instance_per_user ON donebot_cloud_subscriptions(user_id) WHERE purged_at IS NULL;
CREATE TABLE donebot_cloud_nodes (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    endpoint TEXT NOT NULL UNIQUE,
    capacity INTEGER NOT NULL CHECK (capacity BETWEEN 1 AND 10000),
    token_hash TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_seen TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE donebot_cloud_instances (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    node_id UUID REFERENCES donebot_cloud_nodes(id),
    desired TEXT NOT NULL DEFAULT 'stopped' CHECK (desired IN ('running','stopped','deleting','deleted')),
    actual TEXT NOT NULL DEFAULT 'pending' CHECK (actual IN ('pending','running','stopped','error','unknown','deleted')),
    generation BIGINT NOT NULL DEFAULT 0,
    bound BOOLEAN NOT NULL DEFAULT FALSE,
    error TEXT NOT NULL DEFAULT '',
    reconciled_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX donebot_cloud_one_live_instance ON donebot_cloud_instances(user_id) WHERE deleted_at IS NULL;
CREATE INDEX donebot_cloud_reconcile ON donebot_cloud_instances(reconciled_at) WHERE deleted_at IS NULL;
CREATE TABLE donebot_cloud_operations (
    id UUID PRIMARY KEY,
    instance_id UUID NOT NULL REFERENCES donebot_cloud_instances(id),
    request_key TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('start','stop','cancel','purge')),
    generation BIGINT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('billing','pending','leased','succeeded','failed')),
    lease_hash TEXT NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(instance_id,request_key)
);
CREATE INDEX donebot_cloud_commands_ready ON donebot_cloud_operations(state,retry_at);

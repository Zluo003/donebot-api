CREATE TABLE IF NOT EXISTS donebot_media_dispatches (
 receipt_id UUID PRIMARY KEY REFERENCES donebot_media_operations(receipt_id) ON DELETE CASCADE,
 task_id VARCHAR(100) NOT NULL UNIQUE,
 user_id BIGINT NOT NULL REFERENCES users(id),
 api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
 state VARCHAR(20) NOT NULL,
 payload JSONB NOT NULL,
 lease_token UUID,
 due_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_donebot_dispatch_due ON donebot_media_dispatches(due_at) WHERE state NOT IN ('completed','failed','uncertain');

CREATE TABLE IF NOT EXISTS donebot_media_dispatch_clocks (
 user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 next_post_at TIMESTAMPTZ NOT NULL
);

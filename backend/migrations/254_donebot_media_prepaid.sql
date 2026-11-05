ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS event_type VARCHAR(16) NOT NULL DEFAULT 'charge';
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS operation_receipt_id UUID;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS related_request_id VARCHAR(64);
DO $$ BEGIN
    IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='usage_logs_event_type_check') THEN
        ALTER TABLE usage_logs ADD CONSTRAINT usage_logs_event_type_check CHECK (event_type IN ('charge','refund'));
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS idx_usage_logs_operation_receipt ON usage_logs(operation_receipt_id) WHERE operation_receipt_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_logs_media_money_event ON usage_logs(operation_receipt_id,event_type) WHERE operation_receipt_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS donebot_media_precharges (
    receipt_id UUID PRIMARY KEY REFERENCES donebot_media_operations(receipt_id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    account_id BIGINT NOT NULL REFERENCES accounts(id),
    group_id BIGINT NOT NULL REFERENCES groups(id),
    model VARCHAR(100) NOT NULL,
    media_kind VARCHAR(16) NOT NULL CHECK (media_kind IN ('image','video','music')),
    amount NUMERIC(20,8) NOT NULL CHECK (amount >= 0),
    total_amount NUMERIC(20,8) NOT NULL CHECK (total_amount >= 0),
    price_snapshot JSONB NOT NULL,
    settlement_input JSONB,
    provider_task_key VARCHAR(600) NOT NULL DEFAULT '',
    provider_request_id VARCHAR(256) NOT NULL DEFAULT '',
    state VARCHAR(16) NOT NULL DEFAULT 'prepaid' CHECK (state IN ('prepaid','settled','refunded')),
    settled_amount NUMERIC(20,8),
    refunded_amount NUMERIC(20,8) NOT NULL DEFAULT 0 CHECK (refunded_amount >= 0 AND refunded_amount <= amount),
    recovery_after TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_donebot_precharge_task ON donebot_media_precharges(api_key_id,provider_task_key) WHERE provider_task_key<>'';
CREATE INDEX IF NOT EXISTS idx_donebot_precharge_active ON donebot_media_precharges(user_id) WHERE state='prepaid';
CREATE INDEX IF NOT EXISTS idx_donebot_precharge_provider_request ON donebot_media_precharges(api_key_id,provider_request_id) WHERE provider_request_id<>'';

-- 289_donebot_market_studio_and_spend.sql
-- 支持 Bot 广场「AI 工作室整包」、能力实验室实测认证样例与单 Bot 四品类消费透视统计

ALTER TABLE donebot_market_listings
    ADD COLUMN IF NOT EXISTS listing_type VARCHAR(16) NOT NULL DEFAULT 'bot',
    ADD COLUMN IF NOT EXISTS bot_count INT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS bots_summary_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS eval_verified BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS eval_samples_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS cost_tier VARCHAR(16) NOT NULL DEFAULT 'standard';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_donebot_market_listings_type'
    ) THEN
        ALTER TABLE donebot_market_listings
            ADD CONSTRAINT chk_donebot_market_listings_type
            CHECK (listing_type IN ('bot', 'studio'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_donebot_market_listings_cost_tier'
    ) THEN
        ALTER TABLE donebot_market_listings
            ADD CONSTRAINT chk_donebot_market_listings_cost_tier
            CHECK (cost_tier IN ('light', 'standard', 'heavy'));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_donebot_market_listings_type_status
    ON donebot_market_listings(listing_type, status, featured DESC, install_count DESC);

-- 用户自有 Bot 消费透视（记录每次实际扣费所属的 bot_id 与四品类，支持客户端展示单 Bot 实际花费）
CREATE TABLE IF NOT EXISTS donebot_bot_spend_events (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bot_id VARCHAR(128) NOT NULL,
    category VARCHAR(16) NOT NULL CHECK (category IN ('llm', 'image', 'video', 'music')),
    cost_amount NUMERIC(20, 6) NOT NULL DEFAULT 0 CHECK (cost_amount >= 0),
    usage_key VARCHAR(160) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, category, usage_key)
);

CREATE INDEX IF NOT EXISTS idx_donebot_bot_spend_user_created
    ON donebot_bot_spend_events(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_donebot_bot_spend_user_bot
    ON donebot_bot_spend_events(user_id, bot_id, created_at DESC);

-- 异步媒体任务受理时暂存 receipt_id -> (user_id, bot_id, category)，结算时写入单 Bot 消费透视
CREATE TABLE IF NOT EXISTS donebot_bot_media_spend_refs (
    receipt_id VARCHAR(128) PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bot_id VARCHAR(128) NOT NULL,
    category VARCHAR(16) NOT NULL CHECK (category IN ('image', 'video', 'music')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_donebot_bot_media_spend_refs_created
    ON donebot_bot_media_spend_refs(created_at DESC);

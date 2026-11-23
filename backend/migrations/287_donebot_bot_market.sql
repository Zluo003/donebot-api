-- 287_donebot_bot_market.sql
-- DoneBot Bot 分享广场、AI 内容安全审核 Agent、四品类消费归因分润与创作者财务账本

CREATE TABLE IF NOT EXISTS donebot_market_settings (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    auto_publish_on_ai_approve BOOLEAN NOT NULL DEFAULT TRUE,
    review_model VARCHAR(160) NOT NULL DEFAULT '',
    review_api_base VARCHAR(512) NOT NULL DEFAULT '',
    review_api_key TEXT NOT NULL DEFAULT '',
    attribution_days INT NOT NULL DEFAULT 180 CHECK (attribution_days >= 1 AND attribution_days <= 3650),
    freeze_days INT NOT NULL DEFAULT 7 CHECK (freeze_days >= 0 AND freeze_days <= 180),
    min_withdraw_amount NUMERIC(20, 6) NOT NULL DEFAULT 100.000000 CHECK (min_withdraw_amount >= 1),
    transfer_bonus_percent NUMERIC(8, 4) NOT NULL DEFAULT 0.0000 CHECK (transfer_bonus_percent >= 0 AND transfer_bonus_percent <= 100),
    rebate_llm_percent NUMERIC(8, 4) NOT NULL DEFAULT 5.0000 CHECK (rebate_llm_percent >= 0 AND rebate_llm_percent <= 50),
    rebate_image_percent NUMERIC(8, 4) NOT NULL DEFAULT 3.0000 CHECK (rebate_image_percent >= 0 AND rebate_image_percent <= 50),
    rebate_video_percent NUMERIC(8, 4) NOT NULL DEFAULT 2.0000 CHECK (rebate_video_percent >= 0 AND rebate_video_percent <= 50),
    rebate_music_percent NUMERIC(8, 4) NOT NULL DEFAULT 2.0000 CHECK (rebate_music_percent >= 0 AND rebate_music_percent <= 50),
    signing_secret TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO donebot_market_settings (id)
VALUES (1)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS donebot_market_categories (
    id BIGSERIAL PRIMARY KEY,
    slug VARCHAR(64) NOT NULL UNIQUE,
    name VARCHAR(80) NOT NULL,
    description VARCHAR(240) NOT NULL DEFAULT '',
    sort_order INT NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO donebot_market_categories (slug, name, description, sort_order, enabled) VALUES
    ('productivity', '办公效率', '会议纪要、邮件整理、文档写作与日程协作助手', 10, TRUE),
    ('engineering', '研发编程', '代码开发、架构评审、测试排障与自动化运维助手', 20, TRUE),
    ('design', '设计创作', '角色设计、视觉创意、分镜绘图与多媒体创作助手', 30, TRUE),
    ('marketing', '文案营销', '社媒内容、品牌策划、投放文案与增长运营助手', 40, TRUE),
    ('data', '数据分析', '表格统计、指标拆解、商业研报与数据洞察助手', 50, TRUE),
    ('research', '学习研究', '文献检索、知识辅导、行业调研与深度总结助手', 60, TRUE),
    ('lifestyle', '生活助理', '旅行规划、日常管理与个人事务助手', 70, TRUE)
ON CONFLICT (slug) DO NOTHING;

CREATE TABLE IF NOT EXISTS donebot_market_listings (
    id UUID PRIMARY KEY,
    author_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category_id BIGINT NOT NULL REFERENCES donebot_market_categories(id),
    source_bot_id VARCHAR(128) NOT NULL DEFAULT '',
    name VARCHAR(120) NOT NULL,
    tagline VARCHAR(200) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    tags_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    sample_prompts_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    appearance_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    has_soul BOOLEAN NOT NULL DEFAULT FALSE,
    skill_count INT NOT NULL DEFAULT 0,
    skill_names_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    latest_release_id UUID,
    published_release_id UUID,
    latest_version INT NOT NULL DEFAULT 0,
    published_version INT NOT NULL DEFAULT 0,
    status VARCHAR(24) NOT NULL DEFAULT 'pending_review'
        CHECK (status IN ('pending_review', 'manual_review', 'published', 'rejected', 'unlisted', 'suspended')),
    featured BOOLEAN NOT NULL DEFAULT FALSE,
    install_count INT NOT NULL DEFAULT 0,
    total_attributed_cost NUMERIC(20, 6) NOT NULL DEFAULT 0,
    total_creator_reward NUMERIC(20, 6) NOT NULL DEFAULT 0,
    report_count INT NOT NULL DEFAULT 0,
    last_review_note TEXT NOT NULL DEFAULT '',
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_donebot_market_listings_status_cat
    ON donebot_market_listings(status, category_id, featured DESC, install_count DESC, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_donebot_market_listings_author
    ON donebot_market_listings(author_user_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS donebot_market_releases (
    id UUID PRIMARY KEY,
    listing_id UUID NOT NULL REFERENCES donebot_market_listings(id) ON DELETE CASCADE,
    version INT NOT NULL CHECK (version >= 1),
    changelog TEXT NOT NULL DEFAULT '',
    soul_text TEXT NOT NULL DEFAULT '',
    skills_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    package_json JSONB NOT NULL,
    package_sha256 VARCHAR(64) NOT NULL,
    package_bytes INT NOT NULL DEFAULT 0,
    review_status VARCHAR(24) NOT NULL DEFAULT 'pending'
        CHECK (review_status IN ('pending', 'reviewing', 'approved', 'rejected', 'manual_review')),
    ai_verdict VARCHAR(24) NOT NULL DEFAULT '',
    ai_risk_score INT NOT NULL DEFAULT 0,
    ai_report_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    reviewed_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    review_note TEXT NOT NULL DEFAULT '',
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (listing_id, version)
);

CREATE INDEX IF NOT EXISTS idx_donebot_market_releases_review_queue
    ON donebot_market_releases(review_status, created_at ASC);

CREATE TABLE IF NOT EXISTS donebot_market_installs (
    id UUID PRIMARY KEY,
    listing_id UUID NOT NULL REFERENCES donebot_market_listings(id) ON DELETE CASCADE,
    release_id UUID NOT NULL REFERENCES donebot_market_releases(id) ON DELETE CASCADE,
    release_version INT NOT NULL DEFAULT 1,
    author_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    installer_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    installation_id VARCHAR(128) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    expires_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    total_cost NUMERIC(20, 6) NOT NULL DEFAULT 0,
    total_reward NUMERIC(20, 6) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_donebot_market_installs_installer
    ON donebot_market_installs(installer_user_id, listing_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_donebot_market_installs_author
    ON donebot_market_installs(author_user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS donebot_market_media_attributions (
    receipt_id VARCHAR(128) PRIMARY KEY,
    install_id UUID NOT NULL REFERENCES donebot_market_installs(id) ON DELETE CASCADE,
    listing_id UUID NOT NULL REFERENCES donebot_market_listings(id) ON DELETE CASCADE,
    author_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    installer_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category VARCHAR(16) NOT NULL CHECK (category IN ('image', 'video', 'music')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_donebot_market_media_attr_created
    ON donebot_market_media_attributions(created_at DESC);

CREATE TABLE IF NOT EXISTS donebot_market_reports (
    id BIGSERIAL PRIMARY KEY,
    listing_id UUID NOT NULL REFERENCES donebot_market_listings(id) ON DELETE CASCADE,
    reporter_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason_type VARCHAR(32) NOT NULL
        CHECK (reason_type IN ('token_farming', 'malicious_loop', 'sensitive_file', 'prompt_injection', 'illegal_content', 'broken', 'other')),
    detail TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    handled_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    handle_note TEXT NOT NULL DEFAULT '',
    handled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_donebot_market_reports_status
    ON donebot_market_reports(status, created_at DESC);

CREATE TABLE IF NOT EXISTS donebot_creator_wallets (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    frozen_amount NUMERIC(20, 6) NOT NULL DEFAULT 0 CHECK (frozen_amount >= 0),
    available_amount NUMERIC(20, 6) NOT NULL DEFAULT 0 CHECK (available_amount >= 0),
    total_earned NUMERIC(20, 6) NOT NULL DEFAULT 0 CHECK (total_earned >= 0),
    total_transferred NUMERIC(20, 6) NOT NULL DEFAULT 0 CHECK (total_transferred >= 0),
    total_withdrawn NUMERIC(20, 6) NOT NULL DEFAULT 0 CHECK (total_withdrawn >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS donebot_creator_reward_ledger (
    id BIGSERIAL PRIMARY KEY,
    creator_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    listing_id UUID REFERENCES donebot_market_listings(id) ON DELETE SET NULL,
    install_id UUID REFERENCES donebot_market_installs(id) ON DELETE SET NULL,
    installer_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    entry_type VARCHAR(24) NOT NULL
        CHECK (entry_type IN ('accrue', 'transfer_to_balance', 'withdraw_freeze', 'withdraw_paid', 'withdraw_refund', 'risk_clawback')),
    category VARCHAR(16) NOT NULL DEFAULT 'system'
        CHECK (category IN ('llm', 'image', 'video', 'music', 'system')),
    source_key VARCHAR(180) NOT NULL UNIQUE,
    base_cost NUMERIC(20, 6) NOT NULL DEFAULT 0,
    rate_percent NUMERIC(8, 4) NOT NULL DEFAULT 0,
    reward_amount NUMERIC(20, 6) NOT NULL DEFAULT 0,
    bonus_amount NUMERIC(20, 6) NOT NULL DEFAULT 0,
    status VARCHAR(16) NOT NULL DEFAULT 'frozen'
        CHECK (status IN ('frozen', 'available', 'settled', 'reversed')),
    available_at TIMESTAMPTZ,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_donebot_creator_ledger_creator
    ON donebot_creator_reward_ledger(creator_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_donebot_creator_ledger_thaw
    ON donebot_creator_reward_ledger(status, available_at ASC)
    WHERE status = 'frozen' AND entry_type = 'accrue';

CREATE TABLE IF NOT EXISTS donebot_creator_withdrawals (
    id BIGSERIAL PRIMARY KEY,
    creator_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount NUMERIC(20, 6) NOT NULL CHECK (amount > 0),
    alipay_account VARCHAR(160) NOT NULL,
    alipay_real_name VARCHAR(80) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'rejected')),
    payout_trade_no VARCHAR(160) NOT NULL DEFAULT '',
    admin_note TEXT NOT NULL DEFAULT '',
    reviewed_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_donebot_creator_withdrawals_creator
    ON donebot_creator_withdrawals(creator_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_donebot_creator_withdrawals_status
    ON donebot_creator_withdrawals(status, created_at ASC);

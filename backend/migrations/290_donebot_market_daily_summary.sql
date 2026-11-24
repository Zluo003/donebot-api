-- Daily materialized creator reward summaries (one immutable row per closed Shanghai calendar day per creator)
-- and per-event LLM token tracking on the underlying audit ledger.

ALTER TABLE donebot_creator_reward_ledger
    ADD COLUMN IF NOT EXISTS tokens BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_donebot_creator_ledger_accrue_day
    ON donebot_creator_reward_ledger(creator_user_id, created_at)
    WHERE entry_type = 'accrue';

CREATE TABLE IF NOT EXISTS donebot_creator_daily_summaries (
    creator_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    summary_date DATE NOT NULL,
    installed_count INT NOT NULL DEFAULT 0,
    new_installs INT NOT NULL DEFAULT 0,
    llm_tokens BIGINT NOT NULL DEFAULT 0,
    llm_calls INT NOT NULL DEFAULT 0,
    media_count INT NOT NULL DEFAULT 0,
    image_count INT NOT NULL DEFAULT 0,
    video_count INT NOT NULL DEFAULT 0,
    music_count INT NOT NULL DEFAULT 0,
    base_cost NUMERIC(20, 6) NOT NULL DEFAULT 0,
    reward_amount NUMERIC(20, 6) NOT NULL DEFAULT 0,
    status VARCHAR(16) NOT NULL DEFAULT 'frozen'
        CHECK (status IN ('frozen', 'available', 'settled')),
    available_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (creator_user_id, summary_date)
);

CREATE INDEX IF NOT EXISTS idx_donebot_creator_daily_creator_date
    ON donebot_creator_daily_summaries(creator_user_id, summary_date DESC);

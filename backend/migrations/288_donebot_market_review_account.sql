-- Allow Bot Marketplace AI Review Agent to select an existing upstream account directly.
ALTER TABLE donebot_market_settings
    ADD COLUMN IF NOT EXISTS review_account_id BIGINT DEFAULT NULL;

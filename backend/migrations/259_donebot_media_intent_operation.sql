ALTER TABLE donebot_media_operations
    ADD COLUMN IF NOT EXISTS operation TEXT NOT NULL DEFAULT '';

ALTER TABLE donebot_media_operations
    ADD CONSTRAINT donebot_media_intent_operation_check
    CHECK (operation = '' OR (native_path = '/v1/donebot/media/intent' AND operation IN ('generate', 'edit', 'extend')));

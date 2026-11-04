BEGIN;
DO $donebot_metadata$
DECLARE current_metadata jsonb;
BEGIN
  SELECT value::jsonb INTO current_metadata FROM settings
    WHERE key = 'donebot.model_capabilities.v3' FOR UPDATE;
  IF NOT FOUND OR current_metadata IS DISTINCT FROM '{"gpt-6.1-terra":{"reasoning":{"levels":["low","high"],"control":"effort","supported":true,"native_field":"reasoning.effort"},"tool_calling":true},"deepseek-flash":{"tool_calling":true},"gpt-6-astra":{"tool_calling":true},"gpt-6.1-sol":{"tool_calling":true},"claude-opus-5-5":{"tool_calling":true},"claude-sonnet-5-5":{"tool_calling":true},"claude-fable-5-1":{"tool_calling":true},"gemini-3.8-flash":{"tool_calling":true},"gemini-4-argon":{"tool_calling":true}}'::jsonb THEN
    RAISE EXCEPTION 'DoneBot model capabilities changed; inspect current metadata before proceeding';
  END IF;
  UPDATE settings SET value = '{"gpt-6.1-terra":{"reasoning":{"levels":["low","high"],"control":"effort","supported":true,"native_field":"reasoning.effort"},"tool_calling":true},"deepseek-flash":{"tool_calling":true}}'::jsonb::text, updated_at = NOW()
    WHERE key = 'donebot.model_capabilities.v3';
END
$donebot_metadata$;
COMMIT;

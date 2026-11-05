DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM donebot_media_precharges) OR
       EXISTS (SELECT 1 FROM usage_logs WHERE operation_receipt_id IS NOT NULL OR event_type='refund') THEN
        RAISE EXCEPTION 'DoneBot media financial history exists; retain migration 254 and roll back only the application binary after reconciliation';
    END IF;
END $$;
DROP TABLE IF EXISTS donebot_media_precharges;

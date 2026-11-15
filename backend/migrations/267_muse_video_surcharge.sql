-- Preserve the original precharge; Muse output overages append one linked event.
ALTER TABLE usage_logs DROP CONSTRAINT IF EXISTS usage_logs_event_type_check;
ALTER TABLE usage_logs ADD CONSTRAINT usage_logs_event_type_check
    CHECK (event_type IN ('charge', 'refund', 'surcharge'));

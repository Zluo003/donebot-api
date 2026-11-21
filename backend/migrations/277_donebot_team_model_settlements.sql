CREATE TABLE donebot_team_model_settlements (
    execution_id UUID PRIMARY KEY REFERENCES donebot_team_model_dispatches(execution_id),
    actual_amount NUMERIC(20,8) NOT NULL CHECK (actual_amount >= 0),
    request_fingerprint VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE FUNCTION donebot_preserve_team_model_settlement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'team model settlement is immutable';
END;
$$;
CREATE TRIGGER donebot_team_model_settlement_identity BEFORE UPDATE OR DELETE ON donebot_team_model_settlements
    FOR EACH ROW EXECUTE FUNCTION donebot_preserve_team_model_settlement();

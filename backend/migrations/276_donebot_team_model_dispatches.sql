CREATE TABLE donebot_team_model_dispatches (
    execution_id UUID PRIMARY KEY REFERENCES donebot_team_executions(id),
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE FUNCTION donebot_preserve_team_model_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'team model dispatch identity is immutable';
END;
$$;
CREATE TRIGGER donebot_team_model_dispatch_identity BEFORE UPDATE OR DELETE ON donebot_team_model_dispatches
    FOR EACH ROW EXECUTE FUNCTION donebot_preserve_team_model_dispatch();

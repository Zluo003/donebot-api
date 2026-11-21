CREATE TABLE donebot_team_pending_usage (
    execution_id UUID PRIMARY KEY REFERENCES donebot_team_model_dispatches(execution_id),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version = 1),
    command JSONB NOT NULL CHECK (jsonb_typeof(command) = 'object' AND octet_length(command::text) <= 65536),
    usage_log JSONB NOT NULL CHECK (jsonb_typeof(usage_log) = 'object' AND octet_length(usage_log::text) <= 65536),
    platform TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (finished_at IS NULL OR settled_at IS NOT NULL)
);
CREATE INDEX donebot_team_pending_usage_recovery ON donebot_team_pending_usage(next_attempt_at, execution_id)
    WHERE finished_at IS NULL;

CREATE FUNCTION donebot_preserve_team_pending_usage() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'team pending usage is immutable';
    END IF;
    IF NEW.execution_id IS DISTINCT FROM OLD.execution_id OR NEW.version IS DISTINCT FROM OLD.version
       OR NEW.command IS DISTINCT FROM OLD.command OR NEW.usage_log IS DISTINCT FROM OLD.usage_log
       OR NEW.platform IS DISTINCT FROM OLD.platform OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (OLD.settled_at IS NOT NULL AND NEW.settled_at IS DISTINCT FROM OLD.settled_at)
       OR (OLD.finished_at IS NOT NULL AND NEW.finished_at IS DISTINCT FROM OLD.finished_at) THEN
        RAISE EXCEPTION 'team pending usage identity is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER donebot_team_pending_usage_identity BEFORE UPDATE OR DELETE ON donebot_team_pending_usage
    FOR EACH ROW EXECUTE FUNCTION donebot_preserve_team_pending_usage();

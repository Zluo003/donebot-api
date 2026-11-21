ALTER TABLE donebot_team_devices ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0);

CREATE FUNCTION donebot_preserve_team_device_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.id IS DISTINCT FROM NEW.id OR OLD.team_id IS DISTINCT FROM NEW.team_id OR
        OLD.user_id IS DISTINCT FROM NEW.user_id OR OLD.installation_id IS DISTINCT FROM NEW.installation_id THEN
        RAISE EXCEPTION 'team device ownership is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER donebot_team_device_owner BEFORE UPDATE ON donebot_team_devices
    FOR EACH ROW EXECUTE FUNCTION donebot_preserve_team_device_owner();

CREATE TABLE donebot_team_executions (
    id UUID PRIMARY KEY,
    team_id UUID NOT NULL REFERENCES donebot_teams(id),
    request_key UUID NOT NULL,
    payer_id BIGINT NOT NULL REFERENCES users(id),
    actor_id BIGINT NOT NULL REFERENCES users(id),
    actor_version BIGINT NOT NULL CHECK (actor_version > 0),
    executor_id BIGINT NOT NULL REFERENCES users(id),
    executor_version BIGINT NOT NULL CHECK (executor_version > 0),
    node_kind VARCHAR(8) NOT NULL CHECK (node_kind IN ('local','cloud')),
    node_id UUID NOT NULL,
    node_version BIGINT NOT NULL CHECK (node_version > 0),
    purpose VARCHAR(8) NOT NULL CHECK (purpose IN ('llm','image','video','music','tts')),
    bot_id VARCHAR(120) NOT NULL,
    thread_id VARCHAR(120) NOT NULL,
    private_to BIGINT REFERENCES users(id),
    request_hash CHAR(64) NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    state VARCHAR(16) NOT NULL DEFAULT 'prepared' CHECK (state IN ('prepared','accepted','completed','failed','cancelled','uncertain')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_at TIMESTAMPTZ,
    UNIQUE(team_id,request_key),
    CHECK (state='cancelled' OR (state='prepared')=(accepted_at IS NULL)),
    CHECK (private_to IS NULL OR private_to=actor_id),
    CHECK (private_to IS NULL OR node_kind='cloud' OR executor_id=actor_id)
);
CREATE INDEX donebot_team_executions_actor ON donebot_team_executions(team_id,actor_id,created_at DESC);

CREATE FUNCTION donebot_preserve_team_execution() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (to_jsonb(OLD)-'state'-'accepted_at') IS DISTINCT FROM (to_jsonb(NEW)-'state'-'accepted_at') THEN
        RAISE EXCEPTION 'team execution identity, request, audience and payer are immutable';
    END IF;
    IF OLD.accepted_at IS NOT NULL AND OLD.accepted_at IS DISTINCT FROM NEW.accepted_at THEN
        RAISE EXCEPTION 'team execution acceptance is immutable';
    END IF;
    IF OLD.accepted_at IS NULL AND NEW.accepted_at IS NOT NULL AND NOT (OLD.state='prepared' AND NEW.state='accepted') THEN
        RAISE EXCEPTION 'team execution acceptance requires admission';
    END IF;
    IF OLD.state<>NEW.state AND NOT (
        OLD.state='prepared' AND NEW.state IN ('accepted','cancelled') OR
        OLD.state='accepted' AND NEW.state IN ('completed','failed','cancelled','uncertain') OR
        OLD.state='uncertain' AND NEW.state IN ('completed','failed','cancelled')
    ) THEN
        RAISE EXCEPTION 'team execution cannot be readmitted';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER donebot_team_execution_identity BEFORE UPDATE ON donebot_team_executions
    FOR EACH ROW EXECUTE FUNCTION donebot_preserve_team_execution();

CREATE TABLE donebot_team_execution_grants (
    token_hash CHAR(64) PRIMARY KEY,
    execution_id UUID NOT NULL REFERENCES donebot_team_executions(id),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX donebot_team_execution_grants_expiry ON donebot_team_execution_grants(expires_at);

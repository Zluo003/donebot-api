CREATE TABLE donebot_team_billing_keys (
    team_id UUID PRIMARY KEY REFERENCES donebot_teams(id),
    payer_id BIGINT NOT NULL REFERENCES users(id),
    group_id BIGINT NOT NULL REFERENCES groups(id),
    api_key_id BIGINT UNIQUE NOT NULL REFERENCES api_keys(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE FUNCTION donebot_preserve_team_billing_key() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'team billing key ownership is immutable';
END;
$$;
CREATE TRIGGER donebot_team_billing_key_identity BEFORE UPDATE ON donebot_team_billing_keys
    FOR EACH ROW EXECUTE FUNCTION donebot_preserve_team_billing_key();

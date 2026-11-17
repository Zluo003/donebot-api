CREATE TABLE donebot_coding_keys (
    user_id BIGINT NOT NULL REFERENCES users(id),
    engine TEXT NOT NULL CHECK (engine IN ('codex', 'claude')),
    api_key_id BIGINT NOT NULL UNIQUE REFERENCES api_keys(id),
    PRIMARY KEY (user_id, engine)
);

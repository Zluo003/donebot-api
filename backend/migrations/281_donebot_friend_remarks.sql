CREATE TABLE IF NOT EXISTS donebot_friend_remarks (
    owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    friend_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    remark VARCHAR(80) NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (owner_id, friend_id),
    CHECK (owner_id <> friend_id)
);

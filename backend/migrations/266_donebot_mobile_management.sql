ALTER TABLE donebot_mobile_hosts
    ADD COLUMN relay_state TEXT NOT NULL DEFAULT 'connecting',
    ADD COLUMN relay_node TEXT NOT NULL DEFAULT '';
ALTER TABLE donebot_mobile_devices
    ADD COLUMN name TEXT NOT NULL DEFAULT 'DoneBot Mobile',
    ADD COLUMN platform TEXT NOT NULL DEFAULT 'ios',
    ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN last_seen TIMESTAMPTZ;

CREATE TABLE donebot_relay_nodes (
    id TEXT PRIMARY KEY CHECK (id ~ '^relay[1-9][0-9]?$'),
    name TEXT NOT NULL,
    endpoint TEXT NOT NULL UNIQUE,
    enabled BOOLEAN NOT NULL DEFAULT true,
    max_hosts INTEGER NOT NULL CHECK (max_hosts BETWEEN 1 AND 100000),
    max_sessions INTEGER NOT NULL CHECK (max_sessions BETWEEN 1 AND 100000),
    max_sockets INTEGER NOT NULL CHECK (max_sockets BETWEEN 4 AND 1000000),
    token_hash TEXT NOT NULL,
    last_seen TIMESTAMPTZ,
    hosts INTEGER NOT NULL DEFAULT 0,
    sessions INTEGER NOT NULL DEFAULT 0,
    sockets INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

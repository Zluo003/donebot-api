CREATE TABLE donebot_template_releases (
    template_id VARCHAR(80) NOT NULL,
    release VARCHAR(30) NOT NULL,
    metadata JSONB NOT NULL,
    document BYTEA NOT NULL CHECK (octet_length(document) <= 4194304),
    published BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (template_id, release)
);

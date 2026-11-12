CREATE TABLE donebot_cloud_runtime_credentials (
    instance_id UUID PRIMARY KEY REFERENCES donebot_cloud_instances(id),
    credential_ciphertext BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

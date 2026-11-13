ALTER TABLE donebot_cloud_nodes ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE donebot_cloud_nodes DROP CONSTRAINT donebot_cloud_nodes_endpoint_key;
CREATE UNIQUE INDEX donebot_cloud_nodes_live_endpoint ON donebot_cloud_nodes(endpoint) WHERE deleted_at IS NULL;

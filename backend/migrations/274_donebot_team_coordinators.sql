ALTER TABLE donebot_team_executions
    ADD COLUMN coordinator_id UUID REFERENCES donebot_cloud_instances(id),
    ADD COLUMN coordinator_generation BIGINT,
    ADD CONSTRAINT donebot_team_execution_coordinator CHECK (
        (coordinator_id IS NULL AND coordinator_generation IS NULL) OR
        (coordinator_id IS NOT NULL AND coordinator_generation IS NOT NULL AND coordinator_generation >= 0)
    );

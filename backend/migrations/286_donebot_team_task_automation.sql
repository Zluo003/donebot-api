ALTER TABLE donebot_team_tasks
    DROP CONSTRAINT IF EXISTS donebot_team_tasks_status_check;
ALTER TABLE donebot_team_tasks
    ADD CONSTRAINT donebot_team_tasks_status_check
    CHECK (status IN (
        'todo', 'in_progress', 'in_review',
        'bot_reviewing', 'bot_revising', 'ready_for_review',
        'changes_requested', 'blocked', 'done'
    ));

ALTER TABLE donebot_team_tasks
    ADD COLUMN IF NOT EXISTS acceptance_criteria JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS auto_review_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS max_auto_revisions INT NOT NULL DEFAULT 2 CHECK (max_auto_revisions >= 0 AND max_auto_revisions <= 5),
    ADD COLUMN IF NOT EXISTS auto_revision_count INT NOT NULL DEFAULT 0 CHECK (auto_revision_count >= 0),
    ADD COLUMN IF NOT EXISTS assignee_auto_resubmit BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS latest_submission_batch_id UUID,
    ADD COLUMN IF NOT EXISTS latest_bot_review_verdict VARCHAR(24) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS latest_bot_review_summary TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS latest_bot_review_json JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE donebot_team_task_updates
    DROP CONSTRAINT IF EXISTS donebot_team_task_updates_kind_check;
ALTER TABLE donebot_team_task_updates
    ADD CONSTRAINT donebot_team_task_updates_kind_check
    CHECK (kind IN (
        'comment', 'submission', 'approve', 'request_changes', 'status_change',
        'bot_review_pass', 'bot_reject', 'bot_escalate'
    ));

ALTER TABLE donebot_team_task_updates
    ADD COLUMN IF NOT EXISTS actor_kind VARCHAR(16) NOT NULL DEFAULT 'user',
    ADD COLUMN IF NOT EXISTS actor_bot_name VARCHAR(120) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS batch_id UUID;

ALTER TABLE donebot_team_task_updates
    DROP CONSTRAINT IF EXISTS donebot_team_task_updates_actor_kind_check;
ALTER TABLE donebot_team_task_updates
    ADD CONSTRAINT donebot_team_task_updates_actor_kind_check
    CHECK (actor_kind IN ('user', 'bot'));

CREATE INDEX IF NOT EXISTS idx_donebot_team_task_updates_batch
    ON donebot_team_task_updates(task_id, batch_id) WHERE batch_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS donebot_team_task_bot_events (
    id UUID PRIMARY KEY,
    team_id UUID NOT NULL REFERENCES donebot_teams(id) ON DELETE CASCADE,
    task_id UUID NOT NULL REFERENCES donebot_team_tasks(id) ON DELETE CASCADE,
    target_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type VARCHAR(32) NOT NULL
        CHECK (event_type IN ('task_assigned', 'task_submitted', 'task_bot_rejected', 'task_human_rejected')),
    batch_id UUID,
    payload_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(16) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'claimed', 'done', 'failed', 'cancelled')),
    error_text TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_donebot_team_task_bot_events_target
    ON donebot_team_task_bot_events(target_user_id, status, created_at ASC);

CREATE INDEX IF NOT EXISTS idx_donebot_team_task_bot_events_task
    ON donebot_team_task_bot_events(task_id, status);

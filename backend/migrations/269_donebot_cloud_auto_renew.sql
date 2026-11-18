ALTER TABLE donebot_cloud_subscriptions ADD COLUMN auto_renew BOOLEAN NOT NULL DEFAULT TRUE;

UPDATE donebot_cloud_subscriptions s SET auto_renew=FALSE
WHERE s.state='cancelled' OR EXISTS (
    SELECT 1 FROM donebot_cloud_instances i JOIN donebot_cloud_operations o ON o.instance_id=i.id AND o.generation=i.generation
    WHERE i.id=s.instance_id AND o.action='cancel'
);
UPDATE donebot_cloud_subscriptions s SET state='active'
WHERE s.state='cancelled' AND s.purged_at IS NULL AND NOT s.purge_locked
  AND EXISTS (SELECT 1 FROM donebot_cloud_money_events e WHERE e.instance_id=s.instance_id AND e.cycle=s.cycle AND e.event='charge')
  AND NOT EXISTS (SELECT 1 FROM donebot_cloud_money_events e WHERE e.instance_id=s.instance_id AND e.cycle=s.cycle AND e.event='refund');

WITH resumed AS (
    UPDATE donebot_cloud_instances i
    SET desired='running',generation=i.generation+1,
        actual=CASE WHEN i.actual='running' THEN 'running' ELSE 'stopped' END,error=''
    FROM donebot_cloud_subscriptions s
    WHERE i.id=s.instance_id AND i.deleted_at IS NULL AND i.desired='stopped'
      AND s.state IN ('pending','active') AND NOT s.auto_renew AND s.paid_until>clock_timestamp()
      AND s.purged_at IS NULL AND NOT s.purge_locked
      AND EXISTS (SELECT 1 FROM donebot_cloud_operations o WHERE o.instance_id=i.id AND o.generation=i.generation AND o.action='cancel')
    RETURNING i.id,i.generation
)
UPDATE donebot_cloud_operations o SET state='succeeded',error=''
FROM resumed r WHERE o.instance_id=r.id AND o.generation=r.generation-1 AND o.action='cancel';

ALTER TABLE donebot_cloud_operations DROP CONSTRAINT donebot_cloud_operations_action_check;
ALTER TABLE donebot_cloud_operations ADD CONSTRAINT donebot_cloud_operations_action_check
    CHECK (action IN ('start','stop','cancel','keep','purge'));

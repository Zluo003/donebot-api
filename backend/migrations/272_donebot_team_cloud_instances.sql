ALTER TABLE donebot_cloud_instances
    ADD COLUMN team_id UUID REFERENCES donebot_teams(id),
    ADD COLUMN plan_snapshot JSONB,
    ADD CONSTRAINT donebot_cloud_instance_scope CHECK ((team_id IS NULL) = (plan_snapshot IS NULL));

DROP INDEX donebot_cloud_one_live_instance;
CREATE UNIQUE INDEX donebot_cloud_one_live_instance ON donebot_cloud_instances(user_id) WHERE deleted_at IS NULL AND team_id IS NULL;
CREATE UNIQUE INDEX donebot_cloud_one_live_team_instance ON donebot_cloud_instances(team_id) WHERE deleted_at IS NULL AND team_id IS NOT NULL;
ALTER TABLE donebot_cloud_nodes ADD COLUMN team_protocol TEXT NOT NULL DEFAULT '';

CREATE FUNCTION donebot_preserve_cloud_scope() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.team_id IS DISTINCT FROM NEW.team_id OR OLD.user_id IS DISTINCT FROM NEW.user_id OR
        OLD.plan_snapshot IS DISTINCT FROM NEW.plan_snapshot THEN
        RAISE EXCEPTION 'cloud instance ownership and purchased specification are immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER donebot_cloud_instance_scope BEFORE UPDATE ON donebot_cloud_instances
    FOR EACH ROW EXECUTE FUNCTION donebot_preserve_cloud_scope();

ALTER TABLE donebot_cloud_subscriptions
    ADD COLUMN team_id UUID REFERENCES donebot_teams(id),
    ADD COLUMN plan_snapshot JSONB,
    ADD CONSTRAINT donebot_cloud_subscription_scope CHECK ((team_id IS NULL) = (plan_snapshot IS NULL));

DROP INDEX donebot_cloud_one_instance_per_user;
CREATE UNIQUE INDEX donebot_cloud_one_instance_per_user ON donebot_cloud_subscriptions(user_id) WHERE purged_at IS NULL AND team_id IS NULL;
CREATE UNIQUE INDEX donebot_cloud_one_subscription_per_team ON donebot_cloud_subscriptions(team_id) WHERE purged_at IS NULL AND team_id IS NOT NULL;

-- Team billing uses the existing atomic balance and money-event ledger.
CREATE FUNCTION donebot_sync_team_subscription() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND (OLD.team_id IS DISTINCT FROM NEW.team_id OR
        OLD.user_id IS DISTINCT FROM NEW.user_id OR OLD.plan_snapshot IS DISTINCT FROM NEW.plan_snapshot) THEN
        RAISE EXCEPTION 'cloud subscription ownership and purchased specification are immutable';
    END IF;
    IF NEW.team_id IS NOT NULL THEN
        INSERT INTO donebot_team_cloud_subscriptions
            (id,team_id,payer_id,state,plan_snapshot,paid_until,cycle,cycle_price,auto_renew,purged_at)
        VALUES (NEW.instance_id,NEW.team_id,NEW.user_id,NEW.state,NEW.plan_snapshot,NEW.paid_until,NEW.cycle,NEW.cycle_price,NEW.auto_renew,NEW.purged_at)
        ON CONFLICT(id) DO UPDATE SET state=EXCLUDED.state,paid_until=EXCLUDED.paid_until,
            cycle=EXCLUDED.cycle,cycle_price=EXCLUDED.cycle_price,auto_renew=EXCLUDED.auto_renew,purged_at=EXCLUDED.purged_at;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER donebot_team_subscription_ledger AFTER INSERT OR UPDATE ON donebot_cloud_subscriptions
    FOR EACH ROW EXECUTE FUNCTION donebot_sync_team_subscription();

CREATE TABLE donebot_cloud_subscriptions (
    instance_id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    state TEXT NOT NULL CHECK (state IN ('pending','active','past_due','cancelled')),
    paid_until TIMESTAMPTZ NOT NULL,
    cycle INTEGER NOT NULL CHECK (cycle > 0),
    cycle_price NUMERIC(20,6) NOT NULL CHECK (cycle_price > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX donebot_cloud_one_instance_per_user ON donebot_cloud_subscriptions(user_id);
CREATE TABLE donebot_cloud_money_events (
    id BIGSERIAL PRIMARY KEY,
    instance_id UUID NOT NULL REFERENCES donebot_cloud_subscriptions(instance_id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    cycle INTEGER NOT NULL,
    event TEXT NOT NULL CHECK (event IN ('charge','refund')),
    amount NUMERIC(20,6) NOT NULL CHECK (amount > 0),
    balance_after NUMERIC(20,10) NOT NULL,
    paid_until TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(instance_id,cycle,event)
);

//go:build integration

package cloudbilling

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPostgresAutoRenewDoesNotInterruptPaidCloud(t *testing.T) {
	s := postgresFixture(t)
	ctx := context.Background()
	node, token, err := s.SaveNode(ctx, "", NodeInput{Name: "renewal-fixture", Endpoint: "https://10.42.0.1", Capacity: 2, Enabled: true})
	require.NoError(t, err)
	_, err = s.Claim(ctx, node.ID, token, nil)
	require.NoError(t, err)
	for _, owner := range []int64{101, 303} {
		v, _, err := s.Mutate(ctx, owner, "", "start", uuid.NewString())
		require.NoError(t, err)
		claim, err := s.Claim(ctx, node.ID, token, nil)
		require.NoError(t, err)
		require.NotNil(t, claim.Command)
		if owner == 101 {
			require.NoError(t, s.Report(ctx, node.ID, token, claim.Command.ID, claim.Command.LeaseToken, ""))
		}
		before, err := s.Owned(ctx, v.ID, owner)
		require.NoError(t, err)
		cancelled, operation, err := s.Mutate(ctx, owner, v.ID, "cancel", "disable-renewal")
		require.NoError(t, err)
		require.Equal(t, "succeeded", operation.State)
		require.False(t, cancelled.AutoRenew)
		require.Equal(t, before.Generation, cancelled.Generation)
		require.Equal(t, before.Actual, cancelled.Actual)
		require.Equal(t, before.Desired, cancelled.Desired)
		require.Equal(t, before.PaidUntil, cancelled.PaidUntil)
		require.Equal(t, before.SubscriptionState, cancelled.SubscriptionState)
		_, same, err := s.Mutate(ctx, owner, v.ID, "cancel", "disable-renewal")
		require.NoError(t, err)
		require.Equal(t, operation.ID, same.ID)
		if owner == 303 {
			require.NoError(t, s.Report(ctx, node.ID, token, claim.Command.ID, claim.Command.LeaseToken, ""))
		}
		require.NoError(t, s.Sweep(ctx))
		leases, err := s.Claim(ctx, node.ID, token, nil)
		require.NoError(t, err)
		require.Nil(t, leases.Command)
		require.NotEmpty(t, leases.Leases)
		_, _, err = s.Mutate(ctx, owner+999, v.ID, "keep", uuid.NewString())
		require.Error(t, err)
		kept, _, err := s.Mutate(ctx, owner, v.ID, "keep", "enable-renewal")
		require.NoError(t, err)
		require.True(t, kept.AutoRenew)
		require.Equal(t, before.PaidUntil, kept.PaidUntil)
		require.Equal(t, before.Generation, kept.Generation)
		_, _, err = s.Mutate(ctx, owner, v.ID, "cancel", "disable-again")
		require.NoError(t, err)
		_, err = s.db.Exec(`UPDATE donebot_cloud_subscriptions SET paid_until=NOW()-INTERVAL '1 minute' WHERE instance_id=$1::uuid`, v.ID)
		require.NoError(t, err)
		require.NoError(t, s.Sweep(ctx))
		sub, err := s.Action(ctx, v.ID, "renew", 0)
		require.NoError(t, err)
		require.False(t, sub.AutoRenew)
		require.Equal(t, 1, sub.Cycle)
		_, _, err = s.Mutate(ctx, owner, v.ID, "keep", "enable-after-expiry")
		require.Error(t, err)
		stop, err := s.Claim(ctx, node.ID, token, nil)
		require.NoError(t, err)
		require.NotNil(t, stop.Command)
		require.Equal(t, "stop", stop.Command.Action)
		require.NoError(t, s.Report(ctx, node.ID, token, stop.Command.ID, stop.Command.LeaseToken, ""))
		var charges, refunds int
		require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FILTER (WHERE event='charge'),COUNT(*) FILTER (WHERE event='refund') FROM donebot_cloud_money_events WHERE instance_id=$1::uuid`, v.ID).Scan(&charges, &refunds))
		require.Equal(t, 1, charges)
		require.Zero(t, refunds)
		_, _, err = s.Mutate(ctx, owner, v.ID, "start", "purchase-after-expiry")
		require.NoError(t, err)
		sub, err = s.Action(ctx, v.ID, "status", 0)
		require.NoError(t, err)
		require.True(t, sub.AutoRenew)
		require.Equal(t, 2, sub.Cycle)
		start, err := s.Claim(ctx, node.ID, token, nil)
		require.NoError(t, err)
		require.NotNil(t, start.Command)
		require.NoError(t, s.Report(ctx, node.ID, token, start.Command.ID, start.Command.LeaseToken, ""))
	}
}

func TestPostgresAutoRenewMigrationPreservesUnrefundedService(t *testing.T) {
	s := postgresFixture(t)
	ctx := context.Background()
	paid, refunded := uuid.NewString(), uuid.NewString()
	_, err := s.Action(ctx, paid, "purchase", 101)
	require.NoError(t, err)
	_, err = s.Action(ctx, paid, "activate", 0)
	require.NoError(t, err)
	_, err = s.Action(ctx, paid, "cancel", 0)
	require.NoError(t, err)
	_, err = s.Action(ctx, refunded, "purchase", 303)
	require.NoError(t, err)
	_, err = s.Action(ctx, refunded, "cancel", 0)
	require.NoError(t, err)
	_, err = s.db.Exec(`INSERT INTO donebot_cloud_instances(id,user_id,desired,actual,generation) VALUES($1::uuid,101,'stopped','stopped',7)`, paid)
	require.NoError(t, err)
	_, err = s.db.Exec(`INSERT INTO donebot_cloud_operations(id,instance_id,request_key,action,generation,state) VALUES($2::uuid,$1::uuid,'legacy-cancel','cancel',7,'succeeded')`, paid, uuid.NewString())
	require.NoError(t, err)
	_, err = s.db.Exec(`ALTER TABLE donebot_cloud_subscriptions DROP COLUMN auto_renew`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/269_donebot_cloud_auto_renew.sql")
	require.NoError(t, err)
	_, err = s.db.Exec(string(migration))
	require.NoError(t, err)
	for _, id := range []string{paid, refunded} {
		sub, err := s.Action(ctx, id, "status", 0)
		require.NoError(t, err)
		require.False(t, sub.AutoRenew)
		if id == paid {
			require.Equal(t, "active", sub.State)
		} else {
			require.Equal(t, "cancelled", sub.State)
		}
	}
	recovered, err := s.Owned(ctx, paid, 101)
	require.NoError(t, err)
	require.Equal(t, "running", recovered.Desired)
	require.EqualValues(t, 8, recovered.Generation)
	require.False(t, recovered.AutoRenew)
	_, err = s.Action(ctx, paid, "purchase", 101)
	require.NoError(t, err)
	var charges int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM donebot_cloud_money_events WHERE instance_id=$1::uuid AND event='charge'`, paid).Scan(&charges))
	require.Equal(t, 1, charges)
}

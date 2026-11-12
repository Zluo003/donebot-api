//go:build integration

package cloudbilling

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPostgresMultiNodeControlAndRetention(t *testing.T) {
	s := postgresFixture(t)
	ctx := context.Background()
	n1, k1, err := s.SaveNode(ctx, "", NodeInput{Name: "fixture-one", Endpoint: "https://10.42.0.1", Capacity: 1, Enabled: true})
	require.NoError(t, err)
	n2, k2, err := s.SaveNode(ctx, "", NodeInput{Name: "fixture-two", Endpoint: "https://10.42.0.2", Capacity: 1, Enabled: true})
	require.NoError(t, err)
	_, err = s.Claim(ctx, n1.ID, k1, nil)
	require.NoError(t, err)
	_, err = s.Claim(ctx, n2.ID, k2, nil)
	require.NoError(t, err)
	a, oa, err := s.Mutate(ctx, 101, "", "start", "create-a")
	require.NoError(t, err)
	require.Equal(t, "pending", oa.State)
	b, ob, err := s.Mutate(ctx, 303, "", "start", "create-b")
	require.NoError(t, err)
	require.Equal(t, "pending", ob.State)
	a, err = s.Owned(ctx, a.ID, 101)
	require.NoError(t, err)
	b, err = s.Owned(ctx, b.ID, 303)
	require.NoError(t, err)
	require.NotEqual(t, a.NodeID, b.NodeID)
	_, _, err = s.Mutate(ctx, 202, "", "start", "capacity")
	require.NoError(t, err)
	ops, err := s.Operations(ctx, a.ID, 101)
	require.NoError(t, err)
	require.Len(t, ops, 1)
	_, err = s.Owned(ctx, b.ID, 101)
	require.Error(t, err)
	keys := map[string]string{n1.ID: k1, n2.ID: k2}
	claims := map[string]*Claim{}
	for _, node := range []string{n1.ID, n2.ID} {
		claim, e := s.Claim(ctx, node, keys[node], nil)
		require.NoError(t, e)
		require.NotNil(t, claim.Command)
		claims[node] = claim
	}
	ca := claims[a.NodeID].Command
	require.Error(t, s.Report(ctx, b.NodeID, keys[b.NodeID], ca.ID, ca.LeaseToken, ""))
	for node, claim := range claims {
		require.NoError(t, s.Report(ctx, node, keys[node], claim.Command.ID, claim.Command.LeaseToken, ""))
	}
	same, op, err := s.Mutate(ctx, 101, a.ID, "start", "create-a")
	require.NoError(t, err)
	require.Equal(t, a.ID, same.ID)
	require.Equal(t, oa.ID, op.ID)
	_, err = s.db.Exec(`UPDATE users SET balance=0 WHERE id=101; UPDATE donebot_cloud_subscriptions SET paid_until=NOW()-INTERVAL '8 days' WHERE user_id=101`)
	require.NoError(t, err)
	require.NoError(t, s.Sweep(ctx))
	purge, err := s.Claim(ctx, a.NodeID, keys[a.NodeID], nil)
	require.NoError(t, err)
	require.NotNil(t, purge.Command)
	require.Equal(t, "purge", purge.Command.Action)
	require.NoError(t, s.ConfirmPurge(ctx, a.NodeID, keys[a.NodeID], purge.Command.ID, purge.Command.LeaseToken))
	_, _, err = s.Mutate(ctx, 101, a.ID, "start", "too-late")
	require.Error(t, err)
	_, err = s.Action(ctx, a.ID, "purchase", 101)
	require.Error(t, err)
	require.NoError(t, s.Report(ctx, a.NodeID, keys[a.NodeID], purge.Command.ID, purge.Command.LeaseToken, "injected_delete_failure"))
	after, err := s.Owned(ctx, a.ID, 101)
	require.NoError(t, err)
	require.Equal(t, "error", after.Actual)
	require.True(t, after.PurgeLocked)
	_, err = s.db.Exec(`UPDATE donebot_cloud_operations SET retry_at=NOW()-INTERVAL '1 second' WHERE id=$1::uuid`, purge.Command.ID)
	require.NoError(t, err)
	retry, err := s.Claim(ctx, a.NodeID, keys[a.NodeID], nil)
	require.NoError(t, err)
	require.Equal(t, purge.Command.ID, retry.Command.ID)
	require.NotEqual(t, purge.Command.LeaseToken, retry.Command.LeaseToken)
	require.Error(t, s.Report(ctx, a.NodeID, keys[a.NodeID], purge.Command.ID, purge.Command.LeaseToken, ""))
	require.NoError(t, s.ConfirmPurge(ctx, a.NodeID, keys[a.NodeID], retry.Command.ID, retry.Command.LeaseToken))
	require.NoError(t, s.Report(ctx, a.NodeID, keys[a.NodeID], retry.Command.ID, retry.Command.LeaseToken, ""))
	_, err = s.Owned(ctx, a.ID, 101)
	require.Error(t, err)
	var ledger int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM donebot_cloud_money_events WHERE instance_id=$1::uuid`, a.ID).Scan(&ledger))
	require.Equal(t, 1, ledger)
	_, err = s.db.Exec(`UPDATE users SET balance=20 WHERE id=101`)
	require.NoError(t, err)
	replacement, _, err := s.Mutate(ctx, 101, "", "start", "after-purge")
	require.NoError(t, err)
	require.NotEqual(t, a.ID, replacement.ID)
	// A queued deletion loses permission if renewal wins before its irreversible confirmation.
	_, err = s.db.Exec(`UPDATE users SET balance=0 WHERE id=303;UPDATE donebot_cloud_subscriptions SET paid_until=NOW()-INTERVAL '8 days' WHERE user_id=303`)
	require.NoError(t, err)
	require.NoError(t, s.Sweep(ctx))
	queued, err := s.Claim(ctx, b.NodeID, keys[b.NodeID], nil)
	require.NoError(t, err)
	require.NotNil(t, queued.Command)
	require.Equal(t, "purge", queued.Command.Action)
	_, err = s.db.Exec(`UPDATE users SET balance=20 WHERE id=303`)
	require.NoError(t, err)
	_, _, err = s.Mutate(ctx, 303, b.ID, "start", "renew-before-delete")
	require.NoError(t, err)
	require.Error(t, s.ConfirmPurge(ctx, b.NodeID, keys[b.NodeID], queued.Command.ID, queued.Command.LeaseToken))
	// A never-activated instance refunds its pending charge when automatic cleanup succeeds.
	replacement, err = s.Owned(ctx, replacement.ID, 101)
	require.NoError(t, err)
	_, err = s.db.Exec(`UPDATE donebot_cloud_subscriptions SET paid_until=NOW()-INTERVAL '8 days' WHERE instance_id=$1::uuid`, replacement.ID)
	require.NoError(t, err)
	cleanup, err := s.Claim(ctx, replacement.NodeID, keys[replacement.NodeID], nil)
	require.NoError(t, err)
	require.Equal(t, "purge", cleanup.Command.Action)
	require.NoError(t, s.ConfirmPurge(ctx, replacement.NodeID, keys[replacement.NodeID], cleanup.Command.ID, cleanup.Command.LeaseToken))
	require.NoError(t, s.Report(ctx, replacement.NodeID, keys[replacement.NodeID], cleanup.Command.ID, cleanup.Command.LeaseToken, ""))
	var refunds int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM donebot_cloud_money_events WHERE instance_id=$1::uuid AND event='refund'`, replacement.ID).Scan(&refunds))
	require.Equal(t, 1, refunds)
}

func TestPostgresLocalCleanupAfterInterruptedAssignment(t *testing.T) {
	s := postgresFixture(t)
	ctx := context.Background()
	v, op, err := s.Mutate(ctx, 101, "", "start", "no-node")
	require.NoError(t, err)
	require.Equal(t, "capacity_unavailable", op.Error)
	// Simulate a committed payment followed by a rolled-back node assignment.
	_, err = s.Action(ctx, v.ID, "purchase", 101)
	require.NoError(t, err)
	_, err = s.db.Exec(`UPDATE donebot_cloud_subscriptions SET paid_until=NOW()-INTERVAL '8 days' WHERE instance_id=$1::uuid`, v.ID)
	require.NoError(t, err)
	require.NoError(t, s.Sweep(ctx))
	require.NoError(t, s.Sweep(ctx))
	_, err = s.Owned(ctx, v.ID, 101)
	require.Error(t, err)
	var balance string
	require.NoError(t, s.db.QueryRow(`SELECT balance::text FROM users WHERE id=101`).Scan(&balance))
	require.Equal(t, "20.0000000000", balance)
	_, stop, err := s.Mutate(ctx, 202, "", "cancel", "cancel-without-runtime")
	require.NoError(t, err)
	var state string
	require.NoError(t, s.db.QueryRow(`SELECT state FROM donebot_cloud_operations WHERE id=$1::uuid`, stop.ID).Scan(&state))
	require.Equal(t, "succeeded", state)
}

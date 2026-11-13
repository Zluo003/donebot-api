//go:build integration

package cloudbilling

import (
	"context"
	"fmt"
	"sync"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPostgresNodeDeletionPreservesHistoryAndRejectsLiveInstances(t *testing.T) {
	s := postgresFixture(t)
	ctx := context.Background()
	node, token, err := s.SaveNode(ctx, "", NodeInput{Name: "retire-fixture", Endpoint: "https://10.42.0.1", Capacity: 1, Enabled: true})
	require.NoError(t, err)
	_, err = s.Claim(ctx, node.ID, token, nil)
	require.NoError(t, err)
	v, op, err := s.Mutate(ctx, 101, "", "start", "node-retirement")
	require.NoError(t, err)
	require.Equal(t, "pending", op.State)
	for _, state := range []struct{ desired, actual string }{{"running", "pending"}, {"stopped", "stopped"}, {"deleting", "error"}} {
		_, err = s.db.Exec(`UPDATE donebot_cloud_instances SET desired=$2,actual=$3 WHERE id=$1::uuid`, v.ID, state.desired, state.actual)
		require.NoError(t, err)
		err = s.DeleteNode(ctx, node.ID)
		require.Equal(t, "CLOUD_NODE_IN_USE", infraerrors.Reason(err))
		nodes, e := s.Nodes(ctx)
		require.NoError(t, e)
		require.Len(t, nodes, 1)
		require.Equal(t, 1, nodes[0].Assigned)
		require.NoError(t, s.nodeAuth(ctx, node.ID, token))
	}
	_, err = s.db.Exec(`UPDATE donebot_cloud_instances SET desired='running',actual='pending' WHERE id=$1::uuid`, v.ID)
	require.NoError(t, err)
	_, err = s.db.Exec(`UPDATE users SET balance=0 WHERE id=101; UPDATE donebot_cloud_subscriptions SET paid_until=clock_timestamp()-INTERVAL '8 days' WHERE user_id=101`)
	require.NoError(t, err)
	claim, err := s.Claim(ctx, node.ID, token, nil)
	require.NoError(t, err)
	require.NotNil(t, claim.Command)
	require.Equal(t, "purge", claim.Command.Action)
	require.NoError(t, s.ConfirmPurge(ctx, node.ID, token, claim.Command.ID, claim.Command.LeaseToken))
	require.NoError(t, s.Report(ctx, node.ID, token, claim.Command.ID, claim.Command.LeaseToken, ""))

	require.NoError(t, s.DeleteNode(ctx, node.ID))
	require.NoError(t, s.DeleteNode(ctx, node.ID))
	require.NoError(t, s.DeleteNode(ctx, uuid.NewString()))
	require.Equal(t, "CLOUD_NODE_INVALID", infraerrors.Reason(s.DeleteNode(ctx, "invalid")))
	nodes, err := s.Nodes(ctx)
	require.NoError(t, err)
	require.Empty(t, nodes)
	_, err = s.Claim(ctx, node.ID, token, nil)
	require.Equal(t, "CLOUD_NODE_REQUIRED", infraerrors.Reason(err))
	_, err = s.ChangeNodeCredential(ctx, node.ID, false)
	require.Equal(t, "CLOUD_NODE_NOT_FOUND", infraerrors.Reason(err))
	_, _, err = s.SaveNode(ctx, node.ID, NodeInput{Name: "resurrect", Endpoint: node.Endpoint, Capacity: 1, Enabled: true})
	require.Equal(t, "CLOUD_NODE_NOT_FOUND", infraerrors.Reason(err))
	var historicalNode string
	require.NoError(t, s.db.QueryRow(`SELECT node_id::text FROM donebot_cloud_instances WHERE id=$1::uuid`, v.ID).Scan(&historicalNode))
	require.Equal(t, node.ID, historicalNode)
	var events int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM donebot_cloud_money_events WHERE instance_id=$1::uuid`, v.ID).Scan(&events))
	require.Equal(t, 2, events)
	replacement, newToken, err := s.SaveNode(ctx, "", NodeInput{Name: "new-enrollment", Endpoint: node.Endpoint, Capacity: 1, Enabled: true})
	require.NoError(t, err)
	require.NotEqual(t, node.ID, replacement.ID)
	_, err = s.Claim(ctx, replacement.ID, newToken, nil)
	require.NoError(t, err)
	_, err = s.Claim(ctx, replacement.ID, token, nil)
	require.Equal(t, "CLOUD_NODE_REQUIRED", infraerrors.Reason(err))
}

func TestPostgresNodeDeletionRacesAllocation(t *testing.T) {
	s := postgresFixture(t)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		owner := int64(1000 + i)
		_, err := s.db.Exec(`INSERT INTO users(id,balance) VALUES($1,20)`, owner)
		require.NoError(t, err)
		node, token, err := s.SaveNode(ctx, "", NodeInput{Name: "race-fixture", Endpoint: fmt.Sprintf("https://10.42.0.%d", i+1), Capacity: 1, Enabled: true})
		require.NoError(t, err)
		_, err = s.Claim(ctx, node.ID, token, nil)
		require.NoError(t, err)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var deletion, allocation error
		var instance *Instance
		wg.Add(2)
		go func() { defer wg.Done(); <-start; deletion = s.DeleteNode(ctx, node.ID) }()
		go func() { defer wg.Done(); <-start; instance, _, allocation = s.Mutate(ctx, owner, "", "start", "race") }()
		close(start)
		wg.Wait()
		require.NoError(t, allocation)
		var assignedToDeleted bool
		require.NoError(t, s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM donebot_cloud_instances i JOIN donebot_cloud_nodes n ON n.id=i.node_id WHERE i.id=$1::uuid AND i.deleted_at IS NULL AND n.deleted_at IS NOT NULL)`, instance.ID).Scan(&assignedToDeleted))
		require.False(t, assignedToDeleted)
		var charges int
		require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM donebot_cloud_money_events WHERE instance_id=$1::uuid AND event='charge'`, instance.ID).Scan(&charges))
		if deletion == nil {
			require.Zero(t, charges)
			require.Error(t, s.nodeAuth(ctx, node.ID, token))
		} else {
			require.Equal(t, "CLOUD_NODE_IN_USE", infraerrors.Reason(deletion))
			require.Equal(t, 1, charges)
		}
	}
}

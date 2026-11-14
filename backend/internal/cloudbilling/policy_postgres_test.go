//go:build integration

package cloudbilling

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPostgresRuntimeLeaseWindowExpiryAndRevocation(t *testing.T) {
	s := postgresFixture(t)
	ctx := context.Background()
	node, token, err := s.SaveNode(ctx, "", NodeInput{Name: "lease-fixture", Endpoint: "https://10.42.0.1", Capacity: 2, Enabled: true})
	require.NoError(t, err)
	_, err = s.Claim(ctx, node.ID, token, nil)
	require.NoError(t, err)
	i, _, err := s.Mutate(ctx, 101, "", "start", "lease-fixture-start")
	require.NoError(t, err)
	claim, err := s.Claim(ctx, node.ID, token, nil)
	require.NoError(t, err)
	require.Len(t, claim.Leases, 1)
	require.Equal(t, 30*time.Minute, claim.Leases[0].AuthorizedUntil.Sub(claim.ServerTime))
	require.NoError(t, s.Report(ctx, node.ID, token, claim.Command.ID, claim.Command.LeaseToken, ""))
	_, err = s.SaveRuntimePolicy(ctx, RuntimePolicy{OutageGraceMinutes: 15})
	require.NoError(t, err)
	claim, err = s.Claim(ctx, node.ID, token, nil)
	require.NoError(t, err)
	require.Equal(t, 15*time.Minute, claim.Leases[0].AuthorizedUntil.Sub(claim.ServerTime))
	var paidUntil time.Time
	err = s.db.QueryRow(`UPDATE donebot_cloud_subscriptions SET paid_until=clock_timestamp()+INTERVAL '2 minutes' WHERE instance_id=$1::uuid RETURNING paid_until`, i.ID).Scan(&paidUntil)
	require.NoError(t, err)
	claim, err = s.Claim(ctx, node.ID, token, nil)
	require.NoError(t, err)
	require.Equal(t, paidUntil, claim.Leases[0].AuthorizedUntil)
	_, _, err = s.Mutate(ctx, 101, i.ID, "stop", "lease-fixture-stop")
	require.NoError(t, err)
	claim, err = s.Claim(ctx, node.ID, token, nil)
	require.NoError(t, err)
	require.Empty(t, claim.Leases)
	_, err = s.ChangeNodeCredential(ctx, node.ID, true)
	require.NoError(t, err)
	_, err = s.Claim(ctx, node.ID, token, nil)
	require.Error(t, err)
}

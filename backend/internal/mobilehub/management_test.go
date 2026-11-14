//go:build integration

package mobilehub

import (
	"context"
	"fmt"
	"testing"

	infra "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestPostgresDevicePresenceAndRelayStatus(t *testing.T) {
	s := mobilePostgres(t)
	ctx := context.Background()
	config := s.Config
	s.Config = nil
	require.NoError(t, s.PhonePresence(ctx, fixtureIdentity, PhonePresence{fixturePhone, "Fixture iPhone", "ios"}))
	list, err := s.Devices(ctx, 101)
	require.NoError(t, err)
	require.False(t, list.RelayConfigured)
	require.Len(t, list.Mobile, 1)
	require.True(t, list.Mobile[0].Online)
	require.Equal(t, "Fixture iPhone", list.Mobile[0].Name)
	require.Len(t, list.Desktop, 1)
	require.Equal(t, "unregistered", list.Desktop[0].Connection)
	require.False(t, list.Desktop[0].Online)
	other, err := s.Devices(ctx, 202)
	require.NoError(t, err)
	require.Empty(t, other.Mobile)
	require.Len(t, other.Desktop, 1)
	s.Config = config
	in := registerFixture(t, s)
	in.RelayState = "connecting"
	_, err = s.Register(ctx, 101, fixtureHost, in)
	require.NoError(t, err)
	workspaces, err := s.Workspaces(ctx, 101)
	require.NoError(t, err)
	require.Len(t, workspaces, 1)
	require.False(t, workspaces[0].Online)
	in.RelayState = "connected"
	_, err = s.Register(ctx, 101, fixtureHost, in)
	require.NoError(t, err)
	workspaces, err = s.Workspaces(ctx, 101)
	require.NoError(t, err)
	require.True(t, workspaces[0].Online)
	_, err = s.DB.Exec(`UPDATE donebot_mobile_devices SET last_seen=now()-interval '1 minute'`)
	require.NoError(t, err)
	list, err = s.Devices(ctx, 101)
	require.NoError(t, err)
	require.Len(t, list.Mobile, 1)
	require.False(t, list.Mobile[0].Online)
	s.Check = func(context.Context, Identity) error { return infra.Unauthorized("REVOKED", "Synthetic revocation") }
	list, err = s.Devices(ctx, 101)
	require.NoError(t, err)
	require.Empty(t, list.Mobile)
	s.Check = func(context.Context, Identity) error { return fmt.Errorf("synthetic identity outage") }
	_, err = s.Devices(ctx, 101)
	require.ErrorContains(t, err, "synthetic identity outage")
}

func TestPostgresRelayManagement(t *testing.T) {
	s := mobilePostgres(t)
	ctx := context.Background()
	input := NodeInput{ID: "relay1", Name: "Main relay", Endpoint: "donebot.cn/mobile-relay", Enabled: true, MaxSessions: 2}
	token, err := s.SaveNode(ctx, input)
	require.NoError(t, err)
	require.Regexp(t, `^dbr_[A-Za-z0-9_-]{43}$`, token)
	_, err = s.relayNodes(ctx)
	require.Equal(t, 503, infra.Code(err))
	_, err = s.NodeHeartbeat(ctx, input.ID, "wrong", NodeHeartbeat{})
	require.Equal(t, 401, infra.Code(err))
	policy, err := s.NodeHeartbeat(ctx, input.ID, token, NodeHeartbeat{1, 2, 5})
	require.NoError(t, err)
	require.Equal(t, 2, policy.MaxSessions)
	require.GreaterOrEqual(t, policy.MaxHosts, 1)
	require.GreaterOrEqual(t, policy.MaxSockets, policy.MaxHosts+2*policy.MaxSessions)
	nodes, err := s.Nodes(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.Equal(t, 1, nodes[0].Hosts)
	require.Equal(t, 2, nodes[0].Sessions)
	require.Equal(t, 5, nodes[0].Sockets)
	input.MaxSessions = 1
	updatedToken, err := s.SaveNode(ctx, input)
	require.NoError(t, err)
	require.Empty(t, updatedToken)
	policy, err = s.NodeHeartbeat(ctx, input.ID, token, NodeHeartbeat{1, 2, 5})
	require.NoError(t, err)
	require.Equal(t, 1, policy.MaxSessions)
	for i := 2; i <= 4; i++ {
		id := fmt.Sprintf("relay%d", i)
		endpoint := id + ".donebot.cn"
		credential, e := s.SaveNode(ctx, NodeInput{ID: id, Name: id, Endpoint: endpoint, Enabled: true, MaxSessions: 10})
		require.NoError(t, e)
		_, e = s.NodeHeartbeat(ctx, id, credential, NodeHeartbeat{})
		require.NoError(t, e)
	}
	_, err = s.SaveNode(ctx, NodeInput{ID: "relay5", Name: "Fifth", Endpoint: "relay5.donebot.cn", Enabled: true, MaxSessions: 10})
	require.Equal(t, 400, infra.Code(err))
	endpoints, err := s.relayNodes(ctx)
	require.NoError(t, err)
	require.Equal(t, "relay2.donebot.cn", endpoints[0])
	endpoints, err = s.relayNodes(ctx, input.Endpoint)
	require.NoError(t, err)
	require.Equal(t, input.Endpoint, endpoints[0])
	input.Enabled = false
	_, err = s.SaveNode(ctx, input)
	require.NoError(t, err)
	policy, err = s.NodeHeartbeat(ctx, input.ID, token, NodeHeartbeat{1, 2, 5})
	require.NoError(t, err)
	require.False(t, policy.Enabled)
	endpoints, err = s.relayNodes(ctx, input.Endpoint)
	require.NoError(t, err)
	require.NotContains(t, endpoints, input.Endpoint)
	replacement, err := s.RotateNodeToken(ctx, input.ID)
	require.NoError(t, err)
	require.NotEqual(t, token, replacement)
	_, err = s.NodeHeartbeat(ctx, input.ID, token, NodeHeartbeat{})
	require.Equal(t, 401, infra.Code(err))
	_, err = s.NodeHeartbeat(ctx, input.ID, replacement, NodeHeartbeat{})
	require.NoError(t, err)
	_, err = s.DB.Exec(`UPDATE donebot_relay_nodes SET last_seen=now()-interval '1 minute'`)
	require.NoError(t, err)
	_, err = s.relayNodes(ctx)
	require.Equal(t, 503, infra.Code(err))
}

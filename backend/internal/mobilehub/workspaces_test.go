//go:build integration

package mobilehub

import (
	"context"
	"testing"
	"time"

	infra "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestPostgresRemoveOfflineComputerAndRestore(t *testing.T) {
	s := mobilePostgres(t)
	ctx := context.Background()
	in := registerFixture(t, s)
	require.Equal(t, 409, infra.Code(s.RemoveComputer(ctx, fixtureIdentity, fixtureHost)))
	require.NoError(t, s.PhonePresence(ctx, fixtureIdentity, PhonePresence{Installation: fixturePhone, Name: "Fixture phone", Platform: "ios"}))
	_, err := s.DB.Exec(`INSERT INTO donebot_mobile_grants(host_id,device_id,token_hash,acked_hash) VALUES($1,$2,'old-grant','old-grant')`, fixtureHost, fixturePhone)
	require.NoError(t, err)
	_, err = s.DB.Exec(`INSERT INTO donebot_mobile_events(host_id,message_id) VALUES($1,'old-event')`, fixtureHost)
	require.NoError(t, err)
	_, err = s.DB.Exec(`INSERT INTO donebot_mobile_outbox(host_id,device_id,session_id,payload) VALUES($1,$2,'fixture-session','{}')`, fixtureHost, fixturePhone)
	require.NoError(t, err)
	_, err = s.DB.Exec(`UPDATE donebot_mobile_hosts SET last_seen=now()-interval '1 minute' WHERE installation_id=$1`, fixtureHost)
	require.NoError(t, err)
	require.NoError(t, s.RemoveComputer(ctx, fixtureIdentity, fixtureHost))
	require.NoError(t, s.RemoveComputer(ctx, fixtureIdentity, fixtureHost))
	workspaces, err := s.Workspaces(ctx, fixtureIdentity.Owner)
	require.NoError(t, err)
	require.Empty(t, workspaces)
	for _, table := range []string{"donebot_mobile_hosts", "donebot_mobile_grants", "donebot_mobile_events", "donebot_mobile_outbox"} {
		var count int
		require.NoError(t, s.DB.QueryRow(`SELECT count(*) FROM `+table).Scan(&count))
		require.Zero(t, count, table)
	}
	var status, keyStatus string
	require.NoError(t, s.DB.QueryRow(`SELECT i.status,k.status FROM donebot_installs i JOIN api_keys k ON k.id=i.api_key_id WHERE i.installation_id=$1`, fixtureHost).Scan(&status, &keyStatus))
	require.Equal(t, "active", status)
	require.Equal(t, "active", keyStatus)
	var phones int
	require.NoError(t, s.DB.QueryRow(`SELECT count(*) FROM donebot_mobile_devices`).Scan(&phones))
	require.Equal(t, 1, phones)
	_, err = s.Connection(ctx, fixtureIdentity, fixtureHost, fixturePhone)
	require.Error(t, err)
	restored, err := s.Register(ctx, 101, fixtureHost, in)
	require.NoError(t, err)
	require.Equal(t, in.Host, restored.Host)
	require.Empty(t, restored.Devices)
	_, err = s.Register(ctx, 101, fixtureHost, in)
	require.NoError(t, err)
	workspaces, err = s.Workspaces(ctx, 101)
	require.NoError(t, err)
	require.Equal(t, []Workspace{{ID: fixtureHost, Name: in.Name, Kind: "computer", Online: true, Provisioned: true}}, workspaces)
	var key string
	require.NoError(t, s.DB.QueryRow(`SELECT public_key FROM donebot_mobile_hosts WHERE installation_id=$1`, fixtureHost).Scan(&key))
	require.Equal(t, in.Key, key)
}

func TestPostgresRemoveComputerScopeAndRevocation(t *testing.T) {
	for _, kind := range []string{"other-owner", "cloud-fingerprint", "cloud-instance", "revoked", "disabled", "revoked-session", "invalid-id"} {
		t.Run(kind, func(t *testing.T) {
			s := mobilePostgres(t)
			ctx := context.Background()
			in := registerFixture(t, s)
			_, err := s.DB.Exec(`UPDATE donebot_mobile_hosts SET relay_state='off' WHERE installation_id=$1`, fixtureHost)
			require.NoError(t, err)
			who, install := fixtureIdentity, fixtureHost
			switch kind {
			case "other-owner":
				who.Owner = 202
				s.Check = func(context.Context, Identity) error { return nil }
			case "cloud-fingerprint":
				_, err = s.DB.Exec(`UPDATE donebot_installs SET fingerprint='donebot-cloud' WHERE installation_id=$1`, fixtureHost)
			case "cloud-instance":
				_, err = s.DB.Exec(`INSERT INTO donebot_cloud_instances(id,user_id) VALUES($1,101)`, fixtureHost)
			case "revoked":
				_, err = s.DB.Exec(`UPDATE donebot_installs SET status='revoked' WHERE installation_id=$1`, fixtureHost)
			case "disabled":
				_, err = s.DB.Exec(`UPDATE api_keys SET status='disabled' WHERE id=1`)
			case "revoked-session":
				who.Session = "revoked-session"
			case "invalid-id":
				install = "invalid"
			}
			require.NoError(t, err)
			require.Error(t, s.RemoveComputer(ctx, who, install))
			var hosts int
			require.NoError(t, s.DB.QueryRow(`SELECT count(*) FROM donebot_mobile_hosts`).Scan(&hosts))
			require.Equal(t, 1, hosts)
			if kind == "revoked" || kind == "disabled" {
				_, err = s.Register(ctx, 101, fixtureHost, in)
				require.Error(t, err)
			}
		})
	}
}

func TestPostgresRemoveComputerRechecksConcurrentHeartbeat(t *testing.T) {
	s := mobilePostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	registerFixture(t, s)
	_, err := s.DB.Exec(`UPDATE donebot_mobile_hosts SET last_seen=now()-interval '1 minute' WHERE installation_id=$1`, fixtureHost)
	require.NoError(t, err)
	tx, err := s.DB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `SELECT 1 FROM donebot_mobile_hosts WHERE installation_id=$1 FOR UPDATE`, fixtureHost)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() { result <- s.RemoveComputer(ctx, fixtureIdentity, fixtureHost) }()
	require.Eventually(t, func() bool {
		var waiting bool
		err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query LIKE 'DELETE FROM donebot_mobile_hosts%' AND wait_event_type='Lock')`).Scan(&waiting)
		return err == nil && waiting
	}, 3*time.Second, 10*time.Millisecond)
	_, err = tx.ExecContext(ctx, `UPDATE donebot_mobile_hosts SET last_seen=now(),relay_state='connected' WHERE installation_id=$1`, fixtureHost)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	require.Equal(t, 409, infra.Code(<-result))
	workspaces, err := s.Workspaces(ctx, 101)
	require.NoError(t, err)
	require.Len(t, workspaces, 1)
	require.True(t, workspaces[0].Online)
}

package donebotteam

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type executionFixture struct {
	s                          *Service
	team, other, cloud, device string
	installation               map[int64]string
	ctx                        context.Context
}

func newExecutionFixture(t *testing.T) *executionFixture {
	t.Helper()
	dsn := os.Getenv("DONEBOT_TEAM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires an owned disposable PostgreSQL fixture")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := "team_execution_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		require.NoError(t, e)
		require.NoError(t, admin.Close())
	})
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	db.SetMaxOpenConns(12)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TABLE users(id BIGINT PRIMARY KEY,email TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ);
        CREATE TABLE donebot_installs(installation_id UUID UNIQUE NOT NULL,user_id BIGINT NOT NULL,status TEXT NOT NULL DEFAULT 'active',expires_at TIMESTAMPTZ);
        INSERT INTO users(id,email) VALUES(1,'owner@example.invalid'),(2,'admin@example.invalid'),(3,'a@example.invalid'),(4,'b@example.invalid'),(5,'outsider@example.invalid'),(6,'other@example.invalid');
        CREATE TABLE donebot_cloud_nodes(id UUID PRIMARY KEY,team_protocol TEXT,last_seen TIMESTAMPTZ,enabled BOOLEAN NOT NULL DEFAULT TRUE);
        CREATE TABLE donebot_cloud_instances(id UUID PRIMARY KEY,team_id UUID,user_id BIGINT,node_id UUID,deleted_at TIMESTAMPTZ,desired TEXT,actual TEXT,generation BIGINT NOT NULL DEFAULT 1);
        CREATE TABLE donebot_cloud_subscriptions(instance_id UUID PRIMARY KEY,team_id UUID,user_id BIGINT,state TEXT,paid_until TIMESTAMPTZ,purge_locked BOOLEAN);`)
	require.NoError(t, err)
	for _, file := range []string{"271_donebot_teams.sql", "273_donebot_team_execution_grants.sql", "274_donebot_team_coordinators.sql"} {
		migration, e := os.ReadFile("../../migrations/" + file)
		require.NoError(t, e)
		_, e = db.Exec(string(migration))
		require.NoError(t, e)
	}
	f := &executionFixture{s: New(db, true), installation: map[int64]string{}, ctx: context.Background()}
	for _, actor := range []int64{1, 2, 3, 4, 6} {
		f.installation[actor] = uuid.NewString()
		_, err = db.Exec(`INSERT INTO donebot_installs(installation_id,user_id) VALUES($1::uuid,$2)`, f.installation[actor], actor)
		require.NoError(t, err)
	}
	team, err := f.s.Create(f.ctx, 1, "Team", uuid.NewString())
	require.NoError(t, err)
	f.team = team.ID
	other, err := f.s.Create(f.ctx, 6, "Other", uuid.NewString())
	require.NoError(t, err)
	f.other = other.ID
	for actor, role := range map[int64]Role{2: Admin, 3: Member, 4: Member} {
		email := map[int64]string{2: "admin@example.invalid", 3: "a@example.invalid", 4: "b@example.invalid"}[actor]
		invite, e := f.s.Invite(f.ctx, f.team, 1, email, role)
		require.NoError(t, e)
		_, e = f.s.Accept(f.ctx, actor, invite.Token)
		require.NoError(t, e)
	}
	f.cloud = uuid.NewString()
	node := uuid.NewString()
	_, err = db.Exec(`INSERT INTO donebot_cloud_nodes(id,team_protocol,last_seen) VALUES($1::uuid,$2,clock_timestamp())`, node, Protocol)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO donebot_cloud_instances VALUES($1::uuid,$2::uuid,1,$3::uuid,NULL,'running','running',1)`, f.cloud, f.team, node)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO donebot_cloud_subscriptions VALUES($1::uuid,$2::uuid,1,'active',clock_timestamp()+INTERVAL '1 day',FALSE)`, f.cloud, f.team)
	require.NoError(t, err)
	d, err := f.s.SetDevice(f.ctx, f.team, 4, f.installation[4], "B's Mac", true)
	require.NoError(t, err)
	f.device = d.ID
	require.NoError(t, f.s.DeviceHeartbeat(f.ctx, f.team, 4, f.installation[4], d.ID, d.Version))
	return f
}

func (f *executionFixture) request() ExecutionRequest {
	return ExecutionRequest{TeamID: f.team, Actor: 3, Key: uuid.NewString(), NodeKind: "local", NodeID: f.device, Purpose: "video", BotID: "bot-a", ThreadID: "thread-a", RequestHash: strings.Repeat("a", 64)}
}

func TestExecutionValidationAndDisabled(t *testing.T) {
	s := New(nil, false)
	_, err := s.PrepareExecution(context.Background(), ExecutionRequest{})
	require.Error(t, err)
	_, err = s.AuthorizeExecution(context.Background(), "bad", ExecutionRequest{})
	require.Error(t, err)
	_, _, err = s.AdmitExecution(context.Background(), "bad", ExecutionRequest{})
	require.Error(t, err)
	_, err = s.SetDevice(context.Background(), "bad", 1, "bad", "name", true)
	require.Error(t, err)
	require.Error(t, s.RevokeDevice(context.Background(), "bad", 1, "bad"))
	require.Error(t, s.DeviceHeartbeat(context.Background(), "bad", 1, "bad", "bad", 1))
	for _, token := range []string{"", "dbte_", "dbte_" + strings.Repeat("=", 43), strings.Repeat("a", 5000)} {
		require.False(t, validExecutionToken(token))
	}
}

func TestExecutionDeviceConsentPostgres(t *testing.T) {
	f := newExecutionFixture(t)
	for _, actor := range []int64{1, 2, 3, 6} {
		_, err := f.s.SetDevice(f.ctx, f.team, actor, f.installation[4], "Forged", true)
		require.Error(t, err)
	}
	require.Error(t, f.s.RevokeDevice(f.ctx, f.team, 3, f.device))
	d, err := f.s.SetDevice(f.ctx, f.team, 3, f.installation[3], "A's Mac", false)
	require.NoError(t, err)
	require.False(t, d.Enabled)
	require.False(t, d.Online)
	require.Error(t, f.s.DeviceHeartbeat(f.ctx, f.team, 3, f.installation[3], d.ID, d.Version))
	d, err = f.s.SetDevice(f.ctx, f.team, 3, f.installation[3], "A's Mac", true)
	require.NoError(t, err)
	require.False(t, d.Online)
	require.Error(t, f.s.DeviceHeartbeat(f.ctx, f.team, 4, f.installation[3], d.ID, d.Version))
	require.NoError(t, f.s.DeviceHeartbeat(f.ctx, f.team, 3, f.installation[3], d.ID, d.Version))
	devices, err := f.s.Devices(f.ctx, f.team, 3)
	require.NoError(t, err)
	require.Len(t, devices, 2)
	for _, device := range devices {
		require.True(t, device.Online)
	}
	renamed, err := f.s.SetDevice(f.ctx, f.team, 3, f.installation[3], "Renamed Mac", true)
	require.NoError(t, err)
	require.Equal(t, d.Version, renamed.Version)
	require.True(t, renamed.Online)
	require.NotNil(t, renamed.LastSeen)
	require.Error(t, f.s.DeviceHeartbeat(f.ctx, f.team, 3, f.installation[3], d.ID, d.Version-1))
	_, err = f.s.Devices(f.ctx, f.team, 5)
	require.Error(t, err)
	_, err = f.s.Devices(f.ctx, f.other, 3)
	require.Error(t, err)
	owner, err := f.s.SetDevice(f.ctx, f.team, 1, f.installation[1], "Owner's Mac", true)
	require.NoError(t, err)
	require.Error(t, f.s.RevokeDevice(f.ctx, f.team, 2, owner.ID))
	require.NoError(t, f.s.RevokeDevice(f.ctx, f.team, 2, f.device))
	require.Error(t, f.s.DeviceHeartbeat(f.ctx, f.team, 4, f.installation[4], f.device, 1))
	_, err = f.s.PrepareExecution(f.ctx, f.request())
	require.Error(t, err)
}

func TestExecutionBindingPayerAndAudiencePostgres(t *testing.T) {
	f := newExecutionFixture(t)
	in := f.request()
	grant, err := f.s.PrepareExecution(f.ctx, in)
	require.NoError(t, err)
	require.True(t, validExecutionToken(grant.Token))
	require.Equal(t, int64(1), grant.Execution.PayerID)
	require.Equal(t, int64(3), grant.Execution.ActorID)
	require.Equal(t, int64(4), grant.Execution.ExecutorID)
	replay, err := f.s.PrepareExecution(f.ctx, in)
	require.NoError(t, err)
	require.Equal(t, grant.Execution.ID, replay.Execution.ID)
	require.NotEqual(t, grant.Token, replay.Token)
	for name, change := range map[string]func(*ExecutionRequest){
		"actor": func(r *ExecutionRequest) { r.Actor = 2 }, "team": func(r *ExecutionRequest) { r.TeamID = f.other },
		"node": func(r *ExecutionRequest) { r.NodeID = uuid.NewString() }, "kind": func(r *ExecutionRequest) { r.NodeKind = "cloud"; r.NodeID = f.cloud },
		"hash": func(r *ExecutionRequest) { r.RequestHash = strings.Repeat("b", 64) }, "purpose": func(r *ExecutionRequest) { r.Purpose = "image" },
		"bot": func(r *ExecutionRequest) { r.BotID = "bot-b" }, "thread": func(r *ExecutionRequest) { r.ThreadID = "thread-b" },
		"audience": func(r *ExecutionRequest) { r.Private = true }, "key": func(r *ExecutionRequest) { r.Key = uuid.NewString() },
	} {
		t.Run(name, func(t *testing.T) {
			next := in
			change(&next)
			_, e := f.s.AuthorizeExecution(f.ctx, grant.Token, next)
			require.Error(t, e)
			if name != "key" {
				_, e = f.s.PrepareExecution(f.ctx, next)
				require.Error(t, e)
			}
		})
	}
	private := f.request()
	private.Private = true
	_, err = f.s.PrepareExecution(f.ctx, private)
	require.Error(t, err)
	private.NodeKind, private.NodeID = "cloud", f.cloud
	cloud, err := f.s.PrepareExecution(f.ctx, private)
	require.NoError(t, err)
	require.Equal(t, int64(3), cloud.Execution.PrivateTo.Int64)
	require.Equal(t, int64(3), cloud.Execution.ExecutorID)
	private.NodeID = uuid.NewString()
	_, err = f.s.PrepareExecution(f.ctx, private)
	require.Error(t, err)
	for _, assignment := range []string{"payer_id=3", "actor_id=4", "team_id='" + f.other + "'::uuid", "node_id='" + uuid.NewString() + "'::uuid", "request_hash='" + strings.Repeat("b", 64) + "'", "private_to=3"} {
		_, err = f.s.db.Exec(`UPDATE donebot_team_executions SET `+assignment+` WHERE id=$1::uuid`, grant.Execution.ID)
		require.Error(t, err)
	}
	_, err = f.s.db.Exec(`UPDATE donebot_team_devices SET user_id=3 WHERE id=$1::uuid`, f.device)
	require.Error(t, err)
	for _, assignment := range []string{"state='accepted'", "state='completed'", "accepted_at=clock_timestamp()"} {
		_, err = f.s.db.Exec(`UPDATE donebot_team_executions SET `+assignment+` WHERE id=$1::uuid`, grant.Execution.ID)
		require.Error(t, err)
	}
	_, err = f.s.db.Exec(`UPDATE donebot_team_executions SET state='cancelled' WHERE id=$1::uuid`, grant.Execution.ID)
	require.NoError(t, err)
	_, err = f.s.PrepareExecution(f.ctx, in)
	require.Error(t, err)
	_, _, err = f.s.AdmitExecution(f.ctx, grant.Token, in)
	require.Error(t, err)
}

func TestExecutionConcurrentAdmissionAndUncertainPostgres(t *testing.T) {
	f := newExecutionFixture(t)
	in := f.request()
	var wg sync.WaitGroup
	var accepted atomic.Int64
	var mu sync.Mutex
	var ids []string
	var errs []error
	for range 8 {
		wg.Go(func() {
			grant, err := f.s.PrepareExecution(f.ctx, in)
			if err == nil {
				var first bool
				_, first, err = f.s.AdmitExecution(f.ctx, grant.Token, in)
				if first {
					accepted.Add(1)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			errs = append(errs, err)
			if grant != nil {
				ids = append(ids, grant.Execution.ID)
			}
		})
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int64(1), accepted.Load())
	require.Len(t, ids, 8)
	for _, id := range ids {
		require.Equal(t, ids[0], id)
	}
	_, err := f.s.db.Exec(`UPDATE donebot_team_executions SET state='uncertain' WHERE id=$1::uuid`, ids[0])
	require.NoError(t, err)
	grant, err := f.s.PrepareExecution(f.ctx, in)
	require.NoError(t, err)
	e, first, err := f.s.AdmitExecution(f.ctx, grant.Token, in)
	require.NoError(t, err)
	require.False(t, first)
	require.Equal(t, "uncertain", e.State)
	_, err = f.s.db.Exec(`UPDATE donebot_team_executions SET state='prepared' WHERE id=$1::uuid`, ids[0])
	require.Error(t, err)
	_, err = f.s.db.Exec(`UPDATE donebot_team_executions SET state='completed' WHERE id=$1::uuid`, ids[0])
	require.NoError(t, err)
	_, first, err = f.s.AdmitExecution(f.ctx, grant.Token, in)
	require.NoError(t, err)
	require.False(t, first)
}

func TestExecutionRevocationDoesNotResurrectPostgres(t *testing.T) {
	for _, change := range []string{"actor-role", "actor-leave", "executor-role", "device-off", "install-revoke", "expire", "cloud-expire", "cloud-stopped", "node-stale", "cloud-node-disabled", "payer-disabled"} {
		t.Run(change, func(t *testing.T) {
			f := newExecutionFixture(t)
			in := f.request()
			grant, err := f.s.PrepareExecution(f.ctx, in)
			require.NoError(t, err)
			switch change {
			case "actor-role":
				require.NoError(t, f.s.ChangeMember(f.ctx, f.team, 1, 3, Admin, false))
			case "actor-leave":
				require.NoError(t, f.s.ChangeMember(f.ctx, f.team, 1, 3, Member, true))
			case "executor-role":
				require.NoError(t, f.s.ChangeMember(f.ctx, f.team, 1, 4, Admin, false))
			case "device-off":
				_, err = f.s.SetDevice(f.ctx, f.team, 4, f.installation[4], "B's Mac", false)
				require.NoError(t, err)
				d, e := f.s.SetDevice(f.ctx, f.team, 4, f.installation[4], "B's Mac", true)
				require.NoError(t, e)
				require.NoError(t, f.s.DeviceHeartbeat(f.ctx, f.team, 4, f.installation[4], d.ID, d.Version))
			case "install-revoke":
				_, err = f.s.db.Exec(`UPDATE donebot_installs SET status='revoked' WHERE installation_id=$1::uuid`, f.installation[4])
			case "expire":
				_, err = f.s.db.Exec(`UPDATE donebot_team_execution_grants SET expires_at=clock_timestamp()-INTERVAL '1 second'`)
			case "cloud-expire":
				_, err = f.s.db.Exec(`UPDATE donebot_cloud_subscriptions SET paid_until=clock_timestamp()-INTERVAL '1 second'`)
			case "cloud-stopped":
				_, err = f.s.db.Exec(`UPDATE donebot_cloud_instances SET actual='stopped'`)
			case "node-stale":
				_, err = f.s.db.Exec(`UPDATE donebot_team_devices SET last_seen=clock_timestamp()-INTERVAL '1 minute'`)
			case "payer-disabled":
				_, err = f.s.db.Exec(`UPDATE users SET status='disabled' WHERE id=1`)
			case "cloud-node-disabled":
				_, err = f.s.db.Exec(`UPDATE donebot_cloud_nodes SET enabled=FALSE`)
			}
			require.NoError(t, err)
			_, err = f.s.AuthorizeExecution(f.ctx, grant.Token, in)
			require.Error(t, err)
			_, _, err = f.s.AdmitExecution(f.ctx, grant.Token, in)
			require.Error(t, err)
			if change == "device-off" {
				_, err = f.s.PrepareExecution(f.ctx, in)
				require.Error(t, err)
				in.Key = uuid.NewString()
				_, err = f.s.PrepareExecution(f.ctx, in)
				require.NoError(t, err)
			}
			var payer int64
			require.NoError(t, f.s.db.QueryRow(`SELECT payer_id FROM donebot_team_executions WHERE id=$1::uuid`, grant.Execution.ID).Scan(&payer))
			require.Equal(t, int64(1), payer)
		})
	}
}

func TestExecutionCoordinatorGenerationPostgres(t *testing.T) {
	f := newExecutionFixture(t)
	in := f.request()
	in.Coordinator = &Coordinator{TeamID: f.team, InstanceID: f.cloud, Generation: 1}
	grant, err := f.s.PrepareExecution(f.ctx, in)
	require.NoError(t, err)
	for _, coordinator := range []*Coordinator{nil, {TeamID: f.other, InstanceID: f.cloud, Generation: 1}, {TeamID: f.team, InstanceID: uuid.NewString(), Generation: 1}, {TeamID: f.team, InstanceID: f.cloud, Generation: 2}} {
		next := in
		next.Coordinator = coordinator
		_, err = f.s.AuthorizeExecution(f.ctx, grant.Token, next)
		require.Error(t, err)
		_, err = f.s.PrepareExecution(f.ctx, next)
		require.Error(t, err)
	}
	for _, assignment := range []string{"coordinator_id=NULL,coordinator_generation=NULL", "coordinator_generation=2"} {
		_, err = f.s.db.Exec(`UPDATE donebot_team_executions SET `+assignment+` WHERE id=$1::uuid`, grant.Execution.ID)
		require.Error(t, err)
	}
	_, err = f.s.db.Exec(`UPDATE donebot_cloud_instances SET generation=2 WHERE id=$1::uuid`, f.cloud)
	require.NoError(t, err)
	_, _, err = f.s.AdmitExecution(f.ctx, grant.Token, in)
	require.Error(t, err)
	in.Coordinator = &Coordinator{TeamID: f.team, InstanceID: f.cloud, Generation: 2}
	_, err = f.s.PrepareExecution(f.ctx, in)
	require.Error(t, err, "an old request key cannot resurrect on a new runtime generation")
	in.Key = uuid.NewString()
	_, err = f.s.PrepareExecution(f.ctx, in)
	require.NoError(t, err)
}

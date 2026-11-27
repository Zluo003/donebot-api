package donebotteam

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestDisabledBeforeDatabaseAccess(t *testing.T) {
	ctx := context.Background()
	s := New(nil, false)
	_, err := s.List(ctx, 1)
	require.Error(t, err)
	_, err = s.Create(ctx, 1, "Team", uuid.NewString())
	require.Error(t, err)
	_, err = s.Accept(ctx, 1, "invalid")
	require.Error(t, err)
	require.Error(t, s.ChangeMember(ctx, uuid.NewString(), 1, 2, Admin, false))
}
func TestRoleAssignment(t *testing.T) {
	for _, actor := range []Role{Owner, Admin, Member, "unknown"} {
		for _, target := range []Role{Owner, Admin, Member, "unknown"} {
			require.Equal(t, actor == Owner && (target == Admin || target == Member) || actor == Admin && target == Member, canAssign(actor, target), "%s -> %s", actor, target)
		}
	}
}
func TestNames(t *testing.T) {
	for _, name := range []string{"", "  ", "bad\nname", strings.Repeat("名", 101), string([]byte{0xff})} {
		_, err := validateName(name)
		require.Error(t, err)
	}
	name, err := validateName("  制作团队  ")
	require.NoError(t, err)
	require.Equal(t, "制作团队", name)
}

func TestTeamIdentityPostgres(t *testing.T) {
	dsn := os.Getenv("DONEBOT_TEAM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires an owned disposable PostgreSQL fixture")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	schema := "donebot_team_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = db.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	defer func() { _, _ = db.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`) }()
	_, err = db.ExecContext(ctx, `SET search_path TO `+schema)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `CREATE TABLE users(id BIGINT PRIMARY KEY,email TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ); CREATE TABLE donebot_installs(id BIGSERIAL PRIMARY KEY,installation_id UUID UNIQUE NOT NULL); INSERT INTO users(id,email) VALUES(1,'owner@example.invalid'),(2,'admin@example.invalid'),(3,'a@example.invalid'),(4,'b@example.invalid'),(5,'outsider@example.invalid'),(6,'other@example.invalid')`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/271_donebot_teams.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	s := New(db, true)
	key := uuid.NewString()
	team, err := s.Create(ctx, 1, "Studio", key)
	require.NoError(t, err)
	replay, err := s.Create(ctx, 1, "Studio", key)
	require.NoError(t, err)
	require.Equal(t, team.ID, replay.ID)
	_, err = s.Create(ctx, 1, "Changed", key)
	require.Error(t, err)
	other, err := s.Create(ctx, 6, "Other", uuid.NewString())
	require.NoError(t, err)
	join := func(actor int64, email string, role Role) *Invitation {
		inv, e := s.Invite(ctx, team.ID, 1, email, role)
		require.NoError(t, e)
		_, e = s.Accept(ctx, actor, inv.Token)
		require.NoError(t, e)
		return inv
	}
	adminInvite := join(2, "admin@example.invalid", Admin)
	memberInvite := join(3, "a@example.invalid", Member)
	join(4, "b@example.invalid", Member)
	list, err := s.List(ctx, 3)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, Member, list[0].Role)
	_, err = s.Members(ctx, other.ID, 3)
	require.Error(t, err)
	_, err = s.Access(ctx, team.ID, 5)
	require.Error(t, err)
	_, err = s.Invite(ctx, team.ID, 3, "outsider@example.invalid", Member)
	require.Error(t, err)
	_, err = s.Invite(ctx, team.ID, 2, "outsider@example.invalid", Admin)
	require.Error(t, err)
	require.Error(t, s.ChangeMember(ctx, team.ID, 2, 1, Member, true))
	require.Error(t, s.ChangeMember(ctx, team.ID, 1, 1, Member, false))
	require.Error(t, s.ChangeMember(ctx, team.ID, 3, 4, Admin, false))
	invite, err := s.Invite(ctx, team.ID, 2, "outsider@example.invalid", Member)
	require.NoError(t, err)
	_, err = s.Accept(ctx, 4, invite.Token)
	require.Error(t, err)
	_, err = s.Accept(ctx, 5, invite.Token)
	require.NoError(t, err)
	_, err = s.Accept(ctx, 5, invite.Token)
	require.NoError(t, err)
	_, err = s.Accept(ctx, 2, adminInvite.Token)
	require.NoError(t, err)
	require.NoError(t, s.ChangeMember(ctx, team.ID, 1, 3, Admin, false))
	_, err = s.Accept(ctx, 3, memberInvite.Token)
	require.NoError(t, err)
	upgraded, err := s.Access(ctx, team.ID, 3)
	require.NoError(t, err)
	require.Equal(t, Admin, upgraded.Role)
	require.NoError(t, s.ChangeMember(ctx, team.ID, 1, 3, "", true))
	_, err = s.Accept(ctx, 3, memberInvite.Token)
	require.Error(t, err)
	_, err = s.Access(ctx, team.ID, 3)
	require.Error(t, err)
	expired, err := s.Invite(ctx, team.ID, 1, "a@example.invalid", Member)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE donebot_team_invitations SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, expired.ID)
	require.NoError(t, err)
	_, err = s.Accept(ctx, 3, expired.Token)
	require.Error(t, err)
	revoked, err := s.Invite(ctx, team.ID, 1, "a@example.invalid", Member)
	require.NoError(t, err)
	require.NoError(t, s.RevokeInvitation(ctx, team.ID, 2, revoked.ID))
	_, err = s.Accept(ctx, 3, revoked.Token)
	require.Error(t, err)
	stale, err := s.Invite(ctx, team.ID, 2, "a@example.invalid", Member)
	require.NoError(t, err)
	require.NoError(t, s.ChangeMember(ctx, team.ID, 1, 2, "", true))
	_, err = s.Accept(ctx, 3, stale.Token)
	require.Error(t, err)
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); failures <- s.ChangeMember(ctx, team.ID, 1, 1, Member, true) }()
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		require.Error(t, failure)
	}
	members, err := s.Members(ctx, team.ID, 1)
	require.NoError(t, err)
	owners := 0
	for _, member := range members {
		if member.Role == Owner {
			owners++
		}
	}
	require.Equal(t, 1, owners)
	_, err = db.ExecContext(ctx, `UPDATE users SET status='disabled' WHERE id=4`)
	require.NoError(t, err)
	_, err = s.Access(ctx, team.ID, 4)
	require.Error(t, err)
	_, err = db.ExecContext(ctx, `UPDATE donebot_teams SET state='archived' WHERE id=$1`, team.ID)
	require.NoError(t, err)
	_, err = s.Access(ctx, team.ID, 1)
	require.Error(t, err)
}

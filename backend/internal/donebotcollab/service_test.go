package donebotcollab

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestBotMemoryGuard(t *testing.T) {
	clean := map[string]any{
		"format": "openmaus.package",
		"version": 2,
		"package": map[string]any{
			"name": "Reviewer",
			"agents": []any{
				map[string]any{"name": "Reviewer", "soul": "Be thorough", "skills": []any{}},
			},
		},
	}
	require.False(t, containsForbiddenMemoryField(clean))

	withMemory := map[string]any{
		"package": map[string]any{
			"agents": []any{
				map[string]any{"name": "Reviewer", "starterNotes": []any{map[string]any{"path": "a.md", "text": "secret"}}},
			},
		},
	}
	require.True(t, containsForbiddenMemoryField(withMemory))
}

func TestDisabledServiceRefusesAccess(t *testing.T) {
	svc := New(nil, false)
	_, err := svc.ListFriends(context.Background(), 1)
	require.Error(t, err)
	_, err = svc.ListFolders(context.Background(), 1)
	require.Error(t, err)
	_, err = svc.ListBots(context.Background(), 1)
	require.Error(t, err)
}

func TestCollabPostgres(t *testing.T) {
	dsn := os.Getenv("DONEBOT_TEAM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires an owned disposable PostgreSQL fixture")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	schema := "donebot_collab_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = db.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	defer func() { _, _ = db.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`) }()
	_, err = db.ExecContext(ctx, `SET search_path TO `+schema)
	require.NoError(t, err)

	installA := uuid.NewString()
	_, err = db.ExecContext(ctx, `
		CREATE TABLE users(id BIGINT PRIMARY KEY,email TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ);
		CREATE TABLE donebot_installs(id BIGSERIAL PRIMARY KEY,installation_id UUID UNIQUE NOT NULL,user_id BIGINT NOT NULL,status TEXT NOT NULL DEFAULT 'active',expires_at TIMESTAMPTZ);
		INSERT INTO users(id,email) VALUES
			(1,'alice@example.invalid'),
			(2,'bob@example.invalid'),
			(3,'carol@example.invalid'),
			(4,'outsider@example.invalid');
		INSERT INTO donebot_installs(installation_id,user_id,status) VALUES ($1::uuid,1,'active');
	`, installA)
	require.NoError(t, err)

	for _, file := range []string{"../../migrations/271_donebot_teams.sql", "../../migrations/280_donebot_collaboration.sql"} {
		sqlBytes, readErr := os.ReadFile(file)
		require.NoError(t, readErr)
		_, err = db.ExecContext(ctx, string(sqlBytes))
		require.NoError(t, err)
	}

	teams := donebotteam.New(db, true)
	collab := New(db, true)

	// 1. Friend flow: Alice invites Bob, Bob accepts.
	sent, err := collab.SendFriendRequest(ctx, 1, "bob@example.invalid", "一起协作")
	require.NoError(t, err)
	require.Equal(t, "pending", sent.Status)
	_, err = collab.AcceptFriendRequest(ctx, 2, sent.Request.ID)
	require.NoError(t, err)

	friends, err := collab.ListFriends(ctx, 1)
	require.NoError(t, err)
	require.Len(t, friends.Friends, 1)
	require.Equal(t, int64(2), friends.Friends[0].UserID)

	// 2. Team flow: Alice creates a team, invites Carol, Carol sees incoming invitation and accepts by ID.
	team, err := teams.Create(ctx, 1, "研发组", uuid.NewString())
	require.NoError(t, err)
	inv, err := teams.Invite(ctx, team.ID, 1, "carol@example.invalid", donebotteam.Member)
	require.NoError(t, err)
	incoming, err := teams.IncomingInvitations(ctx, 3)
	require.NoError(t, err)
	require.Len(t, incoming, 1)
	require.Equal(t, inv.ID, incoming[0].ID)
	_, err = teams.AcceptInvitation(ctx, 3, inv.ID)
	require.NoError(t, err)

	// 3. Alice shares a folder: team read-only, Bob (friend) write; outsider denied.
	folder, err := collab.SaveFolder(ctx, 1, SaveFolderInput{
		InstallationID: installA,
		DeviceName:     "Alice Mac",
		Name:           "项目资料",
		Enabled:        true,
		Grants: []FolderGrantInput{
			{SubjectType: "team", SubjectID: team.ID, Access: "read"},
			{SubjectType: "user", SubjectID: strconv.FormatInt(2, 10), Access: "write"},
		},
	})
	require.NoError(t, err)
	require.Len(t, folder.Grants, 2)

	// Bob (friend) has write access.
	authBob, err := collab.AuthorizeInvoke(ctx, 2, folder.ID, "write_file")
	require.NoError(t, err)
	require.Equal(t, "write", authBob.GrantedAccess)

	// Carol (team member) can read but cannot write.
	authCarol, err := collab.AuthorizeInvoke(ctx, 3, folder.ID, "read_file")
	require.NoError(t, err)
	require.Equal(t, "read", authCarol.GrantedAccess)
	_, err = collab.AuthorizeInvoke(ctx, 3, folder.ID, "write_file")
	require.Error(t, err)

	// Outsider cannot read.
	_, err = collab.AuthorizeInvoke(ctx, 4, folder.ID, "read_file")
	require.Error(t, err)

	// Removing Bob as friend immediately revokes his folder access.
	require.NoError(t, collab.RemoveFriend(ctx, 1, 2))
	_, err = collab.AuthorizeInvoke(ctx, 2, folder.ID, "read_file")
	require.Error(t, err)

	// 4. Shared Bot template without memory.
	pkg := json.RawMessage(`{"format":"openmaus.package","version":2,"package":{"name":"写作助手","agents":[{"name":"写作助手","soul":"严格遵循中文排版规范"}]}}`)
	sharedBot, err := collab.SaveBot(ctx, 1, SaveBotInput{
		SourceBotID: "bot-writer",
		Name:        "写作助手",
		Title:       "文案与排版",
		SkillCount:  2,
		HasSoul:     true,
		Package:     pkg,
		Grants:      []BotGrantInput{{SubjectType: "team", SubjectID: team.ID}},
	})
	require.NoError(t, err)

	carolBots, err := collab.ListBots(ctx, 3)
	require.NoError(t, err)
	require.Len(t, carolBots.SharedWithMe, 1)
	require.Equal(t, sharedBot.ID, carolBots.SharedWithMe[0].ID)

	gotPkg, err := collab.GetBotPackage(ctx, 3, sharedBot.ID)
	require.NoError(t, err)
	require.Contains(t, string(gotPkg.Package), "写作助手")

	// Rejects packages containing personal memory.
	_, err = collab.SaveBot(ctx, 1, SaveBotInput{
		Name:    "带记忆的Bot",
		Package: json.RawMessage(`{"package":{"agents":[{"name":"X","starterNotes":[{"path":"m.md","text":"private"}]}]}}`),
	})
	require.Error(t, err)

	// Carol leaves team -> loses access to team folder and team shared bot immediately.
	require.NoError(t, teams.Leave(ctx, team.ID, 3))
	_, err = collab.AuthorizeInvoke(ctx, 3, folder.ID, "read_file")
	require.Error(t, err)
	_, err = collab.GetBotPackage(ctx, 3, sharedBot.ID)
	require.Error(t, err)
}

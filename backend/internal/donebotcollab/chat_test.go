package donebotcollab

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestChatHelpers(t *testing.T) {
	require.Equal(t, "u:2:9", friendScopeKey(9, 2))
	a, b, ok := parseFriendScopeKey("u:2:9")
	require.True(t, ok)
	require.Equal(t, int64(2), a)
	require.Equal(t, int64(9), b)

	_, _, ok = parseFriendScopeKey("u:9:2")
	require.False(t, ok)
	_, _, ok = parseFriendScopeKey("bad")
	require.False(t, ok)

	require.Equal(t, "设计方案.pdf", cleanChatFileName("../../dir/设计方案.pdf"))
	require.Equal(t, "file", cleanChatFileName(".."))
	require.Equal(t, "report.txt", cleanChatFileName("a\\b\\report.txt"))
	require.True(t, IsSafeInlineChatMime("image/png"))
	require.True(t, IsSafeInlineChatMime("image/jpeg; charset=binary"))
	require.False(t, IsSafeInlineChatMime("image/svg+xml"))
	require.False(t, IsSafeInlineChatMime("text/html"))

	require.True(t, bodyMentionsBot("@多宝 帮我总结一下", "多宝"))
	require.True(t, bodyMentionsBot("问一下 @多宝，这个怎么写", "多宝"))
	require.True(t, bodyMentionsBot("你好 @duobao", "DuoBao"))
	require.False(t, bodyMentionsBot("@多宝宝 你好", "多宝"))
	require.False(t, bodyMentionsBot("普通消息没有提及", "多宝"))

	_, err := validateAvatarDataURL("data:image/jpeg;base64,/9j/4AAQ")
	require.NoError(t, err)
	_, err = validateAvatarDataURL("javascript:alert(1)")
	require.Error(t, err)
}

func TestCollabChatPostgres(t *testing.T) {
	dsn := os.Getenv("DONEBOT_TEAM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires an owned disposable PostgreSQL fixture")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	schema := "donebot_chat_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = db.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	defer func() { _, _ = db.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`) }()
	_, err = db.ExecContext(ctx, `SET search_path TO `+schema)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		CREATE TABLE users(id BIGINT PRIMARY KEY,email TEXT NOT NULL,username TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ);
		CREATE TABLE user_avatars(user_id BIGINT PRIMARY KEY,url TEXT NOT NULL DEFAULT '');
		CREATE TABLE donebot_installs(id BIGSERIAL PRIMARY KEY,installation_id UUID UNIQUE NOT NULL,user_id BIGINT NOT NULL,status TEXT NOT NULL DEFAULT 'active',expires_at TIMESTAMPTZ);
		INSERT INTO users(id,email,username) VALUES
			(1,'alice@example.invalid','Alice'),
			(2,'bob@example.invalid','Bob'),
			(3,'carol@example.invalid','Carol');
	`)
	require.NoError(t, err)

	for _, file := range []string{
		"../../migrations/271_donebot_teams.sql",
		"../../migrations/280_donebot_collaboration.sql",
		"../../migrations/281_donebot_friend_remarks.sql",
		"../../migrations/282_donebot_collab_messages.sql",
		"../../migrations/283_donebot_collab_groups_and_bots.sql",
		"../../migrations/284_donebot_collab_bot_appearance.sql",
		"../../migrations/285_donebot_team_channels_and_tasks.sql",
		"../../migrations/286_donebot_team_task_automation.sql",
	} {
		sqlBytes, readErr := os.ReadFile(file)
		require.NoError(t, readErr)
		_, err = db.ExecContext(ctx, string(sqlBytes))
		require.NoError(t, err)
	}

	teams := donebotteam.New(db, true)
	collab := New(db, true)
	collab.SetChatStorageDir(t.TempDir())

	// Become friends: Alice & Bob.
	sent, err := collab.SendFriendRequest(ctx, 1, "bob@example.invalid", "")
	require.NoError(t, err)
	_, err = collab.AcceptFriendRequest(ctx, 2, sent.Request.ID)
	require.NoError(t, err)

	// Send text and file messages.
	msg1, err := collab.SendChatMessage(ctx, 1, "friend", "2", "你好 Bob")
	require.NoError(t, err)
	require.Equal(t, "text", msg1.Kind)
	require.Equal(t, "Alice", msg1.SenderNickname)

	fileBytes := []byte("hello-collab-chat-file-payload")
	msg2, err := collab.SendChatFile(ctx, 1, "friend", "2", "需求文档.txt", "text/plain", "看下这份文档", bytes.NewReader(fileBytes), int64(len(fileBytes)))
	require.NoError(t, err)
	require.Equal(t, "file", msg2.Kind)
	require.Equal(t, "需求文档.txt", msg2.FileName)
	require.Equal(t, int64(len(fileBytes)), msg2.FileSize)

	// Bob sees unread count = 2.
	sums, err := collab.ChatSummaries(ctx, 2)
	require.NoError(t, err)
	require.Len(t, sums, 1)
	require.Equal(t, 2, sums[0].UnreadCount)

	// Bob reads messages and downloads the file.
	msgs, err := collab.ListChatMessages(ctx, 2, "friend", "1", 0, 0, 50)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	f, meta, err := collab.OpenChatFile(ctx, 2, msg2.FileID)
	require.NoError(t, err)
	got, err := io.ReadAll(f)
	_ = f.Close()
	require.NoError(t, err)
	require.Equal(t, fileBytes, got)
	require.Equal(t, "需求文档.txt", meta.FileName)

	// Carol (outsider) cannot read or download.
	_, err = collab.ListChatMessages(ctx, 3, "friend", "1", 0, 0, 50)
	require.Error(t, err)
	_, _, err = collab.OpenChatFile(ctx, 3, msg2.FileID)
	require.Error(t, err)

	// Bob marks read -> unread count = 0.
	_, err = collab.MarkChatRead(ctx, 2, "friend", "1", msg2.ID)
	require.NoError(t, err)
	sums, err = collab.ChatSummaries(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, 0, sums[0].UnreadCount)

	// Team chat: Alice creates team, invites Carol, Carol joins and exchanges messages.
	team, err := teams.Create(ctx, 1, "设计小组", uuid.NewString())
	require.NoError(t, err)
	inv, err := teams.Invite(ctx, team.ID, 1, "carol@example.invalid", donebotteam.Member)
	require.NoError(t, err)
	_, err = teams.AcceptInvitation(ctx, 3, inv.ID)
	require.NoError(t, err)

	_, err = collab.SendChatMessage(ctx, 3, "team", team.ID, "大家好")
	require.NoError(t, err)
	teamMsgs, err := collab.ListChatMessages(ctx, 1, "team", team.ID, 0, 0, 50)
	require.NoError(t, err)
	require.Len(t, teamMsgs, 1)
	require.Equal(t, "大家好", teamMsgs[0].Body)

	// Team avatar & Friend group + authorized @bot invocation.
	avatar, err := collab.SetTeamAvatar(ctx, 1, team.ID, "data:image/png;base64,iVBORw0KGgo=")
	require.NoError(t, err)
	require.Equal(t, "data:image/png;base64,iVBORw0KGgo=", avatar)
	// Member (Carol) cannot change team avatar.
	_, err = collab.SetTeamAvatar(ctx, 3, team.ID, "data:image/png;base64,iVBORw0KGgo=")
	require.Error(t, err)

	// Friend group created by Alice with Bob.
	grp, err := collab.CreateGroup(ctx, 1, "周末计划", []int64{2})
	require.NoError(t, err)
	require.Len(t, grp.Members, 2)

	// Alice adds her local bot to the friend group without granting Bob yet.
	installID := uuid.NewString()
	zoom15 := 1.5
	focusZero := 0.0
	focus75 := 0.75
	bot, err := collab.AddChatBot(ctx, 1, "group", grp.ID, AddChatBotInput{
		InstallationID:      installID,
		LocalBotID:          "bot-duobao",
		BotName:             "多宝",
		BotColor:            "chrome",
		BotAvatarURL:        "data:image/png;base64,iVBORw0KGgo=",
		BotMascotBody:       "circle",
		BotMascotFaceColor:  "black",
		BotMascotExpression: "happy",
		BotAvatarCrop:       "rounded",
		BotAvatarZoom:       &zoom15,
		BotAvatarFocusX:     &focusZero,
		BotAvatarFocusY:     &focus75,
	})
	require.NoError(t, err)
	require.True(t, bot.CanInvoke)
	require.Equal(t, "chrome", bot.BotColor)
	require.Equal(t, "circle", bot.BotMascotBody)
	require.Equal(t, "black", bot.BotMascotFaceColor)
	require.Equal(t, "happy", bot.BotMascotExpression)
	require.Equal(t, "rounded", bot.BotAvatarCrop)
	require.Equal(t, 1.5, bot.BotAvatarZoom)
	require.Equal(t, 0.0, bot.BotAvatarFocusX)
	require.Equal(t, 0.75, bot.BotAvatarFocusY)

	// Bob is NOT authorized yet -> @多宝 is denied and creates zero invocations.
	deniedMsg, err := collab.SendChatMessage(ctx, 2, "group", grp.ID, "@多宝 帮我查一下")
	require.NoError(t, err)
	require.Empty(t, deniedMsg.TriggeredBotIDs)
	require.Equal(t, []string{"多宝"}, deniedMsg.DeniedBotNames)
	claimedNone, err := collab.ClaimBotInvocation(ctx, 1, installID)
	require.NoError(t, err)
	require.Nil(t, claimedNone)

	// Alice authorizes Bob -> Bob's @多宝 triggers an invocation that Alice claims & answers locally!
	_, err = collab.UpdateChatBotGrants(ctx, 1, "group", grp.ID, bot.ID, UpdateChatBotGrantsInput{
		AllowAllMembers: false,
		AllowedUserIDs:  []int64{2},
	})
	require.NoError(t, err)
	allowedMsg, err := collab.SendChatMessage(ctx, 2, "group", grp.ID, "@多宝 帮我总结一下")
	require.NoError(t, err)
	require.Equal(t, []string{bot.ID}, allowedMsg.TriggeredBotIDs)
	require.Empty(t, allowedMsg.DeniedBotNames)

	claimed, err := collab.ClaimBotInvocation(ctx, 1, installID)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, "bot-duobao", claimed.LocalBotID)

	reply, err := collab.CompleteBotInvocation(ctx, 1, claimed.ID, "这是本地多宝生成的总结。", "")
	require.NoError(t, err)
	require.Equal(t, "bot", reply.SenderKind)
	require.Equal(t, "多宝", reply.SenderBotName)
	require.Equal(t, "circle", reply.SenderBotMascotBody)
	require.Equal(t, "rounded", reply.SenderBotAvatarCrop)
	require.NotNil(t, reply.SenderBotAvatarFocusX)
	require.Equal(t, 0.0, *reply.SenderBotAvatarFocusX)

	// Sync updated appearance (switch from custom photo to star mascot) -> updates both roster and live messages.
	updatedCount, err := collab.SyncChatBots(ctx, 1, SyncChatBotsInput{
		InstallationID: installID,
		Bots: []SyncChatBotProfileItem{{
			LocalBotID:          "bot-duobao",
			BotName:             "多宝",
			BotColor:            "purple",
			BotAvatarURL:        "data:image/png;base64,iVBORw0KGgo=",
			BotMascotBody:       "star",
			BotMascotFaceColor:  "white",
			BotMascotExpression: "focused",
			BotAvatarCrop:       "mascot",
		}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, updatedCount)

	botsAfter, err := collab.ListChatBots(ctx, 2, "group", grp.ID)
	require.NoError(t, err)
	require.Len(t, botsAfter, 1)
	require.Equal(t, "purple", botsAfter[0].BotColor)
	require.Equal(t, "star", botsAfter[0].BotMascotBody)
	require.Equal(t, "white", botsAfter[0].BotMascotFaceColor)
	require.Equal(t, "focused", botsAfter[0].BotMascotExpression)
	require.Equal(t, "mascot", botsAfter[0].BotAvatarCrop)
	require.Empty(t, botsAfter[0].BotAvatarURL)

	msgsAfter, err := collab.ListChatMessages(ctx, 2, "group", grp.ID, 0, 0, 20)
	require.NoError(t, err)
	var syncedBotMsg *ChatMessage
	for i := range msgsAfter {
		if msgsAfter[i].ID == reply.ID {
			syncedBotMsg = &msgsAfter[i]
		}
	}
	require.NotNil(t, syncedBotMsg)
	require.Equal(t, "purple", syncedBotMsg.SenderBotColor)
	require.Equal(t, "star", syncedBotMsg.SenderBotMascotBody)
	require.Equal(t, "mascot", syncedBotMsg.SenderBotAvatarCrop)
	require.Empty(t, syncedBotMsg.SenderBotAvatarURL)

	// Removing bot preserves original snapshot on historical messages.
	require.NoError(t, collab.RemoveChatBot(ctx, 1, "group", grp.ID, bot.ID))
	msgsRemoved, err := collab.ListChatMessages(ctx, 2, "group", grp.ID, 0, 0, 20)
	require.NoError(t, err)
	var removedBotMsg *ChatMessage
	for i := range msgsRemoved {
		if msgsRemoved[i].ID == reply.ID {
			removedBotMsg = &msgsRemoved[i]
		}
	}
	require.NotNil(t, removedBotMsg)
	require.Equal(t, "circle", removedBotMsg.SenderBotMascotBody)
	require.Equal(t, "rounded", removedBotMsg.SenderBotAvatarCrop)
}

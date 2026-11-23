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

func TestTeamChannelsAndTasksPostgres(t *testing.T) {
	dsn := os.Getenv("DONEBOT_TEAM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires an owned disposable PostgreSQL fixture")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	schema := "donebot_team_ws_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
			(1,'boss@example.invalid','老罗'),
			(2,'designer@example.invalid','小王'),
			(3,'ops@example.invalid','小罗'),
			(4,'outsider@example.invalid','外部人员');
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
	} {
		sqlBytes, readErr := os.ReadFile(file)
		require.NoError(t, readErr)
		_, err = db.ExecContext(ctx, string(sqlBytes))
		require.NoError(t, err)
	}

	teams := donebotteam.New(db, true)
	collab := New(db, true)
	collab.SetChatStorageDir(t.TempDir())

	// Create team with 3 members: 1 (owner), 2 (member), 3 (member).
	team, err := teams.Create(ctx, 1, "AI短剧组", uuid.NewString())
	require.NoError(t, err)
	inv2, err := teams.Invite(ctx, team.ID, 1, "designer@example.invalid", donebotteam.Member)
	require.NoError(t, err)
	_, err = teams.AcceptInvitation(ctx, 2, inv2.ID)
	require.NoError(t, err)
	inv3, err := teams.Invite(ctx, team.ID, 1, "ops@example.invalid", donebotteam.Member)
	require.NoError(t, err)
	_, err = teams.AcceptInvitation(ctx, 3, inv3.ID)
	require.NoError(t, err)

	// 1. Default channel auto-created with backward-compatible team scope.
	channels, err := collab.ListTeamChannels(ctx, 1, team.ID, false)
	require.NoError(t, err)
	require.Len(t, channels, 1)
	require.True(t, channels[0].IsDefault)
	require.Equal(t, "大厅", channels[0].Name)
	require.Equal(t, "team", channels[0].ChatScopeType)
	require.Equal(t, team.ID, channels[0].ChatTargetID)

	// 2. Member job titles.
	_, err = collab.SetTeamMemberJobTitle(ctx, 2, team.ID, 2, "分镜与美术")
	require.NoError(t, err)
	_, err = collab.SetTeamMemberJobTitle(ctx, 1, team.ID, 3, "制片运营")
	require.NoError(t, err)
	// Regular member cannot edit someone else's job title.
	_, err = collab.SetTeamMemberJobTitle(ctx, 2, team.ID, 3, "越权修改")
	require.Error(t, err)
	profiles, err := collab.ListTeamMemberProfiles(ctx, 3, team.ID)
	require.NoError(t, err)
	require.Len(t, profiles, 2)

	// 3. Create public sub-channel and private sub-channel (only 1 & 2, excluding 3).
	pubCh, err := collab.CreateTeamChannel(ctx, 2, team.ID, CreateTeamChannelInput{
		Name:        "第1-5集分镜",
		Description: "分镜图与样片沟通",
	})
	require.NoError(t, err)
	require.Equal(t, "channel", pubCh.ChatScopeType)
	require.Equal(t, pubCh.ID, pubCh.ChatTargetID)

	privCh, err := collab.CreateTeamChannel(ctx, 1, team.ID, CreateTeamChannelInput{
		Name:      "导演审片",
		IsPrivate: true,
		MemberIDs: []int64{2},
	})
	require.NoError(t, err)
	require.True(t, privCh.IsPrivate)
	require.ElementsMatch(t, []int64{1, 2}, privCh.MemberIDs)

	// Member 3 sees default + public only, NOT private.
	ch3, err := collab.ListTeamChannels(ctx, 3, team.ID, false)
	require.NoError(t, err)
	require.Len(t, ch3, 2)
	_, err = collab.ListChatMessages(ctx, 3, "channel", privCh.ID, 0, 0, 20)
	require.Error(t, err)

	// 4. Add team bots: owner 1's bot & member 3's bot.
	install1 := uuid.NewString()
	install3 := uuid.NewString()
	_, err = collab.AddChatBot(ctx, 1, "team", team.ID, AddChatBotInput{
		InstallationID:  install1,
		LocalBotID:      "bot-director",
		BotName:         "导演助手",
		AllowAllMembers: true,
	})
	require.NoError(t, err)
	_, err = collab.AddChatBot(ctx, 3, "team", team.ID, AddChatBotInput{
		InstallationID:  install3,
		LocalBotID:      "bot-ops",
		BotName:         "运营助手",
		AllowAllMembers: true,
	})
	require.NoError(t, err)

	// In private channel (only 1 & 2), mentioning 1's bot triggers it; mentioning 3's bot does NOT leak to 3!
	privMsg, err := collab.SendChatMessage(ctx, 2, "channel", privCh.ID, "@导演助手 @运营助手 总结一下本集节奏")
	require.NoError(t, err)
	require.Len(t, privMsg.TriggeredBotIDs, 1)

	invOps, err := collab.ClaimBotInvocation(ctx, 3, install3)
	require.NoError(t, err)
	require.Nil(t, invOps)

	invDir, err := collab.ClaimBotInvocation(ctx, 1, install1)
	require.NoError(t, err)
	require.NotNil(t, invDir)
	require.Equal(t, "channel", invDir.ScopeType)
	require.Equal(t, privCh.ID, invDir.TargetID)
	require.Equal(t, team.ID, invDir.TeamID)
	require.Equal(t, "AI短剧组 · #导演审片", invDir.ChatName)
	replyMsg, err := collab.CompleteBotInvocation(ctx, 1, invDir.ID, "本集前 3 秒钩子已对齐。", "")
	require.NoError(t, err)
	require.NotNil(t, replyMsg)
	require.Equal(t, "channel", replyMsg.ScopeType)

	// Sub-channel unread summaries appear for member 2 & 1, not for member 3 on private channel.
	sums3, err := collab.ChatSummaries(ctx, 3)
	require.NoError(t, err)
	for _, s := range sums3 {
		require.NotEqual(t, privCh.ID, s.TargetID)
	}

	// 5. Real human task workflow: create task -> assign -> submit deliverable -> review reject -> resubmit -> approve.
	assignee := int64(2)
	reviewer := int64(1)
	due := "2026-12-01"
	task, err := collab.CreateTeamTask(ctx, 1, team.ID, CreateTeamTaskInput{
		ChannelID:       pubCh.ID,
		Title:           "第3集女主定妆图与5秒样片",
		Description:     "需保持与第1集服装一致，输出 9:16 竖屏样片",
		Priority:        "high",
		AssigneeID:      &assignee,
		ReviewerID:      &reviewer,
		CollaboratorIDs: []int64{3},
		DueAt:           &due,
		SourceScopeType: "channel",
		SourceTargetID:  pubCh.ID,
		SourceQuote:     "周五前把第3集定妆图发群里看下",
		NotifyChannel:   true,
	})
	require.NoError(t, err)
	require.Equal(t, "todo", task.Status)
	require.Equal(t, pubCh.ID, task.ChannelID)
	require.Equal(t, []int64{3}, task.CollaboratorIDs)

	// Verify channel got the creation notification.
	pubMsgs, err := collab.ListChatMessages(ctx, 2, "channel", pubCh.ID, 0, 0, 20)
	require.NoError(t, err)
	require.Len(t, pubMsgs, 1)
	require.Contains(t, pubMsgs[0].Body, "新建任务「第3集女主定妆图与5秒样片」")

	// Counts reflect assignment to user 2.
	counts2, err := collab.TeamTaskCountsForUser(ctx, 2)
	require.NoError(t, err)
	require.Len(t, counts2, 1)
	require.Equal(t, 1, counts2[0].AssignedToMe)
	require.Equal(t, 0, counts2[0].ReviewByMe)

	// Assignee starts work & submits deliverable file.
	inProgress := "in_progress"
	updated, err := collab.UpdateTeamTask(ctx, 2, team.ID, task.ID, UpdateTeamTaskInput{Status: &inProgress})
	require.NoError(t, err)
	require.Equal(t, "in_progress", updated.Status)

	payload := []byte("fake-mp4-video-deliverable-v1")
	detail, err := collab.UploadTeamTaskFileUpdate(
		ctx, 2, team.ID, task.ID, "submission", "ep3_preview_v1.mp4", "video/mp4", "第一版样片请导演审阅", true,
		bytes.NewReader(payload), int64(len(payload)),
	)
	require.NoError(t, err)
	require.Equal(t, "in_review", detail.Task.Status)
	require.Equal(t, 1, detail.Task.DeliverableCount)
	lastUpdate := detail.Updates[len(detail.Updates)-1]
	require.Equal(t, "submission", lastUpdate.Kind)
	require.NotEmpty(t, lastUpdate.FileID)

	// Reviewer (1) can download the deliverable; outsider (4) cannot.
	f, meta, err := collab.OpenTeamTaskFile(ctx, 1, lastUpdate.FileID)
	require.NoError(t, err)
	got, err := io.ReadAll(f)
	_ = f.Close()
	require.NoError(t, err)
	require.Equal(t, payload, got)
	require.Equal(t, "ep3_preview_v1.mp4", meta.FileName)

	_, _, err = collab.OpenTeamTaskFile(ctx, 4, lastUpdate.FileID)
	require.Error(t, err)

	// Counts reflect pending review for user 1.
	counts1, err := collab.TeamTaskCountsForUser(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 1, counts1[0].ReviewByMe)

	// Member 3 (not reviewer, not admin) cannot review.
	_, err = collab.ReviewTeamTask(ctx, 3, team.ID, task.ID, "approve", "不错", false)
	require.Error(t, err)

	// Reviewer requests changes without reason -> rejected.
	_, err = collab.ReviewTeamTask(ctx, 1, team.ID, task.ID, "request_changes", "   ", false)
	require.Error(t, err)

	// Reviewer requests changes with concrete feedback.
	detail, err = collab.ReviewTeamTask(ctx, 1, team.ID, task.ID, "request_changes", "第2秒衣服颜色偏暗，请调亮一点", true)
	require.NoError(t, err)
	require.Equal(t, "changes_requested", detail.Task.Status)

	counts2, err = collab.TeamTaskCountsForUser(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, 1, counts2[0].ChangesRequestedToMe)

	// Assignee resubmits and reviewer approves.
	detail, err = collab.SubmitTeamTaskForReview(ctx, 2, team.ID, task.ID, "已调亮服装色彩，v2 已放共享目录", true)
	require.NoError(t, err)
	require.Equal(t, "in_review", detail.Task.Status)

	detail, err = collab.ReviewTeamTask(ctx, 1, team.ID, task.ID, "approve", "这版很好，定稿！", true)
	require.NoError(t, err)
	require.Equal(t, "done", detail.Task.Status)
	require.NotNil(t, detail.Task.CompletedAt)

	counts1, err = collab.TeamTaskCountsForUser(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 0, counts1[0].OpenTotal)

	// 6. Private channel task is hidden from excluded member 3 (list, detail, and file download).
	privTask, err := collab.CreateTeamTask(ctx, 1, team.ID, CreateTeamTaskInput{
		ChannelID:  privCh.ID,
		Title:      "保密商务报价",
		AssigneeID: &assignee,
	})
	require.NoError(t, err)
	// Assigning member 3 into private channel task must be rejected.
	member3 := int64(3)
	_, err = collab.CreateTeamTask(ctx, 1, team.ID, CreateTeamTaskInput{
		ChannelID:  privCh.ID,
		Title:      "非法指派",
		AssigneeID: &member3,
	})
	require.Error(t, err)

	privDetail, err := collab.UploadTeamTaskFileUpdate(
		ctx, 2, team.ID, privTask.ID, "submission", "secret.pdf", "application/pdf", "报价表", false,
		bytes.NewReader([]byte("secret-pdf")), 10,
	)
	require.NoError(t, err)
	privFileID := privDetail.Updates[len(privDetail.Updates)-1].FileID

	tasksFor3, err := collab.ListTeamTasks(ctx, 3, team.ID, ListTeamTasksFilter{Status: "all"})
	require.NoError(t, err)
	for _, tk := range tasksFor3 {
		require.NotEqual(t, privTask.ID, tk.ID)
	}
	_, err = collab.GetTeamTaskDetail(ctx, 3, team.ID, privTask.ID)
	require.Error(t, err)
	_, _, err = collab.OpenTeamTaskFile(ctx, 3, privFileID)
	require.Error(t, err)
}

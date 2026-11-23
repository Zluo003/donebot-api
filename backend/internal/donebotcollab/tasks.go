package donebotcollab

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	MaxOpenTasksPerTeam = 500
	MaxTaskTitleRunes   = 160
	MaxTaskDescRunes    = 8000
	MaxTaskQuoteRunes   = 1000
)

var validTaskStatuses = map[string]bool{
	"todo":              true,
	"in_progress":       true,
	"in_review":         true,
	"changes_requested": true,
	"blocked":           true,
	"done":              true,
}

var validTaskPriorities = map[string]bool{
	"low":    true,
	"normal": true,
	"high":   true,
	"urgent": true,
}

var taskStatusLabels = map[string]string{
	"todo":              "待开始",
	"in_progress":       "进行中",
	"in_review":         "待验收",
	"changes_requested": "待修改",
	"blocked":           "已阻塞",
	"done":              "已完成",
}

type TeamTask struct {
	ID               string     `json:"id"`
	TeamID           string     `json:"team_id"`
	ChannelID        string     `json:"channel_id,omitempty"`
	ChannelName      string     `json:"channel_name,omitempty"`
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	Status           string     `json:"status"`
	Priority         string     `json:"priority"`
	CreatorID        int64      `json:"creator_id"`
	AssigneeID       *int64     `json:"assignee_id,omitempty"`
	ReviewerID       *int64     `json:"reviewer_id,omitempty"`
	CollaboratorIDs  []int64    `json:"collaborator_ids"`
	DueAt            *time.Time `json:"due_at,omitempty"`
	SourceScopeType  string     `json:"source_scope_type,omitempty"`
	SourceTargetID   string     `json:"source_target_id,omitempty"`
	SourceMessageID  *int64     `json:"source_message_id,omitempty"`
	SourceQuote      string     `json:"source_quote,omitempty"`
	UpdateCount      int        `json:"update_count"`
	DeliverableCount int        `json:"deliverable_count"`
	SubmittedAt      *time.Time `json:"submitted_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type TeamTaskCounts struct {
	TeamID               string `json:"team_id"`
	OpenTotal            int    `json:"open_total"`
	AssignedToMe         int    `json:"assigned_to_me"`
	ReviewByMe           int    `json:"review_by_me"`
	ChangesRequestedToMe int    `json:"changes_requested_to_me"`
}

type TeamTaskUpdate struct {
	ID             int64          `json:"id"`
	TaskID         string         `json:"task_id"`
	ActorID        int64          `json:"actor_id"`
	ActorEmail     string         `json:"actor_email"`
	ActorNickname  string         `json:"actor_nickname,omitempty"`
	ActorAvatarURL string         `json:"actor_avatar_url,omitempty"`
	ActorRemark    string         `json:"actor_remark,omitempty"`
	Kind           string         `json:"kind"` // comment | submission | approve | request_changes | status_change
	Body           string         `json:"body"`
	FileID         string         `json:"file_id,omitempty"`
	FileName       string         `json:"file_name,omitempty"`
	FileMime       string         `json:"file_mime,omitempty"`
	FileSize       int64          `json:"file_size,omitempty"`
	FileSha256     string         `json:"file_sha256,omitempty"`
	Meta           map[string]any `json:"meta,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type TeamTaskDetail struct {
	Task    TeamTask         `json:"task"`
	Updates []TeamTaskUpdate `json:"updates"`
}

type ListTeamTasksFilter struct {
	ChannelID string
	Status    string // "" | "open" | "all" | specific status
	Scope     string // "" | "all" | "mine" | "review" | "created"
}

type CreateTeamTaskInput struct {
	ChannelID       string  `json:"channel_id"`
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	Priority        string  `json:"priority"`
	AssigneeID      *int64  `json:"assignee_id"`
	ReviewerID      *int64  `json:"reviewer_id"`
	CollaboratorIDs []int64 `json:"collaborator_ids"`
	DueAt           *string `json:"due_at"`
	SourceScopeType string  `json:"source_scope_type"`
	SourceTargetID  string  `json:"source_target_id"`
	SourceMessageID *int64  `json:"source_message_id"`
	SourceQuote     string  `json:"source_quote"`
	NotifyChannel   bool    `json:"notify_channel"`
}

type UpdateTeamTaskInput struct {
	ChannelID       *string  `json:"channel_id,omitempty"`
	Title           *string  `json:"title,omitempty"`
	Description     *string  `json:"description,omitempty"`
	Status          *string  `json:"status,omitempty"`
	Priority        *string  `json:"priority,omitempty"`
	AssigneeID      *int64   `json:"assignee_id,omitempty"`
	ClearAssignee   bool     `json:"clear_assignee,omitempty"`
	ReviewerID      *int64   `json:"reviewer_id,omitempty"`
	ClearReviewer   bool     `json:"clear_reviewer,omitempty"`
	CollaboratorIDs *[]int64 `json:"collaborator_ids,omitempty"`
	DueAt           *string  `json:"due_at,omitempty"`
	ClearDueAt      bool     `json:"clear_due_at,omitempty"`
	Note            string   `json:"note,omitempty"`
}

func parseOptionalDueAt(raw *string) (*time.Time, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02", trimmed); err == nil {
		endOfDay := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, time.Local)
		return &endOfDay, nil
	}
	return nil, infraerrors.BadRequest("COLLAB_TASK_DUE_INVALID", "截止时间格式无效。")
}

func (s *Service) lookupPersonName(ctx context.Context, db queryExecer, userID int64) string {
	var name string
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(username, ''), email) FROM users WHERE id = $1`, userID).Scan(&name)
	if name == "" {
		return fmt.Sprintf("成员 #%d", userID)
	}
	return name
}

func (s *Service) postTaskChannelNotice(ctx context.Context, tx queryExecer, actor int64, teamID, channelID, body string) error {
	cleanBody, err := cleanChatText(body, MaxChatBodyRunes, false)
	if err != nil {
		return nil
	}
	scopeType := "team"
	scopeKey := teamID
	if channelID != "" && validUUID(channelID) {
		var isDefault bool
		if err = tx.QueryRowContext(ctx, `SELECT is_default FROM donebot_team_channels WHERE id = $1::uuid AND team_id = $2::uuid`, channelID, teamID).Scan(&isDefault); err == nil && !isDefault {
			scopeType = "channel"
			scopeKey = channelID
		}
	}
	var msgID int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO donebot_collab_messages(scope_type, scope_key, sender_id, sender_kind, kind, body)
		VALUES($1, $2, $3, 'user', 'text', $4) RETURNING id`, scopeType, scopeKey, actor, cleanBody).Scan(&msgID); err != nil {
		return err
	}
	return s.upsertChatRead(ctx, tx, actor, scopeType, scopeKey, msgID)
}

func (s *Service) validateTaskChannelMember(ctx context.Context, db queryExecer, teamID, channelID string, userID int64, fieldLabel string) error {
	ok, err := isActiveTeamMember(ctx, db, userID, teamID)
	if err != nil {
		return err
	}
	if !ok {
		return infraerrors.BadRequest("COLLAB_TASK_MEMBER_INVALID", fieldLabel+"必须是当前团队成员。")
	}
	if channelID != "" {
		var isPrivate bool
		if err = db.QueryRowContext(ctx, `SELECT is_private FROM donebot_team_channels WHERE id = $1::uuid AND team_id = $2::uuid`, channelID, teamID).Scan(&isPrivate); err != nil {
			return err
		}
		if isPrivate {
			var inCh bool
			if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donebot_team_channel_members WHERE channel_id = $1::uuid AND user_id = $2)`, channelID, userID).Scan(&inCh); err != nil {
				return err
			}
			if !inCh {
				return infraerrors.BadRequest("COLLAB_TASK_MEMBER_INVALID", fieldLabel+"不在该私密频道中，无法指派。")
			}
		}
	}
	return nil
}

func (s *Service) authorizeTeamTask(ctx context.Context, actor int64, teamID, taskID string) (*TeamTask, string, error) {
	if err := s.available(); err != nil {
		return nil, "", err
	}
	if actor <= 0 {
		return nil, "", infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	if !validUUID(teamID) || !validUUID(taskID) {
		return nil, "", infraerrors.NotFound("COLLAB_TASK_NOT_FOUND", "团队任务不存在。")
	}
	role, ok, err := activeTeamRole(ctx, s.db, actor, teamID)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", infraerrors.Forbidden("COLLAB_TEAM_FORBIDDEN", "你不是该团队成员。")
	}
	task, err := s.fetchTaskByID(ctx, taskID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && task.TeamID != teamID) {
		return nil, "", infraerrors.NotFound("COLLAB_TASK_NOT_FOUND", "团队任务不存在。")
	}
	if err != nil {
		return nil, "", err
	}
	if task.ChannelID != "" {
		allowed, accessErr := s.canAccessChannel(ctx, s.db, actor, task.ChannelID)
		if accessErr != nil {
			return nil, "", accessErr
		}
		if !allowed {
			return nil, "", infraerrors.Forbidden("COLLAB_TASK_FORBIDDEN", "该任务属于私密频道，你无权访问。")
		}
	}
	return task, role, nil
}

const teamTaskSelectSQL = `SELECT t.id::text, t.team_id::text, COALESCE(t.channel_id::text, ''), COALESCE(c.name, ''),
	t.title, t.description, t.status, t.priority, t.creator_id, t.assignee_id, t.reviewer_id,
	t.due_at, t.source_scope_type, t.source_target_id, t.source_message_id, t.source_quote,
	(SELECT COUNT(*) FROM donebot_team_task_updates u WHERE u.task_id = t.id AND u.kind <> 'status_change') AS update_count,
	(SELECT COUNT(*) FROM donebot_team_task_updates u WHERE u.task_id = t.id AND u.kind = 'submission') AS deliverable_count,
	t.submitted_at, t.completed_at, t.created_at, t.updated_at
	FROM donebot_team_tasks t
	LEFT JOIN donebot_team_channels c ON c.id = t.channel_id`

func scanTeamTask(scanner interface{ Scan(...any) error }) (TeamTask, error) {
	var item TeamTask
	var assignee, reviewer, sourceMsg sql.NullInt64
	var dueAt, submittedAt, completedAt sql.NullTime
	err := scanner.Scan(
		&item.ID, &item.TeamID, &item.ChannelID, &item.ChannelName,
		&item.Title, &item.Description, &item.Status, &item.Priority, &item.CreatorID, &assignee, &reviewer,
		&dueAt, &item.SourceScopeType, &item.SourceTargetID, &sourceMsg, &item.SourceQuote,
		&item.UpdateCount, &item.DeliverableCount,
		&submittedAt, &completedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return TeamTask{}, err
	}
	if assignee.Valid {
		v := assignee.Int64
		item.AssigneeID = &v
	}
	if reviewer.Valid {
		v := reviewer.Int64
		item.ReviewerID = &v
	}
	if sourceMsg.Valid {
		v := sourceMsg.Int64
		item.SourceMessageID = &v
	}
	if dueAt.Valid {
		v := dueAt.Time
		item.DueAt = &v
	}
	if submittedAt.Valid {
		v := submittedAt.Time
		item.SubmittedAt = &v
	}
	if completedAt.Valid {
		v := completedAt.Time
		item.CompletedAt = &v
	}
	item.CollaboratorIDs = []int64{}
	return item, nil
}

func (s *Service) attachTaskCollaborators(ctx context.Context, teamID string, tasks []TeamTask) error {
	if len(tasks) == 0 {
		return nil
	}
	byIdx := make(map[string]int, len(tasks))
	for i := range tasks {
		byIdx[tasks[i].ID] = i
	}
	rows, err := s.db.QueryContext(ctx, `SELECT tc.task_id::text, tc.user_id
		FROM donebot_team_task_collaborators tc
		JOIN donebot_team_tasks t ON t.id = tc.task_id AND t.team_id = $1::uuid
		ORDER BY tc.user_id ASC`, teamID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var tid string
		var uid int64
		if err = rows.Scan(&tid, &uid); err != nil {
			return err
		}
		if idx, ok := byIdx[tid]; ok {
			tasks[idx].CollaboratorIDs = append(tasks[idx].CollaboratorIDs, uid)
		}
	}
	return rows.Err()
}

func (s *Service) fetchTaskByID(ctx context.Context, taskID string) (*TeamTask, error) {
	row := s.db.QueryRowContext(ctx, teamTaskSelectSQL+` WHERE t.id = $1::uuid`, taskID)
	task, err := scanTeamTask(row)
	if err != nil {
		return nil, err
	}
	list := []TeamTask{task}
	if err = s.attachTaskCollaborators(ctx, task.TeamID, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

func (s *Service) TeamTaskCountsForUser(ctx context.Context, actor int64) ([]TeamTaskCounts, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.team_id::text,
		COUNT(*) FILTER (WHERE t.status <> 'done') AS open_total,
		COUNT(*) FILTER (WHERE t.assignee_id = $1 AND t.status IN ('todo', 'in_progress', 'changes_requested', 'blocked')) AS assigned_to_me,
		COUNT(*) FILTER (WHERE t.reviewer_id = $1 AND t.status = 'in_review') AS review_by_me,
		COUNT(*) FILTER (WHERE t.assignee_id = $1 AND t.status = 'changes_requested') AS changes_requested_to_me
		FROM donebot_team_tasks t
		JOIN donebot_team_members tm ON tm.team_id = t.team_id AND tm.user_id = $1 AND tm.removed_at IS NULL
		JOIN donebot_teams dt ON dt.id = t.team_id AND dt.state = 'active'
		LEFT JOIN donebot_team_channels c ON c.id = t.channel_id
		WHERE (t.channel_id IS NULL OR c.id IS NULL OR c.is_private = FALSE OR EXISTS(
			SELECT 1 FROM donebot_team_channel_members cm WHERE cm.channel_id = t.channel_id AND cm.user_id = $1
		))
		GROUP BY t.team_id`, actor)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]TeamTaskCounts, 0)
	for rows.Next() {
		var item TeamTaskCounts
		if err = rows.Scan(&item.TeamID, &item.OpenTotal, &item.AssignedToMe, &item.ReviewByMe, &item.ChangesRequestedToMe); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) ListTeamTasks(ctx context.Context, actor int64, teamID string, filter ListTeamTasksFilter) ([]TeamTask, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	ok, err := isActiveTeamMember(ctx, s.db, actor, teamID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, infraerrors.Forbidden("COLLAB_TEAM_FORBIDDEN", "你不是该团队成员。")
	}

	channelFilter := strings.TrimSpace(filter.ChannelID)
	if channelFilter != "" {
		if !validUUID(channelFilter) {
			return nil, infraerrors.BadRequest("COLLAB_CHANNEL_NOT_FOUND", "频道不存在。")
		}
		allowed, accessErr := s.canAccessChannel(ctx, s.db, actor, channelFilter)
		if accessErr != nil {
			return nil, accessErr
		}
		if !allowed {
			return nil, infraerrors.Forbidden("COLLAB_CHANNEL_FORBIDDEN", "无权查看该频道的任务。")
		}
	}

	statusFilter := strings.TrimSpace(filter.Status)
	if statusFilter != "" && statusFilter != "all" && statusFilter != "open" && !validTaskStatuses[statusFilter] {
		return nil, infraerrors.BadRequest("COLLAB_TASK_STATUS_INVALID", "不支持的任务状态筛选。")
	}
	scopeFilter := strings.TrimSpace(filter.Scope)
	if scopeFilter != "" && scopeFilter != "all" && scopeFilter != "mine" && scopeFilter != "review" && scopeFilter != "created" {
		return nil, infraerrors.BadRequest("COLLAB_TASK_SCOPE_INVALID", "不支持的任务视角筛选。")
	}

	rows, err := s.db.QueryContext(ctx, teamTaskSelectSQL+`
		WHERE t.team_id = $1::uuid
			AND (t.channel_id IS NULL OR c.id IS NULL OR c.is_private = FALSE OR EXISTS(
				SELECT 1 FROM donebot_team_channel_members cm WHERE cm.channel_id = t.channel_id AND cm.user_id = $2
			))
			AND ($3 = '' OR t.channel_id = NULLIF($3, '')::uuid)
			AND (
				$4 = '' OR $4 = 'all'
				OR ($4 = 'open' AND t.status <> 'done')
				OR t.status = $4
			)
			AND (
				$5 = '' OR $5 = 'all'
				OR ($5 = 'mine' AND (t.assignee_id = $2 OR EXISTS(SELECT 1 FROM donebot_team_task_collaborators tc WHERE tc.task_id = t.id AND tc.user_id = $2)))
				OR ($5 = 'review' AND t.reviewer_id = $2)
				OR ($5 = 'created' AND t.creator_id = $2)
			)
		ORDER BY CASE WHEN t.status = 'done' THEN 1 ELSE 0 END ASC,
			CASE t.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END ASC,
			t.updated_at DESC, t.created_at DESC`,
		teamID, actor, channelFilter, statusFilter, scopeFilter)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]TeamTask, 0)
	for rows.Next() {
		item, scanErr := scanTeamTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = s.attachTaskCollaborators(ctx, teamID, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) listTaskUpdates(ctx context.Context, actor int64, taskID string) ([]TeamTaskUpdate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.task_id::text, u.actor_id,
		usr.email, COALESCE(usr.username, ''), COALESCE(ua.url, ''), COALESCE(rm.remark, ''),
		u.kind, u.body, u.file_id::text, u.file_name, u.file_mime, u.file_size, u.file_sha256,
		u.meta_json, u.created_at
		FROM donebot_team_task_updates u
		JOIN users usr ON usr.id = u.actor_id
		LEFT JOIN user_avatars ua ON ua.user_id = usr.id
		LEFT JOIN donebot_friend_remarks rm ON rm.owner_id = $1 AND rm.friend_id = usr.id
		WHERE u.task_id = $2::uuid
		ORDER BY u.id ASC`, actor, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]TeamTaskUpdate, 0)
	for rows.Next() {
		var item TeamTaskUpdate
		var fileID sql.NullString
		var metaBytes []byte
		if err = rows.Scan(
			&item.ID, &item.TaskID, &item.ActorID,
			&item.ActorEmail, &item.ActorNickname, &item.ActorAvatarURL, &item.ActorRemark,
			&item.Kind, &item.Body, &fileID, &item.FileName, &item.FileMime, &item.FileSize, &item.FileSha256,
			&metaBytes, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		if fileID.Valid {
			item.FileID = fileID.String
		}
		if len(metaBytes) > 0 {
			var parsed map[string]any
			if json.Unmarshal(metaBytes, &parsed) == nil && len(parsed) > 0 {
				item.Meta = parsed
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) GetTeamTaskDetail(ctx context.Context, actor int64, teamID, taskID string) (*TeamTaskDetail, error) {
	task, _, err := s.authorizeTeamTask(ctx, actor, teamID, taskID)
	if err != nil {
		return nil, err
	}
	updates, err := s.listTaskUpdates(ctx, actor, taskID)
	if err != nil {
		return nil, err
	}
	return &TeamTaskDetail{Task: *task, Updates: updates}, nil
}

func (s *Service) replaceTaskCollaborators(ctx context.Context, tx queryExecer, teamID, channelID, taskID string, ids []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM donebot_team_task_collaborators WHERE task_id = $1::uuid`, taskID); err != nil {
		return err
	}
	seen := map[int64]struct{}{}
	for _, uid := range ids {
		if uid <= 0 {
			continue
		}
		if _, dup := seen[uid]; dup {
			continue
		}
		seen[uid] = struct{}{}
		if err := s.validateTaskChannelMember(ctx, tx, teamID, channelID, uid, "协作者"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO donebot_team_task_collaborators(task_id, user_id) VALUES($1::uuid, $2) ON CONFLICT DO NOTHING`, taskID, uid); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) insertTaskUpdate(ctx context.Context, tx queryExecer, taskID string, actor int64, kind, body string, meta map[string]any) (int64, error) {
	metaJSON := []byte("{}")
	if len(meta) > 0 {
		if b, err := json.Marshal(meta); err == nil {
			metaJSON = b
		}
	}
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO donebot_team_task_updates(task_id, actor_id, kind, body, meta_json)
		VALUES($1::uuid, $2, $3, $4, $5::jsonb) RETURNING id`,
		taskID, actor, kind, body, string(metaJSON)).Scan(&id)
	return id, err
}

func (s *Service) CreateTeamTask(ctx context.Context, actor int64, teamID string, in CreateTeamTaskInput) (*TeamTask, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	ok, err := isActiveTeamMember(ctx, s.db, actor, teamID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, infraerrors.Forbidden("COLLAB_TEAM_FORBIDDEN", "你不是该团队成员。")
	}

	title, err := cleanText(in.Title, MaxTaskTitleRunes, "COLLAB_TASK_TITLE_INVALID", "任务标题需为 1-160 个字符。")
	if err != nil {
		return nil, err
	}
	desc, err := cleanOptionalText(in.Description, MaxTaskDescRunes, "COLLAB_TASK_DESC_INVALID", "任务说明不能超过 8000 个字符。")
	if err != nil {
		return nil, err
	}
	priority := strings.TrimSpace(in.Priority)
	if priority == "" {
		priority = "normal"
	}
	if !validTaskPriorities[priority] {
		return nil, infraerrors.BadRequest("COLLAB_TASK_PRIORITY_INVALID", "无效的任务优先级。")
	}
	dueAt, err := parseOptionalDueAt(in.DueAt)
	if err != nil {
		return nil, err
	}
	sourceQuote, err := cleanOptionalText(in.SourceQuote, MaxTaskQuoteRunes, "COLLAB_TASK_QUOTE_INVALID", "原消息摘录过长。")
	if err != nil {
		return nil, err
	}
	sourceScope := strings.TrimSpace(in.SourceScopeType)
	if sourceScope != "" && sourceScope != "team" && sourceScope != "channel" {
		sourceScope = ""
	}
	sourceTarget := strings.TrimSpace(in.SourceTargetID)
	if len(sourceTarget) > 64 {
		sourceTarget = ""
	}

	channelID := strings.TrimSpace(in.ChannelID)
	if channelID != "" {
		ch, chErr := s.authorizeTeamChannel(ctx, actor, channelID)
		if chErr != nil {
			return nil, chErr
		}
		if ch.TeamID != teamID {
			return nil, infraerrors.BadRequest("COLLAB_CHANNEL_NOT_FOUND", "所选频道不属于当前团队。")
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var openCount int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_team_tasks WHERE team_id = $1::uuid AND status <> 'done'`, teamID).Scan(&openCount); err != nil {
		return nil, err
	}
	if openCount >= MaxOpenTasksPerTeam {
		return nil, infraerrors.BadRequest("COLLAB_TASK_LIMIT", "团队未完成任务已达上限（500 条），请先归档或完成部分任务。")
	}

	var assigneeArg any
	if in.AssigneeID != nil && *in.AssigneeID > 0 {
		if err = s.validateTaskChannelMember(ctx, tx, teamID, channelID, *in.AssigneeID, "负责人"); err != nil {
			return nil, err
		}
		assigneeArg = *in.AssigneeID
	}
	var reviewerArg any
	if in.ReviewerID != nil && *in.ReviewerID > 0 {
		if err = s.validateTaskChannelMember(ctx, tx, teamID, channelID, *in.ReviewerID, "验收人"); err != nil {
			return nil, err
		}
		reviewerArg = *in.ReviewerID
	}
	var channelArg any
	if channelID != "" {
		channelArg = channelID
	}
	var sourceMsgArg any
	if in.SourceMessageID != nil && *in.SourceMessageID > 0 {
		sourceMsgArg = *in.SourceMessageID
	}

	taskID := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_team_tasks(
			id, team_id, channel_id, title, description, status, priority,
			creator_id, assignee_id, reviewer_id, due_at,
			source_scope_type, source_target_id, source_message_id, source_quote
		) VALUES($1::uuid, $2::uuid, $3::uuid, $4, $5, 'todo', $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		taskID, teamID, channelArg, title, desc, priority,
		actor, assigneeArg, reviewerArg, dueAt,
		sourceScope, sourceTarget, sourceMsgArg, sourceQuote); err != nil {
		return nil, err
	}
	if len(in.CollaboratorIDs) > 0 {
		if err = s.replaceTaskCollaborators(ctx, tx, teamID, channelID, taskID, in.CollaboratorIDs); err != nil {
			return nil, err
		}
	}

	summaryParts := []string{"创建了任务"}
	if in.AssigneeID != nil && *in.AssigneeID > 0 {
		summaryParts = append(summaryParts, "指派给 "+s.lookupPersonName(ctx, tx, *in.AssigneeID))
	}
	if in.ReviewerID != nil && *in.ReviewerID > 0 {
		summaryParts = append(summaryParts, "验收人："+s.lookupPersonName(ctx, tx, *in.ReviewerID))
	}
	if _, err = s.insertTaskUpdate(ctx, tx, taskID, actor, "status_change", strings.Join(summaryParts, "，"), map[string]any{"event": "created"}); err != nil {
		return nil, err
	}

	if in.NotifyChannel {
		noticeParts := []string{fmt.Sprintf("📋 新建任务「%s」", title)}
		if in.AssigneeID != nil && *in.AssigneeID > 0 {
			noticeParts = append(noticeParts, "负责人：@"+s.lookupPersonName(ctx, tx, *in.AssigneeID))
		}
		if in.ReviewerID != nil && *in.ReviewerID > 0 {
			noticeParts = append(noticeParts, "验收：@"+s.lookupPersonName(ctx, tx, *in.ReviewerID))
		}
		if dueAt != nil {
			noticeParts = append(noticeParts, "截止："+dueAt.Format("01-02"))
		}
		if err = s.postTaskChannelNotice(ctx, tx, actor, teamID, channelID, strings.Join(noticeParts, " · ")); err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.fetchTaskByID(ctx, taskID)
}

func (s *Service) UpdateTeamTask(ctx context.Context, actor int64, teamID, taskID string, in UpdateTeamTaskInput) (*TeamTask, error) {
	task, _, err := s.authorizeTeamTask(ctx, actor, teamID, taskID)
	if err != nil {
		return nil, err
	}

	nextChannel := task.ChannelID
	if in.ChannelID != nil {
		trimmed := strings.TrimSpace(*in.ChannelID)
		if trimmed != "" {
			ch, chErr := s.authorizeTeamChannel(ctx, actor, trimmed)
			if chErr != nil {
				return nil, chErr
			}
			if ch.TeamID != teamID {
				return nil, infraerrors.BadRequest("COLLAB_CHANNEL_NOT_FOUND", "所选频道不属于当前团队。")
			}
		}
		nextChannel = trimmed
	}

	nextTitle := task.Title
	if in.Title != nil {
		cleaned, titleErr := cleanText(*in.Title, MaxTaskTitleRunes, "COLLAB_TASK_TITLE_INVALID", "任务标题需为 1-160 个字符。")
		if titleErr != nil {
			return nil, titleErr
		}
		nextTitle = cleaned
	}
	nextDesc := task.Description
	if in.Description != nil {
		cleaned, descErr := cleanOptionalText(*in.Description, MaxTaskDescRunes, "COLLAB_TASK_DESC_INVALID", "任务说明不能超过 8000 个字符。")
		if descErr != nil {
			return nil, descErr
		}
		nextDesc = cleaned
	}
	nextStatus := task.Status
	if in.Status != nil {
		sVal := strings.TrimSpace(*in.Status)
		if !validTaskStatuses[sVal] {
			return nil, infraerrors.BadRequest("COLLAB_TASK_STATUS_INVALID", "无效的任务状态。")
		}
		nextStatus = sVal
	}
	nextPriority := task.Priority
	if in.Priority != nil {
		pVal := strings.TrimSpace(*in.Priority)
		if !validTaskPriorities[pVal] {
			return nil, infraerrors.BadRequest("COLLAB_TASK_PRIORITY_INVALID", "无效的任务优先级。")
		}
		nextPriority = pVal
	}

	nextAssignee := task.AssigneeID
	if in.ClearAssignee {
		nextAssignee = nil
	} else if in.AssigneeID != nil {
		if *in.AssigneeID <= 0 {
			nextAssignee = nil
		} else {
			v := *in.AssigneeID
			nextAssignee = &v
		}
	}

	nextReviewer := task.ReviewerID
	if in.ClearReviewer {
		nextReviewer = nil
	} else if in.ReviewerID != nil {
		if *in.ReviewerID <= 0 {
			nextReviewer = nil
		} else {
			v := *in.ReviewerID
			nextReviewer = &v
		}
	}

	nextDue := task.DueAt
	if in.ClearDueAt {
		nextDue = nil
	} else if in.DueAt != nil {
		parsed, dueErr := parseOptionalDueAt(in.DueAt)
		if dueErr != nil {
			return nil, dueErr
		}
		nextDue = parsed
	}

	note, err := cleanOptionalText(in.Note, 1000, "COLLAB_TASK_NOTE_INVALID", "备注不能超过 1000 个字符。")
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if nextAssignee != nil {
		if err = s.validateTaskChannelMember(ctx, tx, teamID, nextChannel, *nextAssignee, "负责人"); err != nil {
			return nil, err
		}
	}
	if nextReviewer != nil {
		if err = s.validateTaskChannelMember(ctx, tx, teamID, nextChannel, *nextReviewer, "验收人"); err != nil {
			return nil, err
		}
	}

	var channelArg any
	if nextChannel != "" {
		channelArg = nextChannel
	}
	nextSubmitted := task.SubmittedAt
	nextCompleted := task.CompletedAt
	now := time.Now()
	if nextStatus != task.Status {
		if nextStatus == "in_review" {
			nextSubmitted = &now
		}
		if nextStatus == "done" {
			nextCompleted = &now
		} else if task.Status == "done" {
			nextCompleted = nil
		}
	}

	if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_tasks
		SET channel_id = $2::uuid, title = $3, description = $4, status = $5, priority = $6,
		    assignee_id = $7, reviewer_id = $8, due_at = $9, submitted_at = $10, completed_at = $11, updated_at = NOW()
		WHERE id = $1::uuid`,
		taskID, channelArg, nextTitle, nextDesc, nextStatus, nextPriority,
		nextAssignee, nextReviewer, nextDue, nextSubmitted, nextCompleted); err != nil {
		return nil, err
	}

	if in.CollaboratorIDs != nil {
		if err = s.replaceTaskCollaborators(ctx, tx, teamID, nextChannel, taskID, *in.CollaboratorIDs); err != nil {
			return nil, err
		}
	}

	changes := make([]string, 0)
	if nextStatus != task.Status {
		changes = append(changes, fmt.Sprintf("状态改为「%s」", taskStatusLabels[nextStatus]))
	}
	if !int64PtrEqual(nextAssignee, task.AssigneeID) {
		if nextAssignee == nil {
			changes = append(changes, "取消了负责人")
		} else {
			changes = append(changes, "负责人改为 "+s.lookupPersonName(ctx, tx, *nextAssignee))
		}
	}
	if !int64PtrEqual(nextReviewer, task.ReviewerID) {
		if nextReviewer == nil {
			changes = append(changes, "取消了验收人")
		} else {
			changes = append(changes, "验收人改为 "+s.lookupPersonName(ctx, tx, *nextReviewer))
		}
	}
	if note != "" {
		changes = append(changes, "说明："+note)
	}
	if len(changes) > 0 {
		if _, err = s.insertTaskUpdate(ctx, tx, taskID, actor, "status_change", strings.Join(changes, "；"), map[string]any{
			"from_status": task.Status,
			"to_status":   nextStatus,
		}); err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.fetchTaskByID(ctx, taskID)
}

func int64PtrEqual(a, b *int64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func (s *Service) DeleteTeamTask(ctx context.Context, actor int64, teamID, taskID string) error {
	task, role, err := s.authorizeTeamTask(ctx, actor, teamID, taskID)
	if err != nil {
		return err
	}
	if task.CreatorID != actor && role != "owner" && role != "admin" {
		return infraerrors.Forbidden("COLLAB_TASK_FORBIDDEN", "仅任务创建人或团队管理员可删除任务。")
	}

	fileIDs := make([]string, 0)
	rows, err := s.db.QueryContext(ctx, `SELECT file_id::text FROM donebot_team_task_updates WHERE task_id = $1::uuid AND file_id IS NOT NULL`, taskID)
	if err == nil {
		for rows.Next() {
			var fid string
			if rows.Scan(&fid) == nil && fid != "" {
				fileIDs = append(fileIDs, fid)
			}
		}
		_ = rows.Close()
	}

	if _, err = s.db.ExecContext(ctx, `DELETE FROM donebot_team_tasks WHERE id = $1::uuid`, taskID); err != nil {
		return err
	}
	dir := strings.TrimSpace(s.chatDir)
	if dir == "" {
		dir = "data/collab-chat"
	}
	for _, fid := range fileIDs {
		_ = os.Remove(filepath.Join(dir, fid))
	}
	return nil
}

func (s *Service) AddTeamTaskComment(ctx context.Context, actor int64, teamID, taskID, rawBody string) (*TeamTaskDetail, error) {
	if _, _, err := s.authorizeTeamTask(ctx, actor, teamID, taskID); err != nil {
		return nil, err
	}
	body, err := cleanChatText(rawBody, MaxChatBodyRunes, false)
	if err != nil {
		return nil, infraerrors.BadRequest("COLLAB_TASK_COMMENT_EMPTY", "评论内容不能为空。")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = s.insertTaskUpdate(ctx, tx, taskID, actor, "comment", body, nil); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_tasks SET updated_at = NOW() WHERE id = $1::uuid`, taskID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetTeamTaskDetail(ctx, actor, teamID, taskID)
}

func (s *Service) SubmitTeamTaskForReview(ctx context.Context, actor int64, teamID, taskID, rawBody string, notifyChannel bool) (*TeamTaskDetail, error) {
	task, _, err := s.authorizeTeamTask(ctx, actor, teamID, taskID)
	if err != nil {
		return nil, err
	}
	body, err := cleanOptionalText(rawBody, MaxChatBodyRunes, "COLLAB_TASK_BODY_INVALID", "交付说明不能超过 8000 个字符。")
	if err != nil {
		return nil, err
	}
	if body == "" {
		body = "已提交交付成果，请验收。"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = s.insertTaskUpdate(ctx, tx, taskID, actor, "submission", body, map[string]any{"from_status": task.Status, "to_status": "in_review"}); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_tasks
		SET status = 'in_review', submitted_at = NOW(), updated_at = NOW()
		WHERE id = $1::uuid`, taskID); err != nil {
		return nil, err
	}
	if notifyChannel {
		notice := fmt.Sprintf("📤 提交验收「%s」", task.Title)
		if task.ReviewerID != nil {
			notice += " · 请 @" + s.lookupPersonName(ctx, tx, *task.ReviewerID) + " 验收"
		}
		if body != "" && body != "已提交交付成果，请验收。" {
			notice += "：" + truncateRunes(body, 120)
		}
		if err = s.postTaskChannelNotice(ctx, tx, actor, teamID, task.ChannelID, notice); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetTeamTaskDetail(ctx, actor, teamID, taskID)
}

func (s *Service) UploadTeamTaskFileUpdate(
	ctx context.Context,
	actor int64,
	teamID, taskID, kind, rawFileName, rawMime, rawBody string,
	notifyChannel bool,
	reader io.Reader,
	contentLength int64,
) (*TeamTaskDetail, error) {
	task, _, err := s.authorizeTeamTask(ctx, actor, teamID, taskID)
	if err != nil {
		return nil, err
	}
	if kind != "comment" && kind != "submission" {
		return nil, infraerrors.BadRequest("COLLAB_TASK_UPDATE_KIND_INVALID", "不支持的附件上传类型。")
	}
	if contentLength > MaxChatFileBytes {
		return nil, infraerrors.BadRequest("COLLAB_FILE_TOO_LARGE", "上传的文件不能超过 100 MB。")
	}
	caption, err := cleanChatText(rawBody, MaxChatCaptionRunes, true)
	if err != nil {
		return nil, err
	}
	fileName := cleanChatFileName(rawFileName)
	fileMime := normalizeChatMime(rawMime, fileName)

	dir := strings.TrimSpace(s.chatDir)
	if dir == "" {
		dir = "data/collab-chat"
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	fileID := uuid.NewString()
	finalPath := filepath.Join(dir, fileID)
	tmpFile, err := os.CreateTemp(dir, "task-upload-*.part")
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmpFile, hasher), io.LimitReader(reader, MaxChatFileBytes+1))
	if copyErr != nil {
		cleanupTmp()
		if strings.Contains(copyErr.Error(), "request body too large") {
			return nil, infraerrors.BadRequest("COLLAB_FILE_TOO_LARGE", "上传的文件不能超过 100 MB。")
		}
		return nil, copyErr
	}
	if written > MaxChatFileBytes {
		cleanupTmp()
		return nil, infraerrors.BadRequest("COLLAB_FILE_TOO_LARGE", "上传的文件不能超过 100 MB。")
	}
	if written == 0 {
		cleanupTmp()
		return nil, infraerrors.BadRequest("COLLAB_FILE_EMPTY", "不能上传空文件。")
	}
	if err = tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}
	if err = os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}

	shaHex := hex.EncodeToString(hasher.Sum(nil))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	metaJSON := "{}"
	if kind == "submission" {
		if b, mErr := json.Marshal(map[string]any{"from_status": task.Status, "to_status": "in_review"}); mErr == nil {
			metaJSON = string(b)
		}
	}

	if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_team_task_updates(
			task_id, actor_id, kind, body, file_id, file_name, file_mime, file_size, file_sha256, meta_json
		) VALUES($1::uuid, $2, $3, $4, $5::uuid, $6, $7, $8, $9, $10::jsonb)`,
		taskID, actor, kind, caption, fileID, fileName, fileMime, written, shaHex, metaJSON); err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}

	if kind == "submission" {
		if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_tasks
			SET status = 'in_review', submitted_at = NOW(), updated_at = NOW()
			WHERE id = $1::uuid`, taskID); err != nil {
			_ = os.Remove(finalPath)
			return nil, err
		}
		if notifyChannel {
			notice := fmt.Sprintf("📤 提交验收「%s」· 交付物：%s", task.Title, fileName)
			if task.ReviewerID != nil {
				notice += " · 请 @" + s.lookupPersonName(ctx, tx, *task.ReviewerID) + " 验收"
			}
			if caption != "" {
				notice += "：" + truncateRunes(caption, 100)
			}
			if err = s.postTaskChannelNotice(ctx, tx, actor, teamID, task.ChannelID, notice); err != nil {
				_ = os.Remove(finalPath)
				return nil, err
			}
		}
	} else {
		if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_tasks SET updated_at = NOW() WHERE id = $1::uuid`, taskID); err != nil {
			_ = os.Remove(finalPath)
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	return s.GetTeamTaskDetail(ctx, actor, teamID, taskID)
}

func (s *Service) ReviewTeamTask(ctx context.Context, actor int64, teamID, taskID, decision, rawBody string, notifyChannel bool) (*TeamTaskDetail, error) {
	task, role, err := s.authorizeTeamTask(ctx, actor, teamID, taskID)
	if err != nil {
		return nil, err
	}
	decision = strings.TrimSpace(decision)
	if decision != "approve" && decision != "request_changes" {
		return nil, infraerrors.BadRequest("COLLAB_TASK_REVIEW_INVALID", "验收决定仅支持通过或退回修改。")
	}
	if task.ReviewerID != nil && *task.ReviewerID != actor && role != "owner" && role != "admin" {
		return nil, infraerrors.Forbidden("COLLAB_TASK_REVIEW_FORBIDDEN", "该任务指定了其他验收人，仅指定验收人或团队管理员可验收。")
	}

	body, err := cleanOptionalText(rawBody, MaxChatBodyRunes, "COLLAB_TASK_REVIEW_BODY_INVALID", "验收意见不能超过 8000 个字符。")
	if err != nil {
		return nil, err
	}
	if decision == "request_changes" && body == "" {
		return nil, infraerrors.BadRequest("COLLAB_TASK_REVIEW_REASON_REQUIRED", "退回修改时请填写具体的修改意见。")
	}

	nextStatus := "done"
	updateKind := "approve"
	if decision == "request_changes" {
		nextStatus = "changes_requested"
		updateKind = "request_changes"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	displayBody := body
	if displayBody == "" && decision == "approve" {
		displayBody = "验收通过，已定稿。"
	}
	if _, err = s.insertTaskUpdate(ctx, tx, taskID, actor, updateKind, displayBody, map[string]any{
		"from_status": task.Status,
		"to_status":   nextStatus,
	}); err != nil {
		return nil, err
	}
	if decision == "approve" {
		if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_tasks
			SET status = 'done', completed_at = NOW(), updated_at = NOW()
			WHERE id = $1::uuid`, taskID); err != nil {
			return nil, err
		}
	} else {
		if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_tasks
			SET status = 'changes_requested', completed_at = NULL, updated_at = NOW()
			WHERE id = $1::uuid`, taskID); err != nil {
			return nil, err
		}
	}

	if notifyChannel {
		var notice string
		if decision == "approve" {
			notice = fmt.Sprintf("✅ 验收通过「%s」", task.Title)
			if body != "" {
				notice += "：" + truncateRunes(body, 120)
			}
		} else {
			notice = fmt.Sprintf("↩️ 退回修改「%s」", task.Title)
			if task.AssigneeID != nil {
				notice += " · @" + s.lookupPersonName(ctx, tx, *task.AssigneeID)
			}
			notice += "：" + truncateRunes(body, 140)
		}
		if err = s.postTaskChannelNotice(ctx, tx, actor, teamID, task.ChannelID, notice); err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetTeamTaskDetail(ctx, actor, teamID, taskID)
}

func (s *Service) OpenTeamTaskFile(ctx context.Context, actor int64, fileID string) (*os.File, ChatFileMeta, error) {
	if err := s.available(); err != nil {
		return nil, ChatFileMeta{}, err
	}
	if actor <= 0 {
		return nil, ChatFileMeta{}, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	if !validUUID(fileID) {
		return nil, ChatFileMeta{}, infraerrors.NotFound("COLLAB_FILE_NOT_FOUND", "任务附件不存在。")
	}

	var teamID, taskID string
	meta := ChatFileMeta{FileID: fileID}
	err := s.db.QueryRowContext(ctx, `SELECT t.team_id::text, u.task_id::text, u.file_name, u.file_mime, u.file_size, u.file_sha256
		FROM donebot_team_task_updates u
		JOIN donebot_team_tasks t ON t.id = u.task_id
		WHERE u.file_id = $1::uuid`, fileID).Scan(
		&teamID, &taskID, &meta.FileName, &meta.FileMime, &meta.FileSize, &meta.FileSha256,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ChatFileMeta{}, infraerrors.NotFound("COLLAB_FILE_NOT_FOUND", "任务附件不存在。")
	}
	if err != nil {
		return nil, ChatFileMeta{}, err
	}
	if _, _, err = s.authorizeTeamTask(ctx, actor, teamID, taskID); err != nil {
		return nil, ChatFileMeta{}, err
	}

	dir := strings.TrimSpace(s.chatDir)
	if dir == "" {
		dir = "data/collab-chat"
	}
	f, err := os.Open(filepath.Join(dir, fileID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ChatFileMeta{}, infraerrors.NotFound("COLLAB_FILE_NOT_FOUND", "附件已过期或不存在。")
	}
	if err != nil {
		return nil, ChatFileMeta{}, err
	}
	return f, meta, nil
}

func truncateRunes(raw string, max int) string {
	runes := []rune(strings.TrimSpace(raw))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}

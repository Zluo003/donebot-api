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
	MaxOpenTasksPerTeam    = 500
	MaxTaskTitleRunes      = 160
	MaxTaskDescRunes       = 8000
	MaxTaskQuoteRunes      = 1000
	MaxAcceptanceCriteria  = 12
	MaxCriterionRunes      = 240
	DefaultMaxAutoRevision = 2
)

var validTaskStatuses = map[string]bool{
	"todo":              true,
	"in_progress":       true,
	"in_review":         true,
	"bot_reviewing":     true,
	"bot_revising":      true,
	"ready_for_review":  true,
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
	"bot_reviewing":     "Bot 初审中",
	"bot_revising":      "Bot 自动返工中",
	"ready_for_review":  "待终审拍板",
	"changes_requested": "待修改",
	"blocked":           "已阻塞",
	"done":              "已完成",
}

type BotReviewSnapshot struct {
	Verdict         string   `json:"verdict"`
	Summary         string   `json:"summary"`
	BotName         string   `json:"bot_name,omitempty"`
	BatchID         string   `json:"batch_id,omitempty"`
	PassedItems     []string `json:"passed_items,omitempty"`
	AttentionItems  []string `json:"attention_items,omitempty"`
	FailedCriteria  []string `json:"failed_criteria,omitempty"`
	ActionableFixes []string `json:"actionable_fixes,omitempty"`
	Round           int      `json:"round,omitempty"`
	ReviewedAt      string   `json:"reviewed_at,omitempty"`
}

type TeamTask struct {
	ID                      string             `json:"id"`
	TeamID                  string             `json:"team_id"`
	ChannelID               string             `json:"channel_id,omitempty"`
	ChannelName             string             `json:"channel_name,omitempty"`
	Title                   string             `json:"title"`
	Description             string             `json:"description"`
	AcceptanceCriteria      []string           `json:"acceptance_criteria"`
	Status                  string             `json:"status"`
	Priority                string             `json:"priority"`
	CreatorID               int64              `json:"creator_id"`
	AssigneeID              *int64             `json:"assignee_id,omitempty"`
	ReviewerID              *int64             `json:"reviewer_id,omitempty"`
	CollaboratorIDs         []int64            `json:"collaborator_ids"`
	DueAt                   *time.Time         `json:"due_at,omitempty"`
	AutoReviewEnabled       bool               `json:"auto_review_enabled"`
	MaxAutoRevisions        int                `json:"max_auto_revisions"`
	AutoRevisionCount       int                `json:"auto_revision_count"`
	AssigneeAutoResubmit    bool               `json:"assignee_auto_resubmit"`
	LatestSubmissionBatchID string             `json:"latest_submission_batch_id,omitempty"`
	LatestBotReviewVerdict  string             `json:"latest_bot_review_verdict,omitempty"`
	LatestBotReviewSummary  string             `json:"latest_bot_review_summary,omitempty"`
	LatestBotReview         *BotReviewSnapshot `json:"latest_bot_review,omitempty"`
	SourceScopeType         string             `json:"source_scope_type,omitempty"`
	SourceTargetID          string             `json:"source_target_id,omitempty"`
	SourceMessageID         *int64             `json:"source_message_id,omitempty"`
	SourceQuote             string             `json:"source_quote,omitempty"`
	UpdateCount             int                `json:"update_count"`
	DeliverableCount        int                `json:"deliverable_count"`
	SubmittedAt             *time.Time         `json:"submitted_at,omitempty"`
	CompletedAt             *time.Time         `json:"completed_at,omitempty"`
	CreatedAt               time.Time          `json:"created_at"`
	UpdatedAt               time.Time          `json:"updated_at"`
}

type TeamTaskCounts struct {
	TeamID               string `json:"team_id"`
	OpenTotal            int    `json:"open_total"`
	AssignedToMe         int    `json:"assigned_to_me"`
	ReviewByMe           int    `json:"review_by_me"`
	ReadyForMyReview     int    `json:"ready_for_my_review"`
	ChangesRequestedToMe int    `json:"changes_requested_to_me"`
	BotRevisingCount     int    `json:"bot_revising_count"`
}

type TeamTaskUpdate struct {
	ID             int64          `json:"id"`
	TaskID         string         `json:"task_id"`
	ActorID        int64          `json:"actor_id"`
	ActorKind      string         `json:"actor_kind"` // "user" | "bot"
	ActorBotName   string         `json:"actor_bot_name,omitempty"`
	ActorEmail     string         `json:"actor_email"`
	ActorNickname  string         `json:"actor_nickname,omitempty"`
	ActorAvatarURL string         `json:"actor_avatar_url,omitempty"`
	ActorRemark    string         `json:"actor_remark,omitempty"`
	Kind           string         `json:"kind"` // comment | submission | approve | request_changes | status_change | bot_review_pass | bot_reject | bot_escalate
	BatchID        string         `json:"batch_id,omitempty"`
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
	Status    string // "" | "open" | "all" | "review_queue" | specific status
	Scope     string // "" | "all" | "mine" | "review" | "created"
}

type CreateTeamTaskInput struct {
	ChannelID            string   `json:"channel_id"`
	Title                string   `json:"title"`
	Description          string   `json:"description"`
	AcceptanceCriteria   []string `json:"acceptance_criteria"`
	Priority             string   `json:"priority"`
	AssigneeID           *int64   `json:"assignee_id"`
	ReviewerID           *int64   `json:"reviewer_id"`
	CollaboratorIDs      []int64  `json:"collaborator_ids"`
	DueAt                *string  `json:"due_at"`
	AutoReviewEnabled    *bool    `json:"auto_review_enabled,omitempty"`
	MaxAutoRevisions     *int     `json:"max_auto_revisions,omitempty"`
	AssigneeAutoResubmit *bool    `json:"assignee_auto_resubmit,omitempty"`
	ActorKind            string   `json:"actor_kind,omitempty"`
	ActorBotName         string   `json:"actor_bot_name,omitempty"`
	SourceScopeType      string   `json:"source_scope_type"`
	SourceTargetID       string   `json:"source_target_id"`
	SourceMessageID      *int64   `json:"source_message_id"`
	SourceQuote          string   `json:"source_quote"`
	NotifyChannel        bool     `json:"notify_channel"`
}

type UpdateTeamTaskInput struct {
	ChannelID            *string   `json:"channel_id,omitempty"`
	Title                *string   `json:"title,omitempty"`
	Description          *string   `json:"description,omitempty"`
	AcceptanceCriteria   *[]string `json:"acceptance_criteria,omitempty"`
	Status               *string   `json:"status,omitempty"`
	Priority             *string   `json:"priority,omitempty"`
	AssigneeID           *int64    `json:"assignee_id,omitempty"`
	ClearAssignee        bool      `json:"clear_assignee,omitempty"`
	ReviewerID           *int64    `json:"reviewer_id,omitempty"`
	ClearReviewer        bool      `json:"clear_reviewer,omitempty"`
	CollaboratorIDs      *[]int64  `json:"collaborator_ids,omitempty"`
	DueAt                *string   `json:"due_at,omitempty"`
	ClearDueAt           bool      `json:"clear_due_at,omitempty"`
	AutoReviewEnabled    *bool     `json:"auto_review_enabled,omitempty"`
	MaxAutoRevisions     *int      `json:"max_auto_revisions,omitempty"`
	AssigneeAutoResubmit *bool     `json:"assignee_auto_resubmit,omitempty"`
	Note                 string    `json:"note,omitempty"`
}

type SubmitTeamTaskInput struct {
	Body          string `json:"body"`
	BatchID       string `json:"batch_id,omitempty"`
	ActorKind     string `json:"actor_kind,omitempty"`
	ActorBotName  string `json:"actor_bot_name,omitempty"`
	NotifyChannel bool   `json:"notify_channel"`
}

type UploadTaskFileOptions struct {
	Kind          string // "submission" | "comment"
	BatchID       string
	Finalize      bool // when Kind=="submission": true transitions status & triggers auto-review; false stages file under BatchID
	FileName      string
	Mime          string
	Body          string
	ActorKind     string
	ActorBotName  string
	NotifyChannel bool
}

type ReviewTeamTaskInput struct {
	Decision      string `json:"decision"` // "approve" | "request_changes"
	Body          string `json:"body"`
	ActorKind     string `json:"actor_kind,omitempty"`
	ActorBotName  string `json:"actor_bot_name,omitempty"`
	NotifyChannel bool   `json:"notify_channel"`
}

func cleanStringList(raw []string, maxItems, maxRunes int) []string {
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		v = truncateRunes(v, maxRunes)
		out = append(out, v)
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

func normalizeActorKind(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "bot") {
		return "bot"
	}
	return "user"
}

func cleanActorBotName(kind, raw string) string {
	if kind != "bot" {
		return ""
	}
	return truncateRunes(strings.TrimSpace(raw), 120)
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
	t.title, t.description, COALESCE(t.acceptance_criteria, '[]'::jsonb), t.status, t.priority,
	t.creator_id, t.assignee_id, t.reviewer_id,
	t.due_at, t.auto_review_enabled, t.max_auto_revisions, t.auto_revision_count, t.assignee_auto_resubmit,
	COALESCE(t.latest_submission_batch_id::text, ''), COALESCE(t.latest_bot_review_verdict, ''),
	COALESCE(t.latest_bot_review_summary, ''), COALESCE(t.latest_bot_review_json, '{}'::jsonb),
	t.source_scope_type, t.source_target_id, t.source_message_id, t.source_quote,
	(SELECT COUNT(*) FROM donebot_team_task_updates u WHERE u.task_id = t.id AND u.kind <> 'status_change') AS update_count,
	(SELECT COUNT(*) FROM donebot_team_task_updates u WHERE u.task_id = t.id AND u.kind = 'submission' AND u.file_id IS NOT NULL) AS deliverable_count,
	t.submitted_at, t.completed_at, t.created_at, t.updated_at
	FROM donebot_team_tasks t
	LEFT JOIN donebot_team_channels c ON c.id = t.channel_id`

func scanTeamTask(scanner interface{ Scan(...any) error }) (TeamTask, error) {
	var item TeamTask
	var criteriaBytes, botReviewBytes []byte
	var assignee, reviewer, sourceMsg sql.NullInt64
	var dueAt, submittedAt, completedAt sql.NullTime
	err := scanner.Scan(
		&item.ID, &item.TeamID, &item.ChannelID, &item.ChannelName,
		&item.Title, &item.Description, &criteriaBytes, &item.Status, &item.Priority,
		&item.CreatorID, &assignee, &reviewer,
		&dueAt, &item.AutoReviewEnabled, &item.MaxAutoRevisions, &item.AutoRevisionCount, &item.AssigneeAutoResubmit,
		&item.LatestSubmissionBatchID, &item.LatestBotReviewVerdict,
		&item.LatestBotReviewSummary, &botReviewBytes,
		&item.SourceScopeType, &item.SourceTargetID, &sourceMsg, &item.SourceQuote,
		&item.UpdateCount, &item.DeliverableCount,
		&submittedAt, &completedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return TeamTask{}, err
	}
	item.AcceptanceCriteria = []string{}
	if len(criteriaBytes) > 0 {
		var parsed []string
		if json.Unmarshal(criteriaBytes, &parsed) == nil && len(parsed) > 0 {
			item.AcceptanceCriteria = parsed
		}
	}
	if len(botReviewBytes) > 2 {
		var snap BotReviewSnapshot
		if json.Unmarshal(botReviewBytes, &snap) == nil && (snap.Verdict != "" || snap.Summary != "") {
			item.LatestBotReview = &snap
		}
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
		COUNT(*) FILTER (WHERE t.assignee_id = $1 AND t.status IN ('todo', 'in_progress', 'bot_revising', 'changes_requested', 'blocked')) AS assigned_to_me,
		COUNT(*) FILTER (WHERE t.reviewer_id = $1 AND t.status IN ('in_review', 'bot_reviewing', 'ready_for_review')) AS review_by_me,
		COUNT(*) FILTER (WHERE t.reviewer_id = $1 AND t.status = 'ready_for_review') AS ready_for_my_review,
		COUNT(*) FILTER (WHERE t.assignee_id = $1 AND t.status = 'changes_requested') AS changes_requested_to_me,
		COUNT(*) FILTER (WHERE t.status = 'bot_revising') AS bot_revising_count
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
		if err = rows.Scan(
			&item.TeamID, &item.OpenTotal, &item.AssignedToMe, &item.ReviewByMe,
			&item.ReadyForMyReview, &item.ChangesRequestedToMe, &item.BotRevisingCount,
		); err != nil {
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
	if statusFilter != "" && statusFilter != "all" && statusFilter != "open" && statusFilter != "review_queue" && !validTaskStatuses[statusFilter] {
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
				OR ($4 = 'review_queue' AND t.status IN ('in_review', 'bot_reviewing', 'ready_for_review'))
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
		COALESCE(u.actor_kind, 'user'), COALESCE(u.actor_bot_name, ''),
		usr.email, COALESCE(usr.username, ''), COALESCE(ua.url, ''), COALESCE(rm.remark, ''),
		u.kind, COALESCE(u.batch_id::text, ''), u.body,
		u.file_id::text, u.file_name, u.file_mime, u.file_size, u.file_sha256,
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
			&item.ActorKind, &item.ActorBotName,
			&item.ActorEmail, &item.ActorNickname, &item.ActorAvatarURL, &item.ActorRemark,
			&item.Kind, &item.BatchID, &item.Body,
			&fileID, &item.FileName, &item.FileMime, &item.FileSize, &item.FileSha256,
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

func (s *Service) insertTaskUpdateFull(
	ctx context.Context,
	tx queryExecer,
	taskID string,
	actor int64,
	actorKind, actorBotName, kind, batchID, body string,
	meta map[string]any,
) (int64, error) {
	metaJSON := []byte("{}")
	if len(meta) > 0 {
		if b, err := json.Marshal(meta); err == nil {
			metaJSON = b
		}
	}
	var batchArg any
	if batchID != "" && validUUID(batchID) {
		batchArg = batchID
	}
	normKind := normalizeActorKind(actorKind)
	botName := cleanActorBotName(normKind, actorBotName)
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO donebot_team_task_updates(
			task_id, actor_id, actor_kind, actor_bot_name, kind, batch_id, body, meta_json
		) VALUES($1::uuid, $2, $3, $4, $5, $6::uuid, $7, $8::jsonb) RETURNING id`,
		taskID, actor, normKind, botName, kind, batchArg, body, string(metaJSON)).Scan(&id)
	return id, err
}

func (s *Service) insertTaskUpdate(ctx context.Context, tx queryExecer, taskID string, actor int64, kind, body string, meta map[string]any) (int64, error) {
	return s.insertTaskUpdateFull(ctx, tx, taskID, actor, "user", "", kind, "", body, meta)
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
	criteria := cleanStringList(in.AcceptanceCriteria, MaxAcceptanceCriteria, MaxCriterionRunes)
	criteriaJSON, _ := json.Marshal(criteria)

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
	autoReview := true
	if in.AutoReviewEnabled != nil {
		autoReview = *in.AutoReviewEnabled
	}
	maxAutoRev := DefaultMaxAutoRevision
	if in.MaxAutoRevisions != nil {
		if *in.MaxAutoRevisions < 0 || *in.MaxAutoRevisions > 5 {
			return nil, infraerrors.BadRequest("COLLAB_TASK_REVISIONS_INVALID", "自动返工上限需在 0-5 轮之间。")
		}
		maxAutoRev = *in.MaxAutoRevisions
	}
	autoResubmit := true
	if in.AssigneeAutoResubmit != nil {
		autoResubmit = *in.AssigneeAutoResubmit
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
			id, team_id, channel_id, title, description, acceptance_criteria, status, priority,
			creator_id, assignee_id, reviewer_id, due_at,
			auto_review_enabled, max_auto_revisions, assignee_auto_resubmit,
			source_scope_type, source_target_id, source_message_id, source_quote
		) VALUES($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::jsonb, 'todo', $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
		taskID, teamID, channelArg, title, desc, string(criteriaJSON), priority,
		actor, assigneeArg, reviewerArg, dueAt,
		autoReview, maxAutoRev, autoResubmit,
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
	if _, err = s.insertTaskUpdateFull(ctx, tx, taskID, actor, in.ActorKind, in.ActorBotName, "status_change", "", strings.Join(summaryParts, "，"), map[string]any{"event": "created"}); err != nil {
		return nil, err
	}

	if in.AssigneeID != nil && *in.AssigneeID > 0 && *in.AssigneeID != actor {
		if err = s.enqueueTaskBotEvent(ctx, tx, teamID, taskID, *in.AssigneeID, "task_assigned", "", map[string]any{
			"creator_id": actor,
			"title":      title,
		}); err != nil {
			return nil, err
		}
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
	nextCriteria := task.AcceptanceCriteria
	if in.AcceptanceCriteria != nil {
		nextCriteria = cleanStringList(*in.AcceptanceCriteria, MaxAcceptanceCriteria, MaxCriterionRunes)
	}
	criteriaJSON, _ := json.Marshal(nextCriteria)

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

	nextAutoReview := task.AutoReviewEnabled
	if in.AutoReviewEnabled != nil {
		nextAutoReview = *in.AutoReviewEnabled
	}
	nextMaxAutoRev := task.MaxAutoRevisions
	if in.MaxAutoRevisions != nil {
		if *in.MaxAutoRevisions < 0 || *in.MaxAutoRevisions > 5 {
			return nil, infraerrors.BadRequest("COLLAB_TASK_REVISIONS_INVALID", "自动返工上限需在 0-5 轮之间。")
		}
		nextMaxAutoRev = *in.MaxAutoRevisions
	}
	nextAutoResubmit := task.AssigneeAutoResubmit
	if in.AssigneeAutoResubmit != nil {
		nextAutoResubmit = *in.AssigneeAutoResubmit
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
		if nextStatus == "in_review" || nextStatus == "bot_reviewing" {
			nextSubmitted = &now
		}
		if nextStatus == "done" {
			nextCompleted = &now
		} else if task.Status == "done" {
			nextCompleted = nil
		}
	}

	if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_tasks
		SET channel_id = $2::uuid, title = $3, description = $4, acceptance_criteria = $5::jsonb,
		    status = $6, priority = $7,
		    assignee_id = $8, reviewer_id = $9, due_at = $10,
		    auto_review_enabled = $11, max_auto_revisions = $12, assignee_auto_resubmit = $13,
		    submitted_at = $14, completed_at = $15, updated_at = NOW()
		WHERE id = $1::uuid`,
		taskID, channelArg, nextTitle, nextDesc, string(criteriaJSON),
		nextStatus, nextPriority,
		nextAssignee, nextReviewer, nextDue,
		nextAutoReview, nextMaxAutoRev, nextAutoResubmit,
		nextSubmitted, nextCompleted); err != nil {
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

func resolveSubmissionNextStatus(task *TeamTask) (status string, reviewTarget int64) {
	if task.AutoReviewEnabled {
		if task.ReviewerID != nil && *task.ReviewerID > 0 {
			return "bot_reviewing", *task.ReviewerID
		}
		if task.CreatorID > 0 {
			return "bot_reviewing", task.CreatorID
		}
	}
	return "in_review", 0
}

func (s *Service) SubmitTeamTaskForReview(ctx context.Context, actor int64, teamID, taskID string, in SubmitTeamTaskInput) (*TeamTaskDetail, error) {
	task, _, err := s.authorizeTeamTask(ctx, actor, teamID, taskID)
	if err != nil {
		return nil, err
	}
	body, err := cleanOptionalText(in.Body, MaxChatBodyRunes, "COLLAB_TASK_BODY_INVALID", "交付说明不能超过 8000 个字符。")
	if err != nil {
		return nil, err
	}
	if body == "" {
		body = "已提交交付成果，请验收。"
	}
	batchID := strings.TrimSpace(in.BatchID)
	if batchID == "" || !validUUID(batchID) {
		batchID = uuid.NewString()
	}

	nextStatus, reviewTarget := resolveSubmissionNextStatus(task)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err = s.cancelPendingTaskBotEvents(ctx, tx, taskID); err != nil {
		return nil, err
	}
	if _, err = s.insertTaskUpdateFull(ctx, tx, taskID, actor, in.ActorKind, in.ActorBotName, "submission", batchID, body, map[string]any{
		"from_status": task.Status,
		"to_status":   nextStatus,
		"batch_id":    batchID,
	}); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_tasks
		SET status = $2, latest_submission_batch_id = $3::uuid, submitted_at = NOW(), updated_at = NOW()
		WHERE id = $1::uuid`, taskID, nextStatus, batchID); err != nil {
		return nil, err
	}
	if nextStatus == "bot_reviewing" && reviewTarget > 0 {
		if err = s.enqueueTaskBotEvent(ctx, tx, teamID, taskID, reviewTarget, "task_submitted", batchID, map[string]any{
			"submitted_by":   actor,
			"actor_kind":     normalizeActorKind(in.ActorKind),
			"actor_bot_name": cleanActorBotName(normalizeActorKind(in.ActorKind), in.ActorBotName),
		}); err != nil {
			return nil, err
		}
	}
	if in.NotifyChannel {
		prefix := "📤 提交验收"
		if normalizeActorKind(in.ActorKind) == "bot" {
			prefix = "🤖 助手提交验收"
		}
		notice := fmt.Sprintf("%s「%s」", prefix, task.Title)
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
	teamID, taskID string,
	opts UploadTaskFileOptions,
	reader io.Reader,
	contentLength int64,
) (*TeamTaskDetail, error) {
	task, _, err := s.authorizeTeamTask(ctx, actor, teamID, taskID)
	if err != nil {
		return nil, err
	}
	if opts.Kind != "comment" && opts.Kind != "submission" {
		return nil, infraerrors.BadRequest("COLLAB_TASK_UPDATE_KIND_INVALID", "不支持的附件上传类型。")
	}
	if contentLength > MaxChatFileBytes {
		return nil, infraerrors.BadRequest("COLLAB_FILE_TOO_LARGE", "上传的文件不能超过 100 MB。")
	}
	caption, err := cleanChatText(opts.Body, MaxChatCaptionRunes, true)
	if err != nil {
		return nil, err
	}
	fileName := cleanChatFileName(opts.FileName)
	fileMime := normalizeChatMime(opts.Mime, fileName)

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

	normActorKind := normalizeActorKind(opts.ActorKind)
	actorBotName := cleanActorBotName(normActorKind, opts.ActorBotName)
	batchID := strings.TrimSpace(opts.BatchID)
	if opts.Kind == "submission" && (batchID == "" || !validUUID(batchID)) {
		batchID = uuid.NewString()
	}
	var batchArg any
	if batchID != "" && validUUID(batchID) {
		batchArg = batchID
	}

	nextStatus, reviewTarget := resolveSubmissionNextStatus(task)
	metaJSON := "{}"
	if opts.Kind == "submission" {
		metaMap := map[string]any{"batch_id": batchID, "staged": !opts.Finalize}
		if opts.Finalize {
			metaMap["from_status"] = task.Status
			metaMap["to_status"] = nextStatus
		}
		if b, mErr := json.Marshal(metaMap); mErr == nil {
			metaJSON = string(b)
		}
	}

	if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_team_task_updates(
			task_id, actor_id, actor_kind, actor_bot_name, kind, batch_id, body,
			file_id, file_name, file_mime, file_size, file_sha256, meta_json
		) VALUES($1::uuid, $2, $3, $4, $5, $6::uuid, $7, $8::uuid, $9, $10, $11, $12, $13::jsonb)`,
		taskID, actor, normActorKind, actorBotName, opts.Kind, batchArg, caption,
		fileID, fileName, fileMime, written, shaHex, metaJSON); err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}

	if opts.Kind == "submission" && opts.Finalize {
		if err = s.cancelPendingTaskBotEvents(ctx, tx, taskID); err != nil {
			_ = os.Remove(finalPath)
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_tasks
			SET status = $2, latest_submission_batch_id = $3::uuid, submitted_at = NOW(), updated_at = NOW()
			WHERE id = $1::uuid`, taskID, nextStatus, batchID); err != nil {
			_ = os.Remove(finalPath)
			return nil, err
		}
		if nextStatus == "bot_reviewing" && reviewTarget > 0 {
			if err = s.enqueueTaskBotEvent(ctx, tx, teamID, taskID, reviewTarget, "task_submitted", batchID, map[string]any{
				"submitted_by":   actor,
				"actor_kind":     normActorKind,
				"actor_bot_name": actorBotName,
			}); err != nil {
				_ = os.Remove(finalPath)
				return nil, err
			}
		}
		if opts.NotifyChannel {
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

func (s *Service) ReviewTeamTask(ctx context.Context, actor int64, teamID, taskID string, in ReviewTeamTaskInput) (*TeamTaskDetail, error) {
	task, role, err := s.authorizeTeamTask(ctx, actor, teamID, taskID)
	if err != nil {
		return nil, err
	}
	decision := strings.TrimSpace(in.Decision)
	if decision != "approve" && decision != "request_changes" {
		return nil, infraerrors.BadRequest("COLLAB_TASK_REVIEW_INVALID", "验收决定仅支持通过或退回修改。")
	}
	if task.ReviewerID != nil && *task.ReviewerID != actor && role != "owner" && role != "admin" {
		return nil, infraerrors.Forbidden("COLLAB_TASK_REVIEW_FORBIDDEN", "该任务指定了其他验收人，仅指定验收人或团队管理员可验收。")
	}

	body, err := cleanOptionalText(in.Body, MaxChatBodyRunes, "COLLAB_TASK_REVIEW_BODY_INVALID", "验收意见不能超过 8000 个字符。")
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

	if err = s.cancelPendingTaskBotEvents(ctx, tx, taskID); err != nil {
		return nil, err
	}

	displayBody := body
	if displayBody == "" && decision == "approve" {
		displayBody = "验收通过，已定稿。"
	}
	meta := map[string]any{
		"from_status": task.Status,
		"to_status":   nextStatus,
	}
	if strings.TrimSpace(in.ActorBotName) != "" {
		meta["via_bot_name"] = truncateRunes(in.ActorBotName, 120)
	}
	if _, err = s.insertTaskUpdateFull(ctx, tx, taskID, actor, "user", "", updateKind, task.LatestSubmissionBatchID, displayBody, meta); err != nil {
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
		if task.AssigneeID != nil && *task.AssigneeID > 0 {
			if err = s.enqueueTaskBotEvent(ctx, tx, teamID, taskID, *task.AssigneeID, "task_human_rejected", task.LatestSubmissionBatchID, map[string]any{
				"reviewer_id": actor,
				"feedback":    body,
			}); err != nil {
				return nil, err
			}
		}
	}

	if in.NotifyChannel {
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

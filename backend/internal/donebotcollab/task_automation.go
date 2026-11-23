package donebotcollab

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

type BotReviewTeamTaskInput struct {
	Verdict         string   `json:"verdict"` // "pass" | "hard_fail" | "escalate"
	Summary         string   `json:"summary"`
	PassedItems     []string `json:"passed_items"`
	AttentionItems  []string `json:"attention_items"`
	FailedCriteria  []string `json:"failed_criteria"`
	ActionableFixes []string `json:"actionable_fixes"`
	BotName         string   `json:"bot_name"`
	BatchID         string   `json:"batch_id,omitempty"`
	NotifyChannel   bool     `json:"notify_channel"`
}

type ClaimedTaskBotEvent struct {
	ID           string          `json:"id"`
	TeamID       string          `json:"team_id"`
	TeamName     string          `json:"team_name"`
	TaskID       string          `json:"task_id"`
	EventType    string          `json:"event_type"`
	BatchID      string          `json:"batch_id,omitempty"`
	Payload      map[string]any  `json:"payload,omitempty"`
	AssigneeName string          `json:"assignee_name,omitempty"`
	ReviewerName string          `json:"reviewer_name,omitempty"`
	CreatorName  string          `json:"creator_name,omitempty"`
	Detail       *TeamTaskDetail `json:"detail"`
	CreatedAt    time.Time       `json:"created_at"`
}

func (s *Service) cancelPendingTaskBotEvents(ctx context.Context, tx queryExecer, taskID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE donebot_team_task_bot_events
		SET status = 'cancelled', completed_at = NOW()
		WHERE task_id = $1::uuid AND status = 'pending'`, taskID)
	return err
}

func (s *Service) enqueueTaskBotEvent(
	ctx context.Context,
	tx queryExecer,
	teamID, taskID string,
	targetUserID int64,
	eventType, batchID string,
	payload map[string]any,
) error {
	if targetUserID <= 0 {
		return nil
	}
	payloadBytes := []byte("{}")
	if len(payload) > 0 {
		if b, err := json.Marshal(payload); err == nil {
			payloadBytes = b
		}
	}
	var batchArg any
	if batchID != "" && validUUID(batchID) {
		batchArg = batchID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO donebot_team_task_bot_events(
			id, team_id, task_id, target_user_id, event_type, batch_id, payload_json, status
		) VALUES($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::uuid, $7::jsonb, 'pending')`,
		uuid.NewString(), teamID, taskID, targetUserID, eventType, batchArg, string(payloadBytes))
	return err
}

// BotReviewTeamTask records the assigner Chief Bot's automated first-pass review.
// "pass" advances the task to ready_for_review for human final sign-off.
// "hard_fail" auto-rejects to the assignee's bot up to MaxAutoRevisions rounds, then auto-escalates to humans.
func (s *Service) BotReviewTeamTask(ctx context.Context, actor int64, teamID, taskID string, in BotReviewTeamTaskInput) (*TeamTaskDetail, error) {
	task, role, err := s.authorizeTeamTask(ctx, actor, teamID, taskID)
	if err != nil {
		return nil, err
	}
	if task.ReviewerID != nil && *task.ReviewerID != actor && role != "owner" && role != "admin" {
		return nil, infraerrors.Forbidden("COLLAB_TASK_REVIEW_FORBIDDEN", "仅指定验收人或团队管理员的主协调助手可执行初审。")
	}
	if task.Status == "done" {
		return nil, infraerrors.BadRequest("COLLAB_TASK_ALREADY_DONE", "该任务已定稿完成，无需再初审。")
	}

	verdict := strings.TrimSpace(in.Verdict)
	if verdict != "pass" && verdict != "hard_fail" && verdict != "escalate" {
		return nil, infraerrors.BadRequest("COLLAB_TASK_BOT_VERDICT_INVALID", "初审结论仅支持 pass、hard_fail 或 escalate。")
	}
	summary, err := cleanOptionalText(in.Summary, MaxChatBodyRunes, "COLLAB_TASK_REVIEW_BODY_INVALID", "初审意见过长。")
	if err != nil {
		return nil, err
	}
	passedItems := cleanStringList(in.PassedItems, 20, 240)
	attentionItems := cleanStringList(in.AttentionItems, 20, 240)
	failedCriteria := cleanStringList(in.FailedCriteria, 20, 240)
	actionableFixes := cleanStringList(in.ActionableFixes, 20, 300)
	botName := truncateRunes(strings.TrimSpace(in.BotName), 120)
	if botName == "" {
		botName = "主协调助手"
	}
	batchID := strings.TrimSpace(in.BatchID)
	if batchID == "" || !validUUID(batchID) {
		batchID = task.LatestSubmissionBatchID
	}
	if (verdict == "hard_fail" || verdict == "escalate") && summary == "" && len(failedCriteria) == 0 && len(actionableFixes) == 0 {
		return nil, infraerrors.BadRequest("COLLAB_TASK_REVIEW_REASON_REQUIRED", "初审驳回或升级时必须说明具体不符合项与修改要求。")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err = s.cancelPendingTaskBotEvents(ctx, tx, taskID); err != nil {
		return nil, err
	}

	nowISO := time.Now().UTC().Format(time.RFC3339)

	switch verdict {
	case "pass":
		if summary == "" {
			summary = "初审已通过硬性核验，请验收人做最终拍板。"
		}
		snap := BotReviewSnapshot{
			Verdict:        "pass",
			Summary:        summary,
			BotName:        botName,
			BatchID:        batchID,
			PassedItems:    passedItems,
			AttentionItems: attentionItems,
			Round:          task.AutoRevisionCount,
			ReviewedAt:     nowISO,
		}
		snapJSON, _ := json.Marshal(snap)
		if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_tasks
			SET status = 'ready_for_review',
			    latest_bot_review_verdict = 'pass',
			    latest_bot_review_summary = $2,
			    latest_bot_review_json = $3::jsonb,
			    updated_at = NOW()
			WHERE id = $1::uuid`, taskID, summary, string(snapJSON)); err != nil {
			return nil, err
		}
		if _, err = s.insertTaskUpdateFull(ctx, tx, taskID, actor, "bot", botName, "bot_review_pass", batchID, summary, map[string]any{
			"verdict":         "pass",
			"passed_items":    passedItems,
			"attention_items": attentionItems,
			"round":           task.AutoRevisionCount,
		}); err != nil {
			return nil, err
		}

	case "hard_fail":
		canAutoRevise := task.MaxAutoRevisions > 0 && task.AutoRevisionCount < task.MaxAutoRevisions && task.AssigneeID != nil && *task.AssigneeID > 0
		if canAutoRevise {
			nextRound := task.AutoRevisionCount + 1
			if summary == "" {
				if len(failedCriteria) > 0 {
					summary = "未满足硬性要求：" + strings.Join(failedCriteria, "；")
				} else {
					summary = "交付物与硬性要求明显不符，请按要求修改后重提。"
				}
			}
			snap := BotReviewSnapshot{
				Verdict:         "hard_fail",
				Summary:         summary,
				BotName:         botName,
				BatchID:         batchID,
				PassedItems:     passedItems,
				FailedCriteria:  failedCriteria,
				ActionableFixes: actionableFixes,
				Round:           nextRound,
				ReviewedAt:      nowISO,
			}
			snapJSON, _ := json.Marshal(snap)
			if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_tasks
				SET status = 'bot_revising',
				    auto_revision_count = $2,
				    latest_bot_review_verdict = 'hard_fail',
				    latest_bot_review_summary = $3,
				    latest_bot_review_json = $4::jsonb,
				    updated_at = NOW()
				WHERE id = $1::uuid`, taskID, nextRound, summary, string(snapJSON)); err != nil {
				return nil, err
			}
			if _, err = s.insertTaskUpdateFull(ctx, tx, taskID, actor, "bot", botName, "bot_reject", batchID, summary, map[string]any{
				"verdict":          "hard_fail",
				"round":            nextRound,
				"max_rounds":       task.MaxAutoRevisions,
				"failed_criteria":  failedCriteria,
				"actionable_fixes": actionableFixes,
			}); err != nil {
				return nil, err
			}
			if err = s.enqueueTaskBotEvent(ctx, tx, teamID, taskID, *task.AssigneeID, "task_bot_rejected", batchID, map[string]any{
				"round":             nextRound,
				"max_rounds":        task.MaxAutoRevisions,
				"reviewer_bot_name": botName,
				"summary":           summary,
				"failed_criteria":   failedCriteria,
				"actionable_fixes":  actionableFixes,
				"auto_resubmit":     task.AssigneeAutoResubmit,
			}); err != nil {
				return nil, err
			}
			if in.NotifyChannel {
				assigneeName := s.lookupPersonName(ctx, tx, *task.AssigneeID)
				notice := fmt.Sprintf("🤖 [初审驳回 %d/%d] 「%s」已由 %s 驳回给 @%s 的助手修改：%s",
					nextRound, task.MaxAutoRevisions, task.Title, botName, assigneeName, truncateRunes(summary, 140))
				if err = s.postTaskChannelNotice(ctx, tx, actor, teamID, task.ChannelID, notice); err != nil {
					return nil, err
				}
			}
		} else {
			// Escalate automatically once auto-revision limit is reached or disabled.
			escalateReason := summary
			if escalateReason == "" {
				escalateReason = "未满足硬性要求，已达自动返工上限，请双方人工介入处理。"
			} else if task.MaxAutoRevisions > 0 && task.AutoRevisionCount >= task.MaxAutoRevisions {
				escalateReason = fmt.Sprintf("已达自动返工上限（%d/%d 轮），转为人工介入：%s", task.AutoRevisionCount, task.MaxAutoRevisions, escalateReason)
			}
			snap := BotReviewSnapshot{
				Verdict:         "escalated",
				Summary:         escalateReason,
				BotName:         botName,
				BatchID:         batchID,
				PassedItems:     passedItems,
				FailedCriteria:  failedCriteria,
				ActionableFixes: actionableFixes,
				Round:           task.AutoRevisionCount,
				ReviewedAt:      nowISO,
			}
			snapJSON, _ := json.Marshal(snap)
			if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_tasks
				SET status = 'changes_requested',
				    latest_bot_review_verdict = 'escalated',
				    latest_bot_review_summary = $2,
				    latest_bot_review_json = $3::jsonb,
				    updated_at = NOW()
				WHERE id = $1::uuid`, taskID, escalateReason, string(snapJSON)); err != nil {
				return nil, err
			}
			if _, err = s.insertTaskUpdateFull(ctx, tx, taskID, actor, "bot", botName, "bot_escalate", batchID, escalateReason, map[string]any{
				"verdict":          "escalated",
				"round":            task.AutoRevisionCount,
				"max_rounds":       task.MaxAutoRevisions,
				"failed_criteria":  failedCriteria,
				"actionable_fixes": actionableFixes,
			}); err != nil {
				return nil, err
			}
			if in.NotifyChannel {
				assigneeHint := ""
				if task.AssigneeID != nil {
					assigneeHint = " · @" + s.lookupPersonName(ctx, tx, *task.AssigneeID)
				}
				notice := fmt.Sprintf("⚠️ [需人工介入] 「%s」%s：%s", task.Title, assigneeHint, truncateRunes(escalateReason, 150))
				if err = s.postTaskChannelNotice(ctx, tx, actor, teamID, task.ChannelID, notice); err != nil {
					return nil, err
				}
			}
		}

	case "escalate":
		if summary == "" {
			summary = "存在需双方真人确认的分歧或需求缺失，已转为人工处理。"
		}
		snap := BotReviewSnapshot{
			Verdict:         "escalated",
			Summary:         summary,
			BotName:         botName,
			BatchID:         batchID,
			PassedItems:     passedItems,
			AttentionItems:  attentionItems,
			FailedCriteria:  failedCriteria,
			ActionableFixes: actionableFixes,
			Round:           task.AutoRevisionCount,
			ReviewedAt:      nowISO,
		}
		snapJSON, _ := json.Marshal(snap)
		if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_tasks
			SET status = 'changes_requested',
			    latest_bot_review_verdict = 'escalated',
			    latest_bot_review_summary = $2,
			    latest_bot_review_json = $3::jsonb,
			    updated_at = NOW()
			WHERE id = $1::uuid`, taskID, summary, string(snapJSON)); err != nil {
			return nil, err
		}
		if _, err = s.insertTaskUpdateFull(ctx, tx, taskID, actor, "bot", botName, "bot_escalate", batchID, summary, map[string]any{
			"verdict":          "escalated",
			"attention_items":  attentionItems,
			"failed_criteria":  failedCriteria,
			"actionable_fixes": actionableFixes,
		}); err != nil {
			return nil, err
		}
		if in.NotifyChannel {
			notice := fmt.Sprintf("⚠️ [需人工介入] 「%s」：%s", task.Title, truncateRunes(summary, 150))
			if err = s.postTaskChannelNotice(ctx, tx, actor, teamID, task.ChannelID, notice); err != nil {
				return nil, err
			}
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetTeamTaskDetail(ctx, actor, teamID, taskID)
}

func isTaskEventStillValid(eventType string, eventBatchID string, task *TeamTask) bool {
	if task == nil || task.Status == "done" {
		return false
	}
	switch eventType {
	case "task_assigned":
		return task.Status == "todo"
	case "task_submitted":
		if task.Status != "bot_reviewing" && task.Status != "in_review" {
			return false
		}
		if eventBatchID != "" && task.LatestSubmissionBatchID != "" && eventBatchID != task.LatestSubmissionBatchID {
			return false
		}
		return true
	case "task_bot_rejected":
		return task.Status == "bot_revising"
	case "task_human_rejected":
		return task.Status == "changes_requested"
	default:
		return false
	}
}

// ClaimTaskBotEvent atomically claims the next pending team-task automation event for actor's local computer.
func (s *Service) ClaimTaskBotEvent(ctx context.Context, actor int64) (*ClaimedTaskBotEvent, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}

	// Reclaim abandoned events whose local worker crashed or went offline.
	_, _ = s.db.ExecContext(ctx, `UPDATE donebot_team_task_bot_events
		SET status = 'pending', claimed_at = NULL
		WHERE target_user_id = $1 AND status = 'claimed' AND claimed_at < NOW() - INTERVAL '5 minutes'`, actor)

	for attempt := 0; attempt < 10; attempt++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}

		var evID, teamID, taskID, eventType string
		var batchID sql.NullString
		var payloadBytes []byte
		var createdAt time.Time
		err = tx.QueryRowContext(ctx, `SELECT e.id::text, e.team_id::text, e.task_id::text, e.event_type,
			e.batch_id::text, e.payload_json, e.created_at
			FROM donebot_team_task_bot_events e
			JOIN donebot_teams t ON t.id = e.team_id AND t.state = 'active'
			WHERE e.target_user_id = $1 AND e.status = 'pending'
			ORDER BY e.created_at ASC, e.id ASC
			LIMIT 1
			FOR UPDATE OF e SKIP LOCKED`, actor).Scan(
			&evID, &teamID, &taskID, &eventType, &batchID, &payloadBytes, &createdAt,
		)
		if errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			return nil, nil
		}
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}

		if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_task_bot_events SET status = 'claimed', claimed_at = NOW() WHERE id = $1::uuid`, evID); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}

		detail, detailErr := s.GetTeamTaskDetail(ctx, actor, teamID, taskID)
		batchStr := ""
		if batchID.Valid {
			batchStr = batchID.String
		}
		if detailErr != nil || !isTaskEventStillValid(eventType, batchStr, &detail.Task) {
			_, _ = s.db.ExecContext(ctx, `UPDATE donebot_team_task_bot_events SET status = 'cancelled', completed_at = NOW() WHERE id = $1::uuid`, evID)
			continue
		}

		var teamName string
		_ = s.db.QueryRowContext(ctx, `SELECT name FROM donebot_teams WHERE id = $1::uuid`, teamID).Scan(&teamName)

		var payload map[string]any
		if len(payloadBytes) > 0 {
			_ = json.Unmarshal(payloadBytes, &payload)
		}

		out := &ClaimedTaskBotEvent{
			ID:          evID,
			TeamID:      teamID,
			TeamName:    teamName,
			TaskID:      taskID,
			EventType:   eventType,
			BatchID:     batchStr,
			Payload:     payload,
			CreatorName: s.lookupPersonName(ctx, s.db, detail.Task.CreatorID),
			Detail:      detail,
			CreatedAt:   createdAt,
		}
		if detail.Task.AssigneeID != nil {
			out.AssigneeName = s.lookupPersonName(ctx, s.db, *detail.Task.AssigneeID)
		}
		if detail.Task.ReviewerID != nil {
			out.ReviewerName = s.lookupPersonName(ctx, s.db, *detail.Task.ReviewerID)
		}
		return out, nil
	}
	return nil, nil
}

func (s *Service) CompleteTaskBotEvent(ctx context.Context, actor int64, eventID, status, errText string) error {
	if err := s.available(); err != nil {
		return err
	}
	if actor <= 0 {
		return infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	if !validUUID(eventID) {
		return infraerrors.NotFound("COLLAB_TASK_EVENT_NOT_FOUND", "任务自动化事件不存在。")
	}
	normStatus := strings.TrimSpace(status)
	if normStatus != "done" && normStatus != "failed" {
		normStatus = "done"
	}
	cleanErr := truncateRunes(strings.TrimSpace(errText), 500)
	res, err := s.db.ExecContext(ctx, `UPDATE donebot_team_task_bot_events
		SET status = $3, error_text = $4, completed_at = NOW()
		WHERE id = $1::uuid AND target_user_id = $2`, eventID, actor, normStatus, cleanErr)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return infraerrors.NotFound("COLLAB_TASK_EVENT_NOT_FOUND", "任务自动化事件不存在。")
	}
	return nil
}

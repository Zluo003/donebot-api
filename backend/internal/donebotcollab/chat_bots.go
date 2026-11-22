package donebotcollab

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	MaxBotsPerChat    = 20
	MaxMentionsPerMsg = 3
	MaxSyncChatBots   = 50
)

var (
	botMascotBodyRe       = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)
	botMascotExpressionRe = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
)

func sanitizeBotMascotBody(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if botMascotBodyRe.MatchString(v) {
		return v
	}
	return ""
}

func sanitizeBotMascotFaceColor(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "white", "black":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func sanitizeBotMascotExpression(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if botMascotExpressionRe.MatchString(v) {
		return v
	}
	return ""
}

func sanitizeBotAvatarCrop(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "mascot", "circle", "rounded", "square":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func clampBotAvatarZoom(v *float64) float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return 1
	}
	clamped := math.Max(1, math.Min(3, *v))
	return math.Round(clamped*100) / 100
}

func clampBotAvatarFocus(v *float64) float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return 0.5
	}
	clamped := math.Max(0, math.Min(1, *v))
	return math.Round(clamped*1000) / 1000
}

type ChatBot struct {
	ID                  string    `json:"id"`
	ScopeType           string    `json:"scope_type"` // "team" | "group"
	TargetID            string    `json:"target_id"`
	OwnerUserID         int64     `json:"owner_user_id"`
	OwnerEmail          string    `json:"owner_email"`
	OwnerNickname       string    `json:"owner_nickname,omitempty"`
	OwnerAvatarURL      string    `json:"owner_avatar_url,omitempty"`
	OwnerRemark         string    `json:"owner_remark,omitempty"`
	InstallationID      string    `json:"installation_id"`
	LocalBotID          string    `json:"local_bot_id"`
	BotName             string    `json:"bot_name"`
	BotColor            string    `json:"bot_color"`
	BotAvatarURL        string    `json:"bot_avatar_url,omitempty"`
	BotMascotBody       string    `json:"bot_mascot_body,omitempty"`
	BotMascotFaceColor  string    `json:"bot_mascot_face_color,omitempty"`
	BotMascotExpression string    `json:"bot_mascot_expression,omitempty"`
	BotAvatarCrop       string    `json:"bot_avatar_crop,omitempty"`
	BotAvatarZoom       float64   `json:"bot_avatar_zoom"`
	BotAvatarFocusX     float64   `json:"bot_avatar_focus_x"`
	BotAvatarFocusY     float64   `json:"bot_avatar_focus_y"`
	BotTitle            string    `json:"bot_title,omitempty"`
	AllowAllMembers     bool      `json:"allow_all_members"`
	AllowedUserIDs      []int64   `json:"allowed_user_ids"`
	CanInvoke           bool      `json:"can_invoke"`
	CreatedAt           time.Time `json:"created_at"`
}

type AddChatBotInput struct {
	InstallationID      string   `json:"installation_id"`
	LocalBotID          string   `json:"local_bot_id"`
	BotName             string   `json:"bot_name"`
	BotColor            string   `json:"bot_color"`
	BotAvatarURL        string   `json:"bot_avatar_url"`
	BotMascotBody       string   `json:"bot_mascot_body"`
	BotMascotFaceColor  string   `json:"bot_mascot_face_color"`
	BotMascotExpression string   `json:"bot_mascot_expression"`
	BotAvatarCrop       string   `json:"bot_avatar_crop"`
	BotAvatarZoom       *float64 `json:"bot_avatar_zoom"`
	BotAvatarFocusX     *float64 `json:"bot_avatar_focus_x"`
	BotAvatarFocusY     *float64 `json:"bot_avatar_focus_y"`
	BotTitle            string   `json:"bot_title"`
	AllowAllMembers     bool     `json:"allow_all_members"`
	AllowedUserIDs      []int64  `json:"allowed_user_ids"`
}

type SyncChatBotProfileItem struct {
	LocalBotID          string   `json:"local_bot_id"`
	BotName             string   `json:"bot_name"`
	BotColor            string   `json:"bot_color"`
	BotAvatarURL        string   `json:"bot_avatar_url"`
	BotMascotBody       string   `json:"bot_mascot_body"`
	BotMascotFaceColor  string   `json:"bot_mascot_face_color"`
	BotMascotExpression string   `json:"bot_mascot_expression"`
	BotAvatarCrop       string   `json:"bot_avatar_crop"`
	BotAvatarZoom       *float64 `json:"bot_avatar_zoom"`
	BotAvatarFocusX     *float64 `json:"bot_avatar_focus_x"`
	BotAvatarFocusY     *float64 `json:"bot_avatar_focus_y"`
	BotTitle            string   `json:"bot_title"`
}

type SyncChatBotsInput struct {
	InstallationID string                   `json:"installation_id"`
	Bots           []SyncChatBotProfileItem `json:"bots"`
}

type UpdateChatBotGrantsInput struct {
	AllowAllMembers bool    `json:"allow_all_members"`
	AllowedUserIDs  []int64 `json:"allowed_user_ids"`
}

type ClaimedBotInvocation struct {
	ID             string        `json:"id"`
	ChatBotID      string        `json:"chat_bot_id"`
	MessageID      int64         `json:"message_id"`
	ScopeType      string        `json:"scope_type"`
	TargetID       string        `json:"target_id"`
	ChatName       string        `json:"chat_name"`
	LocalBotID     string        `json:"local_bot_id"`
	BotName        string        `json:"bot_name"`
	RecentMessages []ChatMessage `json:"recent_messages"`
}

func (s *Service) authorizeBotChatScope(ctx context.Context, actor int64, scopeType, targetID string) (string, string, error) {
	normType, scopeKey, err := s.authorizeChatScope(ctx, actor, scopeType, targetID)
	if err != nil {
		return "", "", err
	}
	if normType != "team" && normType != "group" {
		return "", "", infraerrors.BadRequest("COLLAB_CHAT_BOT_SCOPE_INVALID", "仅团队聊天和好友群聊支持添加机器人。")
	}
	return normType, scopeKey, nil
}

func (s *Service) replaceChatBotGrants(ctx context.Context, tx queryExecer, chatBotID, scopeType, scopeKey string, ownerID int64, allowedIDs []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM donebot_collab_chat_bot_grants WHERE chat_bot_id = $1::uuid`, chatBotID); err != nil {
		return err
	}
	seen := map[int64]struct{}{ownerID: {}}
	for _, uid := range allowedIDs {
		if uid <= 0 {
			continue
		}
		if _, dup := seen[uid]; dup {
			continue
		}
		seen[uid] = struct{}{}
		var isMember bool
		var err error
		if scopeType == "team" {
			isMember, err = isActiveTeamMember(ctx, tx, uid, scopeKey)
		} else {
			isMember, err = isGroupMember(ctx, tx, uid, scopeKey)
		}
		if err != nil {
			return err
		}
		if !isMember {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_collab_chat_bot_grants(chat_bot_id, user_id) VALUES($1::uuid, $2) ON CONFLICT DO NOTHING`, chatBotID, uid); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ListChatBots(ctx context.Context, actor int64, scopeType, targetID string) ([]ChatBot, error) {
	normType, scopeKey, err := s.authorizeBotChatScope(ctx, actor, scopeType, targetID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT b.id::text, b.scope_type, b.scope_key, b.owner_user_id,
		u.email, COALESCE(u.username, ''), COALESCE(ua.url, ''), COALESCE(rm.remark, ''),
		b.installation_id::text, b.local_bot_id, b.bot_name, b.bot_color, COALESCE(b.bot_avatar_url, ''),
		COALESCE(b.bot_mascot_body, ''), COALESCE(b.bot_mascot_face_color, ''), COALESCE(b.bot_mascot_expression, ''),
		COALESCE(b.bot_avatar_crop, ''), b.bot_avatar_zoom, b.bot_avatar_focus_x, b.bot_avatar_focus_y,
		COALESCE(b.bot_title, ''),
		b.allow_all_members, b.created_at
		FROM donebot_collab_chat_bots b
		JOIN users u ON u.id = b.owner_user_id AND u.deleted_at IS NULL AND u.status = 'active'
		LEFT JOIN user_avatars ua ON ua.user_id = u.id
		LEFT JOIN donebot_friend_remarks rm ON rm.owner_id = $1 AND rm.friend_id = u.id
		WHERE b.scope_type = $2 AND b.scope_key = $3
		ORDER BY b.created_at ASC, b.id ASC`, actor, normType, scopeKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]ChatBot, 0)
	byIdx := map[string]int{}
	for rows.Next() {
		var item ChatBot
		if err = rows.Scan(
			&item.ID, &item.ScopeType, &item.TargetID, &item.OwnerUserID,
			&item.OwnerEmail, &item.OwnerNickname, &item.OwnerAvatarURL, &item.OwnerRemark,
			&item.InstallationID, &item.LocalBotID, &item.BotName, &item.BotColor, &item.BotAvatarURL,
			&item.BotMascotBody, &item.BotMascotFaceColor, &item.BotMascotExpression,
			&item.BotAvatarCrop, &item.BotAvatarZoom, &item.BotAvatarFocusX, &item.BotAvatarFocusY,
			&item.BotTitle,
			&item.AllowAllMembers, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		item.AllowedUserIDs = []int64{}
		item.CanInvoke = item.OwnerUserID == actor || item.AllowAllMembers
		byIdx[item.ID] = len(out)
		out = append(out, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	gRows, err := s.db.QueryContext(ctx, `SELECT g.chat_bot_id::text, g.user_id
		FROM donebot_collab_chat_bot_grants g
		JOIN donebot_collab_chat_bots b ON b.id = g.chat_bot_id
		WHERE b.scope_type = $1 AND b.scope_key = $2
		ORDER BY g.user_id ASC`, normType, scopeKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gRows.Close() }()
	for gRows.Next() {
		var botID string
		var uid int64
		if err = gRows.Scan(&botID, &uid); err != nil {
			return nil, err
		}
		if idx, ok := byIdx[botID]; ok {
			out[idx].AllowedUserIDs = append(out[idx].AllowedUserIDs, uid)
			if uid == actor {
				out[idx].CanInvoke = true
			}
		}
	}
	return out, gRows.Err()
}

func (s *Service) HasOwnedChatBots(ctx context.Context, actor int64) (bool, error) {
	if !s.Enabled() || actor <= 0 {
		return false, nil
	}
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donebot_collab_chat_bots WHERE owner_user_id = $1)`, actor).Scan(&exists)
	return exists, err
}

func (s *Service) AddChatBot(ctx context.Context, actor int64, scopeType, targetID string, in AddChatBotInput) (*ChatBot, error) {
	normType, scopeKey, err := s.authorizeBotChatScope(ctx, actor, scopeType, targetID)
	if err != nil {
		return nil, err
	}
	if !validUUID(in.InstallationID) {
		return nil, infraerrors.BadRequest("COLLAB_INSTALL_INVALID", "无效的客户端安装标识。")
	}
	localBotID, err := cleanText(in.LocalBotID, 128, "COLLAB_BOT_ID_INVALID", "无效的机器人标识。")
	if err != nil {
		return nil, err
	}
	botName, err := cleanText(in.BotName, 120, "COLLAB_BOT_NAME_INVALID", "机器人名称需为 1-120 个字符。")
	if err != nil {
		return nil, err
	}
	botTitle, err := cleanOptionalText(in.BotTitle, 200, "COLLAB_BOT_TITLE_INVALID", "机器人简介不能超过 200 个字符。")
	if err != nil {
		return nil, err
	}
	botColor := strings.TrimSpace(in.BotColor)
	if botColor == "" || len(botColor) > 32 {
		botColor = "blue"
	}
	botAvatarCrop := sanitizeBotAvatarCrop(in.BotAvatarCrop)
	botAvatar, err := validateAvatarDataURL(in.BotAvatarURL)
	if err != nil {
		return nil, err
	}
	if botAvatarCrop == "mascot" {
		botAvatar = ""
	}
	botMascotBody := sanitizeBotMascotBody(in.BotMascotBody)
	botMascotFaceColor := sanitizeBotMascotFaceColor(in.BotMascotFaceColor)
	botMascotExpression := sanitizeBotMascotExpression(in.BotMascotExpression)
	botAvatarZoom := clampBotAvatarZoom(in.BotAvatarZoom)
	botAvatarFocusX := clampBotAvatarFocus(in.BotAvatarFocusX)
	botAvatarFocusY := clampBotAvatarFocus(in.BotAvatarFocusY)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_collab_chat_bots WHERE scope_type = $1 AND scope_key = $2`, normType, scopeKey).Scan(&count); err != nil {
		return nil, err
	}
	var existingID string
	_ = tx.QueryRowContext(ctx, `SELECT id::text FROM donebot_collab_chat_bots WHERE scope_type = $1 AND scope_key = $2 AND owner_user_id = $3 AND local_bot_id = $4`,
		normType, scopeKey, actor, localBotID).Scan(&existingID)
	if existingID == "" && count >= MaxBotsPerChat {
		return nil, infraerrors.BadRequest("COLLAB_CHAT_BOTS_LIMIT", "每个群聊最多添加 20 个机器人。")
	}

	id := existingID
	if id == "" {
		id = uuid.NewString()
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_collab_chat_bots(
			id, scope_type, scope_key, owner_user_id, installation_id, local_bot_id,
			bot_name, bot_color, bot_avatar_url,
			bot_mascot_body, bot_mascot_face_color, bot_mascot_expression,
			bot_avatar_crop, bot_avatar_zoom, bot_avatar_focus_x, bot_avatar_focus_y,
			bot_title, allow_all_members
		) VALUES($1::uuid, $2, $3, $4, $5::uuid, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		ON CONFLICT(scope_type, scope_key, owner_user_id, local_bot_id) DO UPDATE
		SET installation_id = EXCLUDED.installation_id,
		    bot_name = EXCLUDED.bot_name,
		    bot_color = EXCLUDED.bot_color,
		    bot_avatar_url = EXCLUDED.bot_avatar_url,
		    bot_mascot_body = EXCLUDED.bot_mascot_body,
		    bot_mascot_face_color = EXCLUDED.bot_mascot_face_color,
		    bot_mascot_expression = EXCLUDED.bot_mascot_expression,
		    bot_avatar_crop = EXCLUDED.bot_avatar_crop,
		    bot_avatar_zoom = EXCLUDED.bot_avatar_zoom,
		    bot_avatar_focus_x = EXCLUDED.bot_avatar_focus_x,
		    bot_avatar_focus_y = EXCLUDED.bot_avatar_focus_y,
		    bot_title = EXCLUDED.bot_title,
		    allow_all_members = EXCLUDED.allow_all_members`,
		id, normType, scopeKey, actor, in.InstallationID, localBotID,
		botName, botColor, botAvatar,
		botMascotBody, botMascotFaceColor, botMascotExpression,
		botAvatarCrop, botAvatarZoom, botAvatarFocusX, botAvatarFocusY,
		botTitle, in.AllowAllMembers); err != nil {
		return nil, err
	}
	if err = s.replaceChatBotGrants(ctx, tx, id, normType, scopeKey, actor, in.AllowedUserIDs); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}

	list, err := s.ListChatBots(ctx, actor, normType, targetID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].LocalBotID == localBotID && list[i].OwnerUserID == actor {
			return &list[i], nil
		}
	}
	return nil, infraerrors.NotFound("COLLAB_CHAT_BOT_NOT_FOUND", "机器人添加失败。")
}

// SyncChatBots refreshes the stored appearance and profile metadata for bots owned by actor on an installation.
func (s *Service) SyncChatBots(ctx context.Context, actor int64, in SyncChatBotsInput) (int, error) {
	if err := s.available(); err != nil {
		return 0, err
	}
	if actor <= 0 {
		return 0, infraerrors.Unauthorized("COLLAB_UNAUTHORIZED", "请先登录。")
	}
	if !validUUID(in.InstallationID) {
		return 0, infraerrors.BadRequest("COLLAB_INSTALL_INVALID", "无效的客户端安装标识。")
	}
	if len(in.Bots) == 0 {
		return 0, nil
	}
	if len(in.Bots) > MaxSyncChatBots {
		return 0, infraerrors.BadRequest("COLLAB_SYNC_TOO_MANY_BOTS", "单次最多同步 50 个机器人资料。")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	updated := 0
	for _, item := range in.Bots {
		localBotID, err := cleanText(item.LocalBotID, 128, "COLLAB_BOT_ID_INVALID", "无效的机器人标识。")
		if err != nil {
			return 0, err
		}
		botName, err := cleanText(item.BotName, 120, "COLLAB_BOT_NAME_INVALID", "机器人名称需为 1-120 个字符。")
		if err != nil {
			return 0, err
		}
		botTitle, err := cleanOptionalText(item.BotTitle, 200, "COLLAB_BOT_TITLE_INVALID", "机器人简介不能超过 200 个字符。")
		if err != nil {
			return 0, err
		}
		botColor := strings.TrimSpace(item.BotColor)
		if botColor == "" || len(botColor) > 32 {
			botColor = "blue"
		}
		botAvatarCrop := sanitizeBotAvatarCrop(item.BotAvatarCrop)
		botAvatar, err := validateAvatarDataURL(item.BotAvatarURL)
		if err != nil {
			return 0, err
		}
		if botAvatarCrop == "mascot" {
			botAvatar = ""
		}
		botMascotBody := sanitizeBotMascotBody(item.BotMascotBody)
		botMascotFaceColor := sanitizeBotMascotFaceColor(item.BotMascotFaceColor)
		botMascotExpression := sanitizeBotMascotExpression(item.BotMascotExpression)
		botAvatarZoom := clampBotAvatarZoom(item.BotAvatarZoom)
		botAvatarFocusX := clampBotAvatarFocus(item.BotAvatarFocusX)
		botAvatarFocusY := clampBotAvatarFocus(item.BotAvatarFocusY)

		res, execErr := tx.ExecContext(ctx, `UPDATE donebot_collab_chat_bots
			SET bot_name = $4,
			    bot_color = $5,
			    bot_avatar_url = $6,
			    bot_mascot_body = $7,
			    bot_mascot_face_color = $8,
			    bot_mascot_expression = $9,
			    bot_avatar_crop = $10,
			    bot_avatar_zoom = $11,
			    bot_avatar_focus_x = $12,
			    bot_avatar_focus_y = $13,
			    bot_title = $14
			WHERE owner_user_id = $1 AND installation_id = $2::uuid AND local_bot_id = $3`,
			actor, in.InstallationID, localBotID,
			botName, botColor, botAvatar,
			botMascotBody, botMascotFaceColor, botMascotExpression,
			botAvatarCrop, botAvatarZoom, botAvatarFocusX, botAvatarFocusY,
			botTitle)
		if execErr != nil {
			return 0, execErr
		}
		if rowsAffected, _ := res.RowsAffected(); rowsAffected > 0 {
			updated += int(rowsAffected)
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

// UpdateChatBotGrants updates which members of a team/group are authorized to @invoke the owner's bot.
func (s *Service) UpdateChatBotGrants(ctx context.Context, actor int64, scopeType, targetID, chatBotID string, in UpdateChatBotGrantsInput) (*ChatBot, error) {
	normType, scopeKey, err := s.authorizeBotChatScope(ctx, actor, scopeType, targetID)
	if err != nil {
		return nil, err
	}
	if !validUUID(chatBotID) {
		return nil, infraerrors.NotFound("COLLAB_CHAT_BOT_NOT_FOUND", "群内机器人不存在。")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var ownerID int64
	err = tx.QueryRowContext(ctx, `SELECT owner_user_id FROM donebot_collab_chat_bots
		WHERE id = $1::uuid AND scope_type = $2 AND scope_key = $3 FOR UPDATE`, chatBotID, normType, scopeKey).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("COLLAB_CHAT_BOT_NOT_FOUND", "群内机器人不存在。")
	}
	if err != nil {
		return nil, err
	}
	if ownerID != actor {
		return nil, infraerrors.Forbidden("COLLAB_CHAT_BOT_FORBIDDEN", "仅机器人所有者可修改调用授权。")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_collab_chat_bots SET allow_all_members = $2 WHERE id = $1::uuid`, chatBotID, in.AllowAllMembers); err != nil {
		return nil, err
	}
	if err = s.replaceChatBotGrants(ctx, tx, chatBotID, normType, scopeKey, actor, in.AllowedUserIDs); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}

	list, err := s.ListChatBots(ctx, actor, normType, targetID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == chatBotID {
			return &list[i], nil
		}
	}
	return nil, infraerrors.NotFound("COLLAB_CHAT_BOT_NOT_FOUND", "群内机器人不存在。")
}

func (s *Service) RemoveChatBot(ctx context.Context, actor int64, scopeType, targetID, chatBotID string) error {
	normType, scopeKey, err := s.authorizeBotChatScope(ctx, actor, scopeType, targetID)
	if err != nil {
		return err
	}
	if !validUUID(chatBotID) {
		return infraerrors.NotFound("COLLAB_CHAT_BOT_NOT_FOUND", "群内机器人不存在。")
	}
	var ownerID int64
	err = s.db.QueryRowContext(ctx, `SELECT owner_user_id FROM donebot_collab_chat_bots WHERE id = $1::uuid AND scope_type = $2 AND scope_key = $3`,
		chatBotID, normType, scopeKey).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return infraerrors.NotFound("COLLAB_CHAT_BOT_NOT_FOUND", "群内机器人不存在。")
	}
	if err != nil {
		return err
	}
	allowed := ownerID == actor
	if !allowed && normType == "team" {
		var role string
		if err = s.db.QueryRowContext(ctx, `SELECT role FROM donebot_team_members WHERE team_id = $1::uuid AND user_id = $2 AND removed_at IS NULL`,
			scopeKey, actor).Scan(&role); err == nil && (role == "owner" || role == "admin") {
			allowed = true
		}
	}
	if !allowed && normType == "group" {
		var creatorID int64
		if err = s.db.QueryRowContext(ctx, `SELECT creator_id FROM donebot_collab_groups WHERE id = $1::uuid`, scopeKey).Scan(&creatorID); err == nil && creatorID == actor {
			allowed = true
		}
	}
	if !allowed {
		return infraerrors.Forbidden("COLLAB_CHAT_BOT_FORBIDDEN", "仅机器人所属成员或群管理员可移除该机器人。")
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM donebot_collab_chat_bots WHERE id = $1::uuid`, chatBotID)
	return err
}

func bodyMentionsBot(body, botName string) bool {
	name := strings.TrimSpace(botName)
	if name == "" {
		return false
	}
	lowerBody := strings.ToLower(body)
	needle := "@" + strings.ToLower(name)
	offset := 0
	for {
		idx := strings.Index(lowerBody[offset:], needle)
		if idx < 0 {
			return false
		}
		end := offset + idx + len(needle)
		if end >= len(lowerBody) {
			return true
		}
		next := []rune(lowerBody[end:])[0]
		if unicode.IsSpace(next) || unicode.IsPunct(next) || unicode.IsSymbol(next) {
			return true
		}
		offset = end
	}
}

func (s *Service) enqueueMentionedBots(
	ctx context.Context,
	tx queryExecer,
	actor int64,
	scopeType, scopeKey string,
	messageID int64,
	body string,
	explicitBotIDs []string,
) (triggered []string, deniedNames []string, err error) {
	if scopeType != "team" && scopeType != "group" {
		return nil, nil, nil
	}
	explicit := map[string]bool{}
	for _, id := range explicitBotIDs {
		if validUUID(id) {
			explicit[id] = true
		}
	}
	if len(explicit) == 0 && !strings.Contains(body, "@") {
		return nil, nil, nil
	}

	rows, err := tx.QueryContext(ctx, `SELECT b.id::text, b.owner_user_id, b.installation_id::text, b.local_bot_id, b.bot_name, b.allow_all_members,
		EXISTS(SELECT 1 FROM donebot_collab_chat_bot_grants g WHERE g.chat_bot_id = b.id AND g.user_id = $3) AS granted
		FROM donebot_collab_chat_bots b
		WHERE b.scope_type = $1 AND b.scope_key = $2
		ORDER BY b.created_at ASC`, scopeType, scopeKey, actor)
	if err != nil {
		return nil, nil, err
	}
	type candidate struct {
		id             string
		ownerID        int64
		installationID string
		localBotID     string
		botName        string
		allowAll       bool
		granted        bool
	}
	candidates := make([]candidate, 0)
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.ownerID, &c.installationID, &c.localBotID, &c.botName, &c.allowAll, &c.granted); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		candidates = append(candidates, c)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, err
	}
	_ = rows.Close()

	for _, c := range candidates {
		if !explicit[c.id] && !bodyMentionsBot(body, c.botName) {
			continue
		}
		if actor != c.ownerID && !c.allowAll && !c.granted {
			if !slices.Contains(deniedNames, c.botName) {
				deniedNames = append(deniedNames, c.botName)
			}
			continue
		}
		if len(triggered) >= MaxMentionsPerMsg {
			break
		}
		invID := uuid.NewString()
		if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_collab_bot_invocations(
				id, chat_bot_id, message_id, scope_type, scope_key, owner_user_id, installation_id, local_bot_id, status
			) VALUES($1::uuid, $2::uuid, $3, $4, $5, $6, $7::uuid, $8, 'pending')
			ON CONFLICT(chat_bot_id, message_id) DO NOTHING`,
			invID, c.id, messageID, scopeType, scopeKey, c.ownerID, c.installationID, c.localBotID); err != nil {
			return nil, nil, err
		}
		triggered = append(triggered, c.id)
	}
	return triggered, deniedNames, nil
}

// ClaimBotInvocation atomically claims one pending @mention invocation owned by actor so work executes on the bot's local computer.
func (s *Service) ClaimBotInvocation(ctx context.Context, actor int64, installationID string) (*ClaimedBotInvocation, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE donebot_collab_bot_invocations
		SET status = 'pending', claimed_at = NULL
		WHERE owner_user_id = $1 AND status = 'claimed' AND claimed_at < NOW() - INTERVAL '3 minutes'`, actor)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var inv ClaimedBotInvocation
	preferInstall := ""
	if validUUID(installationID) {
		preferInstall = installationID
	}
	err = tx.QueryRowContext(ctx, `SELECT i.id::text, i.chat_bot_id::text, i.message_id, i.scope_type, i.scope_key, i.local_bot_id, b.bot_name
		FROM donebot_collab_bot_invocations i
		JOIN donebot_collab_chat_bots b ON b.id = i.chat_bot_id
		WHERE i.owner_user_id = $1 AND i.status = 'pending'
			AND ($2 = '' OR i.installation_id::text = $2 OR i.created_at < NOW() - INTERVAL '2 seconds')
		ORDER BY CASE WHEN $2 <> '' AND i.installation_id::text = $2 THEN 0 ELSE 1 END, i.created_at ASC
		LIMIT 1
		FOR UPDATE OF i SKIP LOCKED`, actor, preferInstall).Scan(
		&inv.ID, &inv.ChatBotID, &inv.MessageID, &inv.ScopeType, &inv.TargetID, &inv.LocalBotID, &inv.BotName,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_collab_bot_invocations SET status = 'claimed', claimed_at = NOW() WHERE id = $1::uuid`, inv.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}

	if inv.ScopeType == "team" {
		_ = s.db.QueryRowContext(ctx, `SELECT name FROM donebot_teams WHERE id = $1::uuid`, inv.TargetID).Scan(&inv.ChatName)
	} else {
		_ = s.db.QueryRowContext(ctx, `SELECT name FROM donebot_collab_groups WHERE id = $1::uuid`, inv.TargetID).Scan(&inv.ChatName)
	}
	recent, err := s.ListChatMessages(ctx, actor, inv.ScopeType, inv.TargetID, 0, 0, 20)
	if err != nil {
		recent = []ChatMessage{}
	}
	inv.RecentMessages = recent
	return &inv, nil
}

// CompleteBotInvocation records a bot's locally-computed reply into the group/team chat.
func (s *Service) CompleteBotInvocation(ctx context.Context, actor int64, invocationID, rawReply, rawErr string) (*ChatMessage, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if !validUUID(invocationID) || actor <= 0 {
		return nil, infraerrors.NotFound("COLLAB_INVOCATION_NOT_FOUND", "机器人调用记录不存在。")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var chatBotID, scopeType, scopeKey, botName, botColor, botAvatar, status string
	var botMascotBody, botMascotFaceColor, botMascotExpression, botAvatarCrop string
	var botAvatarZoom, botAvatarFocusX, botAvatarFocusY float64
	var ownerID int64
	err = tx.QueryRowContext(ctx, `SELECT i.chat_bot_id::text, i.scope_type, i.scope_key, i.owner_user_id, i.status,
		b.bot_name, b.bot_color, COALESCE(b.bot_avatar_url, ''),
		COALESCE(b.bot_mascot_body, ''), COALESCE(b.bot_mascot_face_color, ''), COALESCE(b.bot_mascot_expression, ''),
		COALESCE(b.bot_avatar_crop, ''), b.bot_avatar_zoom, b.bot_avatar_focus_x, b.bot_avatar_focus_y
		FROM donebot_collab_bot_invocations i
		JOIN donebot_collab_chat_bots b ON b.id = i.chat_bot_id
		WHERE i.id = $1::uuid FOR UPDATE OF i`, invocationID).Scan(
		&chatBotID, &scopeType, &scopeKey, &ownerID, &status,
		&botName, &botColor, &botAvatar,
		&botMascotBody, &botMascotFaceColor, &botMascotExpression,
		&botAvatarCrop, &botAvatarZoom, &botAvatarFocusX, &botAvatarFocusY,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("COLLAB_INVOCATION_NOT_FOUND", "机器人调用记录不存在。")
	}
	if err != nil {
		return nil, err
	}
	if ownerID != actor {
		return nil, infraerrors.Forbidden("COLLAB_INVOCATION_FORBIDDEN", "无权提交该机器人的回复。")
	}
	if status == "done" {
		return nil, nil
	}

	reply, replyErr := cleanChatText(rawReply, MaxChatBodyRunes, true)
	if replyErr != nil {
		runes := []rune(strings.TrimSpace(rawReply))
		if len(runes) > MaxChatBodyRunes {
			reply = string(runes[:MaxChatBodyRunes])
		}
	}
	errMsg := strings.TrimSpace(rawErr)
	if len([]rune(errMsg)) > 500 {
		errMsg = string([]rune(errMsg)[:500])
	}

	if reply == "" {
		if errMsg == "" {
			errMsg = "机器人未能生成回复。"
		}
		if _, err = tx.ExecContext(ctx, `UPDATE donebot_collab_bot_invocations SET status = 'failed', error_text = $2, completed_at = NOW() WHERE id = $1::uuid`,
			invocationID, errMsg); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}

	var msgID int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO donebot_collab_messages(
			scope_type, scope_key, sender_id, sender_kind, sender_bot_id,
			sender_bot_name, sender_bot_color, sender_bot_avatar_url,
			sender_bot_mascot_body, sender_bot_mascot_face_color, sender_bot_mascot_expression,
			sender_bot_avatar_crop, sender_bot_avatar_zoom, sender_bot_avatar_focus_x, sender_bot_avatar_focus_y,
			kind, body
		) VALUES($1, $2, $3, 'bot', $4::uuid, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, 'text', $15) RETURNING id`,
		scopeType, scopeKey, actor, chatBotID,
		botName, botColor, botAvatar,
		botMascotBody, botMascotFaceColor, botMascotExpression,
		botAvatarCrop, botAvatarZoom, botAvatarFocusX, botAvatarFocusY,
		reply).Scan(&msgID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_collab_bot_invocations SET status = 'done', error_text = '', completed_at = NOW() WHERE id = $1::uuid`, invocationID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.fetchChatMessageByID(ctx, actor, msgID, scopeKey)
}

package donebotcollab

import (
	"context"
	"database/sql"
	"regexp"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	MaxCollabAvatarChars = 750_000
	MaxGroupMembers      = 100
)

var inlineAvatarDataURL = regexp.MustCompile(`^data:image/(?:png|jpeg|jpg|webp|gif);base64,[A-Za-z0-9+/=]+$`)

func validateAvatarDataURL(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", nil
	}
	if len(v) > MaxCollabAvatarChars || !inlineAvatarDataURL.MatchString(v) {
		return "", infraerrors.BadRequest("COLLAB_AVATAR_INVALID", "头像仅支持 512 KB 以内的 PNG、JPEG、WebP 或 GIF 图片。")
	}
	return v, nil
}

// --- Team Avatars ---

func (s *Service) SetTeamAvatar(ctx context.Context, actor int64, teamID, rawAvatar string) (string, error) {
	if err := s.available(); err != nil {
		return "", err
	}
	if !validUUID(teamID) || actor <= 0 {
		return "", infraerrors.NotFound("TEAM_NOT_FOUND", "团队不存在。")
	}
	avatar, err := validateAvatarDataURL(rawAvatar)
	if err != nil {
		return "", err
	}
	var role string
	err = s.db.QueryRowContext(ctx, `SELECT m.role
		FROM donebot_team_members m
		JOIN donebot_teams t ON t.id = m.team_id AND t.state = 'active'
		WHERE m.team_id = $1::uuid AND m.user_id = $2 AND m.removed_at IS NULL`, teamID, actor).Scan(&role)
	if err == sql.ErrNoRows {
		return "", infraerrors.NotFound("TEAM_NOT_FOUND", "团队不存在或你不是成员。")
	}
	if err != nil {
		return "", err
	}
	if role != "owner" && role != "admin" {
		return "", infraerrors.Forbidden("TEAM_FORBIDDEN", "仅团队所有者或管理员可更换团队头像。")
	}
	if avatar == "" {
		if _, err = s.db.ExecContext(ctx, `DELETE FROM donebot_team_avatars WHERE team_id = $1::uuid`, teamID); err != nil {
			return "", err
		}
		return "", nil
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO donebot_team_avatars(team_id, avatar_url, updated_at)
		VALUES($1::uuid, $2, NOW())
		ON CONFLICT(team_id) DO UPDATE SET avatar_url = EXCLUDED.avatar_url, updated_at = NOW()`, teamID, avatar); err != nil {
		return "", err
	}
	return avatar, nil
}

func (s *Service) LookupTeamAvatars(ctx context.Context, teamIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if !s.Enabled() || len(teamIDs) == 0 {
		return out, nil
	}
	seen := map[string]struct{}{}
	args := make([]any, 0, len(teamIDs))
	placeholders := make([]string, 0, len(teamIDs))
	for _, id := range teamIDs {
		if !validUUID(id) {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		args = append(args, id)
		placeholders = append(placeholders, "$"+strconv.Itoa(len(args))+"::uuid")
	}
	if len(placeholders) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT team_id::text, avatar_url FROM donebot_team_avatars WHERE team_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var tid, url string
		if err = rows.Scan(&tid, &url); err != nil {
			return out, err
		}
		if url != "" {
			out[tid] = url
		}
	}
	return out, rows.Err()
}

// --- Friend Group Chats ---

type CollabGroupMember struct {
	UserID    int64     `json:"user_id"`
	Email     string    `json:"email"`
	Nickname  string    `json:"nickname,omitempty"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	Remark    string    `json:"remark,omitempty"`
	JoinedAt  time.Time `json:"joined_at"`
}

type CollabGroup struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	AvatarURL string              `json:"avatar_url,omitempty"`
	CreatorID int64               `json:"creator_id"`
	Members   []CollabGroupMember `json:"members"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
}

func isGroupMember(ctx context.Context, db queryExecer, actor int64, groupID string) (bool, error) {
	if !validUUID(groupID) || actor <= 0 {
		return false, nil
	}
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM donebot_collab_group_members gm
		JOIN donebot_collab_groups g ON g.id = gm.group_id
		WHERE gm.group_id = $1::uuid AND gm.user_id = $2
	)`, groupID, actor).Scan(&ok)
	return ok, err
}

func (s *Service) ListGroups(ctx context.Context, actor int64) ([]CollabGroup, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT g.id::text, g.name, COALESCE(g.avatar_url, ''), g.creator_id, g.created_at, g.updated_at
		FROM donebot_collab_groups g
		JOIN donebot_collab_group_members gm ON gm.group_id = g.id AND gm.user_id = $1
		ORDER BY g.updated_at DESC, g.id`, actor)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	groups := make([]CollabGroup, 0)
	byIdx := map[string]int{}
	for rows.Next() {
		var g CollabGroup
		if err = rows.Scan(&g.ID, &g.Name, &g.AvatarURL, &g.CreatorID, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		g.Members = []CollabGroupMember{}
		byIdx[g.ID] = len(groups)
		groups = append(groups, g)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return groups, nil
	}

	mRows, err := s.db.QueryContext(ctx, `SELECT gm.group_id::text, u.id, u.email, COALESCE(u.username, ''), COALESCE(ua.url, ''), COALESCE(rm.remark, ''), gm.joined_at
		FROM donebot_collab_group_members gm
		JOIN donebot_collab_group_members self_m ON self_m.group_id = gm.group_id AND self_m.user_id = $1
		JOIN users u ON u.id = gm.user_id AND u.deleted_at IS NULL AND u.status = 'active'
		LEFT JOIN user_avatars ua ON ua.user_id = u.id
		LEFT JOIN donebot_friend_remarks rm ON rm.owner_id = $1 AND rm.friend_id = u.id
		ORDER BY gm.joined_at ASC, u.id ASC`, actor)
	if err != nil {
		return nil, err
	}
	defer func() { _ = mRows.Close() }()
	for mRows.Next() {
		var gid string
		var m CollabGroupMember
		if err = mRows.Scan(&gid, &m.UserID, &m.Email, &m.Nickname, &m.AvatarURL, &m.Remark, &m.JoinedAt); err != nil {
			return nil, err
		}
		if idx, ok := byIdx[gid]; ok {
			groups[idx].Members = append(groups[idx].Members, m)
		}
	}
	return groups, mRows.Err()
}

func (s *Service) getGroupForActor(ctx context.Context, actor int64, groupID string) (*CollabGroup, error) {
	list, err := s.ListGroups(ctx, actor)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == groupID {
			return &list[i], nil
		}
	}
	return nil, infraerrors.NotFound("COLLAB_GROUP_NOT_FOUND", "群聊不存在或你不在该群聊中。")
}

func (s *Service) CreateGroup(ctx context.Context, actor int64, rawName string, memberIDs []int64) (*CollabGroup, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}

	uniqueMembers := make([]int64, 0, len(memberIDs))
	seen := map[int64]struct{}{actor: {}}
	for _, id := range memberIDs {
		if id <= 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		uniqueMembers = append(uniqueMembers, id)
	}
	if len(uniqueMembers) == 0 {
		return nil, infraerrors.BadRequest("COLLAB_GROUP_MEMBERS_REQUIRED", "请至少选择一位好友创建群聊。")
	}
	if len(uniqueMembers)+1 > MaxGroupMembers {
		return nil, infraerrors.BadRequest("COLLAB_GROUP_TOO_MANY", "群聊成员不能超过 100 人。")
	}

	name, err := cleanOptionalText(rawName, 100, "COLLAB_GROUP_NAME_INVALID", "群聊名称不能超过 100 个字符。")
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(name, "\n\r\t") {
		return nil, infraerrors.BadRequest("COLLAB_GROUP_NAME_INVALID", "群聊名称不能包含换行符。")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// Every invited member must currently be an accepted friend of actor.
	namesForDefault := make([]string, 0, 3)
	var actorDisplay string
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(username, ''), email) FROM users WHERE id = $1 AND deleted_at IS NULL AND status = 'active'`, actor).Scan(&actorDisplay); err != nil {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Account is not active")
	}
	namesForDefault = append(namesForDefault, actorDisplay)

	for _, uid := range uniqueMembers {
		a, b := pairOrder(actor, uid)
		var peerDisplay string
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(rm.remark, ''), NULLIF(u.username, ''), u.email)
			FROM donebot_friends f
			JOIN users u ON u.id = $3 AND u.deleted_at IS NULL AND u.status = 'active'
			LEFT JOIN donebot_friend_remarks rm ON rm.owner_id = $4 AND rm.friend_id = u.id
			WHERE f.user_a = $1 AND f.user_b = $2`, a, b, uid, actor).Scan(&peerDisplay)
		if err == sql.ErrNoRows {
			return nil, infraerrors.Forbidden("COLLAB_NOT_FRIEND", "只能拉取已添加的好友进入群聊。")
		}
		if err != nil {
			return nil, err
		}
		if len(namesForDefault) < 3 {
			namesForDefault = append(namesForDefault, peerDisplay)
		}
	}

	if name == "" {
		joined := strings.Join(namesForDefault, "、")
		runes := []rune(joined)
		if len(runes) > 32 {
			joined = string(runes[:32])
		}
		name = joined + "的群聊"
	}

	groupID := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_collab_groups(id, name, creator_id) VALUES($1::uuid, $2, $3)`, groupID, name, actor); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_collab_group_members(group_id, user_id) VALUES($1::uuid, $2)`, groupID, actor); err != nil {
		return nil, err
	}
	for _, uid := range uniqueMembers {
		if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_collab_group_members(group_id, user_id) VALUES($1::uuid, $2) ON CONFLICT DO NOTHING`, groupID, uid); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.getGroupForActor(ctx, actor, groupID)
}

func (s *Service) RenameGroup(ctx context.Context, actor int64, groupID, rawName string) (*CollabGroup, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	ok, err := isGroupMember(ctx, s.db, actor, groupID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, infraerrors.Forbidden("COLLAB_GROUP_FORBIDDEN", "你不是该群聊成员。")
	}
	name, err := cleanText(rawName, 100, "COLLAB_GROUP_NAME_INVALID", "群聊名称需为 1-100 个字符。")
	if err != nil {
		return nil, err
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE donebot_collab_groups SET name = $2, updated_at = NOW() WHERE id = $1::uuid`, groupID, name); err != nil {
		return nil, err
	}
	return s.getGroupForActor(ctx, actor, groupID)
}

func (s *Service) SetGroupAvatar(ctx context.Context, actor int64, groupID, rawAvatar string) (string, error) {
	if err := s.available(); err != nil {
		return "", err
	}
	ok, err := isGroupMember(ctx, s.db, actor, groupID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", infraerrors.Forbidden("COLLAB_GROUP_FORBIDDEN", "你不是该群聊成员。")
	}
	avatar, err := validateAvatarDataURL(rawAvatar)
	if err != nil {
		return "", err
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE donebot_collab_groups SET avatar_url = $2, updated_at = NOW() WHERE id = $1::uuid`, groupID, avatar); err != nil {
		return "", err
	}
	return avatar, nil
}

func (s *Service) AddGroupMembers(ctx context.Context, actor int64, groupID string, memberIDs []int64) (*CollabGroup, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	ok, err := isGroupMember(ctx, s.db, actor, groupID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, infraerrors.Forbidden("COLLAB_GROUP_FORBIDDEN", "你不是该群聊成员。")
	}
	unique := make([]int64, 0, len(memberIDs))
	seen := map[int64]struct{}{actor: {}}
	for _, id := range memberIDs {
		if id <= 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return nil, infraerrors.BadRequest("COLLAB_GROUP_MEMBERS_REQUIRED", "请选择要邀请的好友。")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var currentCount int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_collab_group_members WHERE group_id = $1::uuid`, groupID).Scan(&currentCount); err != nil {
		return nil, err
	}
	if currentCount+len(unique) > MaxGroupMembers {
		return nil, infraerrors.BadRequest("COLLAB_GROUP_TOO_MANY", "群聊成员不能超过 100 人。")
	}
	for _, uid := range unique {
		a, b := pairOrder(actor, uid)
		var isFriend bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM donebot_friends f
			JOIN users u ON u.id = $3 AND u.deleted_at IS NULL AND u.status = 'active'
			WHERE f.user_a = $1 AND f.user_b = $2
		)`, a, b, uid).Scan(&isFriend); err != nil {
			return nil, err
		}
		if !isFriend {
			return nil, infraerrors.Forbidden("COLLAB_NOT_FRIEND", "只能邀请已添加的好友加入群聊。")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_collab_group_members(group_id, user_id) VALUES($1::uuid, $2) ON CONFLICT DO NOTHING`, groupID, uid); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_collab_groups SET updated_at = NOW() WHERE id = $1::uuid`, groupID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.getGroupForActor(ctx, actor, groupID)
}

func (s *Service) LeaveGroup(ctx context.Context, actor int64, groupID string) error {
	if err := s.available(); err != nil {
		return err
	}
	if !validUUID(groupID) || actor <= 0 {
		return infraerrors.NotFound("COLLAB_GROUP_NOT_FOUND", "群聊不存在。")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `DELETE FROM donebot_collab_group_members WHERE group_id = $1::uuid AND user_id = $2`, groupID, actor)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return infraerrors.NotFound("COLLAB_GROUP_NOT_FOUND", "你不在该群聊中。")
	}
	// Remove actor's bots from this group when they leave.
	if _, err = tx.ExecContext(ctx, `DELETE FROM donebot_collab_chat_bots WHERE scope_type = 'group' AND scope_key = $1 AND owner_user_id = $2`, groupID, actor); err != nil {
		return err
	}
	var remaining int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_collab_group_members WHERE group_id = $1::uuid`, groupID).Scan(&remaining); err != nil {
		return err
	}
	if remaining == 0 {
		if _, err = tx.ExecContext(ctx, `DELETE FROM donebot_collab_groups WHERE id = $1::uuid`, groupID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

package donebotcollab

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	MaxChannelsPerTeam   = 50
	MaxChannelNameRunes  = 60
	MaxChannelDescRunes  = 200
	MaxJobTitleRunes     = 40
	DefaultChannelName   = "大厅"
)

type TeamChannel struct {
	ID            string     `json:"id"`
	TeamID        string     `json:"team_id"`
	Name          string     `json:"name"`
	Description   string     `json:"description,omitempty"`
	IsDefault     bool       `json:"is_default"`
	IsPrivate     bool       `json:"is_private"`
	ChatScopeType string     `json:"chat_scope_type"` // "team" for default, "channel" for sub-channels
	ChatTargetID  string     `json:"chat_target_id"`
	CreatedBy     int64      `json:"created_by"`
	MemberIDs     []int64    `json:"member_ids"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type CreateTeamChannelInput struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	IsPrivate   bool    `json:"is_private"`
	MemberIDs   []int64 `json:"member_ids"`
}

type UpdateTeamChannelInput struct {
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	IsPrivate   *bool    `json:"is_private,omitempty"`
	MemberIDs   *[]int64 `json:"member_ids,omitempty"`
	Archived    *bool    `json:"archived,omitempty"`
}

type TeamMemberProfile struct {
	UserID    int64     `json:"user_id"`
	JobTitle  string    `json:"job_title"`
	UpdatedAt time.Time `json:"updated_at"`
}

func activeTeamRole(ctx context.Context, db queryExecer, actor int64, teamID string) (string, bool, error) {
	if actor <= 0 || !validUUID(teamID) {
		return "", false, nil
	}
	var role string
	err := db.QueryRowContext(ctx, `SELECT m.role
		FROM donebot_team_members m
		JOIN donebot_teams t ON t.id = m.team_id AND t.state = 'active'
		WHERE m.team_id = $1::uuid AND m.user_id = $2 AND m.removed_at IS NULL`, teamID, actor).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return role, true, nil
}

func ensureDefaultTeamChannel(ctx context.Context, db queryExecer, teamID string, fallbackCreator int64) (string, error) {
	var existingID string
	err := db.QueryRowContext(ctx, `SELECT id::text FROM donebot_team_channels WHERE team_id = $1::uuid AND is_default = TRUE`, teamID).Scan(&existingID)
	if err == nil && existingID != "" {
		return existingID, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	creatorID := fallbackCreator
	var ownerID int64
	if err = db.QueryRowContext(ctx, `SELECT owner_id FROM donebot_teams WHERE id = $1::uuid`, teamID).Scan(&ownerID); err == nil && ownerID > 0 {
		creatorID = ownerID
	}
	newID := uuid.NewString()
	_, err = db.ExecContext(ctx, `INSERT INTO donebot_team_channels(
			id, team_id, name, description, is_default, is_private, created_by
		) VALUES($1::uuid, $2::uuid, $3, '团队全员日常同步与通知', TRUE, FALSE, $4)
		ON CONFLICT (team_id) WHERE is_default = TRUE DO NOTHING`,
		newID, teamID, DefaultChannelName, creatorID)
	if err != nil {
		return "", err
	}
	if err = db.QueryRowContext(ctx, `SELECT id::text FROM donebot_team_channels WHERE team_id = $1::uuid AND is_default = TRUE`, teamID).Scan(&existingID); err != nil {
		return "", err
	}
	return existingID, nil
}

type authorizedChannel struct {
	ID          string
	TeamID      string
	TeamName    string
	Name        string
	IsDefault   bool
	IsPrivate   bool
	CreatedBy   int64
	ArchivedAt  *time.Time
	ActorRole   string
}

func (s *Service) authorizeTeamChannel(ctx context.Context, actor int64, channelID string) (*authorizedChannel, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	if !validUUID(channelID) {
		return nil, infraerrors.NotFound("COLLAB_CHANNEL_NOT_FOUND", "团队频道不存在。")
	}
	var ch authorizedChannel
	var archived sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT c.id::text, c.team_id::text, t.name, c.name, c.is_default, c.is_private, c.created_by, c.archived_at
		FROM donebot_team_channels c
		JOIN donebot_teams t ON t.id = c.team_id AND t.state = 'active'
		WHERE c.id = $1::uuid`, channelID).Scan(
		&ch.ID, &ch.TeamID, &ch.TeamName, &ch.Name, &ch.IsDefault, &ch.IsPrivate, &ch.CreatedBy, &archived,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("COLLAB_CHANNEL_NOT_FOUND", "团队频道不存在。")
	}
	if err != nil {
		return nil, err
	}
	if archived.Valid {
		t := archived.Time
		ch.ArchivedAt = &t
	}
	role, ok, err := activeTeamRole(ctx, s.db, actor, ch.TeamID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, infraerrors.Forbidden("COLLAB_CHANNEL_FORBIDDEN", "你不是该团队成员，无法访问该频道。")
	}
	ch.ActorRole = role
	if ch.IsPrivate {
		var member bool
		err = s.db.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM donebot_team_channel_members WHERE channel_id = $1::uuid AND user_id = $2
		)`, ch.ID, actor).Scan(&member)
		if err != nil {
			return nil, err
		}
		if !member {
			return nil, infraerrors.Forbidden("COLLAB_CHANNEL_FORBIDDEN", "这是私密频道，仅频道成员可以访问。")
		}
	}
	return &ch, nil
}

func (s *Service) canAccessChannel(ctx context.Context, db queryExecer, actor int64, channelID string) (bool, error) {
	if !validUUID(channelID) || actor <= 0 {
		return false, nil
	}
	var teamID string
	var isPrivate bool
	err := db.QueryRowContext(ctx, `SELECT c.team_id::text, c.is_private
		FROM donebot_team_channels c
		JOIN donebot_teams t ON t.id = c.team_id AND t.state = 'active'
		WHERE c.id = $1::uuid`, channelID).Scan(&teamID, &isPrivate)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ok, err := isActiveTeamMember(ctx, db, actor, teamID)
	if err != nil || !ok {
		return false, err
	}
	if !isPrivate {
		return true, nil
	}
	var inCh bool
	err = db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM donebot_team_channel_members WHERE channel_id = $1::uuid AND user_id = $2
	)`, channelID, actor).Scan(&inCh)
	return inCh, err
}

func channelChatTarget(teamID, channelID string, isDefault bool) (string, string) {
	if isDefault {
		return "team", teamID
	}
	return "channel", channelID
}

func (s *Service) loadChannelMemberIDs(ctx context.Context, teamID string, channelIDs []string) (map[string][]int64, error) {
	res := make(map[string][]int64, len(channelIDs))
	if len(channelIDs) == 0 {
		return res, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT cm.channel_id::text, cm.user_id
		FROM donebot_team_channel_members cm
		JOIN donebot_team_channels c ON c.id = cm.channel_id AND c.team_id = $1::uuid
		JOIN donebot_team_members tm ON tm.team_id = c.team_id AND tm.user_id = cm.user_id AND tm.removed_at IS NULL
		ORDER BY cm.added_at ASC, cm.user_id ASC`, teamID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid string
		var uid int64
		if err = rows.Scan(&cid, &uid); err != nil {
			return nil, err
		}
		res[cid] = append(res[cid], uid)
	}
	return res, rows.Err()
}

func (s *Service) ListTeamChannels(ctx context.Context, actor int64, teamID string, includeArchived bool) ([]TeamChannel, error) {
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
	if _, err = ensureDefaultTeamChannel(ctx, s.db, teamID, actor); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT c.id::text, c.team_id::text, c.name, c.description,
		c.is_default, c.is_private, c.created_by, c.archived_at, c.created_at, c.updated_at
		FROM donebot_team_channels c
		WHERE c.team_id = $1::uuid
			AND ($3 OR c.archived_at IS NULL)
			AND (c.is_private = FALSE OR EXISTS(
				SELECT 1 FROM donebot_team_channel_members m WHERE m.channel_id = c.id AND m.user_id = $2
			))
		ORDER BY c.is_default DESC, (c.archived_at IS NOT NULL) ASC, c.created_at ASC, c.id ASC`,
		teamID, actor, includeArchived)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]TeamChannel, 0)
	privateIDs := make([]string, 0)
	for rows.Next() {
		var ch TeamChannel
		var archived sql.NullTime
		if err = rows.Scan(
			&ch.ID, &ch.TeamID, &ch.Name, &ch.Description,
			&ch.IsDefault, &ch.IsPrivate, &ch.CreatedBy, &archived, &ch.CreatedAt, &ch.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if archived.Valid {
			t := archived.Time
			ch.ArchivedAt = &t
		}
		ch.ChatScopeType, ch.ChatTargetID = channelChatTarget(ch.TeamID, ch.ID, ch.IsDefault)
		ch.MemberIDs = []int64{}
		if ch.IsPrivate {
			privateIDs = append(privateIDs, ch.ID)
		}
		out = append(out, ch)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(privateIDs) > 0 {
		byChannel, err := s.loadChannelMemberIDs(ctx, teamID, privateIDs)
		if err != nil {
			return nil, err
		}
		for i := range out {
			if out[i].IsPrivate {
				if ids := byChannel[out[i].ID]; len(ids) > 0 {
					out[i].MemberIDs = ids
				}
			}
		}
	}
	return out, nil
}

func cleanChannelName(raw string) (string, error) {
	v := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(raw), "#"))
	return cleanText(v, MaxChannelNameRunes, "COLLAB_CHANNEL_NAME_INVALID", "频道名称需为 1-60 个字符。")
}

func (s *Service) replaceChannelMembers(ctx context.Context, tx queryExecer, teamID, channelID string, mustInclude int64, requested []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM donebot_team_channel_members WHERE channel_id = $1::uuid`, channelID); err != nil {
		return err
	}
	seen := map[int64]struct{}{}
	add := func(uid int64) error {
		if uid <= 0 {
			return nil
		}
		if _, dup := seen[uid]; dup {
			return nil
		}
		seen[uid] = struct{}{}
		ok, err := isActiveTeamMember(ctx, tx, uid, teamID)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO donebot_team_channel_members(channel_id, user_id) VALUES($1::uuid, $2) ON CONFLICT DO NOTHING`, channelID, uid)
		return err
	}
	if mustInclude > 0 {
		if err := add(mustInclude); err != nil {
			return err
		}
	}
	for _, uid := range requested {
		if err := add(uid); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) CreateTeamChannel(ctx context.Context, actor int64, teamID string, in CreateTeamChannelInput) (*TeamChannel, error) {
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
	name, err := cleanChannelName(in.Name)
	if err != nil {
		return nil, err
	}
	desc, err := cleanOptionalText(in.Description, MaxChannelDescRunes, "COLLAB_CHANNEL_DESC_INVALID", "频道简介不能超过 200 个字符。")
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = ensureDefaultTeamChannel(ctx, tx, teamID, actor); err != nil {
		return nil, err
	}
	var activeCount int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_team_channels WHERE team_id = $1::uuid AND archived_at IS NULL`, teamID).Scan(&activeCount); err != nil {
		return nil, err
	}
	if activeCount >= MaxChannelsPerTeam {
		return nil, infraerrors.BadRequest("COLLAB_CHANNEL_LIMIT", "每个团队最多创建 50 个活跃频道。")
	}
	var dup bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM donebot_team_channels WHERE team_id = $1::uuid AND archived_at IS NULL AND lower(name) = lower($2)
	)`, teamID, name).Scan(&dup); err != nil {
		return nil, err
	}
	if dup {
		return nil, infraerrors.BadRequest("COLLAB_CHANNEL_EXISTS", "团队内已有同名频道，请换一个名称。")
	}

	channelID := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_team_channels(
			id, team_id, name, description, is_default, is_private, created_by
		) VALUES($1::uuid, $2::uuid, $3, $4, FALSE, $5, $6)`,
		channelID, teamID, name, desc, in.IsPrivate, actor); err != nil {
		return nil, err
	}
	if in.IsPrivate {
		if err = s.replaceChannelMembers(ctx, tx, teamID, channelID, actor, in.MemberIDs); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}

	list, err := s.ListTeamChannels(ctx, actor, teamID, true)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == channelID {
			return &list[i], nil
		}
	}
	return nil, infraerrors.NotFound("COLLAB_CHANNEL_NOT_FOUND", "频道创建失败。")
}

func (s *Service) UpdateTeamChannel(ctx context.Context, actor int64, teamID, channelID string, in UpdateTeamChannelInput) (*TeamChannel, error) {
	ch, err := s.authorizeTeamChannel(ctx, actor, channelID)
	if err != nil {
		return nil, err
	}
	if ch.TeamID != teamID {
		return nil, infraerrors.NotFound("COLLAB_CHANNEL_NOT_FOUND", "团队频道不存在。")
	}
	if ch.CreatedBy != actor && ch.ActorRole != "owner" && ch.ActorRole != "admin" {
		return nil, infraerrors.Forbidden("COLLAB_CHANNEL_FORBIDDEN", "仅频道创建者或团队管理员可修改频道设置。")
	}

	nextName := ch.Name
	if in.Name != nil {
		cleaned, nameErr := cleanChannelName(*in.Name)
		if nameErr != nil {
			return nil, nameErr
		}
		nextName = cleaned
	}
	var nextDesc string
	if in.Description != nil {
		cleaned, descErr := cleanOptionalText(*in.Description, MaxChannelDescRunes, "COLLAB_CHANNEL_DESC_INVALID", "频道简介不能超过 200 个字符。")
		if descErr != nil {
			return nil, descErr
		}
		nextDesc = cleaned
	}
	nextPrivate := ch.IsPrivate
	if in.IsPrivate != nil {
		if ch.IsDefault && *in.IsPrivate {
			return nil, infraerrors.BadRequest("COLLAB_CHANNEL_DEFAULT", "默认频道不能设为私密频道。")
		}
		nextPrivate = *in.IsPrivate
	}
	if in.Archived != nil && *in.Archived && ch.IsDefault {
		return nil, infraerrors.BadRequest("COLLAB_CHANNEL_DEFAULT", "默认频道不能归档。")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if in.Name != nil && !strings.EqualFold(nextName, ch.Name) {
		var dup bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM donebot_team_channels WHERE team_id = $1::uuid AND id <> $2::uuid AND archived_at IS NULL AND lower(name) = lower($3)
		)`, teamID, channelID, nextName).Scan(&dup); err != nil {
			return nil, err
		}
		if dup {
			return nil, infraerrors.BadRequest("COLLAB_CHANNEL_EXISTS", "团队内已有同名频道，请换一个名称。")
		}
	}

	if in.Description == nil {
		if err = tx.QueryRowContext(ctx, `SELECT description FROM donebot_team_channels WHERE id = $1::uuid`, channelID).Scan(&nextDesc); err != nil {
			return nil, err
		}
	}

	var nextArchived sql.NullTime
	if ch.ArchivedAt != nil {
		nextArchived = sql.NullTime{Time: *ch.ArchivedAt, Valid: true}
	}
	if in.Archived != nil {
		if *in.Archived {
			nextArchived = sql.NullTime{Time: time.Now(), Valid: true}
		} else {
			nextArchived = sql.NullTime{}
		}
	}

	if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_channels
		SET name = $2, description = $3, is_private = $4, archived_at = $5, updated_at = NOW()
		WHERE id = $1::uuid`,
		channelID, nextName, nextDesc, nextPrivate, nextArchived); err != nil {
		return nil, err
	}

	if !nextPrivate {
		if _, err = tx.ExecContext(ctx, `DELETE FROM donebot_team_channel_members WHERE channel_id = $1::uuid`, channelID); err != nil {
			return nil, err
		}
	} else if in.MemberIDs != nil {
		if err = s.replaceChannelMembers(ctx, tx, teamID, channelID, actor, *in.MemberIDs); err != nil {
			return nil, err
		}
	} else if !ch.IsPrivate {
		if err = s.replaceChannelMembers(ctx, tx, teamID, channelID, actor, nil); err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	list, err := s.ListTeamChannels(ctx, actor, teamID, true)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == channelID {
			return &list[i], nil
		}
	}
	return nil, infraerrors.NotFound("COLLAB_CHANNEL_NOT_FOUND", "团队频道不存在。")
}

// --- Team Member Job Titles ---

func (s *Service) ListTeamMemberProfiles(ctx context.Context, actor int64, teamID string) ([]TeamMemberProfile, error) {
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
	rows, err := s.db.QueryContext(ctx, `SELECT p.user_id, p.job_title, p.updated_at
		FROM donebot_team_member_profiles p
		JOIN donebot_team_members m ON m.team_id = p.team_id AND m.user_id = p.user_id AND m.removed_at IS NULL
		WHERE p.team_id = $1::uuid AND p.job_title <> ''
		ORDER BY p.user_id ASC`, teamID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]TeamMemberProfile, 0)
	for rows.Next() {
		var item TeamMemberProfile
		if err = rows.Scan(&item.UserID, &item.JobTitle, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) SetTeamMemberJobTitle(ctx context.Context, actor int64, teamID string, targetUserID int64, rawTitle string) (*TeamMemberProfile, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	role, ok, err := activeTeamRole(ctx, s.db, actor, teamID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, infraerrors.Forbidden("COLLAB_TEAM_FORBIDDEN", "你不是该团队成员。")
	}
	if actor != targetUserID && role != "owner" && role != "admin" {
		return nil, infraerrors.Forbidden("COLLAB_TEAM_FORBIDDEN", "仅团队管理员或本人可修改岗位分工。")
	}
	targetOK, err := isActiveTeamMember(ctx, s.db, targetUserID, teamID)
	if err != nil {
		return nil, err
	}
	if !targetOK {
		return nil, infraerrors.NotFound("COLLAB_MEMBER_NOT_FOUND", "该用户不是团队成员。")
	}
	title, err := cleanOptionalText(rawTitle, MaxJobTitleRunes, "COLLAB_JOB_TITLE_INVALID", "岗位分工不能超过 40 个字符。")
	if err != nil {
		return nil, err
	}
	title = strings.ReplaceAll(strings.ReplaceAll(title, "\n", " "), "\t", " ")
	var updatedAt time.Time
	err = s.db.QueryRowContext(ctx, `INSERT INTO donebot_team_member_profiles(team_id, user_id, job_title, updated_at)
		VALUES($1::uuid, $2, $3, NOW())
		ON CONFLICT(team_id, user_id) DO UPDATE
		SET job_title = EXCLUDED.job_title, updated_at = NOW()
		RETURNING updated_at`, teamID, targetUserID, title).Scan(&updatedAt)
	if err != nil {
		return nil, err
	}
	return &TeamMemberProfile{
		UserID:    targetUserID,
		JobTitle:  title,
		UpdatedAt: updatedAt,
	}, nil
}

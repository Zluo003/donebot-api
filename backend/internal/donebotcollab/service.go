package donebotcollab

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	MaxFoldersPerUser = 50
	MaxBotsPerUser    = 50
	MaxGrantsPerItem  = 200
	MaxBotPackageSize = 2 << 20 // 2 MiB
)

type Service struct {
	db      *sql.DB
	relay   *Relay
	chatDir string
	enabled bool
}

func New(db *sql.DB, enabled bool) *Service {
	return &Service{db: db, relay: NewRelay(), chatDir: "data/collab-chat", enabled: enabled}
}

func (s *Service) SetChatStorageDir(dir string) {
	if s == nil {
		return
	}
	if trimmed := strings.TrimSpace(dir); trimmed != "" {
		s.chatDir = trimmed
	}
}

func (s *Service) Enabled() bool { return s != nil && s.enabled && s.db != nil }
func (s *Service) Relay() *Relay {
	if s == nil {
		return nil
	}
	return s.relay
}

func (s *Service) available() error {
	if !s.Enabled() {
		return infraerrors.ServiceUnavailable("COLLAB_UNAVAILABLE", "Remote collaboration is unavailable")
	}
	return nil
}

func validUUID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v.Version() == 4 && v.String() == id
}

func cleanText(raw string, maxRunes int, code, msg string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" || !utf8.ValidString(v) || utf8.RuneCountInString(v) > maxRunes || strings.IndexFunc(v, unicode.IsControl) >= 0 {
		return "", infraerrors.BadRequest(code, msg)
	}
	return v, nil
}

func cleanOptionalText(raw string, maxRunes int, code, msg string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", nil
	}
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) > maxRunes || strings.IndexFunc(v, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0 {
		return "", infraerrors.BadRequest(code, msg)
	}
	return v, nil
}

func normalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 || strings.IndexFunc(email, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", infraerrors.BadRequest("COLLAB_EMAIL_INVALID", "请输入有效的 DoneBot 账号邮箱。")
	}
	return email, nil
}

func pairOrder(a, b int64) (int64, int64) {
	if a < b {
		return a, b
	}
	return b, a
}

// --- Friends ---

type UserProfile struct {
	UserID    int64  `json:"user_id"`
	Email     string `json:"email"`
	Nickname  string `json:"nickname,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Remark    string `json:"remark,omitempty"`
}

type Friend struct {
	UserID    int64     `json:"user_id"`
	Email     string    `json:"email"`
	Nickname  string    `json:"nickname,omitempty"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	Remark    string    `json:"remark,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type FriendRequest struct {
	ID                 string    `json:"id"`
	RequesterID        int64     `json:"requester_id"`
	RequesterEmail     string    `json:"requester_email"`
	RequesterNickname  string    `json:"requester_nickname,omitempty"`
	RequesterAvatarURL string    `json:"requester_avatar_url,omitempty"`
	RecipientID        int64     `json:"recipient_id"`
	RecipientEmail     string    `json:"recipient_email"`
	RecipientNickname  string    `json:"recipient_nickname,omitempty"`
	RecipientAvatarURL string    `json:"recipient_avatar_url,omitempty"`
	Note               string    `json:"note"`
	Status             string    `json:"status"`
	CreatedAt          time.Time `json:"created_at"`
}

type FriendsSnapshot struct {
	Friends          []Friend        `json:"friends"`
	IncomingRequests []FriendRequest `json:"incoming_requests"`
	OutgoingRequests []FriendRequest `json:"outgoing_requests"`
}

type SendFriendRequestResult struct {
	Status  string         `json:"status"` // "pending" | "accepted"
	Request *FriendRequest `json:"request,omitempty"`
	Friend  *Friend        `json:"friend,omitempty"`
}

func (s *Service) ListFriends(ctx context.Context, actor int64) (*FriendsSnapshot, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	out := &FriendsSnapshot{
		Friends:          []Friend{},
		IncomingRequests: []FriendRequest{},
		OutgoingRequests: []FriendRequest{},
	}

	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.email, COALESCE(u.username, ''), COALESCE(ua.url, ''), COALESCE(rm.remark, ''), f.created_at
		FROM donebot_friends f
		JOIN users u ON u.id = CASE WHEN f.user_a = $1 THEN f.user_b ELSE f.user_a END
		LEFT JOIN user_avatars ua ON ua.user_id = u.id
		LEFT JOIN donebot_friend_remarks rm ON rm.owner_id = $1 AND rm.friend_id = u.id
		WHERE (f.user_a = $1 OR f.user_b = $1) AND u.deleted_at IS NULL AND u.status = 'active'
		ORDER BY lower(COALESCE(NULLIF(rm.remark, ''), NULLIF(u.username, ''), u.email)), u.id`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var f Friend
		if err = rows.Scan(&f.UserID, &f.Email, &f.Nickname, &f.AvatarURL, &f.Remark, &f.CreatedAt); err != nil {
			return nil, err
		}
		out.Friends = append(out.Friends, f)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	reqRows, err := s.db.QueryContext(ctx, `SELECT r.id::text, r.requester_id, ru.email, COALESCE(ru.username, ''), COALESCE(rua.url, ''),
		r.recipient_id, tu.email, COALESCE(tu.username, ''), COALESCE(tua.url, ''), r.note, r.status, r.created_at
		FROM donebot_friend_requests r
		JOIN users ru ON ru.id = r.requester_id AND ru.deleted_at IS NULL AND ru.status = 'active'
		LEFT JOIN user_avatars rua ON rua.user_id = ru.id
		JOIN users tu ON tu.id = r.recipient_id AND tu.deleted_at IS NULL AND tu.status = 'active'
		LEFT JOIN user_avatars tua ON tua.user_id = tu.id
		WHERE r.status = 'pending' AND (r.recipient_id = $1 OR r.requester_id = $1)
		ORDER BY r.created_at DESC, r.id`, actor)
	if err != nil {
		return nil, err
	}
	defer reqRows.Close()
	for reqRows.Next() {
		var req FriendRequest
		if err = reqRows.Scan(&req.ID, &req.RequesterID, &req.RequesterEmail, &req.RequesterNickname, &req.RequesterAvatarURL,
			&req.RecipientID, &req.RecipientEmail, &req.RecipientNickname, &req.RecipientAvatarURL, &req.Note, &req.Status, &req.CreatedAt); err != nil {
			return nil, err
		}
		if req.RecipientID == actor {
			out.IncomingRequests = append(out.IncomingRequests, req)
		} else {
			out.OutgoingRequests = append(out.OutgoingRequests, req)
		}
	}
	if err = reqRows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) SendFriendRequest(ctx context.Context, actor int64, rawEmail, rawNote string) (*SendFriendRequestResult, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	email, err := normalizeEmail(rawEmail)
	if err != nil {
		return nil, err
	}
	note, err := cleanOptionalText(rawNote, 200, "COLLAB_NOTE_INVALID", "附言不能超过 200 个字符。")
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var actorEmail string
	if err = tx.QueryRowContext(ctx, `SELECT email FROM users WHERE id=$1 AND deleted_at IS NULL AND status='active'`, actor).Scan(&actorEmail); err != nil {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Account is not active")
	}
	var targetID int64
	var targetEmail string
	if err = tx.QueryRowContext(ctx, `SELECT id, email FROM users WHERE lower(email)=$1 AND deleted_at IS NULL AND status='active'`, email).Scan(&targetID, &targetEmail); errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("COLLAB_USER_NOT_FOUND", "未找到该邮箱对应的 DoneBot 账号。")
	} else if err != nil {
		return nil, err
	}
	if targetID == actor {
		return nil, infraerrors.BadRequest("COLLAB_SELF_FRIEND", "不能添加自己为好友。")
	}

	a, b := pairOrder(actor, targetID)
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donebot_friends WHERE user_a=$1 AND user_b=$2)`, a, b).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, infraerrors.Conflict("COLLAB_ALREADY_FRIENDS", "对方已经是你的好友。")
	}

	// If the target already sent a pending request to actor, accept it atomically.
	var reverseID string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM donebot_friend_requests WHERE requester_id=$1 AND recipient_id=$2 AND status='pending' FOR UPDATE`, targetID, actor).Scan(&reverseID)
	if err == nil {
		if _, err = tx.ExecContext(ctx, `UPDATE donebot_friend_requests SET status='accepted', responded_at=NOW() WHERE id=$1::uuid`, reverseID); err != nil {
			return nil, err
		}
		var createdAt time.Time
		if err = tx.QueryRowContext(ctx, `INSERT INTO donebot_friends(user_a,user_b) VALUES($1,$2) ON CONFLICT(user_a,user_b) DO UPDATE SET user_a=EXCLUDED.user_a RETURNING created_at`, a, b).Scan(&createdAt); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return &SendFriendRequestResult{
			Status: "accepted",
			Friend: &Friend{UserID: targetID, Email: targetEmail, CreatedAt: createdAt},
		}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// Idempotent if actor already has a pending request to target.
	var existing FriendRequest
	err = tx.QueryRowContext(ctx, `SELECT id::text, note, created_at FROM donebot_friend_requests WHERE requester_id=$1 AND recipient_id=$2 AND status='pending'`, actor, targetID).Scan(&existing.ID, &existing.Note, &existing.CreatedAt)
	if err == nil {
		existing.RequesterID = actor
		existing.RequesterEmail = actorEmail
		existing.RecipientID = targetID
		existing.RecipientEmail = targetEmail
		existing.Status = "pending"
		return &SendFriendRequestResult{Status: "pending", Request: &existing}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	req := FriendRequest{
		ID:             uuid.NewString(),
		RequesterID:    actor,
		RequesterEmail: actorEmail,
		RecipientID:    targetID,
		RecipientEmail: targetEmail,
		Note:           note,
		Status:         "pending",
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO donebot_friend_requests(id,requester_id,recipient_id,note,status) VALUES($1::uuid,$2,$3,$4,'pending') RETURNING created_at`,
		req.ID, actor, targetID, note).Scan(&req.CreatedAt); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &SendFriendRequestResult{Status: "pending", Request: &req}, nil
}

func (s *Service) AcceptFriendRequest(ctx context.Context, actor int64, requestID string) (*Friend, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if !validUUID(requestID) || actor <= 0 {
		return nil, infraerrors.NotFound("COLLAB_REQUEST_NOT_FOUND", "好友申请不存在或已失效。")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var requesterID int64
	var requesterEmail string
	err = tx.QueryRowContext(ctx, `SELECT r.requester_id, u.email
		FROM donebot_friend_requests r
		JOIN users u ON u.id = r.requester_id AND u.deleted_at IS NULL AND u.status = 'active'
		WHERE r.id=$1::uuid AND r.recipient_id=$2 AND r.status='pending' FOR UPDATE OF r`, requestID, actor).Scan(&requesterID, &requesterEmail)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("COLLAB_REQUEST_NOT_FOUND", "好友申请不存在或已处理。")
	} else if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_friend_requests SET status='accepted', responded_at=NOW() WHERE id=$1::uuid`, requestID); err != nil {
		return nil, err
	}
	// Cancel any opposite pending request if one exists.
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_friend_requests SET status='accepted', responded_at=NOW() WHERE requester_id=$1 AND recipient_id=$2 AND status='pending'`, actor, requesterID); err != nil {
		return nil, err
	}
	a, b := pairOrder(actor, requesterID)
	var createdAt time.Time
	if err = tx.QueryRowContext(ctx, `INSERT INTO donebot_friends(user_a,user_b) VALUES($1,$2) ON CONFLICT(user_a,user_b) DO UPDATE SET user_a=EXCLUDED.user_a RETURNING created_at`, a, b).Scan(&createdAt); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &Friend{UserID: requesterID, Email: requesterEmail, CreatedAt: createdAt}, nil
}

func (s *Service) RejectFriendRequest(ctx context.Context, actor int64, requestID string) error {
	if err := s.available(); err != nil {
		return err
	}
	if !validUUID(requestID) || actor <= 0 {
		return infraerrors.NotFound("COLLAB_REQUEST_NOT_FOUND", "好友申请不存在或已失效。")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE donebot_friend_requests
		SET status = CASE WHEN requester_id=$2 THEN 'cancelled' ELSE 'rejected' END, responded_at=NOW()
		WHERE id=$1::uuid AND status='pending' AND (requester_id=$2 OR recipient_id=$2)`, requestID, actor)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return infraerrors.NotFound("COLLAB_REQUEST_NOT_FOUND", "好友申请不存在或已处理。")
	}
	return nil
}

func (s *Service) RemoveFriend(ctx context.Context, actor, friendUserID int64) error {
	if err := s.available(); err != nil {
		return err
	}
	if actor <= 0 || friendUserID <= 0 || actor == friendUserID {
		return infraerrors.NotFound("COLLAB_FRIEND_NOT_FOUND", "好友不存在。")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	a, b := pairOrder(actor, friendUserID)
	res, err := tx.ExecContext(ctx, `DELETE FROM donebot_friends WHERE user_a=$1 AND user_b=$2`, a, b)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return infraerrors.NotFound("COLLAB_FRIEND_NOT_FOUND", "好友不存在。")
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM donebot_friend_remarks WHERE (owner_id=$1 AND friend_id=$2) OR (owner_id=$2 AND friend_id=$1)`, actor, friendUserID); err != nil {
		return err
	}

	// If they do not share an active team, drop direct user grants in both directions immediately.
	var sharedTeam bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM donebot_team_members m1
		JOIN donebot_team_members m2 ON m2.team_id=m1.team_id
		JOIN donebot_teams t ON t.id=m1.team_id AND t.state='active'
		WHERE m1.user_id=$1 AND m2.user_id=$2 AND m1.removed_at IS NULL AND m2.removed_at IS NULL
	)`, actor, friendUserID).Scan(&sharedTeam); err != nil {
		return err
	}
	if !sharedTeam {
		actorStr := strconv.FormatInt(actor, 10)
		friendStr := strconv.FormatInt(friendUserID, 10)
		if _, err = tx.ExecContext(ctx, `DELETE FROM donebot_collab_folder_grants g USING donebot_collab_folders f
			WHERE g.folder_id=f.id AND g.subject_type='user' AND (
				(f.owner_id=$1 AND g.subject_id=$2) OR (f.owner_id=$3 AND g.subject_id=$4)
			)`, actor, friendStr, friendUserID, actorStr); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM donebot_collab_bot_grants g USING donebot_collab_bots b
			WHERE g.bot_id=b.id AND g.subject_type='user' AND (
				(b.owner_id=$1 AND g.subject_id=$2) OR (b.owner_id=$3 AND g.subject_id=$4)
			)`, actor, friendStr, friendUserID, actorStr); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetFriendRemark updates or clears actor's private remark name for an existing friend.
func (s *Service) SetFriendRemark(ctx context.Context, actor, friendUserID int64, rawRemark string) (string, error) {
	if err := s.available(); err != nil {
		return "", err
	}
	if actor <= 0 || friendUserID <= 0 || actor == friendUserID {
		return "", infraerrors.NotFound("COLLAB_FRIEND_NOT_FOUND", "好友不存在。")
	}
	remark, err := cleanOptionalText(rawRemark, 80, "COLLAB_REMARK_INVALID", "备注名称不能超过 80 个字符。")
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(remark, "\n\r\t") {
		return "", infraerrors.BadRequest("COLLAB_REMARK_INVALID", "备注名称不能包含换行符。")
	}
	a, b := pairOrder(actor, friendUserID)
	var isFriend bool
	if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donebot_friends WHERE user_a=$1 AND user_b=$2)`, a, b).Scan(&isFriend); err != nil {
		return "", err
	}
	if !isFriend {
		return "", infraerrors.NotFound("COLLAB_FRIEND_NOT_FOUND", "对方还不是你的好友，无法设置备注。")
	}
	if remark == "" {
		if _, err = s.db.ExecContext(ctx, `DELETE FROM donebot_friend_remarks WHERE owner_id=$1 AND friend_id=$2`, actor, friendUserID); err != nil {
			return "", err
		}
		return "", nil
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO donebot_friend_remarks(owner_id, friend_id, remark, updated_at)
		VALUES($1, $2, $3, NOW())
		ON CONFLICT(owner_id, friend_id) DO UPDATE SET remark=EXCLUDED.remark, updated_at=NOW()`, actor, friendUserID, remark); err != nil {
		return "", err
	}
	return remark, nil
}

// LookupUserProfiles fetches public profile fields (nickname, avatar_url) and actor's private friend remark for a set of users.
func (s *Service) LookupUserProfiles(ctx context.Context, actor int64, userIDs []int64) (map[int64]UserProfile, error) {
	out := map[int64]UserProfile{}
	if !s.Enabled() || actor <= 0 || len(userIDs) == 0 {
		return out, nil
	}
	seen := map[int64]struct{}{}
	args := []any{actor}
	placeholders := make([]string, 0, len(userIDs))
	for _, id := range userIDs {
		if id <= 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		args = append(args, id)
		placeholders = append(placeholders, "$"+strconv.Itoa(len(args)))
	}
	if len(placeholders) == 0 {
		return out, nil
	}
	query := `SELECT u.id, u.email, COALESCE(u.username, ''), COALESCE(ua.url, ''), COALESCE(rm.remark, '')
		FROM users u
		LEFT JOIN user_avatars ua ON ua.user_id = u.id
		LEFT JOIN donebot_friend_remarks rm ON rm.owner_id = $1 AND rm.friend_id = u.id
		WHERE u.id IN (` + strings.Join(placeholders, ",") + `) AND u.deleted_at IS NULL`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p UserProfile
		if err = rows.Scan(&p.UserID, &p.Email, &p.Nickname, &p.AvatarURL, &p.Remark); err != nil {
			return out, err
		}
		out[p.UserID] = p
	}
	return out, rows.Err()
}

// --- Shared Folders & ACL ---

type FolderGrantInput struct {
	SubjectType string `json:"subject_type"` // "user" | "team"
	SubjectID   string `json:"subject_id"`
	Access      string `json:"access"` // "read" | "write"
}

type FolderGrant struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	Label       string `json:"label"`
	Access      string `json:"access"`
}

type OwnedFolder struct {
	ID             string        `json:"id"`
	InstallationID string        `json:"installation_id"`
	DeviceName     string        `json:"device_name"`
	Name           string        `json:"name"`
	Enabled        bool          `json:"enabled"`
	Online         bool          `json:"online"`
	Grants         []FolderGrant `json:"grants"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

type CollabSource struct {
	Kind     string `json:"kind"` // "friend" | "team"
	TeamID   string `json:"team_id,omitempty"`
	TeamName string `json:"team_name,omitempty"`
}

type SharedFolder struct {
	ID             string         `json:"id"`
	OwnerUserID    int64          `json:"owner_user_id"`
	OwnerEmail     string         `json:"owner_email"`
	OwnerNickname  string         `json:"owner_nickname,omitempty"`
	OwnerAvatarURL string         `json:"owner_avatar_url,omitempty"`
	OwnerRemark    string         `json:"owner_remark,omitempty"`
	DeviceName     string         `json:"device_name"`
	Name           string         `json:"name"`
	Access         string         `json:"access"` // "read" | "write"
	Online         bool           `json:"online"`
	Sources        []CollabSource `json:"sources"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type FoldersSnapshot struct {
	Owned        []OwnedFolder  `json:"owned"`
	SharedWithMe []SharedFolder `json:"shared_with_me"`
}

type SaveFolderInput struct {
	ID             string             `json:"id"`
	InstallationID string             `json:"installation_id"`
	DeviceName     string             `json:"device_name"`
	Name           string             `json:"name"`
	Enabled        bool               `json:"enabled"`
	Grants         []FolderGrantInput `json:"grants"`
}

type InvokeAuthorization struct {
	FolderID       string
	OwnerUserID    int64
	InstallationID string
	CallerUserID   int64
	CallerEmail    string
	GrantedAccess  string
}

var (
	allowedFolderReadActions  = map[string]bool{"list_files": true, "read_file": true}
	allowedFolderWriteActions = map[string]bool{"write_file": true, "edit_file": true, "append_file": true, "delete_file": true}
)

type queryExecer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func isConnectedUser(ctx context.Context, db queryExecer, actor, target int64) (bool, error) {
	if actor == target {
		return false, nil
	}
	a, b := pairOrder(actor, target)
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT (
		EXISTS(SELECT 1 FROM donebot_friends WHERE user_a=$1 AND user_b=$2)
		OR EXISTS(
			SELECT 1 FROM donebot_team_members m1
			JOIN donebot_team_members m2 ON m2.team_id=m1.team_id
			JOIN donebot_teams t ON t.id=m1.team_id AND t.state='active'
			WHERE m1.user_id=$3 AND m2.user_id=$4 AND m1.removed_at IS NULL AND m2.removed_at IS NULL
		)
	) AND EXISTS(SELECT 1 FROM users WHERE id=$4 AND deleted_at IS NULL AND status='active')`, a, b, actor, target).Scan(&ok)
	return ok, err
}

func isActiveTeamMember(ctx context.Context, db queryExecer, actor int64, teamID string) (bool, error) {
	if !validUUID(teamID) {
		return false, nil
	}
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM donebot_team_members m
		JOIN donebot_teams t ON t.id=m.team_id AND t.state='active'
		WHERE m.team_id=$1::uuid AND m.user_id=$2 AND m.removed_at IS NULL
	)`, teamID, actor).Scan(&ok)
	return ok, err
}

func (s *Service) ListFolders(ctx context.Context, actor int64) (*FoldersSnapshot, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	out := &FoldersSnapshot{
		Owned:        []OwnedFolder{},
		SharedWithMe: []SharedFolder{},
	}

	// 1. Owned folders
	ownedIdx := map[string]int{}
	rows, err := s.db.QueryContext(ctx, `SELECT f.id::text, f.installation_id::text, f.device_name, f.name, f.enabled, f.updated_at
		FROM donebot_collab_folders f
		JOIN donebot_installs i ON i.installation_id=f.installation_id AND i.user_id=f.owner_id
			AND i.status='active' AND (i.expires_at IS NULL OR i.expires_at>clock_timestamp())
		WHERE f.owner_id=$1
		ORDER BY f.updated_at DESC, f.id`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item OwnedFolder
		if err = rows.Scan(&item.ID, &item.InstallationID, &item.DeviceName, &item.Name, &item.Enabled, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.Online = item.Enabled && s.relay.IsOnline(actor, item.InstallationID)
		item.Grants = []FolderGrant{}
		ownedIdx[item.ID] = len(out.Owned)
		out.Owned = append(out.Owned, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	if len(out.Owned) > 0 {
		gRows, err := s.db.QueryContext(ctx, `SELECT g.folder_id::text, g.subject_type, g.subject_id, g.access,
			COALESCE(NULLIF(rm.remark, ''), NULLIF(u.username, ''), u.email, t.name, '') AS label
			FROM donebot_collab_folder_grants g
			JOIN donebot_collab_folders f ON f.id=g.folder_id
			LEFT JOIN users u ON g.subject_type='user' AND u.id::text=g.subject_id AND u.deleted_at IS NULL AND u.status='active'
			LEFT JOIN donebot_friend_remarks rm ON g.subject_type='user' AND rm.owner_id=$1 AND rm.friend_id=u.id
			LEFT JOIN donebot_teams t ON g.subject_type='team' AND t.id::text=g.subject_id AND t.state='active'
			WHERE f.owner_id=$1 AND (
				(g.subject_type='user' AND u.id IS NOT NULL)
				OR (g.subject_type='team' AND t.id IS NOT NULL AND EXISTS(
					SELECT 1 FROM donebot_team_members m WHERE m.team_id=t.id AND m.user_id=$1 AND m.removed_at IS NULL
				))
			)
			ORDER BY g.subject_type, lower(COALESCE(NULLIF(rm.remark, ''), NULLIF(u.username, ''), u.email, t.name, '')), g.subject_id`, actor)
		if err != nil {
			return nil, err
		}
		defer gRows.Close()
		for gRows.Next() {
			var folderID string
			var grant FolderGrant
			if err = gRows.Scan(&folderID, &grant.SubjectType, &grant.SubjectID, &grant.Access, &grant.Label); err != nil {
				return nil, err
			}
			if idx, ok := ownedIdx[folderID]; ok {
				out.Owned[idx].Grants = append(out.Owned[idx].Grants, grant)
			}
		}
		if err = gRows.Err(); err != nil {
			return nil, err
		}
	}

	// 2. Shared with me (real-time friend OR shared active team enforcement)
	shared, err := s.querySharedFolders(ctx, actor)
	if err != nil {
		return nil, err
	}
	out.SharedWithMe = shared
	return out, nil
}

func (s *Service) querySharedFolders(ctx context.Context, actor int64) ([]SharedFolder, error) {
	actorStr := strconv.FormatInt(actor, 10)
	rows, err := s.db.QueryContext(ctx, `SELECT f.id::text, f.owner_id, ou.email, COALESCE(ou.username, ''), COALESCE(oua.url, ''), COALESCE(rm.remark, ''),
		f.installation_id::text, f.device_name, f.name, f.updated_at,
		g.subject_type, g.access, COALESCE(t.id::text, ''), COALESCE(t.name, ''),
		EXISTS(SELECT 1 FROM donebot_friends fr WHERE fr.user_a=LEAST(f.owner_id,$1) AND fr.user_b=GREATEST(f.owner_id,$1)) AS is_friend
		FROM donebot_collab_folders f
		JOIN users ou ON ou.id=f.owner_id AND ou.deleted_at IS NULL AND ou.status='active'
		LEFT JOIN user_avatars oua ON oua.user_id=ou.id
		LEFT JOIN donebot_friend_remarks rm ON rm.owner_id=$1 AND rm.friend_id=ou.id
		JOIN donebot_installs i ON i.installation_id=f.installation_id AND i.user_id=f.owner_id
			AND i.status='active' AND (i.expires_at IS NULL OR i.expires_at>clock_timestamp())
		JOIN donebot_collab_folder_grants g ON g.folder_id=f.id
		LEFT JOIN donebot_teams t ON g.subject_type='team' AND t.id::text=g.subject_id AND t.state='active'
		WHERE f.enabled=TRUE AND f.owner_id<>$1 AND (
			(g.subject_type='user' AND g.subject_id=$2 AND (
				EXISTS(SELECT 1 FROM donebot_friends fr WHERE fr.user_a=LEAST(f.owner_id,$1) AND fr.user_b=GREATEST(f.owner_id,$1))
				OR EXISTS(
					SELECT 1 FROM donebot_team_members m1
					JOIN donebot_team_members m2 ON m2.team_id=m1.team_id
					JOIN donebot_teams st ON st.id=m1.team_id AND st.state='active'
					WHERE m1.user_id=f.owner_id AND m2.user_id=$1 AND m1.removed_at IS NULL AND m2.removed_at IS NULL
				)
			))
			OR (g.subject_type='team' AND t.id IS NOT NULL
				AND EXISTS(SELECT 1 FROM donebot_team_members m1 WHERE m1.team_id=t.id AND m1.user_id=f.owner_id AND m1.removed_at IS NULL)
				AND EXISTS(SELECT 1 FROM donebot_team_members m2 WHERE m2.team_id=t.id AND m2.user_id=$1 AND m2.removed_at IS NULL)
			)
		)
		ORDER BY f.updated_at DESC, f.id`, actor, actorStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type folderAgg struct {
		item    SharedFolder
		sources map[string]CollabSource
	}
	order := []string{}
	byID := map[string]*folderAgg{}
	for rows.Next() {
		var id, ownerEmail, ownerNickname, ownerAvatarURL, ownerRemark, installID, deviceName, name, subjectType, access, teamID, teamName string
		var ownerID int64
		var updatedAt time.Time
		var isFriend bool
		if err = rows.Scan(&id, &ownerID, &ownerEmail, &ownerNickname, &ownerAvatarURL, &ownerRemark, &installID, &deviceName, &name, &updatedAt, &subjectType, &access, &teamID, &teamName, &isFriend); err != nil {
			return nil, err
		}
		agg, ok := byID[id]
		if !ok {
			agg = &folderAgg{
				item: SharedFolder{
					ID:             id,
					OwnerUserID:    ownerID,
					OwnerEmail:     ownerEmail,
					OwnerNickname:  ownerNickname,
					OwnerAvatarURL: ownerAvatarURL,
					OwnerRemark:    ownerRemark,
					DeviceName:     deviceName,
					Name:           name,
					Access:         "read",
					Online:         s.relay.IsOnline(ownerID, installID),
					UpdatedAt:      updatedAt,
				},
				sources: map[string]CollabSource{},
			}
			byID[id] = agg
			order = append(order, id)
		}
		if access == "write" {
			agg.item.Access = "write"
		}
		if subjectType == "team" && teamID != "" {
			agg.sources["team:"+teamID] = CollabSource{Kind: "team", TeamID: teamID, TeamName: teamName}
		} else if isFriend {
			agg.sources["friend"] = CollabSource{Kind: "friend"}
		} else {
			agg.sources["team_direct"] = CollabSource{Kind: "team"}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]SharedFolder, 0, len(order))
	for _, id := range order {
		agg := byID[id]
		sources := make([]CollabSource, 0, len(agg.sources))
		for _, src := range agg.sources {
			sources = append(sources, src)
		}
		sort.Slice(sources, func(i, j int) bool {
			if sources[i].Kind != sources[j].Kind {
				return sources[i].Kind < sources[j].Kind
			}
			return sources[i].TeamName < sources[j].TeamName
		})
		agg.item.Sources = sources
		out = append(out, agg.item)
	}
	return out, nil
}

func (s *Service) SaveFolder(ctx context.Context, actor int64, in SaveFolderInput) (*OwnedFolder, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	if !validUUID(in.InstallationID) {
		return nil, infraerrors.BadRequest("COLLAB_INSTALL_INVALID", "无效的设备标识。")
	}
	name, err := cleanText(in.Name, 120, "COLLAB_FOLDER_NAME_INVALID", "文件夹名称需为 1-120 个字符。")
	if err != nil {
		return nil, err
	}
	deviceName, err := cleanText(in.DeviceName, 100, "COLLAB_DEVICE_NAME_INVALID", "设备名称无效。")
	if err != nil {
		return nil, err
	}
	if len(in.Grants) > MaxGrantsPerItem {
		return nil, infraerrors.BadRequest("COLLAB_TOO_MANY_GRANTS", "单个文件夹最多配置 200 条权限规则。")
	}
	folderID := in.ID
	if folderID == "" {
		folderID = uuid.NewString()
	} else if !validUUID(folderID) {
		return nil, infraerrors.BadRequest("COLLAB_FOLDER_ID_INVALID", "无效的共享文件夹标识。")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var installActive bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM donebot_installs WHERE installation_id=$1::uuid AND user_id=$2
		AND status='active' AND (expires_at IS NULL OR expires_at>clock_timestamp())
	)`, in.InstallationID, actor).Scan(&installActive); err != nil {
		return nil, err
	}
	if !installActive {
		return nil, infraerrors.Forbidden("COLLAB_INSTALL_INACTIVE", "当前设备安装凭据无效，请重新登录。")
	}

	var existingOwner int64
	var existingInstall string
	err = tx.QueryRowContext(ctx, `SELECT owner_id, installation_id::text FROM donebot_collab_folders WHERE id=$1::uuid FOR UPDATE`, folderID).Scan(&existingOwner, &existingInstall)
	if err == nil {
		if existingOwner != actor {
			return nil, infraerrors.NotFound("COLLAB_FOLDER_NOT_FOUND", "共享文件夹不存在。")
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_collab_folders WHERE owner_id=$1`, actor).Scan(&count); err != nil {
			return nil, err
		}
		if count >= MaxFoldersPerUser {
			return nil, infraerrors.BadRequest("COLLAB_FOLDER_LIMIT", "最多共享 50 个文件夹。")
		}
	} else {
		return nil, err
	}

	dedup := map[string]FolderGrantInput{}
	for _, g := range in.Grants {
		if g.Access != "read" && g.Access != "write" {
			return nil, infraerrors.BadRequest("COLLAB_ACCESS_INVALID", "权限必须为只读（read）或可编辑（write）。")
		}
		switch g.SubjectType {
		case "user":
			uid, parseErr := strconv.ParseInt(g.SubjectID, 10, 64)
			if parseErr != nil || uid <= 0 || uid == actor {
				return nil, infraerrors.BadRequest("COLLAB_SUBJECT_INVALID", "无效的授权成员。")
			}
			ok, checkErr := isConnectedUser(ctx, tx, actor, uid)
			if checkErr != nil {
				return nil, checkErr
			}
			if !ok {
				return nil, infraerrors.Forbidden("COLLAB_NOT_CONNECTED", "只能向好友或同团队成员授权共享文件夹。")
			}
			norm := strconv.FormatInt(uid, 10)
			key := "user:" + norm
			if prev, exists := dedup[key]; !exists || prev.Access != "write" {
				dedup[key] = FolderGrantInput{SubjectType: "user", SubjectID: norm, Access: g.Access}
			}
		case "team":
			ok, checkErr := isActiveTeamMember(ctx, tx, actor, g.SubjectID)
			if checkErr != nil {
				return nil, checkErr
			}
			if !ok {
				return nil, infraerrors.Forbidden("COLLAB_TEAM_NOT_FOUND", "只能向你所属的活跃团队授权。")
			}
			key := "team:" + g.SubjectID
			if prev, exists := dedup[key]; !exists || prev.Access != "write" {
				dedup[key] = FolderGrantInput{SubjectType: "team", SubjectID: g.SubjectID, Access: g.Access}
			}
		default:
			return nil, infraerrors.BadRequest("COLLAB_SUBJECT_INVALID", "授权对象类型仅支持 user 或 team。")
		}
	}

	var updatedAt time.Time
	err = tx.QueryRowContext(ctx, `INSERT INTO donebot_collab_folders(id,owner_id,installation_id,device_name,name,enabled,updated_at)
		VALUES($1::uuid,$2,$3::uuid,$4,$5,$6,NOW())
		ON CONFLICT(id) DO UPDATE SET installation_id=EXCLUDED.installation_id,device_name=EXCLUDED.device_name,
			name=EXCLUDED.name,enabled=EXCLUDED.enabled,updated_at=NOW()
		WHERE donebot_collab_folders.owner_id=EXCLUDED.owner_id
		RETURNING updated_at`, folderID, actor, in.InstallationID, deviceName, name, in.Enabled).Scan(&updatedAt)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM donebot_collab_folder_grants WHERE folder_id=$1::uuid`, folderID); err != nil {
		return nil, err
	}
	for _, g := range dedup {
		if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_collab_folder_grants(folder_id,subject_type,subject_id,access)
			VALUES($1::uuid,$2,$3,$4)`, folderID, g.SubjectType, g.SubjectID, g.Access); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if !in.Enabled {
		s.relay.DropFolder(actor, in.InstallationID, folderID)
		if existingInstall != "" && existingInstall != in.InstallationID {
			s.relay.DropFolder(actor, existingInstall, folderID)
		}
	}
	snap, err := s.ListFolders(ctx, actor)
	if err != nil {
		return nil, err
	}
	for i := range snap.Owned {
		if snap.Owned[i].ID == folderID {
			return &snap.Owned[i], nil
		}
	}
	return &OwnedFolder{
		ID:             folderID,
		InstallationID: in.InstallationID,
		DeviceName:     deviceName,
		Name:           name,
		Enabled:        in.Enabled,
		Online:         in.Enabled && s.relay.IsOnline(actor, in.InstallationID),
		Grants:         []FolderGrant{},
		UpdatedAt:      updatedAt,
	}, nil
}

func (s *Service) DeleteFolder(ctx context.Context, actor int64, folderID string) error {
	if err := s.available(); err != nil {
		return err
	}
	if !validUUID(folderID) || actor <= 0 {
		return infraerrors.NotFound("COLLAB_FOLDER_NOT_FOUND", "共享文件夹不存在。")
	}
	var installationID string
	err := s.db.QueryRowContext(ctx, `DELETE FROM donebot_collab_folders WHERE id=$1::uuid AND owner_id=$2 RETURNING installation_id::text`,
		folderID, actor).Scan(&installationID)
	if errors.Is(err, sql.ErrNoRows) {
		return infraerrors.NotFound("COLLAB_FOLDER_NOT_FOUND", "共享文件夹不存在。")
	}
	if err != nil {
		return err
	}
	s.relay.DropFolder(actor, installationID, folderID)
	return nil
}

func (s *Service) AuthorizeInvoke(ctx context.Context, actor int64, folderID, action string) (*InvokeAuthorization, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if !validUUID(folderID) || actor <= 0 {
		return nil, infraerrors.NotFound("COLLAB_FOLDER_NOT_FOUND", "共享文件夹不存在或无权限访问。")
	}
	isRead := allowedFolderReadActions[action]
	isWrite := allowedFolderWriteActions[action]
	if !isRead && !isWrite {
		return nil, infraerrors.BadRequest("COLLAB_ACTION_FORBIDDEN", "不支持该文件操作类型。")
	}

	var callerEmail string
	if err := s.db.QueryRowContext(ctx, `SELECT email FROM users WHERE id=$1 AND deleted_at IS NULL AND status='active'`, actor).Scan(&callerEmail); err != nil {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Account is not active")
	}

	actorStr := strconv.FormatInt(actor, 10)
	var ownerID int64
	var installationID string
	var bestAccess string
	err := s.db.QueryRowContext(ctx, `SELECT f.owner_id, f.installation_id::text,
		CASE WHEN BOOL_OR(g.access='write') THEN 'write' ELSE 'read' END AS effective_access
		FROM donebot_collab_folders f
		JOIN users ou ON ou.id=f.owner_id AND ou.deleted_at IS NULL AND ou.status='active'
		JOIN donebot_installs i ON i.installation_id=f.installation_id AND i.user_id=f.owner_id
			AND i.status='active' AND (i.expires_at IS NULL OR i.expires_at>clock_timestamp())
		JOIN donebot_collab_folder_grants g ON g.folder_id=f.id
		LEFT JOIN donebot_teams t ON g.subject_type='team' AND t.id::text=g.subject_id AND t.state='active'
		WHERE f.id=$1::uuid AND f.enabled=TRUE AND f.owner_id<>$2 AND (
			(g.subject_type='user' AND g.subject_id=$3 AND (
				EXISTS(SELECT 1 FROM donebot_friends fr WHERE fr.user_a=LEAST(f.owner_id,$2) AND fr.user_b=GREATEST(f.owner_id,$2))
				OR EXISTS(
					SELECT 1 FROM donebot_team_members m1
					JOIN donebot_team_members m2 ON m2.team_id=m1.team_id
					JOIN donebot_teams st ON st.id=m1.team_id AND st.state='active'
					WHERE m1.user_id=f.owner_id AND m2.user_id=$2 AND m1.removed_at IS NULL AND m2.removed_at IS NULL
				)
			))
			OR (g.subject_type='team' AND t.id IS NOT NULL
				AND EXISTS(SELECT 1 FROM donebot_team_members m1 WHERE m1.team_id=t.id AND m1.user_id=f.owner_id AND m1.removed_at IS NULL)
				AND EXISTS(SELECT 1 FROM donebot_team_members m2 WHERE m2.team_id=t.id AND m2.user_id=$2 AND m2.removed_at IS NULL)
			)
		)
		GROUP BY f.owner_id, f.installation_id`, folderID, actor, actorStr).Scan(&ownerID, &installationID, &bestAccess)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.Forbidden("COLLAB_FOLDER_FORBIDDEN", "你没有访问该共享文件夹的权限，或对方已停止共享。")
	} else if err != nil {
		return nil, err
	}
	if isWrite && bestAccess != "write" {
		return nil, infraerrors.Forbidden("COLLAB_READ_ONLY", "你对该共享文件夹只有只读权限，无法修改或写入。")
	}
	return &InvokeAuthorization{
		FolderID:       folderID,
		OwnerUserID:    ownerID,
		InstallationID: installationID,
		CallerUserID:   actor,
		CallerEmail:    callerEmail,
		GrantedAccess:  bestAccess,
	}, nil
}

// --- Shared Bots (Capability Templates without Memory) ---

type BotGrantInput struct {
	SubjectType string `json:"subject_type"` // "user" | "team"
	SubjectID   string `json:"subject_id"`
}

type BotGrant struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	Label       string `json:"label"`
}

type OwnedSharedBot struct {
	ID          string     `json:"id"`
	SourceBotID string     `json:"source_bot_id"`
	Name        string     `json:"name"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	SkillCount  int        `json:"skill_count"`
	HasSoul     bool       `json:"has_soul"`
	Grants      []BotGrant `json:"grants"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type SharedBot struct {
	ID             string         `json:"id"`
	OwnerUserID    int64          `json:"owner_user_id"`
	OwnerEmail     string         `json:"owner_email"`
	OwnerNickname  string         `json:"owner_nickname,omitempty"`
	OwnerAvatarURL string         `json:"owner_avatar_url,omitempty"`
	OwnerRemark    string         `json:"owner_remark,omitempty"`
	Name           string         `json:"name"`
	Title          string         `json:"title"`
	Description    string         `json:"description"`
	SkillCount     int            `json:"skill_count"`
	HasSoul        bool           `json:"has_soul"`
	Sources        []CollabSource `json:"sources"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type SharedBotsSnapshot struct {
	Owned        []OwnedSharedBot `json:"owned"`
	SharedWithMe []SharedBot      `json:"shared_with_me"`
}

type SaveBotInput struct {
	ID          string          `json:"id"`
	SourceBotID string          `json:"source_bot_id"`
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	SkillCount  int             `json:"skill_count"`
	HasSoul     bool            `json:"has_soul"`
	Package     json.RawMessage `json:"package"`
	Grants      []BotGrantInput `json:"grants"`
}

type SharedBotPackage struct {
	ID          string          `json:"id"`
	OwnerUserID int64           `json:"owner_user_id"`
	OwnerEmail  string          `json:"owner_email"`
	Name        string          `json:"name"`
	Package     json.RawMessage `json:"package"`
}

func containsForbiddenMemoryField(value any) bool {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			switch strings.ToLower(key) {
			case "memory", "memories", "starternotes", "starter_notes", "dailylogs", "daily_logs", "messages", "threads":
				switch typed := child.(type) {
				case []any:
					if len(typed) > 0 {
						return true
					}
				case map[string]any:
					if len(typed) > 0 {
						return true
					}
				case string:
					if strings.TrimSpace(typed) != "" {
						return true
					}
				case nil:
				default:
					return true
				}
			}
			if containsForbiddenMemoryField(child) {
				return true
			}
		}
	case []any:
		for _, item := range node {
			if containsForbiddenMemoryField(item) {
				return true
			}
		}
	}
	return false
}

func (s *Service) ListBots(ctx context.Context, actor int64) (*SharedBotsSnapshot, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	out := &SharedBotsSnapshot{
		Owned:        []OwnedSharedBot{},
		SharedWithMe: []SharedBot{},
	}

	ownedIdx := map[string]int{}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text, source_bot_id, name, title, description, skill_count, has_soul, updated_at
		FROM donebot_collab_bots WHERE owner_id=$1 ORDER BY updated_at DESC, id`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item OwnedSharedBot
		if err = rows.Scan(&item.ID, &item.SourceBotID, &item.Name, &item.Title, &item.Description, &item.SkillCount, &item.HasSoul, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.Grants = []BotGrant{}
		ownedIdx[item.ID] = len(out.Owned)
		out.Owned = append(out.Owned, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	if len(out.Owned) > 0 {
		gRows, err := s.db.QueryContext(ctx, `SELECT g.bot_id::text, g.subject_type, g.subject_id,
			COALESCE(NULLIF(rm.remark, ''), NULLIF(u.username, ''), u.email, t.name, '') AS label
			FROM donebot_collab_bot_grants g
			JOIN donebot_collab_bots b ON b.id=g.bot_id
			LEFT JOIN users u ON g.subject_type='user' AND u.id::text=g.subject_id AND u.deleted_at IS NULL AND u.status='active'
			LEFT JOIN donebot_friend_remarks rm ON g.subject_type='user' AND rm.owner_id=$1 AND rm.friend_id=u.id
			LEFT JOIN donebot_teams t ON g.subject_type='team' AND t.id::text=g.subject_id AND t.state='active'
			WHERE b.owner_id=$1 AND (
				(g.subject_type='user' AND u.id IS NOT NULL)
				OR (g.subject_type='team' AND t.id IS NOT NULL AND EXISTS(
					SELECT 1 FROM donebot_team_members m WHERE m.team_id=t.id AND m.user_id=$1 AND m.removed_at IS NULL
				))
			)
			ORDER BY g.subject_type, lower(COALESCE(NULLIF(rm.remark, ''), NULLIF(u.username, ''), u.email, t.name, '')), g.subject_id`, actor)
		if err != nil {
			return nil, err
		}
		defer gRows.Close()
		for gRows.Next() {
			var botID string
			var grant BotGrant
			if err = gRows.Scan(&botID, &grant.SubjectType, &grant.SubjectID, &grant.Label); err != nil {
				return nil, err
			}
			if idx, ok := ownedIdx[botID]; ok {
				out.Owned[idx].Grants = append(out.Owned[idx].Grants, grant)
			}
		}
		if err = gRows.Err(); err != nil {
			return nil, err
		}
	}

	actorStr := strconv.FormatInt(actor, 10)
	sRows, err := s.db.QueryContext(ctx, `SELECT b.id::text, b.owner_id, ou.email, COALESCE(ou.username, ''), COALESCE(oua.url, ''), COALESCE(rm.remark, ''),
		b.name, b.title, b.description, b.skill_count, b.has_soul, b.updated_at,
		g.subject_type, COALESCE(t.id::text, ''), COALESCE(t.name, ''),
		EXISTS(SELECT 1 FROM donebot_friends fr WHERE fr.user_a=LEAST(b.owner_id,$1) AND fr.user_b=GREATEST(b.owner_id,$1)) AS is_friend
		FROM donebot_collab_bots b
		JOIN users ou ON ou.id=b.owner_id AND ou.deleted_at IS NULL AND ou.status='active'
		LEFT JOIN user_avatars oua ON oua.user_id=ou.id
		LEFT JOIN donebot_friend_remarks rm ON rm.owner_id=$1 AND rm.friend_id=ou.id
		JOIN donebot_collab_bot_grants g ON g.bot_id=b.id
		LEFT JOIN donebot_teams t ON g.subject_type='team' AND t.id::text=g.subject_id AND t.state='active'
		WHERE b.owner_id<>$1 AND (
			(g.subject_type='user' AND g.subject_id=$2 AND (
				EXISTS(SELECT 1 FROM donebot_friends fr WHERE fr.user_a=LEAST(b.owner_id,$1) AND fr.user_b=GREATEST(b.owner_id,$1))
				OR EXISTS(
					SELECT 1 FROM donebot_team_members m1
					JOIN donebot_team_members m2 ON m2.team_id=m1.team_id
					JOIN donebot_teams st ON st.id=m1.team_id AND st.state='active'
					WHERE m1.user_id=b.owner_id AND m2.user_id=$1 AND m1.removed_at IS NULL AND m2.removed_at IS NULL
				)
			))
			OR (g.subject_type='team' AND t.id IS NOT NULL
				AND EXISTS(SELECT 1 FROM donebot_team_members m1 WHERE m1.team_id=t.id AND m1.user_id=b.owner_id AND m1.removed_at IS NULL)
				AND EXISTS(SELECT 1 FROM donebot_team_members m2 WHERE m2.team_id=t.id AND m2.user_id=$1 AND m2.removed_at IS NULL)
			)
		)
		ORDER BY b.updated_at DESC, b.id`, actor, actorStr)
	if err != nil {
		return nil, err
	}
	defer sRows.Close()

	type botAgg struct {
		item    SharedBot
		sources map[string]CollabSource
	}
	order := []string{}
	byID := map[string]*botAgg{}
	for sRows.Next() {
		var id, ownerEmail, ownerNickname, ownerAvatarURL, ownerRemark, name, title, description, subjectType, teamID, teamName string
		var ownerID int64
		var skillCount int
		var hasSoul, isFriend bool
		var updatedAt time.Time
		if err = sRows.Scan(&id, &ownerID, &ownerEmail, &ownerNickname, &ownerAvatarURL, &ownerRemark, &name, &title, &description, &skillCount, &hasSoul, &updatedAt, &subjectType, &teamID, &teamName, &isFriend); err != nil {
			return nil, err
		}
		agg, ok := byID[id]
		if !ok {
			agg = &botAgg{
				item: SharedBot{
					ID:             id,
					OwnerUserID:    ownerID,
					OwnerEmail:     ownerEmail,
					OwnerNickname:  ownerNickname,
					OwnerAvatarURL: ownerAvatarURL,
					OwnerRemark:    ownerRemark,
					Name:           name,
					Title:          title,
					Description:    description,
					SkillCount:     skillCount,
					HasSoul:        hasSoul,
					UpdatedAt:      updatedAt,
				},
				sources: map[string]CollabSource{},
			}
			byID[id] = agg
			order = append(order, id)
		}
		if subjectType == "team" && teamID != "" {
			agg.sources["team:"+teamID] = CollabSource{Kind: "team", TeamID: teamID, TeamName: teamName}
		} else if isFriend {
			agg.sources["friend"] = CollabSource{Kind: "friend"}
		} else {
			agg.sources["team_direct"] = CollabSource{Kind: "team"}
		}
	}
	if err = sRows.Err(); err != nil {
		return nil, err
	}
	for _, id := range order {
		agg := byID[id]
		sources := make([]CollabSource, 0, len(agg.sources))
		for _, src := range agg.sources {
			sources = append(sources, src)
		}
		sort.Slice(sources, func(i, j int) bool {
			if sources[i].Kind != sources[j].Kind {
				return sources[i].Kind < sources[j].Kind
			}
			return sources[i].TeamName < sources[j].TeamName
		})
		agg.item.Sources = sources
		out.SharedWithMe = append(out.SharedWithMe, agg.item)
	}
	return out, nil
}

func (s *Service) SaveBot(ctx context.Context, actor int64, in SaveBotInput) (*OwnedSharedBot, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	name, err := cleanText(in.Name, 120, "COLLAB_BOT_NAME_INVALID", "Bot 名称需为 1-120 个字符。")
	if err != nil {
		return nil, err
	}
	title, err := cleanOptionalText(in.Title, 200, "COLLAB_BOT_TITLE_INVALID", "Bot 定位不能超过 200 个字符。")
	if err != nil {
		return nil, err
	}
	description, err := cleanOptionalText(in.Description, 1000, "COLLAB_BOT_DESC_INVALID", "Bot 简介不能超过 1000 个字符。")
	if err != nil {
		return nil, err
	}
	sourceBotID, err := cleanOptionalText(in.SourceBotID, 128, "COLLAB_BOT_SOURCE_INVALID", "无效的 Bot 标识。")
	if err != nil {
		return nil, err
	}
	if in.SkillCount < 0 || in.SkillCount > 500 {
		return nil, infraerrors.BadRequest("COLLAB_BOT_SKILLS_INVALID", "技能数量无效。")
	}
	if len(in.Grants) > MaxGrantsPerItem {
		return nil, infraerrors.BadRequest("COLLAB_TOO_MANY_GRANTS", "单个共享 Bot 最多配置 200 条授权。")
	}
	if len(in.Package) > MaxBotPackageSize {
		return nil, infraerrors.BadRequest("COLLAB_BOT_PACKAGE_TOO_LARGE", "共享 Bot 包不能超过 2 MB。")
	}
	var decoded map[string]any
	if len(in.Package) > 0 {
		if err = json.Unmarshal(in.Package, &decoded); err != nil || decoded == nil {
			return nil, infraerrors.BadRequest("COLLAB_BOT_PACKAGE_INVALID", "共享 Bot 包格式无效。")
		}
		if containsForbiddenMemoryField(decoded) {
			return nil, infraerrors.BadRequest("COLLAB_BOT_MEMORY_FORBIDDEN", "共享 Bot 不允许携带个人记忆或历史对话。")
		}
	}

	botID := in.ID
	if botID == "" {
		botID = uuid.NewString()
	} else if !validUUID(botID) {
		return nil, infraerrors.BadRequest("COLLAB_BOT_ID_INVALID", "无效的共享 Bot 标识。")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var existingOwner int64
	var existingPkg json.RawMessage
	err = tx.QueryRowContext(ctx, `SELECT owner_id, package_json FROM donebot_collab_bots WHERE id=$1::uuid FOR UPDATE`, botID).Scan(&existingOwner, &existingPkg)
	if err == nil {
		if existingOwner != actor {
			return nil, infraerrors.NotFound("COLLAB_BOT_NOT_FOUND", "共享 Bot 不存在。")
		}
		if len(in.Package) == 0 {
			in.Package = existingPkg
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		if len(in.Package) == 0 {
			return nil, infraerrors.BadRequest("COLLAB_BOT_PACKAGE_REQUIRED", "需要提供 Bot 能力配置包。")
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_collab_bots WHERE owner_id=$1`, actor).Scan(&count); err != nil {
			return nil, err
		}
		if count >= MaxBotsPerUser {
			return nil, infraerrors.BadRequest("COLLAB_BOT_LIMIT", "最多共享 50 个 Bot。")
		}
	} else {
		return nil, err
	}

	dedup := map[string]BotGrantInput{}
	for _, g := range in.Grants {
		switch g.SubjectType {
		case "user":
			uid, parseErr := strconv.ParseInt(g.SubjectID, 10, 64)
			if parseErr != nil || uid <= 0 || uid == actor {
				return nil, infraerrors.BadRequest("COLLAB_SUBJECT_INVALID", "无效的授权成员。")
			}
			ok, checkErr := isConnectedUser(ctx, tx, actor, uid)
			if checkErr != nil {
				return nil, checkErr
			}
			if !ok {
				return nil, infraerrors.Forbidden("COLLAB_NOT_CONNECTED", "只能向好友或同团队成员共享 Bot。")
			}
			norm := strconv.FormatInt(uid, 10)
			dedup["user:"+norm] = BotGrantInput{SubjectType: "user", SubjectID: norm}
		case "team":
			ok, checkErr := isActiveTeamMember(ctx, tx, actor, g.SubjectID)
			if checkErr != nil {
				return nil, checkErr
			}
			if !ok {
				return nil, infraerrors.Forbidden("COLLAB_TEAM_NOT_FOUND", "只能向你所属的活跃团队共享 Bot。")
			}
			dedup["team:"+g.SubjectID] = BotGrantInput{SubjectType: "team", SubjectID: g.SubjectID}
		default:
			return nil, infraerrors.BadRequest("COLLAB_SUBJECT_INVALID", "授权对象类型仅支持 user 或 team。")
		}
	}

	var updatedAt time.Time
	err = tx.QueryRowContext(ctx, `INSERT INTO donebot_collab_bots(id,owner_id,source_bot_id,name,title,description,skill_count,has_soul,package_json,updated_at)
		VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,NOW())
		ON CONFLICT(id) DO UPDATE SET source_bot_id=EXCLUDED.source_bot_id,name=EXCLUDED.name,title=EXCLUDED.title,
			description=EXCLUDED.description,skill_count=EXCLUDED.skill_count,has_soul=EXCLUDED.has_soul,
			package_json=EXCLUDED.package_json,updated_at=NOW()
		WHERE donebot_collab_bots.owner_id=EXCLUDED.owner_id
		RETURNING updated_at`, botID, actor, sourceBotID, name, title, description, in.SkillCount, in.HasSoul, string(in.Package)).Scan(&updatedAt)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM donebot_collab_bot_grants WHERE bot_id=$1::uuid`, botID); err != nil {
		return nil, err
	}
	for _, g := range dedup {
		if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_collab_bot_grants(bot_id,subject_type,subject_id)
			VALUES($1::uuid,$2,$3)`, botID, g.SubjectType, g.SubjectID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}

	snap, err := s.ListBots(ctx, actor)
	if err != nil {
		return nil, err
	}
	for i := range snap.Owned {
		if snap.Owned[i].ID == botID {
			return &snap.Owned[i], nil
		}
	}
	return &OwnedSharedBot{
		ID:          botID,
		SourceBotID: sourceBotID,
		Name:        name,
		Title:       title,
		Description: description,
		SkillCount:  in.SkillCount,
		HasSoul:     in.HasSoul,
		Grants:      []BotGrant{},
		UpdatedAt:   updatedAt,
	}, nil
}

func (s *Service) GetBotPackage(ctx context.Context, actor int64, botID string) (*SharedBotPackage, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if !validUUID(botID) || actor <= 0 {
		return nil, infraerrors.NotFound("COLLAB_BOT_NOT_FOUND", "共享 Bot 不存在或无权限查看。")
	}
	actorStr := strconv.FormatInt(actor, 10)
	var out SharedBotPackage
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT b.id::text, b.owner_id, ou.email, b.name, b.package_json::text
		FROM donebot_collab_bots b
		JOIN users ou ON ou.id=b.owner_id AND ou.deleted_at IS NULL AND ou.status='active'
		WHERE b.id=$1::uuid AND (
			b.owner_id=$2
			OR EXISTS(
				SELECT 1 FROM donebot_collab_bot_grants g
				LEFT JOIN donebot_teams t ON g.subject_type='team' AND t.id::text=g.subject_id AND t.state='active'
				WHERE g.bot_id=b.id AND (
					(g.subject_type='user' AND g.subject_id=$3 AND (
						EXISTS(SELECT 1 FROM donebot_friends fr WHERE fr.user_a=LEAST(b.owner_id,$2) AND fr.user_b=GREATEST(b.owner_id,$2))
						OR EXISTS(
							SELECT 1 FROM donebot_team_members m1
							JOIN donebot_team_members m2 ON m2.team_id=m1.team_id
							JOIN donebot_teams st ON st.id=m1.team_id AND st.state='active'
							WHERE m1.user_id=b.owner_id AND m2.user_id=$2 AND m1.removed_at IS NULL AND m2.removed_at IS NULL
						)
					))
					OR (g.subject_type='team' AND t.id IS NOT NULL
						AND EXISTS(SELECT 1 FROM donebot_team_members m1 WHERE m1.team_id=t.id AND m1.user_id=b.owner_id AND m1.removed_at IS NULL)
						AND EXISTS(SELECT 1 FROM donebot_team_members m2 WHERE m2.team_id=t.id AND m2.user_id=$2 AND m2.removed_at IS NULL)
					)
				)
			)
		)`, botID, actor, actorStr).Scan(&out.ID, &out.OwnerUserID, &out.OwnerEmail, &out.Name, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.Forbidden("COLLAB_BOT_FORBIDDEN", "你没有权限获取该共享 Bot，或对方已停止共享。")
	} else if err != nil {
		return nil, err
	}
	out.Package = json.RawMessage(raw)
	return &out, nil
}

func (s *Service) DeleteBot(ctx context.Context, actor int64, botID string) error {
	if err := s.available(); err != nil {
		return err
	}
	if !validUUID(botID) || actor <= 0 {
		return infraerrors.NotFound("COLLAB_BOT_NOT_FOUND", "共享 Bot 不存在。")
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM donebot_collab_bots WHERE id=$1::uuid AND owner_id=$2`, botID, actor)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return infraerrors.NotFound("COLLAB_BOT_NOT_FOUND", "共享 Bot 不存在。")
	}
	return nil
}

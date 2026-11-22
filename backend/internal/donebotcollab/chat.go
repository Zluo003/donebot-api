package donebotcollab

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
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
	MaxChatBodyRunes     = 8000
	MaxChatCaptionRunes  = 2000
	MaxChatFileNameRunes = 200
	MaxChatFileBytes     = MaxTransferBytes
	defaultChatPageLimit = 60
	maxChatPageLimit     = 200
)

type ChatMessage struct {
	ID              int64     `json:"id"`
	ScopeType       string    `json:"scope_type"`
	TargetID        string    `json:"target_id"`
	SenderID        int64     `json:"sender_id"`
	SenderEmail     string    `json:"sender_email"`
	SenderNickname  string    `json:"sender_nickname,omitempty"`
	SenderAvatarURL string    `json:"sender_avatar_url,omitempty"`
	SenderRemark    string    `json:"sender_remark,omitempty"`
	Kind            string    `json:"kind"` // "text" | "file"
	Body            string    `json:"body"`
	FileID          string    `json:"file_id,omitempty"`
	FileName        string    `json:"file_name,omitempty"`
	FileMime        string    `json:"file_mime,omitempty"`
	FileSize        int64     `json:"file_size,omitempty"`
	FileSha256      string    `json:"file_sha256,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type ChatSummary struct {
	ScopeType      string     `json:"scope_type"` // "friend" | "team"
	TargetID       string     `json:"target_id"`
	LastMessageID  int64      `json:"last_message_id"`
	LastKind       string     `json:"last_kind,omitempty"`
	LastBody       string     `json:"last_body,omitempty"`
	LastFileName   string     `json:"last_file_name,omitempty"`
	LastSenderID   int64      `json:"last_sender_id,omitempty"`
	LastSenderName string     `json:"last_sender_name,omitempty"`
	LastCreatedAt  *time.Time `json:"last_created_at,omitempty"`
	UnreadCount    int        `json:"unread_count"`
}

type ChatFileMeta struct {
	FileID     string
	FileName   string
	FileMime   string
	FileSize   int64
	FileSha256 string
}

var safeInlineImageMimes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
	"image/gif":  true,
}

// IsSafeInlineChatMime reports whether a chat attachment MIME type is safe to render inline.
func IsSafeInlineChatMime(mimeType string) bool {
	base := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	return safeInlineImageMimes[base]
}

func friendScopeKey(a, b int64) string {
	lo, hi := pairOrder(a, b)
	return fmt.Sprintf("u:%d:%d", lo, hi)
}

func parseFriendScopeKey(key string) (int64, int64, bool) {
	if !strings.HasPrefix(key, "u:") {
		return 0, 0, false
	}
	parts := strings.Split(key[2:], ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	a, errA := strconv.ParseInt(parts[0], 10, 64)
	b, errB := strconv.ParseInt(parts[1], 10, 64)
	if errA != nil || errB != nil || a <= 0 || b <= 0 || a >= b {
		return 0, 0, false
	}
	return a, b, true
}

func cleanChatFileName(raw string) string {
	v := strings.TrimSpace(raw)
	v = strings.ReplaceAll(v, "\\", "/")
	v = path.Base(v)
	if v == "." || v == "/" || v == ".." {
		v = ""
	}
	v = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, v)
	v = strings.TrimSpace(v)
	if v == "" || !utf8.ValidString(v) {
		return "file"
	}
	if utf8.RuneCountInString(v) > MaxChatFileNameRunes {
		ext := path.Ext(v)
		if utf8.RuneCountInString(ext) > 20 {
			ext = ""
		}
		runes := []rune(strings.TrimSuffix(v, ext))
		keep := MaxChatFileNameRunes - utf8.RuneCountInString(ext)
		if keep < 1 {
			keep = 1
		}
		if len(runes) > keep {
			runes = runes[:keep]
		}
		v = string(runes) + ext
	}
	return v
}

func normalizeChatMime(rawMime, fileName string) string {
	candidate := strings.ToLower(strings.TrimSpace(strings.Split(rawMime, ";")[0]))
	if candidate == "" || candidate == "application/octet-stream" {
		if ext := strings.ToLower(path.Ext(fileName)); ext != "" {
			if guessed := mime.TypeByExtension(ext); guessed != "" {
				candidate = strings.ToLower(strings.TrimSpace(strings.Split(guessed, ";")[0]))
			}
		}
	}
	if candidate == "" || len(candidate) > 120 || !strings.Contains(candidate, "/") {
		return "application/octet-stream"
	}
	for _, r := range candidate {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "application/octet-stream"
		}
	}
	return candidate
}

func cleanChatText(raw string, maxRunes int, allowEmpty bool) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		if allowEmpty {
			return "", nil
		}
		return "", infraerrors.BadRequest("COLLAB_CHAT_EMPTY", "消息内容不能为空。")
	}
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) > maxRunes ||
		strings.IndexFunc(v, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 {
		return "", infraerrors.BadRequest("COLLAB_CHAT_INVALID", fmt.Sprintf("消息长度不能超过 %d 个字符。", maxRunes))
	}
	return v, nil
}

func (s *Service) authorizeChatScope(ctx context.Context, actor int64, scopeType, targetID string) (string, string, error) {
	if err := s.available(); err != nil {
		return "", "", err
	}
	if actor <= 0 {
		return "", "", infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	scopeType = strings.TrimSpace(scopeType)
	targetID = strings.TrimSpace(targetID)
	switch scopeType {
	case "friend":
		peerID, err := strconv.ParseInt(targetID, 10, 64)
		if err != nil || peerID <= 0 || peerID == actor {
			return "", "", infraerrors.NotFound("COLLAB_FRIEND_NOT_FOUND", "好友不存在。")
		}
		a, b := pairOrder(actor, peerID)
		var ok bool
		err = s.db.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM donebot_friends f
			JOIN users u ON u.id=$3 AND u.deleted_at IS NULL AND u.status='active'
			WHERE f.user_a=$1 AND f.user_b=$2
		)`, a, b, peerID).Scan(&ok)
		if err != nil {
			return "", "", err
		}
		if !ok {
			return "", "", infraerrors.Forbidden("COLLAB_CHAT_FORBIDDEN", "对方还不是你的好友，无法发送或查看消息。")
		}
		return "friend", friendScopeKey(actor, peerID), nil
	case "team":
		ok, err := isActiveTeamMember(ctx, s.db, actor, targetID)
		if err != nil {
			return "", "", err
		}
		if !ok {
			return "", "", infraerrors.Forbidden("COLLAB_CHAT_FORBIDDEN", "你不是该团队成员，无法访问团队聊天。")
		}
		return "team", targetID, nil
	default:
		return "", "", infraerrors.BadRequest("COLLAB_CHAT_SCOPE_INVALID", "不支持的聊天类型。")
	}
}

func (s *Service) verifyScopeKeyAccess(ctx context.Context, actor int64, scopeType, scopeKey string) error {
	switch scopeType {
	case "friend":
		a, b, ok := parseFriendScopeKey(scopeKey)
		if !ok || (actor != a && actor != b) {
			return infraerrors.Forbidden("COLLAB_CHAT_FORBIDDEN", "无权访问该文件。")
		}
		other := a
		if actor == a {
			other = b
		}
		_, _, err := s.authorizeChatScope(ctx, actor, "friend", strconv.FormatInt(other, 10))
		return err
	case "team":
		_, _, err := s.authorizeChatScope(ctx, actor, "team", scopeKey)
		return err
	default:
		return infraerrors.Forbidden("COLLAB_CHAT_FORBIDDEN", "无权访问该文件。")
	}
}

func (s *Service) upsertChatRead(ctx context.Context, q queryExecer, actor int64, scopeType, scopeKey string, lastReadID int64) error {
	if lastReadID < 0 {
		lastReadID = 0
	}
	_, err := q.ExecContext(ctx, `INSERT INTO donebot_collab_reads(user_id, scope_type, scope_key, last_read_id, updated_at)
		VALUES($1, $2, $3, $4, NOW())
		ON CONFLICT(user_id, scope_type, scope_key) DO UPDATE
		SET last_read_id = GREATEST(donebot_collab_reads.last_read_id, EXCLUDED.last_read_id),
		    updated_at = NOW()`, actor, scopeType, scopeKey, lastReadID)
	return err
}

func (s *Service) fetchChatMessageByID(ctx context.Context, actor int64, id int64, targetID string) (*ChatMessage, error) {
	var msg ChatMessage
	var fileID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT m.id, m.scope_type, m.sender_id, u.email, COALESCE(u.username, ''), COALESCE(ua.url, ''), COALESCE(rm.remark, ''),
		m.kind, m.body, m.file_id::text, m.file_name, m.file_mime, m.file_size, m.file_sha256, m.created_at
		FROM donebot_collab_messages m
		JOIN users u ON u.id = m.sender_id
		LEFT JOIN user_avatars ua ON ua.user_id = u.id
		LEFT JOIN donebot_friend_remarks rm ON rm.owner_id = $1 AND rm.friend_id = u.id
		WHERE m.id = $2`, actor, id).Scan(
		&msg.ID, &msg.ScopeType, &msg.SenderID, &msg.SenderEmail, &msg.SenderNickname, &msg.SenderAvatarURL, &msg.SenderRemark,
		&msg.Kind, &msg.Body, &fileID, &msg.FileName, &msg.FileMime, &msg.FileSize, &msg.FileSha256, &msg.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	msg.TargetID = targetID
	if fileID.Valid {
		msg.FileID = fileID.String
	}
	return &msg, nil
}

// ChatSummaries returns the latest message preview and unread count for all active friend and team chats of actor.
func (s *Service) ChatSummaries(ctx context.Context, actor int64) ([]ChatSummary, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if actor <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}

	type scopeTarget struct {
		scopeType string
		scopeKey  string
		targetID  string
	}
	scopes := make([]scopeTarget, 0)

	fRows, err := s.db.QueryContext(ctx, `SELECT CASE WHEN f.user_a=$1 THEN f.user_b ELSE f.user_a END AS peer_id
		FROM donebot_friends f
		JOIN users u ON u.id = CASE WHEN f.user_a=$1 THEN f.user_b ELSE f.user_a END
		WHERE (f.user_a=$1 OR f.user_b=$1) AND u.deleted_at IS NULL AND u.status='active'`, actor)
	if err != nil {
		return nil, err
	}
	defer fRows.Close()
	for fRows.Next() {
		var peerID int64
		if err = fRows.Scan(&peerID); err != nil {
			return nil, err
		}
		scopes = append(scopes, scopeTarget{
			scopeType: "friend",
			scopeKey:  friendScopeKey(actor, peerID),
			targetID:  strconv.FormatInt(peerID, 10),
		})
	}
	if err = fRows.Err(); err != nil {
		return nil, err
	}

	tRows, err := s.db.QueryContext(ctx, `SELECT t.id::text
		FROM donebot_team_members m
		JOIN donebot_teams t ON t.id = m.team_id AND t.state = 'active'
		WHERE m.user_id = $1 AND m.removed_at IS NULL`, actor)
	if err != nil {
		return nil, err
	}
	defer tRows.Close()
	for tRows.Next() {
		var teamID string
		if err = tRows.Scan(&teamID); err != nil {
			return nil, err
		}
		scopes = append(scopes, scopeTarget{
			scopeType: "team",
			scopeKey:  teamID,
			targetID:  teamID,
		})
	}
	if err = tRows.Err(); err != nil {
		return nil, err
	}

	if len(scopes) == 0 {
		return []ChatSummary{}, nil
	}

	out := make([]ChatSummary, 0, len(scopes))
	for _, sc := range scopes {
		sum := ChatSummary{
			ScopeType: sc.scopeType,
			TargetID:  sc.targetID,
		}
		var createdAt time.Time
		err = s.db.QueryRowContext(ctx, `SELECT m.id, m.kind, m.body, m.file_name, m.sender_id,
			COALESCE(NULLIF(rm.remark, ''), NULLIF(u.username, ''), u.email) AS sender_name,
			m.created_at
			FROM donebot_collab_messages m
			JOIN users u ON u.id = m.sender_id
			LEFT JOIN donebot_friend_remarks rm ON rm.owner_id = $1 AND rm.friend_id = u.id
			WHERE m.scope_type = $2 AND m.scope_key = $3
			ORDER BY m.id DESC
			LIMIT 1`, actor, sc.scopeType, sc.scopeKey).Scan(
			&sum.LastMessageID, &sum.LastKind, &sum.LastBody, &sum.LastFileName, &sum.LastSenderID, &sum.LastSenderName, &createdAt,
		)
		if err == nil {
			sum.LastCreatedAt = &createdAt
			if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*)
				FROM donebot_collab_messages m
				LEFT JOIN donebot_collab_reads r
					ON r.user_id = $1 AND r.scope_type = m.scope_type AND r.scope_key = m.scope_key
				WHERE m.scope_type = $2 AND m.scope_key = $3
					AND m.sender_id <> $1
					AND m.id > COALESCE(r.last_read_id, 0)`, actor, sc.scopeType, sc.scopeKey).Scan(&sum.UnreadCount); err != nil {
				return nil, err
			}
			out = append(out, sum)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].LastMessageID > out[j].LastMessageID
	})
	return out, nil
}

// ListChatMessages returns chronological chat messages for an authorized friend or team conversation.
func (s *Service) ListChatMessages(ctx context.Context, actor int64, scopeType, targetID string, afterID, beforeID int64, limit int) ([]ChatMessage, error) {
	normType, scopeKey, err := s.authorizeChatScope(ctx, actor, scopeType, targetID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultChatPageLimit
	}
	if limit > maxChatPageLimit {
		limit = maxChatPageLimit
	}

	var rows *sql.Rows
	reverse := false
	if afterID > 0 {
		rows, err = s.db.QueryContext(ctx, `SELECT m.id, m.scope_type, m.sender_id, u.email, COALESCE(u.username, ''), COALESCE(ua.url, ''), COALESCE(rm.remark, ''),
			m.kind, m.body, m.file_id::text, m.file_name, m.file_mime, m.file_size, m.file_sha256, m.created_at
			FROM donebot_collab_messages m
			JOIN users u ON u.id = m.sender_id
			LEFT JOIN user_avatars ua ON ua.user_id = u.id
			LEFT JOIN donebot_friend_remarks rm ON rm.owner_id = $1 AND rm.friend_id = u.id
			WHERE m.scope_type = $2 AND m.scope_key = $3 AND m.id > $4
			ORDER BY m.id ASC
			LIMIT $5`, actor, normType, scopeKey, afterID, limit)
	} else if beforeID > 0 {
		reverse = true
		rows, err = s.db.QueryContext(ctx, `SELECT m.id, m.scope_type, m.sender_id, u.email, COALESCE(u.username, ''), COALESCE(ua.url, ''), COALESCE(rm.remark, ''),
			m.kind, m.body, m.file_id::text, m.file_name, m.file_mime, m.file_size, m.file_sha256, m.created_at
			FROM donebot_collab_messages m
			JOIN users u ON u.id = m.sender_id
			LEFT JOIN user_avatars ua ON ua.user_id = u.id
			LEFT JOIN donebot_friend_remarks rm ON rm.owner_id = $1 AND rm.friend_id = u.id
			WHERE m.scope_type = $2 AND m.scope_key = $3 AND m.id < $4
			ORDER BY m.id DESC
			LIMIT $5`, actor, normType, scopeKey, beforeID, limit)
	} else {
		reverse = true
		rows, err = s.db.QueryContext(ctx, `SELECT m.id, m.scope_type, m.sender_id, u.email, COALESCE(u.username, ''), COALESCE(ua.url, ''), COALESCE(rm.remark, ''),
			m.kind, m.body, m.file_id::text, m.file_name, m.file_mime, m.file_size, m.file_sha256, m.created_at
			FROM donebot_collab_messages m
			JOIN users u ON u.id = m.sender_id
			LEFT JOIN user_avatars ua ON ua.user_id = u.id
			LEFT JOIN donebot_friend_remarks rm ON rm.owner_id = $1 AND rm.friend_id = u.id
			WHERE m.scope_type = $2 AND m.scope_key = $3
			ORDER BY m.id DESC
			LIMIT $4`, actor, normType, scopeKey, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ChatMessage, 0)
	for rows.Next() {
		var msg ChatMessage
		var fileID sql.NullString
		if err = rows.Scan(
			&msg.ID, &msg.ScopeType, &msg.SenderID, &msg.SenderEmail, &msg.SenderNickname, &msg.SenderAvatarURL, &msg.SenderRemark,
			&msg.Kind, &msg.Body, &fileID, &msg.FileName, &msg.FileMime, &msg.FileSize, &msg.FileSha256, &msg.CreatedAt,
		); err != nil {
			return nil, err
		}
		msg.TargetID = targetID
		if fileID.Valid {
			msg.FileID = fileID.String
		}
		out = append(out, msg)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, nil
}

// ListChatFiles lists recent file messages shared inside a friend or team chat (newest first).
func (s *Service) ListChatFiles(ctx context.Context, actor int64, scopeType, targetID string, limit int) ([]ChatMessage, error) {
	normType, scopeKey, err := s.authorizeChatScope(ctx, actor, scopeType, targetID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > maxChatPageLimit {
		limit = maxChatPageLimit
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.scope_type, m.sender_id, u.email, COALESCE(u.username, ''), COALESCE(ua.url, ''), COALESCE(rm.remark, ''),
		m.kind, m.body, m.file_id::text, m.file_name, m.file_mime, m.file_size, m.file_sha256, m.created_at
		FROM donebot_collab_messages m
		JOIN users u ON u.id = m.sender_id
		LEFT JOIN user_avatars ua ON ua.user_id = u.id
		LEFT JOIN donebot_friend_remarks rm ON rm.owner_id = $1 AND rm.friend_id = u.id
		WHERE m.scope_type = $2 AND m.scope_key = $3 AND m.kind = 'file' AND m.file_id IS NOT NULL
		ORDER BY m.id DESC
		LIMIT $4`, actor, normType, scopeKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ChatMessage, 0)
	for rows.Next() {
		var msg ChatMessage
		var fileID sql.NullString
		if err = rows.Scan(
			&msg.ID, &msg.ScopeType, &msg.SenderID, &msg.SenderEmail, &msg.SenderNickname, &msg.SenderAvatarURL, &msg.SenderRemark,
			&msg.Kind, &msg.Body, &fileID, &msg.FileName, &msg.FileMime, &msg.FileSize, &msg.FileSha256, &msg.CreatedAt,
		); err != nil {
			return nil, err
		}
		msg.TargetID = targetID
		if fileID.Valid {
			msg.FileID = fileID.String
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}

// SendChatMessage inserts a text message into a friend or team conversation.
func (s *Service) SendChatMessage(ctx context.Context, actor int64, scopeType, targetID, rawBody string) (*ChatMessage, error) {
	normType, scopeKey, err := s.authorizeChatScope(ctx, actor, scopeType, targetID)
	if err != nil {
		return nil, err
	}
	body, err := cleanChatText(rawBody, MaxChatBodyRunes, false)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var msgID int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO donebot_collab_messages(scope_type, scope_key, sender_id, kind, body)
		VALUES($1, $2, $3, 'text', $4) RETURNING id`, normType, scopeKey, actor, body).Scan(&msgID); err != nil {
		return nil, err
	}
	if err = s.upsertChatRead(ctx, tx, actor, normType, scopeKey, msgID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.fetchChatMessageByID(ctx, actor, msgID, targetID)
}

// SendChatFile stores an uploaded file (up to 100 MiB) and records a chat message in the conversation.
func (s *Service) SendChatFile(
	ctx context.Context,
	actor int64,
	scopeType, targetID, rawFileName, rawMime, rawCaption string,
	reader io.Reader,
	contentLength int64,
) (*ChatMessage, error) {
	normType, scopeKey, err := s.authorizeChatScope(ctx, actor, scopeType, targetID)
	if err != nil {
		return nil, err
	}
	if contentLength > MaxChatFileBytes {
		return nil, infraerrors.BadRequest("COLLAB_FILE_TOO_LARGE", "发送的文件不能超过 100 MB。")
	}
	caption, err := cleanChatText(rawCaption, MaxChatCaptionRunes, true)
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
	tmpFile, err := os.CreateTemp(dir, "upload-*.part")
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
			return nil, infraerrors.BadRequest("COLLAB_FILE_TOO_LARGE", "发送的文件不能超过 100 MB。")
		}
		return nil, copyErr
	}
	if written > MaxChatFileBytes {
		cleanupTmp()
		return nil, infraerrors.BadRequest("COLLAB_FILE_TOO_LARGE", "发送的文件不能超过 100 MB。")
	}
	if written == 0 {
		cleanupTmp()
		return nil, infraerrors.BadRequest("COLLAB_FILE_EMPTY", "不能发送空文件。")
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
	defer tx.Rollback()

	var msgID int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO donebot_collab_messages(
			scope_type, scope_key, sender_id, kind, body, file_id, file_name, file_mime, file_size, file_sha256
		) VALUES($1, $2, $3, 'file', $4, $5::uuid, $6, $7, $8, $9) RETURNING id`,
		normType, scopeKey, actor, caption, fileID, fileName, fileMime, written, shaHex).Scan(&msgID); err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	if err = s.upsertChatRead(ctx, tx, actor, normType, scopeKey, msgID); err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	return s.fetchChatMessageByID(ctx, actor, msgID, targetID)
}

// MarkChatRead advances actor's read cursor in an authorized conversation.
func (s *Service) MarkChatRead(ctx context.Context, actor int64, scopeType, targetID string, lastReadID int64) (int64, error) {
	normType, scopeKey, err := s.authorizeChatScope(ctx, actor, scopeType, targetID)
	if err != nil {
		return 0, err
	}
	if lastReadID <= 0 {
		if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM donebot_collab_messages WHERE scope_type=$1 AND scope_key=$2`, normType, scopeKey).Scan(&lastReadID); err != nil {
			return 0, err
		}
	}
	if err = s.upsertChatRead(ctx, s.db, actor, normType, scopeKey, lastReadID); err != nil {
		return 0, err
	}
	return lastReadID, nil
}

// OpenChatFile authorizes access and opens a stored chat file for streaming download.
func (s *Service) OpenChatFile(ctx context.Context, actor int64, fileID string) (*os.File, ChatFileMeta, error) {
	if err := s.available(); err != nil {
		return nil, ChatFileMeta{}, err
	}
	if actor <= 0 {
		return nil, ChatFileMeta{}, infraerrors.Unauthorized("AUTH_REQUIRED", "Login required")
	}
	if !validUUID(fileID) {
		return nil, ChatFileMeta{}, infraerrors.NotFound("COLLAB_FILE_NOT_FOUND", "聊天文件不存在。")
	}

	var scopeType, scopeKey string
	meta := ChatFileMeta{FileID: fileID}
	err := s.db.QueryRowContext(ctx, `SELECT scope_type, scope_key, file_name, file_mime, file_size, file_sha256
		FROM donebot_collab_messages
		WHERE file_id = $1::uuid AND kind = 'file'`, fileID).Scan(
		&scopeType, &scopeKey, &meta.FileName, &meta.FileMime, &meta.FileSize, &meta.FileSha256,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ChatFileMeta{}, infraerrors.NotFound("COLLAB_FILE_NOT_FOUND", "聊天文件不存在。")
	}
	if err != nil {
		return nil, ChatFileMeta{}, err
	}
	if err = s.verifyScopeKeyAccess(ctx, actor, scopeType, scopeKey); err != nil {
		return nil, ChatFileMeta{}, err
	}

	dir := strings.TrimSpace(s.chatDir)
	if dir == "" {
		dir = "data/collab-chat"
	}
	f, err := os.Open(filepath.Join(dir, fileID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ChatFileMeta{}, infraerrors.NotFound("COLLAB_FILE_NOT_FOUND", "文件已过期或不存在。")
	}
	if err != nil {
		return nil, ChatFileMeta{}, err
	}
	return f, meta, nil
}

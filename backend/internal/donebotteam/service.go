package donebotteam

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const Protocol = "donebot.teams.v1"

type Role string

const (
	Owner  Role = "owner"
	Admin  Role = "admin"
	Member Role = "member"
)

type Service struct {
	db      *sql.DB
	enabled bool
}

func New(db *sql.DB, enabled bool) *Service { return &Service{db: db, enabled: enabled} }
func (s *Service) Enabled() bool            { return s != nil && s.enabled && s.db != nil }
func (s *Service) available() error {
	if !s.Enabled() {
		return infraerrors.ServiceUnavailable("TEAMS_DISABLED", "Team collaboration is disabled")
	}
	return nil
}

type Team struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Role          Role      `json:"role"`
	MemberVersion int64     `json:"member_version"`
	CreatedAt     time.Time `json:"created_at"`
}
type TeamMember struct {
	UserID  int64  `json:"user_id"`
	Email   string `json:"email"`
	Role    Role   `json:"role"`
	Version int64  `json:"version"`
}
type Invitation struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      Role      `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
	Token     string    `json:"token,omitempty"`
}

func validID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v.Version() == 4 && v.String() == id
}
func notFound() error { return infraerrors.NotFound("TEAM_NOT_FOUND", "Team or membership not found") }
func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 100 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", infraerrors.BadRequest("TEAM_NAME_INVALID", "Use a name between 1 and 100 characters")
	}
	return name, nil
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Locking the team serializes membership changes with grants and invitation acceptance.
func access(ctx context.Context, db queryer, id string, actor int64, lock bool) (*Team, error) {
	if !validID(id) || actor <= 0 {
		return nil, notFound()
	}
	query := `SELECT t.id::text,t.name,m.role,m.version,t.created_at FROM donebot_teams t JOIN donebot_team_members m ON m.team_id=t.id JOIN users u ON u.id=m.user_id WHERE t.id=$1::uuid AND t.state='active' AND m.user_id=$2 AND m.removed_at IS NULL AND u.deleted_at IS NULL AND u.status='active'`
	if lock {
		query += ` FOR UPDATE OF t`
	}
	out := &Team{}
	err := db.QueryRowContext(ctx, query, id, actor).Scan(&out.ID, &out.Name, &out.Role, &out.MemberVersion, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	return out, err
}
func (s *Service) Access(ctx context.Context, id string, actor int64) (*Team, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	return access(ctx, s.db, id, actor, false)
}
func (s *Service) List(ctx context.Context, actor int64) ([]Team, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.id::text,t.name,m.role,m.version,t.created_at FROM donebot_teams t JOIN donebot_team_members m ON m.team_id=t.id JOIN users u ON u.id=m.user_id WHERE m.user_id=$1 AND m.removed_at IS NULL AND t.state='active' AND u.deleted_at IS NULL AND u.status='active' ORDER BY t.created_at,t.id`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Team{}
	for rows.Next() {
		var t Team
		if err = rows.Scan(&t.ID, &t.Name, &t.Role, &t.MemberVersion, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Service) Create(ctx context.Context, actor int64, name, key string) (*Team, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	name, err := validateName(name)
	if err != nil {
		return nil, err
	}
	if !validID(key) || actor <= 0 {
		return nil, infraerrors.BadRequest("TEAM_REQUEST_KEY", "A UUID v4 Idempotency-Key is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `INSERT INTO donebot_teams(id,name,owner_id,request_key) SELECT $1::uuid,$2,id,$4::uuid FROM users WHERE id=$3 AND deleted_at IS NULL AND status='active' ON CONFLICT(owner_id,request_key) DO UPDATE SET request_key=EXCLUDED.request_key RETURNING id::text`, uuid.NewString(), name, actor, key).Scan(&id)
	if err != nil {
		return nil, err
	}
	var savedName, state string
	if err = tx.QueryRowContext(ctx, `SELECT name,state FROM donebot_teams WHERE id=$1::uuid FOR UPDATE`, id).Scan(&savedName, &state); err != nil {
		return nil, err
	}
	if savedName != name || state != "active" {
		return nil, infraerrors.Conflict("TEAM_REQUEST_CONFLICT", "Idempotency-Key has already been used")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_team_members(team_id,user_id,role) VALUES($1::uuid,$2,'owner') ON CONFLICT DO NOTHING`, id, actor); err != nil {
		return nil, err
	}
	out, err := access(ctx, tx, id, actor, false)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
func canManage(role Role) bool { return role == Owner || role == Admin }
func canAssign(actor, target Role) bool {
	return target == Member && canManage(actor) || target == Admin && actor == Owner
}
func (s *Service) Rename(ctx context.Context, id string, actor int64, name string) (*Team, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	name, err := validateName(name)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	t, err := access(ctx, tx, id, actor, true)
	if err != nil {
		return nil, err
	}
	if !canManage(t.Role) {
		return nil, infraerrors.Forbidden("TEAM_ADMIN_REQUIRED", "Team admin required")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_teams SET name=$2,updated_at=NOW() WHERE id=$1::uuid`, id, name); err != nil {
		return nil, err
	}
	t.Name = name
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return t, nil
}
func (s *Service) Members(ctx context.Context, id string, actor int64) ([]TeamMember, error) {
	if _, err := s.Access(ctx, id, actor); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.user_id,u.email,m.role,m.version FROM donebot_team_members m JOIN users u ON u.id=m.user_id JOIN donebot_teams t ON t.id=m.team_id WHERE m.team_id=$1::uuid AND m.removed_at IS NULL AND u.deleted_at IS NULL AND u.status='active' AND t.state='active' AND EXISTS(SELECT 1 FROM donebot_team_members a JOIN users au ON au.id=a.user_id WHERE a.team_id=m.team_id AND a.user_id=$2 AND a.removed_at IS NULL AND au.deleted_at IS NULL AND au.status='active') ORDER BY m.joined_at,m.user_id`, id, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TeamMember{}
	for rows.Next() {
		var m TeamMember
		if err = rows.Scan(&m.UserID, &m.Email, &m.Role, &m.Version); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Service) ChangeMember(ctx context.Context, id string, actor, target int64, role Role, remove bool) error {
	if err := s.available(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := access(ctx, tx, id, actor, true)
	if err != nil {
		return err
	}
	var before Role
	if err = tx.QueryRowContext(ctx, `SELECT role FROM donebot_team_members WHERE team_id=$1::uuid AND user_id=$2 AND removed_at IS NULL FOR UPDATE`, id, target).Scan(&before); errors.Is(err, sql.ErrNoRows) {
		return notFound()
	} else if err != nil {
		return err
	}
	allowed := before != Owner && (remove && actor == target || canManage(t.Role) && (t.Role == Owner || before == Member) && (remove || canAssign(t.Role, role)))
	if !allowed {
		return infraerrors.Forbidden("TEAM_MEMBER_CHANGE_DENIED", "Only the owner manages admins; the owner cannot be removed or demoted")
	}
	if remove {
		_, err = tx.ExecContext(ctx, `UPDATE donebot_team_members SET removed_at=NOW(),version=version+1 WHERE team_id=$1::uuid AND user_id=$2`, id, target)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE donebot_team_members SET role=$3,version=version+1 WHERE team_id=$1::uuid AND user_id=$2`, id, target, role)
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_devices SET enabled=FALSE,revoked_at=NOW() WHERE team_id=$1::uuid AND user_id=$2`, id, target); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_invitations SET revoked_at=NOW() WHERE team_id=$1::uuid AND invited_by=$2 AND accepted_at IS NULL AND revoked_at IS NULL`, id, target); err != nil {
		return err
	}
	return tx.Commit()
}
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (s *Service) Invite(ctx context.Context, id string, actor int64, email string, role Role) (*Invitation, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	address, parseErr := mail.ParseAddress(email)
	if parseErr != nil || address.Address != email || len(email) > 254 || strings.IndexFunc(email, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return nil, infraerrors.BadRequest("TEAM_EMAIL_INVALID", "A valid account email is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	t, err := access(ctx, tx, id, actor, true)
	if err != nil {
		return nil, err
	}
	if !canAssign(t.Role, role) {
		return nil, infraerrors.Forbidden("TEAM_INVITE_DENIED", "Only the owner can invite admins")
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	out := &Invitation{ID: uuid.NewString(), Email: email, Role: role, Token: token}
	err = tx.QueryRowContext(ctx, `INSERT INTO donebot_team_invitations(id,team_id,email,role,token_hash,invited_by,expires_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,clock_timestamp()+INTERVAL '7 days') RETURNING expires_at`, out.ID, id, email, role, tokenHash(token), actor).Scan(&out.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *Service) Accept(ctx context.Context, actor int64, token string) (*Team, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	bytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(bytes) != 32 || base64.RawURLEncoding.EncodeToString(bytes) != token {
		return nil, notFound()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT team_id::text FROM donebot_team_invitations WHERE token_hash=$1`, tokenHash(token)).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	} else if err != nil {
		return nil, err
	}
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM donebot_teams WHERE id=$1::uuid FOR UPDATE`, id).Scan(&state); err != nil {
		return nil, err
	}
	if state != "active" {
		return nil, notFound()
	}
	var email string
	var role Role
	var accepted sql.NullInt64
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT i.email,i.role,i.accepted_by,(i.revoked_at IS NULL AND i.expires_at>clock_timestamp()) FROM donebot_team_invitations i JOIN donebot_team_members inviter ON inviter.team_id=i.team_id AND inviter.user_id=i.invited_by AND inviter.removed_at IS NULL JOIN users u ON u.id=inviter.user_id AND u.deleted_at IS NULL AND u.status='active' WHERE i.token_hash=$1 AND (inviter.role='owner' OR inviter.role='admin' AND i.role='member') FOR UPDATE OF i`, tokenHash(token)).Scan(&email, &role, &accepted, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	} else if err != nil {
		return nil, err
	}
	var actual string
	if err = tx.QueryRowContext(ctx, `SELECT lower(email) FROM users WHERE id=$1 AND deleted_at IS NULL AND status='active'`, actor).Scan(&actual); err != nil {
		return nil, notFound()
	}
	if actual != email || !active || accepted.Valid && accepted.Int64 != actor {
		return nil, notFound()
	}
	if !accepted.Valid {
		_, err = tx.ExecContext(ctx, `INSERT INTO donebot_team_members(team_id,user_id,role) VALUES($1::uuid,$2,$3) ON CONFLICT(team_id,user_id) DO UPDATE SET role=EXCLUDED.role,removed_at=NULL,version=donebot_team_members.version+1,joined_at=NOW() WHERE donebot_team_members.removed_at IS NOT NULL`, id, actor, role)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_invitations SET accepted_by=$2,accepted_at=NOW() WHERE token_hash=$1`, tokenHash(token), actor); err != nil {
			return nil, err
		}
	}
	out, err := access(ctx, tx, id, actor, false)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *Service) RevokeInvitation(ctx context.Context, id string, actor int64, invitation string) error {
	if err := s.available(); err != nil {
		return err
	}
	if !validID(invitation) {
		return notFound()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := access(ctx, tx, id, actor, true)
	if err != nil {
		return err
	}
	if !canManage(t.Role) {
		return infraerrors.Forbidden("TEAM_ADMIN_REQUIRED", "Team admin required")
	}
	result, err := tx.ExecContext(ctx, `UPDATE donebot_team_invitations SET revoked_at=NOW() WHERE id=$1::uuid AND team_id=$2::uuid AND ($3='owner' OR role='member')`, invitation, id, t.Role)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound()
	}
	return tx.Commit()
}

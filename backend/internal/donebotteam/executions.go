package donebotteam

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"regexp"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

type ExecutionRequest struct {
	TeamID      string
	Actor       int64
	Key         string
	NodeKind    string
	NodeID      string
	Purpose     string
	BotID       string
	ThreadID    string
	Private     bool
	RequestHash string
}

// Payer and member versions are derived from locked server records, never from request identity fields.
type Execution struct {
	ID              string
	TeamID          string
	Key             string
	PayerID         int64
	ActorID         int64
	ActorVersion    int64
	ExecutorID      int64
	ExecutorVersion int64
	NodeKind        string
	NodeID          string
	NodeVersion     int64
	Purpose         string
	BotID           string
	ThreadID        string
	PrivateTo       sql.NullInt64
	RequestHash     string
	State           string
}

type ExecutionGrant struct {
	Execution *Execution
	Token     string
	ExpiresAt time.Time
}

var executionPart = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,120}$`)
var executionHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validExecution(in ExecutionRequest) bool {
	if !validID(in.TeamID) || !validID(in.Key) || !validID(in.NodeID) || in.Actor < 1 ||
		!executionPart.MatchString(in.BotID) || !executionPart.MatchString(in.ThreadID) || !executionHash.MatchString(in.RequestHash) {
		return false
	}
	if in.NodeKind != "local" && in.NodeKind != "cloud" {
		return false
	}
	switch in.Purpose {
	case "llm", "image", "video", "music", "tts":
		return true
	default:
		return false
	}
}

const executionColumns = `e.id::text,e.team_id::text,e.request_key::text,e.payer_id,e.actor_id,e.actor_version,e.executor_id,e.executor_version,e.node_kind,e.node_id::text,e.node_version,e.purpose,e.bot_id,e.thread_id,e.private_to,e.request_hash,e.state`

func scanExecution(row interface{ Scan(...any) error }) (*Execution, error) {
	e := &Execution{}
	err := row.Scan(&e.ID, &e.TeamID, &e.Key, &e.PayerID, &e.ActorID, &e.ActorVersion, &e.ExecutorID, &e.ExecutorVersion, &e.NodeKind, &e.NodeID, &e.NodeVersion, &e.Purpose, &e.BotID, &e.ThreadID, &e.PrivateTo, &e.RequestHash, &e.State)
	return e, err
}

func executionDenied() error {
	return infraerrors.Forbidden("TEAM_EXECUTION_DENIED", "The team execution grant or its membership is no longer authorized")
}

func executionCloud(ctx context.Context, tx *sql.Tx, teamID string) (payer int64, instance string, err error) {
	err = tx.QueryRowContext(ctx, `SELECT t.owner_id,i.id::text FROM donebot_teams t
        JOIN users p ON p.id=t.owner_id JOIN donebot_cloud_instances i ON i.team_id=t.id AND i.user_id=t.owner_id
        JOIN donebot_cloud_subscriptions s ON s.instance_id=i.id AND s.team_id=t.id AND s.user_id=t.owner_id
        JOIN donebot_cloud_nodes n ON n.id=i.node_id
        WHERE t.id=$1::uuid AND t.state='active' AND p.status='active' AND p.deleted_at IS NULL
        AND i.deleted_at IS NULL AND i.desired='running' AND i.actual='running'
        AND s.state='active' AND s.paid_until>clock_timestamp() AND NOT s.purge_locked
        AND n.team_protocol=$2 AND n.last_seen>clock_timestamp()-INTERVAL '30 seconds'
        FOR SHARE OF p,i,s,n`, teamID, Protocol).Scan(&payer, &instance)
	if errors.Is(err, sql.ErrNoRows) {
		err = executionDenied()
	}
	return
}

func executionNode(ctx context.Context, tx *sql.Tx, in ExecutionRequest, actorVersion int64, cloudID string) (executor, executorVersion, version int64, err error) {
	if in.NodeKind == "cloud" {
		if in.NodeID != cloudID {
			return 0, 0, 0, executionDenied()
		}
		return in.Actor, actorVersion, 1, nil
	}
	err = tx.QueryRowContext(ctx, `SELECT d.user_id,m.version,d.version FROM donebot_team_devices d
        JOIN donebot_team_members m ON m.team_id=d.team_id AND m.user_id=d.user_id
        JOIN users u ON u.id=d.user_id JOIN donebot_installs i ON i.installation_id=d.installation_id AND i.user_id=d.user_id
        WHERE d.team_id=$1::uuid AND d.id=$2::uuid AND d.enabled AND d.revoked_at IS NULL AND d.member_version=m.version
        AND d.last_seen>clock_timestamp()-INTERVAL '30 seconds'
        AND m.removed_at IS NULL AND u.status='active' AND u.deleted_at IS NULL AND i.status='active'
        AND (i.expires_at IS NULL OR i.expires_at>clock_timestamp())
        FOR SHARE OF d,m,u,i`, in.TeamID, in.NodeID).Scan(&executor, &executorVersion, &version)
	if errors.Is(err, sql.ErrNoRows) || err == nil && in.Private && executor != in.Actor {
		err = executionDenied()
	}
	return
}

// The coordinator calls this only after checking the conversation audience and local execution consent.
func (s *Service) PrepareExecution(ctx context.Context, in ExecutionRequest) (*ExecutionGrant, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if !validExecution(in) {
		return nil, infraerrors.BadRequest("TEAM_EXECUTION_INVALID", "Invalid execution binding")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	member, err := access(ctx, tx, in.TeamID, in.Actor, true)
	if err != nil {
		return nil, err
	}
	payer, cloudID, err := executionCloud(ctx, tx, in.TeamID)
	if err != nil {
		return nil, err
	}
	executor, executorVersion, nodeVersion, err := executionNode(ctx, tx, in, member.MemberVersion, cloudID)
	if err != nil {
		return nil, err
	}
	var privateTo sql.NullInt64
	if in.Private {
		privateTo = sql.NullInt64{Int64: in.Actor, Valid: true}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO donebot_team_executions(id,team_id,request_key,payer_id,actor_id,actor_version,executor_id,executor_version,node_kind,node_id,node_version,purpose,bot_id,thread_id,private_to,request_hash)
        VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10::uuid,$11,$12,$13,$14,$15,$16)
        ON CONFLICT(team_id,request_key) DO NOTHING`, uuid.NewString(), in.TeamID, in.Key, payer, in.Actor, member.MemberVersion, executor, executorVersion, in.NodeKind, in.NodeID, nodeVersion, in.Purpose, in.BotID, in.ThreadID, privateTo, in.RequestHash)
	if err != nil {
		return nil, err
	}
	e, err := scanExecution(tx.QueryRowContext(ctx, `SELECT `+executionColumns+` FROM donebot_team_executions e WHERE e.team_id=$1::uuid AND e.request_key=$2::uuid FOR UPDATE`, in.TeamID, in.Key))
	if err != nil {
		return nil, err
	}
	if e.ActorID != in.Actor || e.ActorVersion != member.MemberVersion || e.PayerID != payer || e.ExecutorID != executor || e.ExecutorVersion != executorVersion ||
		e.NodeID != in.NodeID || e.NodeKind != in.NodeKind || e.NodeVersion != nodeVersion || e.Purpose != in.Purpose || e.BotID != in.BotID || e.ThreadID != in.ThreadID || e.PrivateTo != privateTo || e.RequestHash != in.RequestHash {
		return nil, infraerrors.Conflict("TEAM_EXECUTION_CONFLICT", "Execution key is already bound to a different request or authorization")
	}
	if e.State == "cancelled" {
		return nil, executionDenied()
	}
	bytes := make([]byte, 32)
	if _, err = rand.Read(bytes); err != nil {
		return nil, err
	}
	token := "dbte_" + base64.RawURLEncoding.EncodeToString(bytes)
	var expires time.Time
	err = tx.QueryRowContext(ctx, `INSERT INTO donebot_team_execution_grants(token_hash,execution_id,expires_at) VALUES($1,$2::uuid,clock_timestamp()+INTERVAL '5 minutes') RETURNING expires_at`, tokenHash(token), e.ID).Scan(&expires)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &ExecutionGrant{Execution: e, Token: token, ExpiresAt: expires}, nil
}

func validExecutionToken(token string) bool {
	if len(token) != 48 || token[:5] != "dbte_" {
		return false
	}
	bytes, err := base64.RawURLEncoding.DecodeString(token[5:])
	return err == nil && len(bytes) == 32 && base64.RawURLEncoding.EncodeToString(bytes) == token[5:]
}

func (s *Service) executionGrant(ctx context.Context, tx *sql.Tx, token string, expected ExecutionRequest) (*Execution, error) {
	if !validExecutionToken(token) || !validExecution(expected) {
		return nil, executionDenied()
	}
	member, err := access(ctx, tx, expected.TeamID, expected.Actor, true)
	if err != nil {
		return nil, err
	}
	e, err := scanExecution(tx.QueryRowContext(ctx, `SELECT `+executionColumns+` FROM donebot_team_execution_grants g JOIN donebot_team_executions e ON e.id=g.execution_id WHERE g.token_hash=$1 AND e.team_id=$2::uuid AND e.actor_id=$3 AND g.expires_at>clock_timestamp() FOR UPDATE OF e`, tokenHash(token), expected.TeamID, expected.Actor))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, executionDenied()
	} else if err != nil {
		return nil, err
	}
	if e.TeamID != expected.TeamID || e.Key != expected.Key || e.ActorID != expected.Actor || e.ActorVersion != member.MemberVersion ||
		e.NodeID != expected.NodeID || e.NodeKind != expected.NodeKind || e.Purpose != expected.Purpose || e.BotID != expected.BotID || e.ThreadID != expected.ThreadID ||
		e.RequestHash != expected.RequestHash || e.PrivateTo.Valid != expected.Private || e.PrivateTo.Valid && e.PrivateTo.Int64 != expected.Actor {
		return nil, executionDenied()
	}
	payer, cloudID, err := executionCloud(ctx, tx, e.TeamID)
	if err != nil {
		return nil, err
	}
	executor, executorVersion, version, err := executionNode(ctx, tx, expected, member.MemberVersion, cloudID)
	if err != nil {
		return nil, err
	}
	if e.PayerID != payer || e.ExecutorID != executor || e.ExecutorVersion != executorVersion || e.NodeVersion != version || e.State == "cancelled" {
		return nil, executionDenied()
	}
	return e, nil
}

func (s *Service) AuthorizeExecution(ctx context.Context, token string, expected ExecutionRequest) (*Execution, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	e, err := s.executionGrant(ctx, tx, token, expected)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return e, nil
}

// A committed acceptance is consumed even if the provider response is lost; retries query the original task.
func (s *Service) AdmitExecution(ctx context.Context, token string, expected ExecutionRequest) (*Execution, bool, error) {
	if err := s.available(); err != nil {
		return nil, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	e, err := s.executionGrant(ctx, tx, token, expected)
	if err != nil {
		return nil, false, err
	}
	first := e.State == "prepared"
	if first {
		if _, err = tx.ExecContext(ctx, `UPDATE donebot_team_executions SET state='accepted',accepted_at=clock_timestamp() WHERE id=$1::uuid`, e.ID); err != nil {
			return nil, false, err
		}
		e.State = "accepted"
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return e, first, nil
}

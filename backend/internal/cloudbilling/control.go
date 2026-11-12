package cloudbilling

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type Node struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Endpoint string     `json:"endpoint"`
	Capacity int        `json:"capacity"`
	Enabled  bool       `json:"enabled"`
	LastSeen *time.Time `json:"last_seen"`
	Assigned int        `json:"assigned"`
}
type NodeInput struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Capacity int    `json:"capacity"`
	Enabled  bool   `json:"enabled"`
}
type Instance struct {
	ID                string     `json:"id"`
	UserID            int64      `json:"user_id"`
	NodeID            string     `json:"node_id"`
	Desired           string     `json:"desired"`
	Actual            string     `json:"actual"`
	Generation        int64      `json:"generation"`
	Bound             bool       `json:"bound"`
	Error             string     `json:"error"`
	PaidUntil         *time.Time `json:"paid_until"`
	SubscriptionState string     `json:"subscription_state"`
	PurgeLocked       bool       `json:"purge_locked"`
}
type Operation struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id"`
	Action     string `json:"action"`
	Generation int64  `json:"generation"`
	State      string `json:"state"`
	Error      string `json:"error"`
	LeaseToken string `json:"lease_token,omitempty"`
}
type RunLease struct {
	InstanceID      string    `json:"instance_id"`
	Generation      int64     `json:"generation"`
	AuthorizedUntil time.Time `json:"authorized_until"`
}
type RunningCommand struct {
	ID         string `json:"operation_id"`
	LeaseToken string `json:"lease_token"`
}
type Claim struct {
	Command    *Operation `json:"command"`
	Leases     []RunLease `json:"leases"`
	ServerTime time.Time  `json:"server_time"`
}

func tokenHash(token string) string {
	v := sha256.Sum256([]byte(token))
	return hex.EncodeToString(v[:])
}
func newToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func validID(id string) bool {
	v, e := uuid.Parse(id)
	return e == nil && v.Version() == 4 && v.String() == id
}
func (s *Service) enabled() error {
	if s.cfg == nil || !s.cfg.DoneBot.Enabled {
		return service.ErrDoneBotDisabled
	}
	return nil
}
func (s *Service) SaveNode(ctx context.Context, id string, in NodeInput) (*Node, string, error) {
	if err := s.enabled(); err != nil {
		return nil, "", err
	}
	u, err := url.Parse(in.Endpoint)
	if err != nil || u == nil || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || u.Hostname() == "" || u.Scheme != "https" && (os.Getenv("DONEBOT_CLOUD_TEST_LOOPBACK_NODES") != "1" || u.Scheme != "http" || !net.ParseIP(u.Hostname()).IsLoopback()) {
		return nil, "", infraerrors.BadRequest("CLOUD_NODE_ENDPOINT", "Use an HTTPS origin on the private network")
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 100 || in.Capacity < 1 || in.Capacity > 10000 {
		return nil, "", infraerrors.BadRequest("CLOUD_NODE_INVALID", "A name and capacity between 1 and 10000 are required")
	}
	token := ""
	if id == "" {
		id = uuid.NewString()
		token = newToken()
	}
	if !validID(id) {
		return nil, "", infraerrors.BadRequest("CLOUD_NODE_INVALID", "Invalid node ID")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = tx.Rollback() }()
	if token != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO donebot_cloud_nodes(id,name,endpoint,capacity,token_hash,enabled) VALUES($1::uuid,$2,$3,$4,$5,$6)`, id, in.Name, strings.TrimSuffix(in.Endpoint, "/"), in.Capacity, tokenHash(token), in.Enabled)
	} else {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT capacity FROM donebot_cloud_nodes WHERE id=$1::uuid FOR UPDATE`, id).Scan(&count); err != nil {
			return nil, "", err
		}
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_cloud_instances WHERE node_id=$1::uuid AND deleted_at IS NULL`, id).Scan(&count); err != nil {
			return nil, "", err
		}
		if count > in.Capacity {
			return nil, "", infraerrors.Conflict("CLOUD_NODE_CAPACITY", "Capacity is below the number of assigned instances")
		}
		_, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_nodes SET name=$2,endpoint=$3,capacity=$4,enabled=$5 WHERE id=$1::uuid`, id, in.Name, strings.TrimSuffix(in.Endpoint, "/"), in.Capacity, in.Enabled)
	}
	if err != nil {
		return nil, "", err
	}
	if err = tx.Commit(); err != nil {
		return nil, "", err
	}
	return &Node{ID: id, Name: in.Name, Endpoint: in.Endpoint, Capacity: in.Capacity, Enabled: in.Enabled}, token, nil
}
func (s *Service) Nodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT n.id::text,n.name,n.endpoint,n.capacity,n.enabled,n.last_seen,(SELECT COUNT(*) FROM donebot_cloud_instances i WHERE i.node_id=n.id AND i.deleted_at IS NULL) FROM donebot_cloud_nodes n ORDER BY n.created_at`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Node{}
	for rows.Next() {
		var n Node
		if err = rows.Scan(&n.ID, &n.Name, &n.Endpoint, &n.Capacity, &n.Enabled, &n.LastSeen, &n.Assigned); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

const instanceColumns = `i.id::text,i.user_id,COALESCE(i.node_id::text,''),i.desired,i.actual,i.generation,i.bound,i.error,s.paid_until,COALESCE(s.state,'unpaid'),COALESCE(s.purge_locked,FALSE)`
const instanceJoin = ` FROM donebot_cloud_instances i LEFT JOIN donebot_cloud_subscriptions s ON s.instance_id=i.id `

type scanner interface{ Scan(...any) error }

func scanInstance(row scanner) (*Instance, error) {
	v := &Instance{}
	err := row.Scan(&v.ID, &v.UserID, &v.NodeID, &v.Desired, &v.Actual, &v.Generation, &v.Bound, &v.Error, &v.PaidUntil, &v.SubscriptionState, &v.PurgeLocked)
	return v, err
}
func (s *Service) Instances(ctx context.Context, owner int64) ([]Instance, error) {
	where := "WHERE i.deleted_at IS NULL"
	args := []any{}
	if owner > 0 {
		where += " AND i.user_id=$1"
		args = append(args, owner)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+instanceColumns+instanceJoin+where+` ORDER BY i.created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Instance{}
	for rows.Next() {
		v, e := scanInstance(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}
func (s *Service) Owned(ctx context.Context, id string, owner int64) (*Instance, error) {
	if !validID(id) || owner <= 0 {
		return nil, infraerrors.NotFound("CLOUD_NOT_FOUND", "Cloud instance not found")
	}
	v, err := scanInstance(s.db.QueryRowContext(ctx, `SELECT `+instanceColumns+instanceJoin+` WHERE i.id=$1::uuid AND i.user_id=$2 AND i.deleted_at IS NULL`, id, owner))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("CLOUD_NOT_FOUND", "Cloud instance not found")
	}
	return v, err
}

var requestKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

func (s *Service) Mutate(ctx context.Context, owner int64, id, action, key string) (*Instance, *Operation, error) {
	if err := s.enabled(); err != nil {
		return nil, nil, err
	}
	if owner <= 0 || !requestKey.MatchString(key) || action != "start" && action != "stop" && action != "cancel" {
		return nil, nil, infraerrors.BadRequest("CLOUD_OPERATION_INVALID", "A valid action and Idempotency-Key are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('donebot-cloud-user:' || $1::text,0))`, owner); err != nil {
		return nil, nil, err
	}
	if id == "" {
		err = tx.QueryRowContext(ctx, `SELECT id::text FROM donebot_cloud_instances WHERE user_id=$1 AND deleted_at IS NULL FOR UPDATE`, owner).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			id = uuid.NewString()
			_, err = tx.ExecContext(ctx, `INSERT INTO donebot_cloud_instances(id,user_id) VALUES($1::uuid,$2)`, id, owner)
		}
	}
	if err != nil {
		return nil, nil, err
	}
	if !validID(id) {
		return nil, nil, infraerrors.NotFound("CLOUD_NOT_FOUND", "Cloud instance not found")
	}
	v, err := scanInstance(tx.QueryRowContext(ctx, `SELECT `+instanceColumns+instanceJoin+` WHERE i.id=$1::uuid AND i.user_id=$2 AND i.deleted_at IS NULL FOR UPDATE OF i`, id, owner))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, infraerrors.NotFound("CLOUD_NOT_FOUND", "Cloud instance not found")
	}
	if err != nil {
		return nil, nil, err
	}
	op := &Operation{}
	err = tx.QueryRowContext(ctx, `SELECT id::text,instance_id::text,action,generation,state,error FROM donebot_cloud_operations WHERE instance_id=$1::uuid AND request_key=$2`, id, key).Scan(&op.ID, &op.InstanceID, &op.Action, &op.Generation, &op.State, &op.Error)
	if err == nil {
		if op.Action != action {
			return nil, nil, infraerrors.Conflict("CLOUD_IDEMPOTENCY_CONFLICT", "Idempotency key was used for another action")
		}
		return v, op, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, err
	}
	var purgeLocked bool
	err = tx.QueryRowContext(ctx, `SELECT purge_locked FROM donebot_cloud_subscriptions WHERE instance_id=$1::uuid FOR UPDATE`, id).Scan(&purgeLocked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, err
	}
	if purgeLocked || v.Desired == "deleted" {
		return nil, nil, infraerrors.Conflict("CLOUD_DELETING", "Data deletion has started")
	}
	desired, state := "stopped", "pending"
	if action == "start" {
		desired, state = "running", "billing"
	}
	v.Generation++
	v.Desired = desired
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_instances SET desired=$2,actual='pending',generation=$3,error='' WHERE id=$1::uuid`, id, desired, v.Generation); err != nil {
		return nil, nil, err
	}
	op = &Operation{ID: uuid.NewString(), InstanceID: id, Action: action, Generation: v.Generation, State: state}
	if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_cloud_operations(id,instance_id,request_key,action,generation,state) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6)`, op.ID, id, key, action, v.Generation, state); err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, err
	}
	if action == "start" {
		s.prepare(ctx, op)
		_ = s.db.QueryRowContext(ctx, `SELECT state,error FROM donebot_cloud_operations WHERE id=$1::uuid`, op.ID).Scan(&op.State, &op.Error)
	} else if v.NodeID == "" {
		_, err = s.CompleteLocal(ctx, op)
		if err != nil {
			return v, op, err
		}
	}
	return v, op, nil
}
func (s *Service) prepare(ctx context.Context, op *Operation) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	v, err := scanInstance(tx.QueryRowContext(ctx, `SELECT `+instanceColumns+instanceJoin+` WHERE i.id=$1::uuid FOR UPDATE OF i`, op.InstanceID))
	if err != nil {
		return
	}
	if v.Generation != op.Generation || v.Desired != "running" {
		_, _ = tx.ExecContext(ctx, `UPDATE donebot_cloud_operations SET state='failed',error='superseded' WHERE id=$1::uuid AND state='billing'`, op.ID)
		_ = tx.Commit()
		return
	}
	if v.NodeID == "" {
		err = tx.QueryRowContext(ctx, `SELECT n.id::text FROM donebot_cloud_nodes n WHERE n.enabled AND n.last_seen>clock_timestamp()-INTERVAL '30 seconds' AND (SELECT COUNT(*) FROM donebot_cloud_instances i WHERE i.node_id=n.id AND i.deleted_at IS NULL)<n.capacity ORDER BY (SELECT COUNT(*) FROM donebot_cloud_instances i WHERE i.node_id=n.id AND i.deleted_at IS NULL)::float/n.capacity,n.id FOR UPDATE OF n SKIP LOCKED LIMIT 1`).Scan(&v.NodeID)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_instances SET node_id=$2::uuid WHERE id=$1::uuid`, v.ID, v.NodeID)
		}
	}
	if err != nil {
		_ = tx.Rollback()
		s.operationFailed(ctx, op, "capacity_unavailable")
		return
	}
	if _, err = s.Action(ctx, v.ID, "purchase", v.UserID); err != nil {
		_ = tx.Rollback()
		code := "billing_failed"
		if infraerrors.Reason(err) == "CLOUD_INSUFFICIENT_BALANCE" {
			code = "billing_insufficient_balance"
		}
		s.operationFailed(ctx, op, code)
		return
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_operations SET state='pending',error='' WHERE id=$1::uuid AND state='billing'`, op.ID); err == nil {
		_ = tx.Commit()
	}
}
func (s *Service) operationFailed(ctx context.Context, op *Operation, code string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE donebot_cloud_operations SET state='failed',error=$2 WHERE id=$1::uuid`, op.ID, code)
	_, _ = s.db.ExecContext(ctx, `UPDATE donebot_cloud_instances SET actual='error',error=$2 WHERE id=$1::uuid AND generation=$3`, op.InstanceID, code, op.Generation)
}
func (s *Service) CompleteLocal(ctx context.Context, op *Operation) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var local bool
	if err = tx.QueryRowContext(ctx, `SELECT node_id IS NULL AND NOT bound AND generation=$2 FROM donebot_cloud_instances WHERE id=$1::uuid FOR UPDATE`, op.InstanceID, op.Generation).Scan(&local); err != nil {
		return false, err
	}
	if !local || op.Action == "start" {
		return false, infraerrors.Conflict("CLOUD_NODE_REQUIRED", "Operation requires the assigned node")
	}
	if op.Action == "cancel" || op.Action == "purge" {
		if _, err := s.Action(ctx, op.InstanceID, "cancel", 0); err != nil {
			return false, err
		}
	}
	actual := "stopped"
	if op.Action == "purge" {
		actual = "deleted"
		if _, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_subscriptions SET purged_at=clock_timestamp(),state='cancelled',purge_locked=TRUE WHERE instance_id=$1::uuid`, op.InstanceID); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_operations SET state='succeeded' WHERE id=$1::uuid`, op.ID); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_instances SET actual=$2,error='',desired=CASE WHEN $2='deleted' THEN 'deleted' ELSE desired END,deleted_at=CASE WHEN $2='deleted' THEN clock_timestamp() ELSE deleted_at END WHERE id=$1::uuid`, op.InstanceID, actual); err != nil {
		return false, err
	}
	err = tx.Commit()
	return err == nil, err
}
func (s *Service) Operations(ctx context.Context, id string, owner int64) ([]Operation, error) {
	if _, err := s.Owned(ctx, id, owner); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,instance_id::text,action,generation,state,error FROM donebot_cloud_operations WHERE instance_id=$1::uuid ORDER BY created_at`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Operation{}
	for rows.Next() {
		var o Operation
		if err = rows.Scan(&o.ID, &o.InstanceID, &o.Action, &o.Generation, &o.State, &o.Error); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *Service) nodeAuth(ctx context.Context, id, token string) error {
	if !validID(id) || len(token) < 32 {
		return infraerrors.Unauthorized("CLOUD_NODE_REQUIRED", "Node authentication required")
	}
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donebot_cloud_nodes WHERE id=$1::uuid AND token_hash=$2)`, id, tokenHash(token)).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return infraerrors.Unauthorized("CLOUD_NODE_REQUIRED", "Node authentication required")
	}
	return s.enabled()
}

func (s *Service) Claim(ctx context.Context, node, token string, running []RunningCommand) (*Claim, error) {
	if err := s.nodeAuth(ctx, node, token); err != nil {
		return nil, err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE donebot_cloud_nodes SET last_seen=clock_timestamp() WHERE id=$1::uuid`, node)
	if err != nil {
		return nil, err
	}
	sweepCtx, sweepCancel := context.WithTimeout(ctx, time.Second)
	_ = s.Sweep(sweepCtx)
	sweepCancel()
	for _, r := range running {
		if !validID(r.ID) {
			continue
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE donebot_cloud_operations o SET lease_until=clock_timestamp()+INTERVAL '90 seconds' FROM donebot_cloud_instances i WHERE o.id=$1::uuid AND o.lease_hash=$2 AND o.state='leased' AND o.instance_id=i.id AND i.node_id=$3::uuid AND i.generation=o.generation`, r.ID, tokenHash(r.LeaseToken), node)
	}
	result := &Claim{Leases: []RunLease{}}
	if err = s.db.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&result.ServerTime); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.id::text,i.generation,LEAST(s.paid_until,clock_timestamp()+INTERVAL '20 seconds') FROM donebot_cloud_instances i JOIN donebot_cloud_subscriptions s ON s.instance_id=i.id JOIN users u ON u.id=i.user_id WHERE i.node_id=$1::uuid AND i.deleted_at IS NULL AND i.desired='running' AND s.state IN ('pending','active') AND s.paid_until>clock_timestamp() AND NOT s.purge_locked AND u.status='active' AND u.deleted_at IS NULL`, node)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var l RunLease
		if err = rows.Scan(&l.InstanceID, &l.Generation, &l.AuthorizedUntil); err != nil {
			rows.Close()
			return nil, err
		}
		result.Leases = append(result.Leases, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(running) > 0 {
		return result, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	op := &Operation{}
	err = tx.QueryRowContext(ctx, `SELECT o.id::text,o.instance_id::text,o.action,o.generation,o.state,o.error FROM donebot_cloud_operations o JOIN donebot_cloud_instances i ON i.id=o.instance_id WHERE i.node_id=$1::uuid AND o.generation=i.generation AND i.deleted_at IS NULL AND (o.state='pending' OR o.state='leased' AND o.lease_until<clock_timestamp() OR o.state='failed' AND (o.action<>'start' OR o.attempts>0)) AND o.retry_at<=clock_timestamp() ORDER BY o.created_at FOR UPDATE OF o SKIP LOCKED LIMIT 1`, node).Scan(&op.ID, &op.InstanceID, &op.Action, &op.Generation, &op.State, &op.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return result, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	op.LeaseToken = newToken()
	op.State = "leased"
	_, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_operations SET state='leased',lease_hash=$2,lease_until=clock_timestamp()+INTERVAL '90 seconds',attempts=attempts+1 WHERE id=$1::uuid`, op.ID, tokenHash(op.LeaseToken))
	if err != nil {
		return nil, err
	}
	if op.Action == "start" {
		_, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_instances SET bound=TRUE WHERE id=$1::uuid`, op.InstanceID)
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	result.Command = op
	return result, nil
}
func (s *Service) lockedCommand(ctx context.Context, tx *sql.Tx, node, id, lease string) (*Operation, *Instance, error) {
	if !validID(id) {
		return nil, nil, infraerrors.NotFound("CLOUD_COMMAND_NOT_FOUND", "Command not found")
	}
	o := &Operation{}
	err := tx.QueryRowContext(ctx, `SELECT o.id::text,o.instance_id::text,o.action,o.generation,o.state,o.error FROM donebot_cloud_operations o JOIN donebot_cloud_instances i ON i.id=o.instance_id WHERE o.id=$1::uuid AND i.node_id=$2::uuid AND o.lease_hash=$3 AND o.generation=i.generation AND o.state='leased' AND o.lease_until>clock_timestamp() FOR UPDATE OF i,o`, id, node, tokenHash(lease)).Scan(&o.ID, &o.InstanceID, &o.Action, &o.Generation, &o.State, &o.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, infraerrors.Conflict("CLOUD_STALE_COMMAND", "Command lease ended or assignment changed")
	}
	if err != nil {
		return nil, nil, err
	}
	v, err := scanInstance(tx.QueryRowContext(ctx, `SELECT `+instanceColumns+instanceJoin+` WHERE i.id=$1::uuid`, o.InstanceID))
	return o, v, err
}
func (s *Service) ConfirmPurge(ctx context.Context, node, token, id, lease string) error {
	if err := s.nodeAuth(ctx, node, token); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	o, v, err := s.lockedCommand(ctx, tx, node, id, lease)
	if err != nil {
		return err
	}
	if o.Action != "purge" || v.Desired != "deleting" {
		return infraerrors.Conflict("CLOUD_PURGE_CANCELLED", "Deletion is no longer authorized")
	}
	var until, databaseNow time.Time
	var locked bool
	if err = tx.QueryRowContext(ctx, `SELECT paid_until,clock_timestamp(),purge_locked FROM donebot_cloud_subscriptions WHERE instance_id=$1::uuid FOR UPDATE`, v.ID).Scan(&until, &databaseNow, &locked); err != nil {
		return err
	}
	if locked {
		return tx.Commit()
	}
	plan, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	if databaseNow.Before(until.Add(time.Duration(plan.RetentionDays) * 24 * time.Hour)) {
		return infraerrors.Conflict("CLOUD_RENEWED", "Subscription renewed or retention extended")
	}
	_, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_subscriptions SET purge_locked=TRUE WHERE instance_id=$1::uuid`, v.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) Report(ctx context.Context, node, token, id, lease, code string) error {
	if err := s.nodeAuth(ctx, node, token); err != nil {
		return err
	}
	if code != "" && !regexp.MustCompile(`^[a-z_]{1,100}$`).MatchString(code) {
		return infraerrors.BadRequest("CLOUD_REPORT_INVALID", "Invalid error code")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	o, v, err := s.lockedCommand(ctx, tx, node, id, lease)
	if err != nil {
		return err
	}
	if code != "" {
		_, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_operations SET state='failed',error=$2,retry_at=clock_timestamp()+INTERVAL '15 seconds' WHERE id=$1::uuid`, o.ID, code)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_instances SET actual='error',error=$2 WHERE id=$1::uuid`, v.ID, code)
		}
	} else {
		actual := "stopped"
		switch o.Action {
		case "start":
			sub, e := s.Action(ctx, v.ID, "activate", 0)
			if e != nil {
				return e
			}
			if sub.State != "active" || !sub.PaidUntil.After(time.Now()) {
				return infraerrors.Conflict("CLOUD_EXPIRED", "Subscription is inactive")
			}
			actual = "running"
		case "cancel":
			if _, e := s.Action(ctx, v.ID, "cancel", 0); e != nil {
				return e
			}
		case "purge":
			if !v.PurgeLocked {
				return infraerrors.Conflict("CLOUD_PURGE_NOT_CONFIRMED", "Deletion must be confirmed before execution")
			}
			var credentialExists bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donebot_cloud_runtime_credentials WHERE instance_id=$1::uuid)`, v.ID).Scan(&credentialExists); err != nil {
				return err
			}
			if credentialExists {
				if s.installs == nil {
					return infraerrors.ServiceUnavailable("CLOUD_RUNTIME_UNAVAILABLE", "Cloud credential revocation unavailable")
				}
				if err = s.installs.RevokeForUser(ctx, v.UserID, v.ID); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `DELETE FROM donebot_cloud_runtime_credentials WHERE instance_id=$1::uuid`, v.ID); err != nil {
					return err
				}
			}
			if _, e := s.Action(ctx, v.ID, "cancel", 0); e != nil {
				return e
			}
			actual = "deleted"
			if _, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_subscriptions SET purged_at=clock_timestamp(),state='cancelled' WHERE instance_id=$1::uuid`, v.ID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_operations SET state='succeeded',error='' WHERE id=$1::uuid`, o.ID)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_instances SET actual=$2,error='',desired=CASE WHEN $2='deleted' THEN 'deleted' ELSE desired END,deleted_at=CASE WHEN $2='deleted' THEN clock_timestamp() ELSE deleted_at END WHERE id=$1::uuid`, v.ID, actual)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) systemOperation(ctx context.Context, id, action, key string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var gen int64
	var desired string
	if err = tx.QueryRowContext(ctx, `SELECT generation,desired FROM donebot_cloud_instances WHERE id=$1::uuid AND deleted_at IS NULL FOR UPDATE`, id).Scan(&gen, &desired); err != nil {
		return err
	}
	var until time.Time
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT paid_until,state FROM donebot_cloud_subscriptions WHERE instance_id=$1::uuid FOR UPDATE`, id).Scan(&until, &state); err != nil {
		return err
	}
	if action != "start" && until.After(time.Now()) && state != "cancelled" {
		return nil
	}
	if action == "start" && (desired != "running" || !until.After(time.Now()) || state != "active" && state != "pending") {
		return nil
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donebot_cloud_operations WHERE instance_id=$1::uuid AND request_key=$2)`, id, key).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	if action == "purge" {
		desired = "deleting"
	}
	gen++
	if _, err = tx.ExecContext(ctx, `UPDATE donebot_cloud_instances SET generation=$2,desired=$3,actual='pending' WHERE id=$1::uuid`, id, gen, desired); err != nil {
		return err
	}
	operationState := "pending"
	if action == "start" {
		operationState = "billing"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO donebot_cloud_operations(id,instance_id,request_key,action,generation,state) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6)`, uuid.NewString(), id, key, action, gen, operationState); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) Sweep(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	var locked bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended('donebot-cloud-sweep',0))`).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended('donebot-cloud-sweep',0))`)
	}()
	rows, err := s.db.QueryContext(ctx, `SELECT o.id::text,o.instance_id::text,o.action,o.generation,o.state,o.error FROM donebot_cloud_operations o JOIN donebot_cloud_instances i ON i.id=o.instance_id WHERE o.generation=i.generation AND (o.state='billing' OR o.state='pending' AND i.node_id IS NULL AND o.action<>'start') ORDER BY o.created_at LIMIT 100`)
	if err != nil {
		return err
	}
	ops := []Operation{}
	for rows.Next() {
		var o Operation
		if err = rows.Scan(&o.ID, &o.InstanceID, &o.Action, &o.Generation, &o.State, &o.Error); err != nil {
			rows.Close()
			return err
		}
		ops = append(ops, o)
	}
	rows.Close()
	for i := range ops {
		if ops[i].State == "billing" {
			s.prepare(ctx, &ops[i])
		} else {
			_, _ = s.CompleteLocal(ctx, &ops[i])
		}
	}
	plan, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT `+instanceColumns+instanceJoin+` WHERE i.deleted_at IS NULL AND s.instance_id IS NOT NULL AND (s.paid_until<=clock_timestamp() OR i.desired='running' AND i.actual IN ('stopped','error')) ORDER BY i.reconciled_at,i.id LIMIT 100`)
	if err != nil {
		return err
	}
	instances := []Instance{}
	for rows.Next() {
		v, e := scanInstance(rows)
		if e != nil {
			rows.Close()
			return e
		}
		instances = append(instances, *v)
	}
	rows.Close()
	for _, v := range instances {
		if _, err = s.db.ExecContext(ctx, `UPDATE donebot_cloud_instances SET reconciled_at=clock_timestamp() WHERE id=$1::uuid`, v.ID); err != nil {
			return err
		}
		if !v.PurgeLocked && v.PaidUntil != nil && !v.PaidUntil.After(time.Now()) && time.Now().Before(v.PaidUntil.Add(time.Duration(plan.RetentionDays)*24*time.Hour)) && (v.SubscriptionState == "active" || v.SubscriptionState == "past_due") {
			_, _ = s.Action(ctx, v.ID, "renew", 0)
		}
		sub, e := s.Action(ctx, v.ID, "status", 0)
		if e != nil {
			continue
		}
		if !sub.PaidUntil.After(time.Now()) || sub.State == "cancelled" {
			if time.Now().After(sub.PaidUntil.Add(time.Duration(plan.RetentionDays) * 24 * time.Hour)) {
				_ = s.systemOperation(ctx, v.ID, "purge", "purge-"+sub.PaidUntil.UTC().Format("20060102T150405"))
			} else {
				_ = s.systemOperation(ctx, v.ID, "stop", "expire-"+sub.PaidUntil.UTC().Format("20060102T150405"))
			}
		} else if v.Desired == "running" && v.Actual != "running" {
			_ = s.systemOperation(ctx, v.ID, "start", "resume-"+sub.PaidUntil.UTC().Format("20060102T150405"))
		}
	}
	return nil
}

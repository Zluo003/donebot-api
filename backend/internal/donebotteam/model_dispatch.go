package donebotteam

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
)

// ModelRequestHash binds the exact HTTP method, request URI (including query) and unrevised body bytes.
func ModelRequestHash(method, uri string, body []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(method + "\n" + uri + "\n"))
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func executionRequest(e *Execution) ExecutionRequest {
	r := ExecutionRequest{TeamID: e.TeamID, Actor: e.ActorID, ActorVersion: e.ActorVersion, Key: e.Key, NodeKind: e.NodeKind, NodeID: e.NodeID, NodeVersion: e.NodeVersion,
		Purpose: e.Purpose, BotID: e.BotID, ThreadID: e.ThreadID, Private: e.PrivateTo.Valid, RequestHash: e.RequestHash}
	if e.CoordinatorID.Valid && e.CoordinatorGeneration.Valid {
		r.Coordinator = &Coordinator{TeamID: e.TeamID, InstanceID: e.CoordinatorID.String, Generation: e.CoordinatorGeneration.Int64}
	}
	return r
}

// ResolveModelExecution uses only immutable gateway records for actor, audience, executor and payer.
func (s *Service) ResolveModelExecution(ctx context.Context, token, method, uri string, body []byte) (*Execution, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if !validExecutionToken(token) {
		return nil, executionDenied()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	e, err := scanExecution(tx.QueryRowContext(ctx, `SELECT `+executionColumns+` FROM donebot_team_execution_grants g
		JOIN donebot_team_executions e ON e.id=g.execution_id WHERE g.token_hash=$1 AND g.expires_at>clock_timestamp()`, tokenHash(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, executionDenied()
	}
	if err != nil {
		return nil, err
	}
	if e.Purpose != "llm" || !e.CoordinatorID.Valid || e.RequestHash != ModelRequestHash(method, uri, body) {
		return nil, executionDenied()
	}
	if e, err = s.executionGrant(ctx, tx, token, executionRequest(e)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return e, nil
}

func bindModelDispatch(ctx context.Context, tx *sql.Tx, e *Execution, keyID int64, first bool) error {
	if e.Purpose != "llm" || !e.CoordinatorID.Valid {
		return executionDenied()
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT TRUE FROM donebot_team_billing_keys b JOIN api_keys k ON k.id=b.api_key_id
		JOIN groups g ON g.id=b.group_id WHERE b.team_id=$1::uuid AND b.payer_id=$2 AND b.api_key_id=$3
		AND k.user_id=b.payer_id AND k.group_id=b.group_id AND k.status='active' AND k.deleted_at IS NULL
		AND k.key LIKE 'sk-dbteam-%' AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp())
		AND g.status='active' AND g.deleted_at IS NULL FOR SHARE OF b,k,g`, e.TeamID, e.PayerID, keyID).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !valid {
		return executionDenied()
	}
	if err != nil {
		return err
	}
	if first {
		_, err = tx.ExecContext(ctx, `INSERT INTO donebot_team_model_dispatches(execution_id,api_key_id) VALUES($1::uuid,$2)`, e.ID, keyID)
		return err
	}
	var stored int64
	if err = tx.QueryRowContext(ctx, `SELECT api_key_id FROM donebot_team_model_dispatches WHERE execution_id=$1::uuid`, e.ID).Scan(&stored); errors.Is(err, sql.ErrNoRows) || err == nil && stored != keyID {
		return executionDenied()
	}
	return err
}

func (s *Service) AdmitModelExecution(ctx context.Context, token string, execution *Execution, keyID int64) (*Execution, bool, error) {
	if execution == nil || keyID < 1 {
		return nil, false, executionDenied()
	}
	return s.admitExecution(ctx, token, executionRequest(execution), keyID)
}

// A transport response is not durable provider completion or settlement. Acceptance can never be replayed as a new dispatch.
func (s *Service) ModelResponseReturned(ctx context.Context, id string) error {
	if err := s.available(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE donebot_team_executions e SET state='uncertain'
		WHERE e.id=$1::uuid AND e.state='accepted' AND EXISTS(SELECT 1 FROM donebot_team_model_dispatches d WHERE d.execution_id=e.id)`, id)
	return err
}

type ModelExecutionStatus struct {
	ID           string  `json:"id"`
	State        string  `json:"state"`
	ReceiptID    string  `json:"receipt_id"`
	BillingState string  `json:"billing_state"`
	Currency     string  `json:"currency"`
	ActualAmount *string `json:"actual_amount"`
}

func (s *Service) ModelExecutionStatus(ctx context.Context, coordinator *Coordinator, id string) (*ModelExecutionStatus, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if coordinator == nil || !validID(id) {
		return nil, executionDenied()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	e, err := scanExecution(tx.QueryRowContext(ctx, `SELECT `+executionColumns+` FROM donebot_team_executions e
		WHERE e.id=$1::uuid AND e.team_id=$2::uuid AND e.coordinator_id=$3::uuid AND e.coordinator_generation=$4`, id, coordinator.TeamID, coordinator.InstanceID, coordinator.Generation))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, executionDenied()
	}
	if err != nil {
		return nil, err
	}
	if _, err = s.authorizeStoredExecution(ctx, tx, e, executionRequest(e)); err != nil {
		return nil, err
	}
	status := &ModelExecutionStatus{ID: e.ID, State: e.State, ReceiptID: e.ID, Currency: "CNY", BillingState: "pending"}
	var amount sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT SUM(u.actual_cost)::text FROM donebot_team_model_dispatches d
		JOIN donebot_team_billing_keys b ON b.team_id=$2::uuid AND b.api_key_id=d.api_key_id
		JOIN usage_logs u ON u.api_key_id=d.api_key_id AND u.user_id=b.payer_id AND u.group_id=b.group_id
		AND u.request_id='client:'||d.execution_id::text WHERE d.execution_id=$1::uuid`, e.ID, e.TeamID).Scan(&amount)
	if err != nil {
		return nil, err
	}
	if amount.Valid {
		status.BillingState = "recorded"
		status.ActualAmount = &amount.String
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return status, nil
}

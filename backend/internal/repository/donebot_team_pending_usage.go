package repository

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.DoneBotTeamUsageRepository = (*usageBillingRepository)(nil)

func (r *usageBillingRepository) StageTeamUsage(ctx context.Context, pending *service.DoneBotTeamPendingUsage) error {
	if pending == nil || pending.ExecutionID == "" || service.DoneBotTeamUsageExecutionID(ctx) != pending.ExecutionID {
		return service.ErrDoneBotTeamBillingUnavailable
	}
	cmd, log := &pending.Command, &pending.Usage
	cmd.Normalize()
	hash, err := hex.DecodeString(cmd.RequestFingerprint)
	if err != nil || len(hash) != 32 || cmd.RequestID != "client:"+pending.ExecutionID ||
		cmd.SubscriptionID != nil || cmd.SubscriptionCost != 0 || cmd.BillingType != service.BillingTypeBalance ||
		cmd.UserID != log.UserID || cmd.APIKeyID != log.APIKeyID || cmd.AccountID != log.AccountID ||
		cmd.RequestID != log.RequestID || log.SubscriptionID != nil || log.EventType != "" && log.EventType != service.UsageEventCharge {
		return service.ErrDoneBotTeamBillingUnavailable
	}
	for _, amount := range []float64{cmd.BalanceCost, cmd.APIKeyQuotaCost, cmd.APIKeyRateLimitCost, cmd.AccountQuotaCost} {
		if amount < 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
			return service.ErrDoneBotTeamBillingUnavailable
		}
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	command, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	snapshot, err := json.Marshal(log)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO donebot_team_pending_usage(execution_id,command,usage_log,platform)
        SELECT e.id,$4::jsonb,$5::jsonb,$6 FROM donebot_team_executions e
        JOIN donebot_team_model_dispatches d ON d.execution_id=e.id AND d.api_key_id=$2
        JOIN donebot_team_billing_keys b ON b.team_id=e.team_id AND b.api_key_id=d.api_key_id AND b.payer_id=e.payer_id
        WHERE e.id=$1::uuid AND e.payer_id=$3 AND e.purpose='llm'
        AND e.state IN ('accepted','uncertain','completed','failed','cancelled')
        ON CONFLICT (execution_id) DO NOTHING`, pending.ExecutionID, cmd.APIKeyID, cmd.UserID, string(command), string(snapshot), pending.Platform)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 1 {
		return nil
	}
	var matches bool
	err = r.db.QueryRowContext(ctx, `SELECT command=$2::jsonb AND platform=$3 FROM donebot_team_pending_usage WHERE execution_id=$1::uuid`, pending.ExecutionID, string(command), pending.Platform).Scan(&matches)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrDoneBotTeamBillingUnavailable
	}
	if err != nil {
		return err
	}
	if !matches {
		return service.ErrUsageBillingRequestConflict
	}
	return nil
}

func (r *usageBillingRepository) PendingTeamUsage(ctx context.Context, limit int) ([]string, error) {
	if limit < 1 || limit > 100 {
		limit = 32
	}
	rows, err := r.db.QueryContext(ctx, `SELECT execution_id::text FROM donebot_team_pending_usage
        WHERE finished_at IS NULL AND next_attempt_at<=NOW() ORDER BY next_attempt_at,execution_id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *usageBillingRepository) ApplyTeamUsage(ctx context.Context, id string) (*service.DoneBotTeamPendingUsage, *service.UsageBillingApplyResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var command, snapshot []byte
	var settled sql.NullTime
	pending := &service.DoneBotTeamPendingUsage{ExecutionID: id}
	err = tx.QueryRowContext(ctx, `SELECT command,usage_log,platform,settled_at FROM donebot_team_pending_usage
        WHERE execution_id=$1::uuid AND version=1 FOR UPDATE`, id).Scan(&command, &snapshot, &pending.Platform, &settled)
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(command, &pending.Command); err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(snapshot, &pending.Usage); err != nil {
		return nil, nil, err
	}
	result := &service.UsageBillingApplyResult{}
	if !settled.Valid {
		cmd := &pending.Command
		applied, err := r.claimUsageBillingKey(ctx, tx, cmd)
		if err != nil {
			return nil, nil, err
		}
		result.Applied = applied
		if applied {
			if err := r.applyUsageBillingEffects(ctx, tx, cmd, result); err != nil {
				return nil, nil, err
			}
			if err := recordDoneBotTeamModelSettlementForExecution(ctx, tx, id, cmd); err != nil {
				return nil, nil, err
			}
		} else {
			var matches bool
			err := tx.QueryRowContext(ctx, `SELECT request_fingerprint=$2 AND actual_amount=$3
                FROM donebot_team_model_settlements WHERE execution_id=$1::uuid`, id, cmd.RequestFingerprint, cmd.BalanceCost).Scan(&matches)
			if err != nil {
				return nil, nil, err
			}
			if !matches {
				return nil, nil, service.ErrUsageBillingRequestConflict
			}
		}
		if err := execUsageLogInsertNoResult(ctx, tx, prepareUsageLogInsert(pending.Usage.UsageLog())); err != nil {
			return nil, nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE donebot_team_pending_usage SET settled_at=NOW() WHERE execution_id=$1::uuid`, id); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return pending, result, nil
}

func (r *usageBillingRepository) FinishTeamUsage(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE donebot_team_pending_usage SET finished_at=COALESCE(finished_at,NOW())
        WHERE execution_id=$1::uuid AND settled_at IS NOT NULL`, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return service.ErrDoneBotTeamBillingUnavailable
	}
	return nil
}

func (r *usageBillingRepository) RetryTeamUsage(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE donebot_team_pending_usage SET attempts=LEAST(attempts,30)+1,
        next_attempt_at=NOW()+make_interval(secs=>LEAST(60,POWER(2,LEAST(attempts+1,6)))::int)
        WHERE execution_id=$1::uuid AND finished_at IS NULL`, id)
	return err
}

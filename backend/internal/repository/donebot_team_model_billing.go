package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func recordDoneBotTeamModelSettlement(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) error {
	id := service.DoneBotTeamUsageExecutionID(ctx)
	if id == "" {
		return nil
	}
	return recordDoneBotTeamModelSettlementForExecution(ctx, tx, id, cmd)
}

func recordDoneBotTeamModelSettlementForExecution(ctx context.Context, tx *sql.Tx, id string, cmd *service.UsageBillingCommand) error {
	if cmd.RequestID != "client:"+id || cmd.SubscriptionID != nil || cmd.SubscriptionCost != 0 {
		return service.ErrDoneBotTeamBillingUnavailable
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO donebot_team_model_settlements(execution_id,actual_amount,request_fingerprint)
		SELECT e.id,$4,$5 FROM donebot_team_executions e
		JOIN donebot_team_model_dispatches d ON d.execution_id=e.id AND d.api_key_id=$2
		JOIN donebot_team_billing_keys b ON b.team_id=e.team_id AND b.api_key_id=d.api_key_id AND b.payer_id=e.payer_id
		WHERE e.id=$1::uuid AND e.payer_id=$3 AND e.purpose='llm'
		AND e.state IN ('accepted','uncertain','completed','failed','cancelled')`, id, cmd.APIKeyID, cmd.UserID, cmd.BalanceCost, cmd.RequestFingerprint)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return service.ErrDoneBotTeamBillingUnavailable
	}
	return nil
}

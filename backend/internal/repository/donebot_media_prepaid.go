package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
)

const doneBotPrechargeColumns = `receipt_id::text,user_id,api_key_id,account_id,group_id,model,media_kind,amount,total_amount,price_snapshot,provider_task_key,provider_request_id,state,COALESCE(settled_amount,0),refunded_amount`

type doneBotPrechargeScanner interface{ Scan(...any) error }

func scanDoneBotPrecharge(row doneBotPrechargeScanner) (*service.DoneBotMediaPrecharge, error) {
	p := &service.DoneBotMediaPrecharge{}
	var snapshot []byte
	if err := row.Scan(&p.ReceiptID, &p.UserID, &p.APIKeyID, &p.AccountID, &p.GroupID, &p.Model, &p.Kind, &p.Amount, &p.TotalAmount, &snapshot, &p.TaskKey, &p.ProviderRequestID, &p.State, &p.SettledAmount, &p.RefundedAmount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(snapshot, &p.Snapshot); err != nil {
		return nil, err
	}
	return p, nil
}

func (r *usageBillingRepository) LookupDoneBotMediaPrecharge(ctx context.Context, receipt string, keyID int64, taskKey string) (*service.DoneBotMediaPrecharge, error) {
	if receipt == "" && (keyID <= 0 || taskKey == "") {
		return nil, nil
	}
	return scanDoneBotPrecharge(r.db.QueryRowContext(ctx, `SELECT `+doneBotPrechargeColumns+` FROM donebot_media_precharges WHERE
        (NULLIF($1,'')::uuid IS NOT NULL AND receipt_id=NULLIF($1,'')::uuid AND ($2=0 OR api_key_id=$2)) OR
        ($1='' AND api_key_id=$2 AND (provider_task_key=$3 OR provider_request_id=$3)) LIMIT 1`, receipt, keyID, taskKey))
}

func (r *usageBillingRepository) PrepayDoneBotMedia(ctx context.Context, p *service.DoneBotMediaPrecharge) error {
	if p == nil || p.ReceiptID == "" || p.UserID <= 0 || p.APIKeyID <= 0 || p.AccountID <= 0 || p.GroupID <= 0 || p.Amount < 0 || p.TotalAmount < 0 || math.IsNaN(p.Amount) || math.IsInf(p.Amount, 0) || math.IsNaN(p.TotalAmount) || math.IsInf(p.TotalAmount, 0) {
		return service.ErrDoneBotMediaInvalid
	}
	snapshot, err := json.Marshal(p.Snapshot)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var concurrency int
	if err = tx.QueryRowContext(ctx, `SELECT concurrency FROM users WHERE id=$1 AND deleted_at IS NULL AND status='active' FOR UPDATE`, p.UserID).Scan(&concurrency); err != nil {
		return err
	}
	var opUser, opKey, opGroup int64
	var opKind, opStatus, opProvider string
	if err = tx.QueryRowContext(ctx, `SELECT user_id,api_key_id,group_id,media_kind,status,provider FROM donebot_media_operations WHERE receipt_id=$1::uuid FOR UPDATE`, p.ReceiptID).Scan(&opUser, &opKey, &opGroup, &opKind, &opStatus, &opProvider); err != nil {
		return err
	}
	if opUser != p.UserID || opKey != p.APIKeyID || opGroup != p.GroupID || opKind != p.Kind {
		return service.ErrDoneBotMediaInvalid
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donebot_media_precharges WHERE receipt_id=$1::uuid)`, p.ReceiptID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return service.ErrDoneBotMediaPending
	}
	if opStatus != "dispatching" && (opStatus != "accepted" || opKind != "image" || opProvider != "donebot") {
		return service.ErrDoneBotMediaPending
	}
	limit := concurrency / 2
	if limit < 1 {
		limit = 1
	}
	var pending int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_media_precharges WHERE user_id=$1 AND state='prepaid'`, p.UserID).Scan(&pending); err != nil {
		return err
	}
	if pending >= limit {
		return service.ErrDoneBotMediaConcurrency
	}
	var balance float64
	err = tx.QueryRowContext(ctx, `UPDATE users SET balance=balance-$1::numeric,updated_at=NOW() WHERE id=$2 AND balance >= $1::numeric RETURNING balance`, p.Amount, p.UserID).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrInsufficientBalance
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO donebot_media_precharges(receipt_id,user_id,api_key_id,account_id,group_id,model,media_kind,amount,total_amount,price_snapshot) VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb)`, p.ReceiptID, p.UserID, p.APIKeyID, p.AccountID, p.GroupID, p.Model, p.Kind, p.Amount, p.TotalAmount, string(snapshot))
	if err != nil {
		return err
	}
	if err = insertDoneBotMoneyEvent(ctx, tx, p, "charge", p.Amount, p.TotalAmount, ""); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE donebot_media_operations SET usage_request_id=$2,estimated_cents=$3::numeric*100,updated_at=NOW() WHERE receipt_id=$1::uuid`, p.ReceiptID, service.DoneBotMediaChargeRequestID(p.ReceiptID), p.Amount)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func insertDoneBotMoneyEvent(ctx context.Context, tx *sql.Tx, p *service.DoneBotMediaPrecharge, event string, actual, total float64, related string) error {
	request := service.DoneBotMediaChargeRequestID(p.ReceiptID)
	if event == "refund" {
		request = service.DoneBotMediaRefundRequestID(p.ReceiptID)
	}
	mode := p.Kind
	if mode == "music" {
		mode = "per_request"
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO usage_logs(user_id,api_key_id,account_id,group_id,request_id,model,requested_model,total_cost,actual_cost,rate_multiplier,account_rate_multiplier,billing_type,billing_mode,event_type,operation_receipt_id,related_request_id,created_at)
    VALUES($1,$2,$3,$4,$5,$6,$6,$7,$8,$9,$10,0,$11,$12,$13::uuid,NULLIF($14,''),NOW())`, p.UserID, p.APIKeyID, p.AccountID, p.GroupID, request, p.Model, total, actual, p.Snapshot.RateMultiplier, p.Snapshot.AccountRateMultiplier, mode, event, p.ReceiptID, related)
	return err
}

func (r *usageBillingRepository) BindDoneBotMediaTask(ctx context.Context, receipt string, keyID int64, taskKey string) error {
	if taskKey == "" || len(taskKey) > 600 {
		return service.ErrDoneBotMediaInvalid
	}
	taskID, provider := taskKey, "donebot"
	for _, prefix := range []string{"seedance:", "apimart:image:", "apimart:music:"} {
		if strings.HasPrefix(taskKey, prefix) {
			taskID = strings.TrimPrefix(taskKey, prefix)
			provider = strings.Split(prefix, ":")[0]
			break
		}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE donebot_media_precharges SET provider_task_key=$3,updated_at=NOW() WHERE receipt_id=$1::uuid AND api_key_id=$2 AND (provider_task_key='' OR provider_task_key=$3)`, receipt, keyID, taskKey)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrDoneBotMediaNotFound
	}
	result, err = tx.ExecContext(ctx, `UPDATE donebot_media_operations SET task_id=$3,provider_task_key=$4,provider=$5,updated_at=NOW() WHERE receipt_id=$1::uuid AND api_key_id=$2 AND (task_id='' OR task_id=$3) AND (provider_task_key='' OR provider_task_key=$4) AND (provider='' OR provider=$5)`, receipt, keyID, taskID, taskKey, provider)
	if err != nil {
		return err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrDoneBotMediaInvalid
	}
	return tx.Commit()
}

func (r *usageBillingRepository) SettleDoneBotMediaPrecharge(ctx context.Context, in service.DoneBotMediaPrepaidSettlement) (*service.UsageBillingApplyResult, error) {
	p, err := r.LookupDoneBotMediaPrecharge(ctx, in.ReceiptID, 0, "")
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, service.ErrDoneBotMediaNotFound
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var balance float64
	if err = tx.QueryRowContext(ctx, `SELECT balance FROM users WHERE id=$1 FOR UPDATE`, p.UserID).Scan(&balance); err != nil {
		return nil, err
	}
	p, err = scanDoneBotPrecharge(tx.QueryRowContext(ctx, `SELECT `+doneBotPrechargeColumns+` FROM donebot_media_precharges WHERE receipt_id=$1::uuid FOR UPDATE`, in.ReceiptID))
	if err != nil {
		return nil, err
	}
	if p.State != "prepaid" {
		return &service.UsageBillingApplyResult{}, nil
	}
	if math.IsNaN(in.ActualAmount) || math.IsInf(in.ActualAmount, 0) || math.IsNaN(in.TotalAmount) || math.IsInf(in.TotalAmount, 0) {
		return nil, service.ErrDoneBotMediaReconciliation
	}
	actual, total := decimal.NewFromFloat(in.ActualAmount).Round(service.UsageBillingMonetaryScale), decimal.NewFromFloat(in.TotalAmount).Round(service.UsageBillingMonetaryScale)
	amount, totalAmount := decimal.NewFromFloat(p.Amount), decimal.NewFromFloat(p.TotalAmount)
	if in.Failure {
		actual = decimal.Zero
		total = decimal.Zero
	} else if actual.IsNegative() || total.IsNegative() || actual.GreaterThan(amount) || total.GreaterThan(totalAmount) {
		return nil, service.ErrDoneBotMediaReconciliation
	}
	refund, totalRefund := amount.Sub(actual), totalAmount.Sub(total)
	result := &service.UsageBillingApplyResult{Applied: true}
	if refund.IsPositive() || totalRefund.IsPositive() || in.Failure {
		if err = insertDoneBotMoneyEvent(ctx, tx, p, "refund", refund.Neg().InexactFloat64(), totalRefund.Neg().InexactFloat64(), service.DoneBotMediaChargeRequestID(p.ReceiptID)); err != nil {
			return nil, err
		}
		if err = tx.QueryRowContext(ctx, `UPDATE users SET balance=balance+$1::numeric,updated_at=NOW() WHERE id=$2 RETURNING balance`, refund.String(), p.UserID).Scan(&balance); err != nil {
			return nil, err
		}
		result.NewBalance = &balance
	}
	state, opStatus := "settled", "completed"
	if in.Failure {
		state = "refunded"
		opStatus = "refunded"
	} else {
		if in.Effects == nil || in.Effects.APIKeyID != p.APIKeyID || in.Effects.UserID != p.UserID || in.Effects.AccountID != p.AccountID {
			return nil, service.ErrDoneBotMediaInvalid
		}
		cmd := *in.Effects
		cmd.BalanceCost = 0
		cmd.SubscriptionCost = 0
		cmd.Normalize()
		claimed, e := r.claimUsageBillingKey(ctx, tx, &cmd)
		if e != nil {
			return nil, e
		}
		if !claimed {
			return nil, fmt.Errorf("prepaid task settlement has an unrelated prior billing identity")
		}
		if err = r.applyUsageBillingEffects(ctx, tx, &cmd, result); err != nil {
			return nil, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE donebot_media_precharges SET state=$2,settled_amount=$3,refunded_amount=$4,provider_request_id=$5,updated_at=NOW() WHERE receipt_id=$1::uuid`, p.ReceiptID, state, actual.String(), refund.String(), in.ProviderRequestID)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE donebot_media_operations SET status=$2,updated_at=NOW() WHERE receipt_id=$1::uuid`, p.ReceiptID, opStatus)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *usageBillingRepository) TakeDoneBotMediaRecovery(ctx context.Context, limit int) ([]service.DoneBotMediaPrecharge, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	_, err := r.db.ExecContext(ctx, `UPDATE donebot_media_operations o SET status='uncertain',updated_at=NOW() FROM donebot_media_precharges p WHERE o.receipt_id=p.receipt_id AND p.state='prepaid' AND p.settlement_input IS NULL AND o.provider='donebot' AND o.media_kind='image' AND o.status IN ('dispatching','accepted','processing') AND p.created_at<NOW()-INTERVAL '30 minutes'`)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `WITH due AS (
        SELECT p.receipt_id FROM donebot_media_precharges p JOIN donebot_media_operations o USING(receipt_id)
        WHERE p.state='prepaid' AND p.recovery_after<=NOW() AND (p.provider_task_key<>'' OR p.settlement_input IS NOT NULL OR o.status IN ('failed','cancelled'))
        ORDER BY p.recovery_after,p.receipt_id FOR UPDATE OF p SKIP LOCKED LIMIT $1
    ), claimed AS (
        UPDATE donebot_media_precharges p SET recovery_after=NOW()+INTERVAL '60 seconds' FROM due WHERE p.receipt_id=due.receipt_id RETURNING p.*
    ) SELECT c.receipt_id::text,c.user_id,c.api_key_id,c.account_id,c.group_id,c.model,c.media_kind,c.amount,c.total_amount,c.price_snapshot,c.provider_task_key,c.provider_request_id,c.state,COALESCE(c.settled_amount,0),c.refunded_amount,o.status,c.created_at,c.settlement_input
    FROM claimed c JOIN donebot_media_operations o USING(receipt_id)`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []service.DoneBotMediaPrecharge{}
	for rows.Next() {
		p := service.DoneBotMediaPrecharge{}
		var snapshot, settlement []byte
		if err = rows.Scan(&p.ReceiptID, &p.UserID, &p.APIKeyID, &p.AccountID, &p.GroupID, &p.Model, &p.Kind, &p.Amount, &p.TotalAmount, &snapshot, &p.TaskKey, &p.ProviderRequestID, &p.State, &p.SettledAmount, &p.RefundedAmount, &p.OperationStatus, &p.CreatedAt, &settlement); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(snapshot, &p.Snapshot); err != nil {
			return nil, err
		}
		if len(settlement) > 0 {
			p.Settlement = &service.DoneBotMediaPrepaidSettlement{}
			if err = json.Unmarshal(settlement, p.Settlement); err != nil {
				return nil, err
			}
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (r *usageBillingRepository) MarkDoneBotMediaRejected(ctx context.Context, receipt string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE donebot_media_operations o SET status='failed',updated_at=NOW() WHERE o.receipt_id=$1::uuid AND o.status IN ('dispatching','accepted','processing','uncertain') AND NOT EXISTS(SELECT 1 FROM donebot_media_precharges p WHERE p.receipt_id=o.receipt_id AND p.state='settled')`, receipt)
	return err
}

func (r *usageBillingRepository) StageDoneBotMediaSettlement(ctx context.Context, in service.DoneBotMediaPrepaidSettlement) error {
	if in.Failure || in.Effects == nil {
		return service.ErrDoneBotMediaInvalid
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE donebot_media_precharges SET settlement_input=$2::jsonb,recovery_after=NOW(),updated_at=NOW() WHERE receipt_id=$1::uuid AND state='prepaid' AND (settlement_input IS NULL OR settlement_input=$2::jsonb) AND amount >= $3::numeric AND total_amount >= $4::numeric`, in.ReceiptID, string(payload), in.ActualAmount, in.TotalAmount)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	p, err := r.LookupDoneBotMediaPrecharge(ctx, in.ReceiptID, 0, "")
	if err != nil {
		return err
	}
	if p != nil && p.State == "settled" {
		return nil
	}
	return service.ErrDoneBotMediaReconciliation
}

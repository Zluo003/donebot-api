package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

func (r *usageBillingRepository) TakeDoneBotDispatches(ctx context.Context, limit int) ([]service.DoneBotDispatch, error) {
	rows, err := r.db.QueryContext(ctx, `WITH due AS (
 SELECT receipt_id FROM donebot_media_dispatches WHERE due_at<=NOW() AND state NOT IN ('completed','failed')
 AND (state<>'uncertain' OR (
   (NULLIF(payload->>'provider_task_id','') IS NOT NULL
     AND payload->>'protocol' IN ('apimart_images','apimart_music','seedance'))
   OR (jsonb_typeof(payload->'result')='object' AND (
     NULLIF(payload->>'response','') IS NOT NULL
     OR (payload->>'donebot_intent'='true' AND jsonb_array_length(
       CASE WHEN jsonb_typeof(payload->'artifacts')='array' THEN payload->'artifacts' ELSE '[]'::jsonb END)>0)
   ))
 ))
 ORDER BY due_at FOR UPDATE SKIP LOCKED LIMIT $1
 ) UPDATE donebot_media_dispatches d SET lease_token=$2::uuid,due_at=NOW()+INTERVAL '90 seconds'
 FROM due WHERE d.receipt_id=due.receipt_id RETURNING d.payload,d.lease_token::text`, limit, uuid.NewString())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := []service.DoneBotDispatch{}
	for rows.Next() {
		var raw []byte
		var lease string
		if err = rows.Scan(&raw, &lease); err != nil {
			return nil, err
		}
		var job service.DoneBotDispatch
		if err = json.Unmarshal(raw, &job); err != nil {
			return nil, err
		}
		job.Lease = lease
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (r *usageBillingRepository) SaveDoneBotDispatch(ctx context.Context, job *service.DoneBotDispatch, delay time.Duration) error {
	raw, err := json.Marshal(job)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE donebot_media_dispatches SET payload=$3::jsonb,state=$4,due_at=NOW()+$5*INTERVAL '1 millisecond',updated_at=NOW()
 WHERE receipt_id=$1::uuid AND lease_token=$2::uuid`, job.ReceiptID, job.Lease, string(raw), job.State, delay.Milliseconds())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.DoneBotDispatchLeaseError()
	}
	if job.State == "failed" || job.State == "uncertain" {
		diagnostic, _ := json.Marshal(map[string]any{"error": job.Diagnostic})
		status := job.State
		if status == "failed" {
			status = "refunded"
		}
		_, err = r.db.ExecContext(ctx, `UPDATE donebot_media_operations SET status=$2,replay_body=$3,replay_content_type='application/json',updated_at=NOW() WHERE receipt_id=$1::uuid AND (status NOT IN ('completed','refunded') OR ($2='refunded' AND status='refunded'))`, job.ReceiptID, status, diagnostic)
		return err
	}
	return nil
}

func (r *usageBillingRepository) LookupDoneBotDispatch(ctx context.Context, userID, keyID int64, id string) (*service.DoneBotDispatch, error) {
	var raw []byte
	err := r.db.QueryRowContext(ctx, `SELECT payload FROM donebot_media_dispatches WHERE user_id=$1 AND api_key_id=$2 AND task_id=$3`, userID, keyID, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrDoneBotMediaNotFound
	}
	if err != nil {
		return nil, err
	}
	var task service.DoneBotDispatch
	if err = json.Unmarshal(raw, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (r *usageBillingRepository) ReassignDoneBotMediaAccount(ctx context.Context, receipt string, keyID, accountID int64, _ float64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var clock int64
	lease := ""
	if dispatch := service.DoneBotDispatchFromContext(ctx); dispatch != nil {
		lease = dispatch.Lease
	}
	var valid bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donebot_media_dispatches WHERE receipt_id=$1::uuid AND ($2='' OR lease_token=NULLIF($2,'')::uuid) AND state='dispatching')`, receipt, lease).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return service.DoneBotDispatchLeaseError()
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO donebot_media_dispatch_clocks(user_id,next_post_at)
 SELECT user_id,NOW()+INTERVAL '1 second' FROM donebot_media_precharges WHERE receipt_id=$1::uuid AND api_key_id=$2
 ON CONFLICT(user_id) DO UPDATE SET next_post_at=NOW()+INTERVAL '1 second' WHERE donebot_media_dispatch_clocks.next_post_at<=NOW()
 RETURNING user_id`, receipt, keyID).Scan(&clock)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrDoneBotMediaDispatchSpacing
	}
	if err != nil {
		return err
	}
	var settlementValid bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(
	 SELECT 1 FROM donebot_media_precharges p
	 JOIN donebot_media_dispatches d ON d.receipt_id=p.receipt_id
	 WHERE p.receipt_id=$1::uuid AND p.api_key_id=$2 AND p.state='prepaid' AND p.settlement_input IS NULL
	 AND d.state='dispatching' AND (d.payload->>'account_id')::bigint=$3
	)`, receipt, keyID, accountID).Scan(&settlementValid)
	if err != nil {
		return err
	}
	if !settlementValid {
		return service.ErrDoneBotMediaPending
	}
	return tx.Commit()
}

func (r *usageBillingRepository) RenewDoneBotDispatchLease(ctx context.Context, receipt, lease string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE donebot_media_dispatches SET due_at=NOW()+INTERVAL '90 seconds' WHERE receipt_id=$1::uuid AND lease_token=$2::uuid AND state NOT IN ('completed','failed')`, receipt, lease)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.DoneBotDispatchLeaseError()
	}
	return nil
}

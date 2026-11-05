package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type doneBotMediaOperationRepository struct{ client *dbent.Client }

func NewDoneBotMediaOperationRepository(client *dbent.Client) service.DoneBotMediaOperationRepository {
	return &doneBotMediaOperationRepository{client: client}
}

const doneBotMediaColumns = `receipt_id::text,user_id,api_key_id,group_id,installation_id::text,
 idempotency_hash,request_fingerprint,media_kind,model,method,native_path,status,
 task_id,provider_task_key,provider,usage_request_id,estimated_cents::text,
 replay_status,replay_content_type,replay_retry_after,replay_body,created_at,updated_at`

const doneBotMediaOwnerWhere = `user_id=$1 AND api_key_id=$2 AND group_id=$3 AND installation_id=$4::uuid`

func doneBotMediaOwnerArgs(owner service.DoneBotMediaOwner) []any {
	return []any{owner.UserID, owner.APIKeyID, owner.GroupID, owner.InstallationID}
}

func scanDoneBotMediaOperation(rows *sql.Rows) (*service.DoneBotMediaOperation, error) {
	op := &service.DoneBotMediaOperation{}
	var estimated sql.NullString
	var status sql.NullInt32
	err := rows.Scan(&op.ReceiptID, &op.Owner.UserID, &op.Owner.APIKeyID, &op.Owner.GroupID, &op.Owner.InstallationID,
		&op.IdempotencyHash, &op.Fingerprint, &op.Kind, &op.Model, &op.Method, &op.Path, &op.Status,
		&op.TaskID, &op.TaskKey, &op.Provider, &op.UsageRequestID, &estimated,
		&status, &op.ReplayContentType, &op.ReplayRetryAfter, &op.ReplayBody, &op.CreatedAt, &op.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if estimated.Valid {
		value := json.Number(estimated.String)
		op.EstimatedCents = &value
	}
	if status.Valid {
		op.ReplayStatus = int(status.Int32)
	}
	return op, nil
}

func (r *doneBotMediaOperationRepository) query(ctx context.Context, query string, args ...any) (*service.DoneBotMediaOperation, error) {
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrDoneBotMediaNotFound
	}
	return scanDoneBotMediaOperation(rows)
}

func (r *doneBotMediaOperationRepository) Begin(ctx context.Context, op *service.DoneBotMediaOperation) (*service.DoneBotMediaOperation, bool, error) {
	var estimated any
	if op.EstimatedCents != nil {
		estimated = op.EstimatedCents.String()
	}
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, `INSERT INTO donebot_media_operations
 (receipt_id,user_id,api_key_id,group_id,installation_id,idempotency_hash,request_fingerprint,media_kind,model,method,native_path,status,usage_request_id,estimated_cents,task_id,provider_task_key,provider)
 VALUES($1::uuid,$2,$3,$4,$5::uuid,$6,$7,$8,$9,$10,$11,'dispatching',$12,$13::numeric,$14,$15,$16)
 ON CONFLICT(user_id,api_key_id,group_id,installation_id,idempotency_hash) DO NOTHING`,
		op.ReceiptID, op.Owner.UserID, op.Owner.APIKeyID, op.Owner.GroupID, op.Owner.InstallationID, op.IdempotencyHash, op.Fingerprint, op.Kind, op.Model, op.Method, op.Path, op.UsageRequestID, estimated, op.TaskID, op.TaskKey, op.Provider)
	if err != nil {
		return nil, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	args := append(doneBotMediaOwnerArgs(op.Owner), op.IdempotencyHash)
	stored, err := r.query(ctx, `SELECT `+doneBotMediaColumns+` FROM donebot_media_operations WHERE `+doneBotMediaOwnerWhere+` AND idempotency_hash=$5`, args...)
	return stored, n == 1, err
}

func (r *doneBotMediaOperationRepository) Lookup(ctx context.Context, owner service.DoneBotMediaOwner, receiptID string) (*service.DoneBotMediaOperation, error) {
	return r.query(ctx, `SELECT `+doneBotMediaColumns+` FROM donebot_media_operations WHERE `+doneBotMediaOwnerWhere+` AND receipt_id=$5::uuid`, append(doneBotMediaOwnerArgs(owner), receiptID)...)
}

func (r *doneBotMediaOperationRepository) LookupTask(ctx context.Context, owner service.DoneBotMediaOwner, taskID, kind string) (*service.DoneBotMediaOperation, error) {
	args := append(doneBotMediaOwnerArgs(owner), taskID, kind)
	return r.query(ctx, `SELECT `+doneBotMediaColumns+` FROM donebot_media_operations WHERE `+doneBotMediaOwnerWhere+`
 AND (task_id=$5 OR provider_task_key=$5) AND media_kind=$6 AND task_id<>'' ORDER BY created_at LIMIT 1`, args...)
}

func (r *doneBotMediaOperationRepository) SetEstimate(ctx context.Context, owner service.DoneBotMediaOwner, receiptID string, amount *json.Number) error {
	var estimate any
	if amount != nil {
		estimate = amount.String()
	}
	args := append(doneBotMediaOwnerArgs(owner), receiptID, estimate)
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, `UPDATE donebot_media_operations SET estimated_cents=$6::numeric,updated_at=NOW() WHERE `+doneBotMediaOwnerWhere+` AND receipt_id=$5::uuid AND status='dispatching'`, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return service.ErrDoneBotMediaNotFound
	}
	return nil
}

func (r *doneBotMediaOperationRepository) Complete(ctx context.Context, owner service.DoneBotMediaOwner, receiptID string, c service.DoneBotMediaCompletion) (*service.DoneBotMediaOperation, error) {
	args := append(doneBotMediaOwnerArgs(owner), receiptID, c.Status, c.TaskID, c.TaskKey, c.Provider, c.UsageRequestID, c.HTTPStatus, c.ContentType, c.Body, c.RetryAfter)
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, `UPDATE donebot_media_operations SET
 status=CASE WHEN status='processing' AND $6='accepted' THEN status ELSE $6 END,
 task_id=COALESCE(NULLIF($7,''),task_id),provider_task_key=COALESCE(NULLIF($8,''),provider_task_key),
 provider=COALESCE(NULLIF($9,''),provider),usage_request_id=CASE WHEN usage_request_id LIKE 'dbm:charge:%' THEN usage_request_id ELSE COALESCE(NULLIF($10,''),usage_request_id) END,
 replay_status=CASE WHEN replay_body IS NULL AND $13::bytea IS NOT NULL THEN NULLIF($11,0) ELSE replay_status END,
 replay_content_type=CASE WHEN replay_body IS NULL AND $13::bytea IS NOT NULL THEN $12 ELSE replay_content_type END,
 replay_retry_after=CASE WHEN replay_body IS NULL AND $13::bytea IS NOT NULL THEN $14 ELSE replay_retry_after END,
 replay_body=COALESCE(replay_body,$13::bytea),updated_at=NOW()
 WHERE `+doneBotMediaOwnerWhere+` AND receipt_id=$5::uuid
 AND (status IN ('dispatching','accepted','processing','uncertain') OR status=$6 OR ($6='refunded' AND status IN ('completed','failed','cancelled')))
 AND (task_id='' OR $7='' OR task_id=$7)
 AND (provider_task_key='' OR $8='' OR provider_task_key=$8)
 AND (provider='' OR $9='' OR provider=$9)
 AND (usage_request_id LIKE 'client:%' OR usage_request_id LIKE 'dbm:charge:%' OR $10='' OR usage_request_id=$10)`, args...)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	op, err := r.Lookup(ctx, owner, receiptID)
	if err != nil {
		return nil, err
	}
	if n == 0 && ((c.TaskID != "" && c.TaskID != op.TaskID) || (c.TaskKey != "" && c.TaskKey != op.TaskKey) || (c.UsageRequestID != "" && c.UsageRequestID != op.UsageRequestID && !strings.HasPrefix(op.UsageRequestID, "dbm:charge:"))) {
		return nil, fmt.Errorf("media operation metadata conflict")
	}
	return op, nil
}

package repository

import (
	"context"
	"database/sql"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type doneBotInstallRepository struct{ client *dbent.Client }

func NewDoneBotInstallRepository(client *dbent.Client) service.DoneBotInstallRepository {
	return &doneBotInstallRepository{client: client}
}
func (r *doneBotInstallRepository) WithInstallationLock(ctx context.Context, id string, fn func(context.Context) error) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	// Namespace the advisory lock to avoid colliding with other services' locks.
	if _, err = tx.ExecContext(txCtx, `SELECT pg_advisory_xact_lock(hashtextextended('donebot-install:' || $1, 0))`, id); err != nil {
		return err
	}
	if err = fn(txCtx); err != nil {
		return err
	}
	return tx.Commit()
}

const doneBotInstallSelect = `SELECT i.id, i.installation_id::text, i.name, i.user_id, i.api_key_id, i.group_id,
 CASE WHEN i.status = 'revoked' OR k.deleted_at IS NOT NULL OR k.status = 'disabled' THEN 'revoked' ELSE i.status END,
 COALESCE(i.fingerprint, ''), COALESCE(k.last_used_at, i.last_seen_at), COALESCE(k.expires_at, i.expires_at), i.created_at, i.updated_at
 FROM donebot_installs i JOIN api_keys k ON k.id = i.api_key_id `

func scanDoneBotInstall(rows *sql.Rows) (*service.DoneBotInstall, error) {
	i := &service.DoneBotInstall{}
	err := rows.Scan(&i.ID, &i.InstallationID, &i.Name, &i.UserID, &i.APIKeyID, &i.GroupID, &i.Status, &i.Fingerprint, &i.LastSeenAt, &i.ExpiresAt, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}
func (r *doneBotInstallRepository) get(ctx context.Context, where string, value any) (*service.DoneBotInstall, error) {
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, doneBotInstallSelect+where, value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrDoneBotInstallNotFound
	}
	return scanDoneBotInstall(rows)
}
func (r *doneBotInstallRepository) GetByInstallationID(ctx context.Context, id string) (*service.DoneBotInstall, error) {
	return r.get(ctx, "WHERE i.installation_id = $1::uuid", id)
}
func (r *doneBotInstallRepository) GetByAPIKeyID(ctx context.Context, id int64) (*service.DoneBotInstall, error) {
	return r.get(ctx, "WHERE i.api_key_id = $1", id)
}
func (r *doneBotInstallRepository) GetByID(ctx context.Context, id int64) (*service.DoneBotInstall, error) {
	return r.get(ctx, "WHERE i.id = $1", id)
}
func (r *doneBotInstallRepository) Create(ctx context.Context, i *service.DoneBotInstall) error {
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `INSERT INTO donebot_installs (installation_id,name,user_id,api_key_id,group_id,status,fingerprint)
 VALUES ($1::uuid,$2,$3,$4,$5,$6,NULLIF($7,'')) RETURNING id,created_at,updated_at`, i.InstallationID, i.Name, i.UserID, i.APIKeyID, i.GroupID, i.Status, i.Fingerprint)
	if err != nil {
		return translatePersistenceError(err, nil, service.ErrDoneBotInstallConflict)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return fmt.Errorf("insert installation returned no row")
	}
	return rows.Scan(&i.ID, &i.CreatedAt, &i.UpdatedAt)
}
func (r *doneBotInstallRepository) MarkRevoked(ctx context.Context, id int64) error {
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, `UPDATE donebot_installs SET status='revoked', updated_at=NOW() WHERE id=$1`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return service.ErrDoneBotInstallNotFound
	}
	return nil
}
func (r *doneBotInstallRepository) List(ctx context.Context, params pagination.PaginationParams) ([]service.DoneBotInstall, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, `SELECT COUNT(*) FROM donebot_installs`)
	if err != nil {
		return nil, nil, err
	}
	var total int64
	if rows.Next() {
		err = rows.Scan(&total)
	} else {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = client.QueryContext(ctx, doneBotInstallSelect+`ORDER BY i.id DESC LIMIT $1 OFFSET $2`, params.Limit(), params.Offset())
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	installs := make([]service.DoneBotInstall, 0)
	for rows.Next() {
		i, err := scanDoneBotInstall(rows)
		if err != nil {
			return nil, nil, err
		}
		installs = append(installs, *i)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return installs, paginationResultFromTotal(total, params), nil
}

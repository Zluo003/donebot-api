package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type assetRepository struct{ client *dbent.Client }

func NewAssetRepository(client *dbent.Client) service.AssetRepository {
	return &assetRepository{client}
}
func (r *assetRepository) WithHashLock(ctx context.Context, hash string, fn func(context.Context) error) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txctx := dbent.NewTxContext(ctx, tx)
	if _, err = tx.ExecContext(txctx, `SELECT pg_advisory_xact_lock(hashtextextended('asset:' || $1,0))`, hash); err != nil {
		return err
	}
	if err = fn(txctx); err != nil {
		return err
	}
	return tx.Commit()
}

const assetColumns = `id,sha256,mime,size,storage,object_key,url,owner_key_id,created_at,expires_at`

func scanAsset(rows *sql.Rows) (*service.GatewayAsset, error) {
	a := &service.GatewayAsset{}
	err := rows.Scan(&a.ID, &a.SHA256, &a.MIME, &a.Size, &a.Storage, &a.ObjectKey, &a.URL, &a.OwnerKeyID, &a.CreatedAt, &a.ExpiresAt)
	return a, err
}
func (r *assetRepository) get(ctx context.Context, where string, value any) (*service.GatewayAsset, error) {
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `SELECT `+assetColumns+` FROM assets WHERE `+where, value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrAssetNotFound
	}
	return scanAsset(rows)
}
func (r *assetRepository) GetByID(ctx context.Context, id string) (*service.GatewayAsset, error) {
	return r.get(ctx, `id=$1`, id)
}
func (r *assetRepository) GetBySHA256(ctx context.Context, hash string) (*service.GatewayAsset, error) {
	return r.get(ctx, `sha256=$1`, hash)
}
func (r *assetRepository) Create(ctx context.Context, a *service.GatewayAsset) error {
	_, err := clientFromContext(ctx, r.client).ExecContext(ctx, `INSERT INTO assets (`+assetColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, a.ID, a.SHA256, a.MIME, a.Size, a.Storage, a.ObjectKey, a.URL, a.OwnerKeyID, a.CreatedAt, a.ExpiresAt)
	return err
}
func (r *assetRepository) AddOwner(ctx context.Context, id string, keyID int64) error {
	_, err := clientFromContext(ctx, r.client).ExecContext(ctx, `INSERT INTO asset_owners (asset_id,api_key_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, keyID)
	return err
}
func (r *assetRepository) HasOwner(ctx context.Context, id string, keyID int64) (bool, error) {
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `SELECT EXISTS (SELECT 1 FROM asset_owners WHERE asset_id=$1 AND api_key_id=$2)`, id, keyID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return false, fmt.Errorf("asset owner query returned no row")
	}
	var ok bool
	err = rows.Scan(&ok)
	return ok, err
}
func (r *assetRepository) RemoveOwner(ctx context.Context, id string, keyID int64) (bool, error) {
	_, err := clientFromContext(ctx, r.client).ExecContext(ctx, `DELETE FROM asset_owners WHERE asset_id=$1 AND api_key_id=$2`, id, keyID)
	if err != nil {
		return false, err
	}
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `SELECT NOT EXISTS (SELECT 1 FROM asset_owners WHERE asset_id=$1)`, id)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return false, fmt.Errorf("asset owners query returned no row")
	}
	var empty bool
	err = rows.Scan(&empty)
	return empty, err
}
func (r *assetRepository) Delete(ctx context.Context, id string) error {
	_, err := clientFromContext(ctx, r.client).ExecContext(ctx, `DELETE FROM assets WHERE id=$1`, id)
	return err
}
func (r *assetRepository) ListExpired(ctx context.Context, now time.Time, limit int) ([]service.GatewayAsset, error) {
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `SELECT `+assetColumns+` FROM assets WHERE expires_at<=$1 ORDER BY expires_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []service.GatewayAsset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

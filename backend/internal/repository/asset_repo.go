package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type assetRepository struct {
	client *dbent.Client
	db     *sql.DB
}

func NewAssetRepository(client *dbent.Client) service.AssetRepository {
	return &assetRepository{client: client}
}

type assetConnectionContextKey struct{}
type assetSQLExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (r *assetRepository) executor(ctx context.Context) assetSQLExecutor {
	if conn, _ := ctx.Value(assetConnectionContextKey{}).(*sql.Conn); conn != nil {
		return conn
	}
	return clientFromContext(ctx, r.client)
}
func ProvideAssetRepository(client *dbent.Client, db *sql.DB) service.AssetRepository {
	return &assetRepository{client: client, db: db}
}
func (r *assetRepository) WithHashLock(ctx context.Context, hash string, fn func(context.Context) error) error {
	if conn, _ := ctx.Value(assetConnectionContextKey{}).(*sql.Conn); conn != nil {
		return fn(ctx)
	}
	if tx := dbent.TxFromContext(ctx); tx != nil {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('asset:' || $1,0))`, hash); err != nil {
			return err
		}
		return fn(ctx)
	}
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

const assetColumns = `id,sha256,mime,size,storage,object_key,url,owner_key_id,created_at,expires_at,purpose,storage_config`

func scanAsset(rows *sql.Rows) (*service.GatewayAsset, error) {
	a := &service.GatewayAsset{}
	var storageConfig []byte
	err := rows.Scan(&a.ID, &a.SHA256, &a.MIME, &a.Size, &a.Storage, &a.ObjectKey, &a.URL, &a.OwnerKeyID, &a.CreatedAt, &a.ExpiresAt, &a.Purpose, &storageConfig)
	a.StorageConfig = storageConfig
	return a, err
}
func (r *assetRepository) get(ctx context.Context, where string, value any) (*service.GatewayAsset, error) {
	rows, err := r.executor(ctx).QueryContext(ctx, `SELECT `+assetColumns+` FROM assets WHERE `+where, value)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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
	return r.get(ctx, `sha256=$1 AND purpose='upload'`, hash)
}
func (r *assetRepository) Create(ctx context.Context, a *service.GatewayAsset) error {
	_, err := r.executor(ctx).ExecContext(ctx, `INSERT INTO assets (`+assetColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, a.ID, a.SHA256, a.MIME, a.Size, a.Storage, a.ObjectKey, a.URL, a.OwnerKeyID, a.CreatedAt, a.ExpiresAt, assetPurpose(a.Purpose), assetStorageConfig(a.StorageConfig))
	return err
}
func (r *assetRepository) AddOwner(ctx context.Context, id string, keyID int64) error {
	_, err := r.executor(ctx).ExecContext(ctx, `INSERT INTO asset_owners (asset_id,api_key_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, keyID)
	return err
}
func (r *assetRepository) HasOwner(ctx context.Context, id string, keyID int64) (bool, error) {
	rows, err := r.executor(ctx).QueryContext(ctx, `SELECT EXISTS (SELECT 1 FROM asset_owners WHERE asset_id=$1 AND api_key_id=$2)`, id, keyID)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return false, fmt.Errorf("asset owner query returned no row")
	}
	var ok bool
	err = rows.Scan(&ok)
	return ok, err
}
func (r *assetRepository) RemoveOwner(ctx context.Context, id string, keyID int64) (bool, error) {
	_, err := r.executor(ctx).ExecContext(ctx, `DELETE FROM asset_owners WHERE asset_id=$1 AND api_key_id=$2`, id, keyID)
	if err != nil {
		return false, err
	}
	rows, err := r.executor(ctx).QueryContext(ctx, `SELECT NOT EXISTS (SELECT 1 FROM asset_owners WHERE asset_id=$1)`, id)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return false, fmt.Errorf("asset owners query returned no row")
	}
	var empty bool
	err = rows.Scan(&empty)
	return empty, err
}
func (r *assetRepository) Delete(ctx context.Context, id string) error {
	_, err := r.executor(ctx).ExecContext(ctx, `DELETE FROM assets WHERE id=$1`, id)
	return err
}
func (r *assetRepository) ListExpired(ctx context.Context, now time.Time, limit int) ([]service.GatewayAsset, error) {
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	rows, err := r.executor(ctx).QueryContext(ctx, `SELECT `+assetColumns+` FROM assets WHERE expires_at<=$1 ORDER BY expires_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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

func assetPurpose(p string) string {
	if p == "" {
		return service.AssetPurposeUpload
	}
	return p
}
func assetStorageConfig(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}
func (r *assetRepository) GetByPurposeSHA256(ctx context.Context, purpose, hash string) (*service.GatewayAsset, error) {
	rows, err := r.executor(ctx).QueryContext(ctx, `SELECT `+assetColumns+` FROM assets WHERE purpose=$1 AND sha256=$2`, purpose, hash)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrAssetNotFound
	}
	return scanAsset(rows)
}
func (r *assetRepository) ListOldest(ctx context.Context, purpose string, limit int) ([]service.GatewayAsset, error) {
	if limit <= 0 || limit > 10000 {
		limit = 100
	}
	rows, err := r.executor(ctx).QueryContext(ctx, `SELECT `+assetColumns+` FROM assets WHERE purpose=$1 ORDER BY created_at,id LIMIT $2`, purpose, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []service.GatewayAsset
	for rows.Next() {
		a, e := scanAsset(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}
func (r *assetRepository) StorageUsage(ctx context.Context, purpose string) (int64, int64, error) {
	rows, err := r.executor(ctx).QueryContext(ctx, `SELECT COALESCE(SUM(size),0),COUNT(*) FROM assets WHERE purpose=$1`, purpose)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return 0, 0, rows.Err()
	}
	var size, count int64
	err = rows.Scan(&size, &count)
	return size, count, err
}

func (r *assetRepository) ApplyStorageRetention(ctx context.Context, purpose string, hours int) error {
	_, err := r.executor(ctx).ExecContext(ctx, `UPDATE assets SET expires_at=created_at+($2 * interval '1 hour') WHERE purpose=$1 AND expires_at>created_at+($2 * interval '1 hour')`, purpose, hours)
	return err
}

// A pinned connection holds a session advisory lock while each statement is
// committed independently. Failed writes cannot resurrect evicted metadata,
// and concurrent waiters do not exhaust the pool needed by the lock holder.
func (r *assetRepository) WithPurposeLock(ctx context.Context, purpose string, fn func(context.Context) error) error {
	if r.db == nil {
		return r.WithHashLock(ctx, "policy:"+purpose, fn)
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended('asset:policy:' || $1,0))`, purpose); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return err
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, unlockErr := conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended('asset:policy:' || $1,0))`, purpose); unlockErr != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	return fn(context.WithValue(ctx, assetConnectionContextKey{}, conn))
}

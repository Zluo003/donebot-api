package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type assetTestRepo struct {
	rows   map[string]*GatewayAsset
	owners map[string]map[int64]bool
}

func (r *assetTestRepo) WithHashLock(ctx context.Context, _ string, fn func(context.Context) error) error {
	return fn(ctx)
}
func (r *assetTestRepo) GetByID(_ context.Context, id string) (*GatewayAsset, error) {
	if a := r.rows[id]; a != nil {
		return a, nil
	}
	return nil, ErrAssetNotFound
}
func (r *assetTestRepo) GetBySHA256(ctx context.Context, hash string) (*GatewayAsset, error) {
	for _, a := range r.rows {
		if a.SHA256 == hash {
			return a, nil
		}
	}
	return nil, ErrAssetNotFound
}
func (r *assetTestRepo) Create(_ context.Context, a *GatewayAsset) error {
	r.rows[a.ID] = a
	return nil
}
func (r *assetTestRepo) AddOwner(_ context.Context, id string, keyID int64) error {
	if r.owners[id] == nil {
		r.owners[id] = map[int64]bool{}
	}
	r.owners[id][keyID] = true
	return nil
}
func (r *assetTestRepo) HasOwner(_ context.Context, id string, keyID int64) (bool, error) {
	return r.owners[id][keyID], nil
}
func (r *assetTestRepo) RemoveOwner(_ context.Context, id string, keyID int64) (bool, error) {
	delete(r.owners[id], keyID)
	return len(r.owners[id]) == 0, nil
}
func (r *assetTestRepo) Delete(_ context.Context, id string) error {
	delete(r.rows, id)
	delete(r.owners, id)
	return nil
}
func (r *assetTestRepo) ListExpired(_ context.Context, now time.Time, _ int) ([]GatewayAsset, error) {
	var out []GatewayAsset
	for _, a := range r.rows {
		if !a.ExpiresAt.After(now) {
			out = append(out, *a)
		}
	}
	return out, nil
}

type assetTestStorage struct {
	saves      int
	deletes    int
	failDelete bool
}

func (s *assetTestStorage) Save(context.Context, string, string, []byte) (string, error) {
	s.saves++
	return "", nil
}
func (s *assetTestStorage) Delete(context.Context, string) error {
	s.deletes++
	if s.failDelete {
		return errors.New("object storage temporarily unavailable")
	}
	return nil
}
func (*assetTestStorage) Kind() string { return "local" }
func TestAssetUploadDeduplicatesAndKeepsOwnerAccessSeparate(t *testing.T) {
	repo := &assetTestRepo{rows: map[string]*GatewayAsset{}, owners: map[string]map[int64]bool{}}
	storage := &assetTestStorage{}
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}, AssetStorage: config.AssetStorageConfig{TTLHours: 168, MaxBytes: 1024, PublicBaseURL: "https://assets.example.com"}}
	cfg.JWT.Secret = "asset-test-signing-secret"
	svc := NewAssetService(repo, storage, cfg)
	first, err := svc.Upload(context.Background(), 1, "ref.png", "image/png", []byte("same-content"), "")
	require.NoError(t, err)
	require.False(t, first.Deduplicated)
	second, err := svc.Upload(context.Background(), 2, "ref.png", "image/png", []byte("same-content"), "")
	require.NoError(t, err)
	require.True(t, second.Deduplicated)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, 1, storage.saves)
	_, err = svc.Get(context.Background(), 3, first.ID, "")
	require.ErrorIs(t, err, ErrAssetNotFound)
	require.NoError(t, svc.Delete(context.Background(), 1, first.ID))
	require.Equal(t, 0, storage.deletes)
	_, err = svc.Get(context.Background(), 2, first.ID, "")
	require.NoError(t, err)
	require.NoError(t, svc.Delete(context.Background(), 2, first.ID))
	require.Equal(t, 1, storage.deletes)
}
func TestAssetSignedURLExpiresAndCannotBeRebound(t *testing.T) {
	cfg := &config.Config{AssetStorage: config.AssetStorageConfig{PublicBaseURL: "https://assets.example.com"}}
	cfg.JWT.Secret = "asset-test-signing-secret"
	svc := NewAssetService(nil, nil, cfg)
	now := time.Now().UTC()
	svc.now = func() time.Time { return now }
	token := svc.sign("ast_one", now.Add(time.Minute))
	require.NoError(t, svc.verify("ast_one", token))
	require.Error(t, svc.verify("ast_two", token))
	svc.now = func() time.Time { return now.Add(time.Minute) }
	require.Error(t, svc.verify("ast_one", token))
}
func TestAssetUploadRejectsLimitBeforeStorageAndCleanupRemovesExpired(t *testing.T) {
	repo := &assetTestRepo{rows: map[string]*GatewayAsset{}, owners: map[string]map[int64]bool{}}
	storage := &assetTestStorage{}
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}, AssetStorage: config.AssetStorageConfig{TTLHours: 1, MaxBytes: 2}}
	cfg.JWT.Secret = "asset-test-signing-secret"
	svc := NewAssetService(repo, storage, cfg)
	_, err := svc.Upload(context.Background(), 1, "x", "text/plain", []byte("abc"), "")
	require.ErrorIs(t, err, ErrAssetTooLarge)
	require.Zero(t, storage.saves)
	a := &GatewayAsset{ID: "ast_expired", SHA256: "expired", ObjectKey: "ex/expired", Storage: "local", ExpiresAt: time.Now().Add(-time.Second)}
	repo.rows[a.ID] = a
	count, err := svc.CleanupExpired(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, 1, storage.deletes)
	_, err = repo.GetByID(context.Background(), a.ID)
	require.True(t, errors.Is(err, ErrAssetNotFound))
}

func TestAssetCleanupKeepsRecordOnStorageFailureAndRetries(t *testing.T) {
	repo := &assetTestRepo{rows: map[string]*GatewayAsset{}, owners: map[string]map[int64]bool{}}
	storage := &assetTestStorage{failDelete: true}
	cfg := &config.Config{}
	repo.rows["ast_expired"] = &GatewayAsset{ID: "ast_expired", SHA256: "expired", Storage: "local", ObjectKey: "ex/expired", ExpiresAt: time.Now().Add(-time.Second)}
	svc := NewAssetService(repo, storage, cfg)
	count, err := svc.CleanupExpired(context.Background(), 100)
	require.Error(t, err)
	require.Zero(t, count)
	require.Len(t, repo.rows, 1)
	storage.failDelete = false
	count, err = svc.CleanupExpired(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Empty(t, repo.rows)
}

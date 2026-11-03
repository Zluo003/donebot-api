package service

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type orderedAssetCleanupRepo struct{ *assetTestRepo }

func (r orderedAssetCleanupRepo) ListExpired(ctx context.Context, now time.Time, limit int) ([]GatewayAsset, error) {
	assets, err := r.assetTestRepo.ListExpired(ctx, now, limit)
	sort.Slice(assets, func(i, j int) bool { return assets[i].ExpiresAt.Before(assets[j].ExpiresAt) })
	return assets, err
}

type selectivelyFailingAssetStorage struct {
	assetTestStorage
	failKey string
	failure error
}

func (s *selectivelyFailingAssetStorage) Delete(ctx context.Context, key string) error {
	if key == s.failKey {
		return s.failure
	}
	return s.assetTestStorage.Delete(ctx, key)
}

func TestAssetCleanupContinuesAfterUnavailableDriverAndObjectFailure(t *testing.T) {
	now := time.Now().UTC()
	rows := map[string]*GatewayAsset{
		"ast_s3":   {ID: "ast_s3", SHA256: "s3", Storage: "s3", ObjectKey: "s3/expired", ExpiresAt: now.Add(-3 * time.Hour)},
		"ast_bad":  {ID: "ast_bad", SHA256: "bad", Storage: "local", ObjectKey: "local/bad", ExpiresAt: now.Add(-2 * time.Hour)},
		"ast_good": {ID: "ast_good", SHA256: "good", Storage: "local", ObjectKey: "local/good", ExpiresAt: now.Add(-time.Hour)},
	}
	repo := orderedAssetCleanupRepo{&assetTestRepo{rows: rows, owners: map[string]map[int64]bool{}}}
	deleteFailure := errors.New("delete forbidden for this object")
	storage := &selectivelyFailingAssetStorage{failKey: "local/bad", failure: deleteFailure}
	svc := NewAssetService(repo, storage, &config.Config{})

	count, err := svc.CleanupExpired(context.Background(), 100)
	require.Equal(t, 1, count, "one bad driver/object must not block other expired assets")
	require.ErrorIs(t, err, ErrAssetStorageUnavailable)
	require.ErrorIs(t, err, deleteFailure, "return all cleanup failures for diagnosis and retry")
	require.NotContains(t, rows, "ast_good")
	require.Contains(t, rows, "ast_s3", "unavailable driver must retain its record")
	require.Contains(t, rows, "ast_bad", "failed deletion must retain its record")
	require.Equal(t, 1, storage.deletes)
}

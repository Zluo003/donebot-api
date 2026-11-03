//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDoneBotStorageRealCapacityAndOriginalDriverAfterSwitch(t *testing.T) {
	ctx := context.Background()
	stamp := time.Now().UnixNano()
	users := NewUserRepository(integrationEntClient, integrationDB)
	user := &service.User{Email: fmt.Sprintf("storage-%d@example.com", stamp), PasswordHash: "test", Role: service.RoleUser, Status: service.StatusActive}
	require.NoError(t, users.Create(ctx, user))
	key := &service.APIKey{UserID: user.ID, Key: fmt.Sprintf("storage-%d", stamp), Name: "storage test", Status: service.StatusActive}
	require.NoError(t, NewAPIKeyRepository(integrationEntClient, integrationDB).Create(ctx, key))
	settingsRepo := NewSettingRepository(integrationEntClient)
	before, _ := settingsRepo.GetValue(ctx, "donebot_storage_policies")
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`DELETE FROM assets WHERE owner_key_id=$1`, key.ID)
		_, _ = integrationDB.Exec(`DELETE FROM api_keys WHERE id=$1`, key.ID)
		_, _ = integrationDB.Exec(`DELETE FROM users WHERE id=$1`, user.ID)
		if before == "" {
			_ = settingsRepo.Delete(ctx, "donebot_storage_policies")
		} else {
			_ = settingsRepo.Set(ctx, "donebot_storage_policies", before)
		}
	})
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}, AssetStorage: config.AssetStorageConfig{Driver: "local", LocalPath: t.TempDir(), TTLHours: 1, MaxBytes: 100, PublicBaseURL: "https://assets.example.com"}}
	cfg.JWT.Secret = "storage-test-signing"
	cfg.ImageStorage.Enabled = true
	assetsRepo := ProvideAssetRepository(integrationEntClient, integrationDB)
	var failSave, failDelete atomic.Bool
	factory := ProvideDoneBotStorageFactory()
	policies := service.NewDoneBotStorageService(settingsRepo, nil, func(ctx context.Context, p service.DoneBotStoragePolicy) (service.AssetStorage, error) {
		base, e := factory(ctx, p)
		return &storageFailureInjection{AssetStorage: base, fail: &failSave, failDelete: &failDelete}, e
	}, cfg, assetsRepo)
	input, err := policies.Get(ctx)
	require.NoError(t, err)
	input.Upload.MaxStorageBytes = 10
	input.Upload.CleanupThresholdPercent = 90
	input.Generated.PublicBaseURL = "https://assets.example.com"
	_, err = policies.Update(ctx, *input)
	require.NoError(t, err)
	storage, err := ProvideAssetStorage(cfg)
	require.NoError(t, err)
	assets := service.ProvideAssetService(assetsRepo, storage, cfg, policies)
	defer assets.Stop()
	first, err := assets.Upload(ctx, key.ID, "one", "text/plain", []byte("1111"), "")
	require.NoError(t, err)
	second, err := assets.Upload(ctx, key.ID, "two", "text/plain", []byte("2222"), "")
	require.NoError(t, err)
	failSave.Store(true)
	_, err = assets.Upload(ctx, key.ID, "failure", "text/plain", []byte("FAIL"), "")
	require.Error(t, err)
	failSave.Store(false)
	var afterFailure int64
	require.NoError(t, integrationDB.QueryRow(`SELECT COALESCE(SUM(size),0) FROM assets WHERE purpose='upload' AND owner_key_id=$1`, key.ID).Scan(&afterFailure))
	require.Equal(t, int64(4), afterFailure, "failed upload must not roll back committed evictions")

	_, err = assets.Upload(ctx, key.ID, "three", "text/plain", []byte("33"), "")
	require.NoError(t, err)
	_, err = assets.Get(ctx, key.ID, first.ID, "")
	require.ErrorIs(t, err, service.ErrAssetNotFound)
	generated, err := assets.UploadGenerated(ctx, key.ID, "two", "text/plain", []byte("2222"), "")
	require.NoError(t, err)
	require.NotEqual(t, second.ID, generated.ID)
	// Editing the local path only affects new objects. Existing signed URLs and
	// deletion retain the original policy across an application reconstruction.
	input.Upload.LocalPath = t.TempDir()
	_, err = policies.Update(ctx, *input)
	require.NoError(t, err)
	restarted := service.ProvideAssetService(assetsRepo, storage, cfg, policies)
	defer restarted.Stop()
	u, err := url.Parse(second.URL)
	require.NoError(t, err)
	_, body, err := restarted.Download(ctx, second.ID, u.Query().Get("token"))
	require.NoError(t, err)
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	require.Equal(t, "2222", string(got))
	failDelete.Store(true)
	require.Error(t, restarted.Delete(ctx, key.ID, second.ID))
	_, err = restarted.Get(ctx, key.ID, second.ID, "")
	require.NoError(t, err, "failed physical deletion preserves ownership")
	failDelete.Store(false)
	require.NoError(t, restarted.Delete(ctx, key.ID, second.ID))
	_, err = os.Stat(filepath.Join(cfg.AssetStorage.LocalPath, "upload", second.SHA256[:2], second.SHA256+".txt"))
	require.True(t, os.IsNotExist(err))
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, e := restarted.Upload(ctx, key.ID, "concurrent", "text/plain", []byte(fmt.Sprintf("%03d", n)), "")
			errs <- e
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	var size int64
	require.NoError(t, integrationDB.QueryRow(`SELECT COALESCE(SUM(size),0) FROM assets WHERE purpose='upload' AND owner_key_id=$1`, key.ID).Scan(&size))
	require.LessOrEqual(t, size, int64(9))
}

type storageFailureInjection struct {
	service.AssetStorage
	fail       *atomic.Bool
	failDelete *atomic.Bool
}

func (s *storageFailureInjection) Save(ctx context.Context, key, contentType string, data []byte) (string, error) {
	if s.fail.Load() {
		return "", errors.New("injected upload failure")
	}
	return s.AssetStorage.Save(ctx, key, contentType, data)
}
func (s *storageFailureInjection) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.AssetStorage.(service.LocalAssetReader).Open(ctx, key)
}

func (s *storageFailureInjection) Delete(ctx context.Context, key string) error {
	if s.failDelete.Load() {
		return errors.New("injected delete failure")
	}
	return s.AssetStorage.Delete(ctx, key)
}

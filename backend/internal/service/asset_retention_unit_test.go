//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestDoneBotOutputUsesConfiguredGeneratedRetention(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	repo := &policyAssetRepo{assetTestRepo: &assetTestRepo{rows: map[string]*GatewayAsset{}, owners: map[string]map[int64]bool{}}}
	storage := &assetTestStorage{}
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}, AssetStorage: config.AssetStorageConfig{TTLHours: 168, MaxBytes: 1024}}
	cfg.JWT.Secret = "asset-test-signing-secret"
	settings := newStubSettingRepo()
	storageSettings := NewDoneBotStorageService(settings, reversibleEncryptor{}, func(context.Context, DoneBotStoragePolicy) (AssetStorage, error) {
		return storage, nil
	}, cfg, repo)
	policies := storageSettings.defaults()
	policies.Generated.Enabled = true
	policies.Generated.TTLHours = 5
	raw, err := json.Marshal(policies)
	require.NoError(t, err)
	require.NoError(t, settings.Set(ctx, settingKeyDoneBotStorage, string(raw)))

	assets := NewAssetService(repo, storage, cfg)
	assets.storageSettings = storageSettings
	assets.now = func() time.Time { return now }

	upload, err := assets.Upload(ctx, 42, "upload.png", "image/png", []byte("upload"), "")
	require.NoError(t, err)
	generated, err := assets.UploadGenerated(ctx, 42, "generated.png", "image/png", []byte("generated"), "")
	require.NoError(t, err)
	output, err := assets.UploadDoneBotOutput(ctx, 42, "output.png", "image/png", []byte("output"))
	require.NoError(t, err)
	require.Equal(t, AssetPurposeDoneBotOutput, output.Purpose)
	require.Equal(t, now.Add(5*time.Hour), output.ExpiresAt)
	require.Equal(t, generated.ExpiresAt, output.ExpiresAt)

	assets.now = func() time.Time { return output.ExpiresAt }
	_, err = assets.Get(ctx, 42, output.ID, "")
	require.ErrorIs(t, err, ErrAssetNotFound)
	count, err := assets.CleanupExpired(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.Equal(t, 2, storage.deletes)
	require.NotNil(t, repo.rows[upload.ID])
}

func TestPersistedDoneBotOutputUsesConfiguredGeneratedRetention(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	repo := &policyAssetRepo{assetTestRepo: &assetTestRepo{rows: map[string]*GatewayAsset{}, owners: map[string]map[int64]bool{}}}
	storage := &assetTestStorage{}
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}, AssetStorage: config.AssetStorageConfig{TTLHours: 9, MaxBytes: 1024}}
	settings := newStubSettingRepo()
	storageSettings := NewDoneBotStorageService(settings, reversibleEncryptor{}, func(context.Context, DoneBotStoragePolicy) (AssetStorage, error) {
		return storage, nil
	}, cfg, repo)
	policies := storageSettings.defaults()
	policies.Generated.Enabled = true
	policies.Generated.TTLHours = 5
	raw, err := json.Marshal(policies)
	require.NoError(t, err)
	require.NoError(t, settings.Set(ctx, settingKeyDoneBotStorage, string(raw)))
	assets := NewAssetService(repo, storage, cfg)
	assets.storageSettings = storageSettings
	assets.now = func() time.Time { return now }

	upload, err := assets.Upload(ctx, 1, "upload.txt", "text/plain", []byte("upload"), "")
	require.NoError(t, err)
	generated, err := assets.UploadGenerated(ctx, 1, "generated.txt", "text/plain", []byte("generated"), "")
	require.NoError(t, err)
	output, err := assets.UploadDoneBotOutput(ctx, 1, "output.png", "image/png", []byte("output"))
	require.NoError(t, err)
	require.Equal(t, AssetPurposeDoneBotOutput, output.Purpose)
	require.Equal(t, now.Add(5*time.Hour), output.ExpiresAt)
	require.Equal(t, generated.ExpiresAt, output.ExpiresAt)
	_, err = assets.Get(ctx, 1, output.ID, "")
	require.NoError(t, err)
	assets.now = func() time.Time { return output.ExpiresAt }
	_, err = assets.Get(ctx, 1, output.ID, "")
	require.ErrorIs(t, err, ErrAssetNotFound)
	count, err := assets.CleanupExpired(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.Equal(t, 2, storage.deletes)
	require.NotNil(t, repo.rows[upload.ID])
}

//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type policyAssetRepo struct{ *assetTestRepo }

func (r *policyAssetRepo) GetByPurposeSHA256(ctx context.Context, purpose, hash string) (*GatewayAsset, error) {
	for _, a := range r.rows {
		if a.Purpose == purpose && a.SHA256 == hash {
			return a, nil
		}
	}
	return nil, ErrAssetNotFound
}
func (r *policyAssetRepo) ListOldest(_ context.Context, purpose string, limit int) ([]GatewayAsset, error) {
	var out []GatewayAsset
	for _, a := range r.rows {
		if a.Purpose == purpose {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (r *policyAssetRepo) StorageUsage(_ context.Context, purpose string) (int64, int64, error) {
	var size, count int64
	for _, a := range r.rows {
		if a.Purpose == purpose {
			size += a.Size
			count++
		}
	}
	return size, count, nil
}

func TestDoneBotStoragePoliciesPersistMaskAndKeepSecret(t *testing.T) {
	ctx := context.Background()
	repo := newStubSettingRepo()
	cfg := &config.Config{}
	cfg.Totp.EncryptionKeyConfigured = true
	cfg.AssetStorage.LocalPath = t.TempDir()
	s := NewDoneBotStorageService(repo, reversibleEncryptor{}, nil, cfg, nil)
	p, err := s.Get(ctx)
	require.NoError(t, err)
	p.Generated.Enabled = false
	p.Upload.Driver = "s3"
	p.Upload.S3 = DoneBotStorageS3{Bucket: "bucket", AccessKeyID: "id", SecretAccessKey: "private"}
	_, err = s.Update(ctx, *p)
	require.NoError(t, err)
	require.NotContains(t, repo.values[settingKeyDoneBotStorage], `"private"`)
	got, err := s.Get(ctx)
	require.NoError(t, err)
	require.Empty(t, got.Upload.S3.SecretAccessKey)
	require.True(t, got.Upload.SecretConfigured)
	_, err = s.Update(ctx, *got)
	require.NoError(t, err)
	effective, err := s.Policy(ctx, "upload")
	require.NoError(t, err)
	require.Equal(t, "private", effective.S3.SecretAccessKey)
}
func TestDoneBotStorageNamespacesCapacityOldestAndSwitch(t *testing.T) {
	ctx := context.Background()
	repo := &policyAssetRepo{&assetTestRepo{rows: map[string]*GatewayAsset{}, owners: map[string]map[int64]bool{}}}
	cfg := &config.Config{}
	cfg.Totp.EncryptionKeyConfigured = true
	cfg.DoneBot.Enabled = true
	cfg.ImageStorage.Enabled = true
	cfg.JWT.Secret = "signing"
	cfg.AssetStorage.LocalPath = "old"
	cfg.AssetStorage.MaxBytes = 100
	cfg.AssetStorage.TTLHours = 1
	stores := map[string]*assetTestStorage{}
	factory := func(_ context.Context, p DoneBotStoragePolicy) (AssetStorage, error) {
		if stores[p.LocalPath] == nil {
			stores[p.LocalPath] = &assetTestStorage{}
		}
		return stores[p.LocalPath], nil
	}
	settings := NewDoneBotStorageService(newStubSettingRepo(), reversibleEncryptor{}, factory, cfg, repo)
	pol, err := settings.Get(ctx)
	require.NoError(t, err)
	pol.Upload.MaxStorageBytes = 10
	pol.Upload.CleanupThresholdPercent = 90
	pol.Generated.PublicBaseURL = "https://gateway.example"
	_, err = settings.Update(ctx, *pol)
	require.NoError(t, err)
	svc := NewAssetService(repo, &assetTestStorage{}, cfg)
	svc.storageSettings = settings
	now := time.Now()
	svc.now = func() time.Time { return now }
	a, err := svc.Upload(ctx, 1, "a", "text/plain", []byte("aaaa"), "https://gateway.example")
	require.NoError(t, err)
	now = now.Add(time.Second)
	b, err := svc.Upload(ctx, 1, "b", "text/plain", []byte("bbbb"), "https://gateway.example")
	require.NoError(t, err)
	now = now.Add(time.Second)
	_, err = svc.Upload(ctx, 1, "c", "text/plain", []byte("cc"), "https://gateway.example")
	require.NoError(t, err)
	require.Nil(t, repo.rows[a.ID])
	require.NotNil(t, repo.rows[b.ID])
	require.Equal(t, 1, stores["old"].deletes)
	generated, err := svc.UploadGenerated(ctx, 1, "same", "text/plain", []byte("bbbb"), "")
	require.NoError(t, err)
	require.NotEqual(t, b.ID, generated.ID)
	pol.Upload.LocalPath = "new"
	_, err = settings.Update(ctx, *pol)
	require.NoError(t, err)
	require.NoError(t, svc.Delete(ctx, 1, b.ID))
	require.Equal(t, 2, stores["old"].deletes)
	_, err = svc.Upload(ctx, 1, "large", "text/plain", []byte("0123456789"), "")
	require.Error(t, err, "file above threshold must fail without deleting other data")
}

func TestDoneBotStorageRejectsInvalidLimitsAndFailedDeletes(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.AssetStorage.LocalPath = t.TempDir()
	s := NewDoneBotStorageService(newStubSettingRepo(), reversibleEncryptor{}, nil, cfg, nil)
	base, err := s.Get(ctx)
	require.NoError(t, err)
	for _, mutate := range []func(*DoneBotStoragePolicy){func(p *DoneBotStoragePolicy) { p.MaxStorageBytes = 0 }, func(p *DoneBotStoragePolicy) { p.MaxFileBytes = -1 }, func(p *DoneBotStoragePolicy) { p.CleanupThresholdPercent = 101 }, func(p *DoneBotStoragePolicy) { p.TTLHours = 0 }, func(p *DoneBotStoragePolicy) { p.PublicBaseURL = "http://user:secret@example.com" }} {
		bad := *base
		mutate(&bad.Upload)
		_, err = s.Update(ctx, bad)
		require.Error(t, err)
	}
	bad := *base
	bad.Generated.Enabled = true
	bad.Generated.PublicBaseURL = ""
	_, err = s.Update(ctx, bad)
	require.Error(t, err)
}

type storageDeleteFailureRepo struct {
	*policyAssetRepo
	fail bool
}

func (r *storageDeleteFailureRepo) Delete(ctx context.Context, id string) error {
	if r.fail {
		return errors.New("metadata temporarily unavailable")
	}
	return r.policyAssetRepo.Delete(ctx, id)
}
func TestDoneBotStorageDeleteFailureKeepsOwnerForRetry(t *testing.T) {
	for _, metadata := range []bool{false, true} {
		t.Run(fmt.Sprintf("metadata=%t", metadata), func(t *testing.T) {
			ctx := context.Background()
			repo := &storageDeleteFailureRepo{policyAssetRepo: &policyAssetRepo{&assetTestRepo{rows: map[string]*GatewayAsset{}, owners: map[string]map[int64]bool{}}}}
			storage := &assetTestStorage{}
			cfg := &config.Config{}
			cfg.DoneBot.Enabled = true
			cfg.AssetStorage.LocalPath = t.TempDir()
			cfg.JWT.Secret = "signing"
			settings := NewDoneBotStorageService(newStubSettingRepo(), nil, func(context.Context, DoneBotStoragePolicy) (AssetStorage, error) { return storage, nil }, cfg, repo)
			svc := NewAssetService(repo, storage, cfg)
			svc.storageSettings = settings
			a, err := svc.Upload(ctx, 1, "file", "text/plain", []byte("bytes"), "")
			require.NoError(t, err)
			repo.fail = metadata
			storage.failDelete = !metadata
			require.Error(t, svc.Delete(ctx, 1, a.ID))
			_, err = svc.Get(ctx, 1, a.ID, "")
			require.NoError(t, err, "owner must retain retry access after failed physical or metadata delete")
			repo.fail = false
			storage.failDelete = false
			require.NoError(t, svc.Delete(ctx, 1, a.ID))
		})
	}
}

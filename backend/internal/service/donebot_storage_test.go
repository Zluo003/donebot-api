//go:build unit

package service

import (
	"context"
	"encoding/json"
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
		if a.Purpose == purpose || purpose == AssetPurposeGenerated && a.Purpose == AssetPurposeDoneBotOutput {
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
		if a.Purpose == purpose || purpose == AssetPurposeGenerated && a.Purpose == AssetPurposeDoneBotOutput {
			size += a.Size
			count++
		}
	}
	return size, count, nil
}

func TestDoneBotStorageLegacyPublicBaseURLDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, driver, publicURL, nestedURL, generatedURL, wantUpload, wantGenerated string
	}{
		{name: "top-level S3 URL takes precedence", driver: "s3", publicURL: "https://upload.example", nestedURL: "https://legacy.example", wantUpload: "https://upload.example", wantGenerated: "https://upload.example"},
		{name: "legacy S3 URL supports retention beyond presign limit", driver: "s3", nestedURL: "https://legacy.example", wantUpload: "https://legacy.example", wantGenerated: "https://legacy.example"},
		{name: "local ignores legacy S3 URL", driver: "local", nestedURL: "https://legacy.example"},
		{name: "local keeps its own public URL", driver: "local", publicURL: "https://gateway.example", nestedURL: "https://legacy.example", wantUpload: "https://gateway.example", wantGenerated: "https://gateway.example"},
		{name: "generated legacy S3 URL remains independent", driver: "s3", nestedURL: "https://legacy.example", generatedURL: "https://generated.example", wantUpload: "https://legacy.example", wantGenerated: "https://generated.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{AssetStorage: config.AssetStorageConfig{
				Driver: tc.driver, PublicBaseURL: tc.publicURL, TTLHours: 240,
				S3: config.ImageStorageConfig{Bucket: "uploads", AccessKeyID: "id", SecretAccessKey: "secret", PublicBaseURL: tc.nestedURL},
			}}
			require.NoError(t, cfg.AssetStorage.Validate())
			if tc.generatedURL != "" {
				cfg.ImageStorage = config.ImageStorageConfig{Enabled: true, Driver: "s3", Bucket: "outputs", AccessKeyID: "id", SecretAccessKey: "secret", PublicBaseURL: tc.generatedURL}
			}
			service := NewDoneBotStorageService(nil, nil, nil, cfg, nil)
			policies := service.defaults()
			require.Equal(t, tc.wantUpload, policies.Upload.PublicBaseURL)
			require.Equal(t, tc.wantGenerated, policies.Generated.PublicBaseURL)
			require.NotNil(t, policies.Upload.LocalPublicBaseURL)
			require.NotNil(t, policies.Upload.S3.PublicBaseURL)
			if tc.driver == "s3" {
				require.Empty(t, *policies.Upload.LocalPublicBaseURL)
				require.Equal(t, tc.wantUpload, *policies.Upload.S3.PublicBaseURL)
			} else {
				require.Equal(t, tc.wantUpload, *policies.Upload.LocalPublicBaseURL)
				require.Equal(t, tc.nestedURL, *policies.Upload.S3.PublicBaseURL)
			}
			require.NoError(t, normalizeDoneBotStoragePolicy(&policies.Upload, AssetPurposeUpload), "validated legacy S3 settings must remain valid as a runtime policy")
			if tc.driver == "s3" {
				require.Equal(t, "uploads", policies.Upload.S3.Bucket)
			}
			if tc.generatedURL != "" {
				require.Equal(t, "outputs", policies.Generated.S3.Bucket)
				require.Equal(t, tc.generatedURL, *policies.Generated.S3.PublicBaseURL)
				require.NoError(t, normalizeDoneBotStoragePolicy(&policies.Generated, AssetPurposeGenerated))
			}
		})
	}
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
	pol.Generated.LocalPublicBaseURL = doneBotStorageURL("https://gateway.example")
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
	for _, mutate := range []func(*DoneBotStoragePolicy){func(p *DoneBotStoragePolicy) { p.MaxStorageBytes = 0 }, func(p *DoneBotStoragePolicy) { p.MaxFileBytes = -1 }, func(p *DoneBotStoragePolicy) { p.CleanupThresholdPercent = 101 }, func(p *DoneBotStoragePolicy) { p.TTLHours = 0 }, func(p *DoneBotStoragePolicy) {
		p.LocalPublicBaseURL = doneBotStorageURL("http://user:secret@example.com")
	}, func(p *DoneBotStoragePolicy) {
		p.S3.PublicBaseURL = doneBotStorageURL("https://objects.example/path?token=secret")
	}} {
		bad := *base
		mutate(&bad.Upload)
		_, err = s.Update(ctx, bad)
		require.Error(t, err)
	}
	bad := *base
	bad.Generated.Enabled = true
	bad.Generated.LocalPublicBaseURL = doneBotStorageURL("")
	_, err = s.Update(ctx, bad)
	require.Error(t, err)
}

func TestDoneBotStoragePublicBaseURLsStayIndependentAcrossReloads(t *testing.T) {
	ctx := context.Background()
	repo := newStubSettingRepo()
	cfg := &config.Config{}
	cfg.Totp.EncryptionKeyConfigured = true
	cfg.AssetStorage.LocalPath = t.TempDir()
	cfg.AssetStorage.PublicBaseURL = "https://gateway.example"
	service := NewDoneBotStorageService(repo, reversibleEncryptor{}, nil, cfg, nil)
	input, err := service.Get(ctx)
	require.NoError(t, err)
	input.Upload.Driver = "s3"
	input.Upload.PublicBaseURL = "https://stale-mirror.example"
	input.Upload.LocalPublicBaseURL = doneBotStorageURL(" https://local.example/ ")
	input.Upload.S3 = DoneBotStorageS3{Bucket: "outputs", AccessKeyID: "fixture-access", SecretAccessKey: "fixture-secret", PublicBaseURL: doneBotStorageURL(" https://r2.example/ ")}
	response, err := service.Update(ctx, *input)
	require.NoError(t, err)
	require.Equal(t, "https://r2.example", response.Upload.PublicBaseURL)
	require.Equal(t, "https://local.example", *response.Upload.LocalPublicBaseURL)
	require.Equal(t, "https://r2.example", *response.Upload.S3.PublicBaseURL)
	require.Empty(t, response.Upload.S3.SecretAccessKey)
	require.True(t, response.Upload.SecretConfigured)
	require.NotContains(t, repo.values[settingKeyDoneBotStorage], `"fixture-secret"`)

	service = NewDoneBotStorageService(repo, reversibleEncryptor{}, nil, cfg, nil)
	input, err = service.Get(ctx)
	require.NoError(t, err)
	input.Upload.Driver = "local"
	response, err = service.Update(ctx, *input)
	require.NoError(t, err)
	require.Equal(t, "https://local.example", response.Upload.PublicBaseURL)
	require.Equal(t, "https://r2.example", *response.Upload.S3.PublicBaseURL)

	service = NewDoneBotStorageService(repo, reversibleEncryptor{}, nil, cfg, nil)
	input, err = service.Get(ctx)
	require.NoError(t, err)
	input.Upload.Driver = "s3"
	response, err = service.Update(ctx, *input)
	require.NoError(t, err)
	require.Equal(t, "https://r2.example", response.Upload.PublicBaseURL)
	effective, err := service.Policy(ctx, AssetPurposeUpload)
	require.NoError(t, err)
	require.Equal(t, "fixture-secret", effective.S3.SecretAccessKey)

	response.Upload.S3.PublicBaseURL = doneBotStorageURL("")
	response.Upload.TTLHours = 168
	response, err = service.Update(ctx, *response)
	require.NoError(t, err)
	require.Empty(t, response.Upload.PublicBaseURL, "an explicit empty S3 URL selects presigned links")
	require.Equal(t, "https://local.example", *response.Upload.LocalPublicBaseURL)
	service = NewDoneBotStorageService(repo, reversibleEncryptor{}, nil, cfg, nil)
	effective, err = service.Policy(ctx, AssetPurposeUpload)
	require.NoError(t, err)
	require.Empty(t, effective.PublicBaseURL)
	response.Upload.TTLHours = 169
	_, err = service.Update(ctx, *response)
	require.ErrorContains(t, err, "S3 ttl_hours cannot exceed 168")
}

func TestDoneBotStorageLegacyUpdatesOnlyChangeSelectedPublicBaseURL(t *testing.T) {
	ctx := context.Background()
	repo := newStubSettingRepo()
	cfg := &config.Config{}
	cfg.Totp.EncryptionKeyConfigured = true
	cfg.AssetStorage.LocalPath = t.TempDir()
	service := NewDoneBotStorageService(repo, reversibleEncryptor{}, nil, cfg, nil)
	input, err := service.Get(ctx)
	require.NoError(t, err)
	input.Upload.LocalPublicBaseURL = doneBotStorageURL("https://local.example")
	input.Upload.S3 = DoneBotStorageS3{Bucket: "bucket", AccessKeyID: "fixture-access", SecretAccessKey: "fixture-secret", PublicBaseURL: doneBotStorageURL("https://r2.example")}
	response, err := service.Update(ctx, *input)
	require.NoError(t, err)
	for _, tc := range []struct{ driver, publicURL, localURL, s3URL string }{
		{"local", "https://new-local.example", "https://new-local.example", "https://r2.example"},
		{"s3", "https://new-r2.example", "https://new-local.example", "https://new-r2.example"},
		{"local", "", "", "https://new-r2.example"},
	} {
		response.Upload.Driver = tc.driver
		response.Upload.PublicBaseURL = tc.publicURL
		response.Upload.LocalPublicBaseURL = nil
		response.Upload.S3.PublicBaseURL = nil
		response, err = service.Update(ctx, *response)
		require.NoError(t, err)
		require.Equal(t, tc.publicURL, response.Upload.PublicBaseURL)
		require.Equal(t, tc.localURL, *response.Upload.LocalPublicBaseURL)
		require.Equal(t, tc.s3URL, *response.Upload.S3.PublicBaseURL)
		require.Empty(t, response.Upload.S3.SecretAccessKey)
		effective, err := service.Policy(ctx, AssetPurposeUpload)
		require.NoError(t, err)
		if tc.driver == "s3" {
			require.Equal(t, "fixture-secret", effective.S3.SecretAccessKey)
		} else {
			require.Equal(t, "sealed:enc:fixture-secret", effective.S3.SecretAccessKey)
		}
	}
}

func TestDoneBotStorageGeneratedDriverSwitchKeepsLocalPathAndURL(t *testing.T) {
	ctx := context.Background()
	repo := newStubSettingRepo()
	cfg := &config.Config{}
	cfg.Totp.EncryptionKeyConfigured = true
	cfg.AssetStorage.LocalPath = t.TempDir()
	cfg.AssetStorage.PublicBaseURL = "https://gateway.example"
	cfg.ImageStorage.Enabled = true
	service := NewDoneBotStorageService(repo, reversibleEncryptor{}, nil, cfg, nil)
	input, err := service.Get(ctx)
	require.NoError(t, err)
	originalPath := input.Generated.LocalPath
	input.Generated.Driver = "s3"
	input.Generated.S3 = DoneBotStorageS3{Bucket: "generated", Prefix: "generated/", AccessKeyID: "fixture-access", SecretAccessKey: "fixture-secret", PublicBaseURL: doneBotStorageURL("https://r2.example")}
	_, err = service.Update(ctx, *input)
	require.NoError(t, err)

	service = NewDoneBotStorageService(repo, reversibleEncryptor{}, nil, cfg, nil)
	input, err = service.Get(ctx)
	require.NoError(t, err)
	input.Generated.Driver = "local"
	response, err := service.Update(ctx, *input)
	require.NoError(t, err)
	require.Equal(t, "https://gateway.example", response.Generated.PublicBaseURL)
	require.Equal(t, "https://r2.example", *response.Generated.S3.PublicBaseURL)
	require.Equal(t, originalPath, response.Generated.LocalPath)
	require.Equal(t, "generated/", response.Generated.S3.Prefix)

	service = NewDoneBotStorageService(repo, reversibleEncryptor{}, nil, cfg, nil)
	input, err = service.Get(ctx)
	require.NoError(t, err)
	input.Generated.Driver = "s3"
	response, err = service.Update(ctx, *input)
	require.NoError(t, err)
	require.Equal(t, "https://r2.example", response.Generated.PublicBaseURL)
	require.Equal(t, originalPath, response.Generated.LocalPath)
}

func TestDoneBotStorageLegacyPoliciesAssignURLOnlyToStoredDriver(t *testing.T) {
	for _, driver := range []string{"local", "s3"} {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			repo := newStubSettingRepo()
			service := NewDoneBotStorageService(repo, nil, nil, nil, nil)
			legacy := service.defaults()
			legacy.Upload.Driver = driver
			legacy.Upload.PublicBaseURL = "https://legacy.example"
			legacy.Upload.LocalPublicBaseURL, legacy.Upload.S3.PublicBaseURL = nil, nil
			legacy.Generated.LocalPublicBaseURL, legacy.Generated.S3.PublicBaseURL = nil, nil
			raw, err := json.Marshal(legacy)
			require.NoError(t, err)
			require.NoError(t, repo.Set(ctx, settingKeyDoneBotStorage, string(raw)))
			response, err := service.Get(ctx)
			require.NoError(t, err)
			require.Equal(t, "https://legacy.example", response.Upload.PublicBaseURL)
			if driver == "local" {
				require.Equal(t, "https://legacy.example", *response.Upload.LocalPublicBaseURL)
				require.Empty(t, *response.Upload.S3.PublicBaseURL)
			} else {
				require.Empty(t, *response.Upload.LocalPublicBaseURL)
				require.Equal(t, "https://legacy.example", *response.Upload.S3.PublicBaseURL)
			}
			require.Equal(t, string(raw), repo.values[settingKeyDoneBotStorage], "reading does not rewrite persisted policies")
		})
	}
}

func TestDoneBotStorageSnapshotsKeepOriginalDriverAndEffectiveURL(t *testing.T) {
	ctx := context.Background()
	var captured DoneBotStoragePolicy
	service := NewDoneBotStorageService(nil, reversibleEncryptor{}, func(_ context.Context, policy DoneBotStoragePolicy) (AssetStorage, error) {
		captured = policy
		return &assetTestStorage{}, nil
	}, nil, nil)
	for _, driver := range []string{"local", "s3"} {
		t.Run(driver, func(t *testing.T) {
			legacy := DoneBotStoragePolicy{Driver: driver, LocalPath: "original/generated", PublicBaseURL: "https://original.example", S3: DoneBotStorageS3{Bucket: "original", Prefix: "generated/", SecretAccessKey: "sealed:enc:fixture-secret"}}
			raw, err := json.Marshal(legacy)
			require.NoError(t, err)
			asset := &GatewayAsset{Storage: driver, StorageConfig: raw, ObjectKey: "generated/ab/hash.png"}
			_, err = service.originalStorage(ctx, asset)
			require.NoError(t, err)
			require.Equal(t, driver, captured.Driver)
			require.Equal(t, "original/generated", captured.LocalPath)
			require.Equal(t, "generated/", captured.S3.Prefix)
			require.Equal(t, "https://original.example", captured.PublicBaseURL)
			require.Equal(t, string(raw), string(asset.StorageConfig))

			policy := legacy
			policy.LocalPublicBaseURL = doneBotStorageURL("https://local.example")
			policy.S3.PublicBaseURL = doneBotStorageURL("https://r2.example")
			policy.S3.SecretAccessKey = "fixture-secret"
			_, err = service.storage(ctx, policy)
			require.NoError(t, err)
			require.Equal(t, policy.EffectivePublicBaseURL(), captured.PublicBaseURL)
			snapshot, err := service.snapshot(policy)
			require.NoError(t, err)
			require.NotContains(t, string(snapshot), `"fixture-secret"`)
			var stored DoneBotStoragePolicy
			require.NoError(t, json.Unmarshal(snapshot, &stored))
			if driver == "local" {
				require.Equal(t, "https://local.example", stored.PublicBaseURL)
				require.Equal(t, "original/generated", stored.LocalPath)
				require.Nil(t, stored.S3.PublicBaseURL)
				require.Empty(t, stored.S3.SecretAccessKey)
			} else {
				require.Equal(t, "https://r2.example", stored.PublicBaseURL)
				require.Empty(t, stored.LocalPath)
				require.Nil(t, stored.LocalPublicBaseURL)
			}
			_, err = service.originalStorage(ctx, &GatewayAsset{StorageConfig: snapshot})
			require.NoError(t, err)
			require.Equal(t, stored.PublicBaseURL, captured.PublicBaseURL)
			if driver == "s3" {
				require.Equal(t, "fixture-secret", captured.S3.SecretAccessKey)
			}
		})
	}
}

func TestDoneBotStorageLocalDoesNotDecryptInactiveS3Secret(t *testing.T) {
	ctx := context.Background()
	repo := newStubSettingRepo()
	assets := &policyAssetRepo{&assetTestRepo{rows: map[string]*GatewayAsset{}, owners: map[string]map[int64]bool{}}}
	cfg := &config.Config{}
	cfg.DoneBot.Enabled = true
	cfg.Totp.EncryptionKeyConfigured = true
	cfg.JWT.Secret = "fixture-signing"
	cfg.AssetStorage.LocalPath = t.TempDir()
	cfg.AssetStorage.PublicBaseURL = "https://gateway.example"
	settings := NewDoneBotStorageService(repo, reversibleEncryptor{}, func(_ context.Context, policy DoneBotStoragePolicy) (AssetStorage, error) {
		require.Equal(t, "local", policy.Driver)
		return &assetTestStorage{}, nil
	}, cfg, assets)
	stored := settings.defaults()
	stored.Upload.S3.SecretAccessKey = "sealed:unreadable-inactive-secret"
	raw, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, repo.Set(ctx, settingKeyDoneBotStorage, string(raw)))
	input, err := settings.Get(ctx)
	require.NoError(t, err)
	require.Empty(t, input.Upload.S3.SecretAccessKey)
	require.True(t, input.Upload.SecretConfigured)
	_, err = settings.Update(ctx, *input)
	require.NoError(t, err)
	require.Contains(t, repo.values[settingKeyDoneBotStorage], "sealed:unreadable-inactive-secret")
	_, err = settings.Policy(ctx, AssetPurposeUpload)
	require.NoError(t, err)
	svc := NewAssetService(assets, &assetTestStorage{}, cfg)
	svc.storageSettings = settings
	asset, err := svc.Upload(ctx, 1, "local.txt", "text/plain", []byte("local"), "")
	require.NoError(t, err)
	require.NotContains(t, string(asset.StorageConfig), "unreadable-inactive-secret")
	legacyLocal, err := json.Marshal(stored.Upload)
	require.NoError(t, err)
	_, err = settings.originalStorage(ctx, &GatewayAsset{StorageConfig: legacyLocal})
	require.NoError(t, err)
	input.Upload.Driver = "s3"
	input.Upload.S3.Bucket = "bucket"
	input.Upload.S3.AccessKeyID = "fixture-access"
	_, err = settings.Update(ctx, *input)
	require.NoError(t, err)
	_, err = settings.Policy(ctx, AssetPurposeUpload)
	require.Error(t, err, "the active S3 driver still requires a decryptable secret")
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

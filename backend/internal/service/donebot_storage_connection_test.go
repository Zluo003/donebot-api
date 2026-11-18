//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type connectivityStorage struct {
	assetTestStorage
	prefixes []string
	err      error
}

func (s *connectivityStorage) TestConnection(_ context.Context, prefix string) error {
	s.prefixes = append(s.prefixes, prefix)
	return s.err
}

func syntheticOSSSettings() DoneBotStorageS3 {
	return DoneBotStorageS3{Endpoint: "https://oss-accelerate.aliyuncs.com", DownloadEndpoint: "https://oss-cn-hangzhou.aliyuncs.com", Region: "cn-hangzhou", Bucket: "fixture-bucket", Prefix: "generated/", AccessKeyID: "fixture-key", SecretAccessKey: "fixture-oss-secret"}
}

func TestDoneBotStorageConnectivityUsesUnsavedPolicyAndMatchingSavedSecretWithoutSaving(t *testing.T) {
	for _, purpose := range []string{AssetPurposeUpload, AssetPurposeGenerated} {
		for _, driver := range []string{"s3", "oss"} {
			t.Run(purpose+"/"+driver, func(t *testing.T) {
				ctx := context.Background()
				repo := newStubSettingRepo()
				probe := &connectivityStorage{}
				var used DoneBotStoragePolicy
				cfg := &config.Config{}
				cfg.Totp.EncryptionKeyConfigured = true
				svc := NewDoneBotStorageService(repo, reversibleEncryptor{}, func(_ context.Context, p DoneBotStoragePolicy) (AssetStorage, error) { used = p; return probe, nil }, cfg, nil)
				policies := svc.defaults()
				policies.Generated.Enabled = false
				selected := &policies.Upload
				if purpose == AssetPurposeGenerated {
					selected = &policies.Generated
				}
				selected.Driver = driver
				selected.Enabled = true
				*selected.objectStorage() = syntheticOSSSettings()
				selected.S3.PublicBaseURL = doneBotStorageURL("https://fixture.example")
				_, err := svc.Update(ctx, policies)
				require.NoError(t, err)
				saved := repo.values[settingKeyDoneBotStorage]
				safe, err := svc.Get(ctx)
				require.NoError(t, err)
				in := safe.Upload
				if purpose == AssetPurposeGenerated {
					in = safe.Generated
				}
				require.Empty(t, in.S3.SecretAccessKey)
				require.Empty(t, in.OSS.SecretAccessKey)
				require.True(t, in.SecretConfigured)
				in.Enabled = false
				in.objectStorage().Bucket = "unsaved-bucket"
				in.objectStorage().Prefix = "unsaved/"
				require.NoError(t, svc.TestConnection(ctx, purpose, in))
				require.True(t, used.Enabled)
				require.Equal(t, "unsaved-bucket", used.objectStorage().Bucket)
				require.Equal(t, "fixture-oss-secret", used.objectStorage().SecretAccessKey)
				require.Equal(t, []string{"unsaved/"}, probe.prefixes)
				require.Equal(t, saved, repo.values[settingKeyDoneBotStorage])
				require.Zero(t, probe.saves)
				in.objectStorage().Endpoint = "https://other-endpoint.example"
				require.Error(t, svc.TestConnection(ctx, purpose, in))
				require.Len(t, probe.prefixes, 1)
				in.objectStorage().Endpoint = syntheticOSSSettings().Endpoint
				if driver == "oss" {
					in.OSS.DownloadEndpoint = "https://oss-cn-shanghai.aliyuncs.com"
					require.Error(t, svc.TestConnection(ctx, purpose, in))
					in.OSS.DownloadEndpoint = syntheticOSSSettings().DownloadEndpoint
				}
				in.objectStorage().SecretAccessKey = "fresh-secret"
				require.NoError(t, svc.TestConnection(ctx, purpose, in))
				require.Equal(t, "fresh-secret", used.objectStorage().SecretAccessKey)
				require.Equal(t, saved, repo.values[settingKeyDoneBotStorage])
			})
		}
	}
}

func TestOSSSettingsRejectAccelerationDownloadsWrongRegionAndEmbeddedBucket(t *testing.T) {
	valid := syntheticOSSSettings()
	require.NoError(t, validateObjectStorageSettings("oss", valid))
	for name, mutate := range map[string]func(*DoneBotStorageS3){
		"accelerated download":  func(p *DoneBotStorageS3) { p.DownloadEndpoint = "https://oss-accelerate.aliyuncs.com" },
		"mismatched region":     func(p *DoneBotStorageS3) { p.DownloadEndpoint = "https://oss-cn-shanghai.aliyuncs.com" },
		"bucket in host":        func(p *DoneBotStorageS3) { p.Endpoint = "https://fixture-bucket.oss-accelerate.aliyuncs.com" },
		"auto region":           func(p *DoneBotStorageS3) { p.Region = "auto" },
		"region prefix":         func(p *DoneBotStorageS3) { p.Region = "oss-cn-hangzhou" },
		"signature in endpoint": func(p *DoneBotStorageS3) { p.Endpoint += "?Signature=not-allowed" },
		"insecure":              func(p *DoneBotStorageS3) { p.DownloadEndpoint = "http://oss-cn-hangzhou.aliyuncs.com" },
		"bad bucket":            func(p *DoneBotStorageS3) { p.Bucket = "bucket/another" },
	} {
		t.Run(name, func(t *testing.T) {
			in := valid
			mutate(&in)
			require.Error(t, validateObjectStorageSettings("oss", in))
		})
	}
}

func TestOSSStorageRetainsSeparateSecretsAndDeletesExpiredObjectsAtOriginalBucket(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.Totp.EncryptionKeyConfigured = true
	cfg.DoneBot.Enabled = true
	cfg.JWT.Secret = "fixture"
	cfg.AssetStorage.LocalPath = "fixture"
	repo := &policyAssetRepo{&assetTestRepo{rows: map[string]*GatewayAsset{}, owners: map[string]map[int64]bool{}}}
	settingsRepo := newStubSettingRepo()
	stores := map[string]*assetTestStorage{}
	var originalKeys []string
	settings := NewDoneBotStorageService(settingsRepo, reversibleEncryptor{}, func(_ context.Context, p DoneBotStoragePolicy) (AssetStorage, error) {
		require.Equal(t, "oss", p.Driver)
		require.Equal(t, "fixture-oss-secret", p.OSS.SecretAccessKey)
		bucket := p.OSS.Bucket
		if stores[bucket] == nil {
			stores[bucket] = &assetTestStorage{}
		}
		originalKeys = append(originalKeys, bucket)
		return stores[bucket], nil
	}, cfg, repo)
	pol := settings.defaults()
	pol.Upload.Enabled = false
	pol.Generated.Enabled = true
	pol.Generated.Driver = "oss"
	pol.Generated.OSS = syntheticOSSSettings()
	pol.Generated.TTLHours = 4
	pol.Generated.S3 = DoneBotStorageS3{Endpoint: "https://s3.example", Bucket: "old-s3", AccessKeyID: "old-key", SecretAccessKey: "independent-s3-secret", PublicBaseURL: doneBotStorageURL("https://s3.example")}
	out, err := settings.Update(ctx, pol)
	require.NoError(t, err)
	require.True(t, out.Generated.S3.SecretConfigured)
	require.True(t, out.Generated.OSS.SecretConfigured)
	require.Empty(t, out.Generated.S3.SecretAccessKey)
	require.Empty(t, out.Generated.OSS.SecretAccessKey)
	require.NotContains(t, settingsRepo.values[settingKeyDoneBotStorage], `"secret_access_key":"fixture-oss-secret"`)
	require.NotContains(t, settingsRepo.values[settingKeyDoneBotStorage], `"secret_access_key":"independent-s3-secret"`)
	svc := NewAssetService(repo, &assetTestStorage{}, cfg)
	svc.storageSettings = settings
	now := time.Now().UTC()
	svc.now = func() time.Time { return now }
	asset, err := svc.UploadGenerated(ctx, 5, "fixture.txt", "text/plain", []byte("fixture"), "")
	require.NoError(t, err)
	stored := repo.rows[asset.ID]
	require.True(t, strings.HasPrefix(stored.ObjectKey, "generated/generated/"), stored.ObjectKey)
	require.WithinDuration(t, now.Add(4*time.Hour), stored.ExpiresAt, time.Second)
	var snapshot DoneBotStoragePolicy
	require.NoError(t, json.Unmarshal(stored.StorageConfig, &snapshot))
	require.Empty(t, snapshot.S3.SecretAccessKey)
	require.NotContains(t, string(stored.StorageConfig), "independent-s3-secret")
	out.Generated.OSS.Bucket = "new-bucket"
	_, err = settings.Update(ctx, *out)
	require.NoError(t, err)
	now = now.Add(4 * time.Hour)
	stores["fixture-bucket"].failDelete = true
	deleted, err := svc.CleanupExpired(ctx, 100)
	require.Error(t, err)
	require.Zero(t, deleted)
	require.NotNil(t, repo.rows[asset.ID])
	stores["fixture-bucket"].failDelete = false
	deleted, err = svc.CleanupExpired(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 1, deleted)
	require.Empty(t, repo.rows)
	require.Equal(t, "fixture-bucket", originalKeys[len(originalKeys)-1])
	require.Nil(t, stores["new-bucket"])
}

func TestClientReleaseStorageConnectivityTestsDraftAndDoesNotSave(t *testing.T) {
	for _, driver := range []string{"s3", "oss"} {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			repo := &clientReleaseSettingsStub{values: map[string]string{}}
			probe := &connectivityStorage{}
			var used DoneBotStoragePolicy
			storage := NewDoneBotStorageService(repo, reversibleEncryptor{}, func(_ context.Context, p DoneBotStoragePolicy) (AssetStorage, error) { used = p; return probe, nil }, nil, nil)
			svc := NewClientReleaseService(storage)
			in := ClientReleaseStorageSettings{Driver: driver, S3: syntheticOSSSettings(), OSS: syntheticOSSSettings()}
			safe, err := svc.UpdateStorageSettings(ctx, in)
			require.NoError(t, err)
			saved := repo.values[clientReleaseStorageSettingKey]
			safe.objectStorage().Prefix = "unsaved/"
			require.NoError(t, svc.TestStorageConnection(ctx, *safe))
			require.Equal(t, []string{"unsaved/"}, probe.prefixes)
			require.Equal(t, "fixture-oss-secret", used.objectStorage().SecretAccessKey)
			require.Equal(t, saved, repo.values[clientReleaseStorageSettingKey])
			safe.objectStorage().AccessKeyID = "changed-key"
			require.Error(t, svc.TestStorageConnection(ctx, *safe))
			require.Len(t, probe.prefixes, 1)
			safe.objectStorage().SecretAccessKey = "replacement"
			require.NoError(t, svc.TestStorageConnection(ctx, *safe))
			require.Equal(t, "replacement", used.objectStorage().SecretAccessKey)
			require.Equal(t, saved, repo.values[clientReleaseStorageSettingKey])
		})
	}
}

func TestImageStorageConnectivityActuallyCallsTesterAndDoesNotSave(t *testing.T) {
	ctx := context.Background()
	repo := newStubSettingRepo()
	probe := &imageConnectivityProbe{}
	calls := 0
	svc := NewImageStorageSettingService(repo, reversibleEncryptor{}, nil, func(context.Context, *config.ImageStorageConfig) (ImageStorage, error) { calls++; return probe, nil }, config.ImageStorageConfig{})
	in := ImageStorageSettings{Enabled: true, Endpoint: "https://s3.example", Region: "auto", Bucket: "fixture", AccessKeyID: "key", SecretAccessKey: "secret", Prefix: "fixture/"}
	require.NoError(t, svc.TestConnection(ctx, in))
	require.Equal(t, 1, calls)
	require.Equal(t, []string{"fixture/"}, probe.prefixes)
	require.Empty(t, repo.values)
	probe.err = fmt.Errorf("synthetic probe failure")
	require.Error(t, svc.TestConnection(ctx, in))
}

type imageConnectivityProbe struct {
	recordingStorage
	prefixes []string
	err      error
}

func (p *imageConnectivityProbe) TestConnection(_ context.Context, prefix string) error {
	p.prefixes = append(p.prefixes, prefix)
	return p.err
}

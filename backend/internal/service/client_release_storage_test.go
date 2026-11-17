package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type releaseStorageEncryptor struct{}

func (releaseStorageEncryptor) Encrypt(value string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(value)), nil
}
func (releaseStorageEncryptor) Decrypt(value string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(value)
	return string(data), err
}

func TestClientReleaseStorageIsIndependentAndRetainsOldPackages(t *testing.T) {
	ctx := context.Background()
	repo := &clientReleaseSettingsStub{values: map[string]string{settingKeyDoneBotStorage: `{"upload":{"driver":"s3","s3":{"bucket":"media","secret_access_key":"unreadable-media-secret"}}}`}}
	objects := map[string]*clientReleaseStorageStub{}
	var used []DoneBotStoragePolicy
	svc := NewClientReleaseService(NewDoneBotStorageService(repo, releaseStorageEncryptor{}, func(_ context.Context, p DoneBotStoragePolicy) (AssetStorage, error) {
		used = append(used, p)
		key := p.Driver + ":" + p.LocalPath
		if p.Driver == "s3" {
			key = p.Driver + ":" + p.S3.Bucket + ":" + p.S3.Prefix
		}
		if objects[key] == nil {
			objects[key] = &clientReleaseStorageStub{files: map[string][]byte{}}
		}
		return objects[key], nil
	}, nil, nil))
	initial, err := svc.StorageSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "local", initial.Driver)
	require.Equal(t, "data/client-packages", initial.LocalPath)
	payload := []byte("synthetic installer")
	_, err = svc.Upload(ctx, "1.0.0", ClientReleaseWindowsX64, "DoneBot.exe", "", bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	require.Equal(t, "data/client-packages", used[len(used)-1].LocalPath)
	secret := "synthetic-package-secret"
	in := ClientReleaseStorageSettings{Driver: "s3", LocalPath: "isolated/packages", S3: DoneBotStorageS3{Endpoint: "https://storage.example", Region: "auto", Bucket: "client-downloads", Prefix: "releases", AccessKeyID: "package-key", SecretAccessKey: secret}}
	out, err := svc.UpdateStorageSettings(ctx, in)
	require.NoError(t, err)
	require.True(t, out.SecretConfigured)
	require.Empty(t, out.S3.SecretAccessKey)
	require.NotContains(t, repo.values[clientReleaseStorageSettingKey], secret)
	require.Contains(t, repo.values[clientReleaseStorageSettingKey], "sealed:")
	require.Contains(t, repo.values[settingKeyDoneBotStorage], "unreadable-media-secret")
	require.Equal(t, "releases/", out.S3.Prefix)
	out.S3.Bucket = "second-downloads"
	out, err = svc.UpdateStorageSettings(ctx, *out)
	require.NoError(t, err)
	require.True(t, out.SecretConfigured)
	_, err = svc.Upload(ctx, "1.0.1", ClientReleaseWindowsX64, "DoneBot.exe", "", bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	require.Equal(t, "second-downloads", used[len(used)-1].S3.Bucket)
	require.Equal(t, secret, used[len(used)-1].S3.SecretAccessKey)
	require.NotContains(t, repo.values[clientReleasesSettingKey], secret)
	repo.values[settingKeyDoneBotStorage] = `{"upload":{"enabled":false,"driver":"local","local_path":"elsewhere"}}`
	in = *out
	in.Driver = "local"
	in.LocalPath = "new-packages"
	_, err = svc.UpdateStorageSettings(ctx, in)
	require.NoError(t, err)
	for _, version := range []string{"1.0.0", "1.0.1"} {
		store, e := svc.load(ctx)
		require.NoError(t, e)
		record := findClientRelease(store.Releases, version)
		artifact := record.Platforms[ClientReleaseWindowsX64]
		reader, e := svc.storage.OpenReleaseObject(ctx, artifact.ObjectKey, artifact.StorageConfig)
		require.NoError(t, e)
		actual, e := io.ReadAll(reader)
		require.NoError(t, e)
		require.NoError(t, reader.Close())
		require.Equal(t, payload, actual)
	}
	_, err = svc.Upload(ctx, "1.0.2", ClientReleaseWindowsX64, "DoneBot.exe", "", bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	require.Equal(t, "new-packages", used[len(used)-1].LocalPath)
}

func TestClientReleaseStorageValidationAndEncryptionRequired(t *testing.T) {
	ctx := context.Background()
	repo := &clientReleaseSettingsStub{values: map[string]string{}}
	svc := NewClientReleaseService(NewDoneBotStorageService(repo, nil, nil, nil, nil))
	for _, tc := range []ClientReleaseStorageSettings{
		{Driver: "unknown"}, {Driver: "local"}, {Driver: "s3", S3: DoneBotStorageS3{Bucket: "packages"}},
		{Driver: "local", LocalPath: "packages", S3: DoneBotStorageS3{Endpoint: "file:///tmp/packages"}},
	} {
		_, err := svc.UpdateStorageSettings(ctx, tc)
		require.Error(t, err)
	}
	_, err := svc.UpdateStorageSettings(ctx, ClientReleaseStorageSettings{Driver: "s3", S3: DoneBotStorageS3{Bucket: "packages", AccessKeyID: "key", SecretAccessKey: "secret"}})
	require.ErrorIs(t, err, ErrSecretEncryptionKeyNotConfigured)
	require.Empty(t, repo.values)
}

func TestClientReleaseLegacySnapshotRemainsReadable(t *testing.T) {
	ctx := context.Background()
	objects := &clientReleaseStorageStub{files: map[string][]byte{"client-releases/old.exe": []byte("old-package")}}
	storage := NewDoneBotStorageService(nil, releaseStorageEncryptor{}, func(_ context.Context, p DoneBotStoragePolicy) (AssetStorage, error) {
		if p.S3.Bucket != "legacy-media" || p.S3.SecretAccessKey != "old-secret" {
			return nil, fmt.Errorf("wrong original storage")
		}
		return objects, nil
	}, nil, nil)
	secret, _ := storage.encryptSecret("old-secret")
	raw, _ := json.Marshal(DoneBotStoragePolicy{Driver: "s3", S3: DoneBotStorageS3{Bucket: "legacy-media", SecretAccessKey: secret}})
	reader, err := storage.OpenReleaseObject(ctx, "client-releases/old.exe", raw)
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "old-package", strings.TrimSpace(string(data)))
}

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

type mobileReleaseFixtureSettings struct {
	SettingRepository
	values map[string]string
}

func (s *mobileReleaseFixtureSettings) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (s *mobileReleaseFixtureSettings) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

type mobileReleaseFixtureStorage struct {
	objects map[string][]byte
}

func (s *mobileReleaseFixtureStorage) Save(_ context.Context, key, _ string, data []byte) (string, error) {
	s.objects[key] = append([]byte(nil), data...)
	return key, nil
}

func (s *mobileReleaseFixtureStorage) SaveStream(ctx context.Context, key, contentType string, source io.Reader, size int64) (string, error) {
	data, err := io.ReadAll(io.LimitReader(source, size+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) != size {
		return "", io.ErrUnexpectedEOF
	}
	return s.Save(ctx, key, contentType, data)
}

func (s *mobileReleaseFixtureStorage) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	return nil
}

func (s *mobileReleaseFixtureStorage) Kind() string { return "local" }

func (s *mobileReleaseFixtureStorage) Open(_ context.Context, key string) (io.ReadCloser, error) {
	data, ok := s.objects[key]
	if !ok {
		return nil, io.EOF
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func newMobileReleaseFixtureService(t *testing.T) (*ClientReleaseService, *mobileReleaseFixtureSettings, *mobileReleaseFixtureStorage) {
	t.Helper()
	settings := &mobileReleaseFixtureSettings{values: map[string]string{
		clientReleaseStorageSettingKey: `{"driver":"local","local_path":"` + t.TempDir() + `"}`,
	}}
	store := &mobileReleaseFixtureStorage{objects: map[string][]byte{}}
	storage := NewDoneBotStorageService(settings, nil, func(_ context.Context, _ DoneBotStoragePolicy) (AssetStorage, error) {
		return store, nil
	}, nil, nil)
	return NewClientReleaseService(storage), settings, store
}

func fakeAPKBytes(label string) []byte {
	return append([]byte{'P', 'K', 0x03, 0x04}, []byte("donebot-apk-"+label)...)
}

func TestMobileReleaseLifecycleValidationAndTransitiveForceUpdate(t *testing.T) {
	svc, settings, store := newMobileReleaseFixtureService(t)
	ctx := context.Background()

	// Reject non-APK magic bytes
	_, err := svc.UploadMobile(ctx, "0.1.0", 1, MobileReleaseAndroidAPK, "DoneBot-0.1.0.apk", "bad", false, bytes.NewReader([]byte("not-a-zip-file")), 14)
	require.Error(t, err)

	// Reject non-.apk extension
	apk1 := fakeAPKBytes("v1")
	_, err = svc.UploadMobile(ctx, "0.1.0", 1, MobileReleaseAndroidAPK, "DoneBot-0.1.0.zip", "bad ext", false, bytes.NewReader(apk1), int64(len(apk1)))
	require.Error(t, err)

	// Upload draft v0.1.0, then replace draft before publishing (old object cleaned up)
	draft1, err := svc.UploadMobile(ctx, "0.1.0", 1, MobileReleaseAndroidAPK, "DoneBot-0.1.0-draft.apk", "initial", false, bytes.NewReader(apk1), int64(len(apk1)))
	require.NoError(t, err)
	require.Equal(t, "draft", draft1.Status)
	require.Len(t, store.objects, 1)

	apk1b := fakeAPKBytes("v1-final")
	draft1b, err := svc.UploadMobile(ctx, "0.1.0", 1, MobileReleaseAndroidAPK, "DoneBot-0.1.0-android.apk", "首发版本", false, bytes.NewReader(apk1b), int64(len(apk1b)))
	require.NoError(t, err)
	require.Len(t, store.objects, 1)
	expectedHash := sha256.Sum256(apk1b)
	require.Equal(t, hex.EncodeToString(expectedHash[:]), draft1b.Platforms[MobileReleaseAndroidAPK].SHA256)

	// Publish v0.1.0
	pub1, err := svc.PublishMobile(ctx, "0.1.0")
	require.NoError(t, err)
	require.Equal(t, "published", pub1.Status)
	require.NotNil(t, pub1.PublishedAt)

	// Desktop releases remain completely untouched
	_, err = svc.Latest(ctx)
	require.Error(t, err)
	require.NotContains(t, settings.values, clientReleasesSettingKey)

	// Reject publishing v0.1.1 with same versionCode=1
	apk2 := fakeAPKBytes("v2")
	_, err = svc.UploadMobile(ctx, "0.1.1", 1, MobileReleaseAndroidAPK, "DoneBot-0.1.1-android.apk", "security", true, bytes.NewReader(apk2), int64(len(apk2)))
	require.NoError(t, err)
	_, err = svc.PublishMobile(ctx, "0.1.1")
	require.Error(t, err)

	// Re-upload v0.1.1 with versionCode=2 and force_update=true, then publish
	_, err = svc.UploadMobile(ctx, "0.1.1", 2, MobileReleaseAndroidAPK, "DoneBot-0.1.1-android.apk", "安全更新", true, bytes.NewReader(apk2), int64(len(apk2)))
	require.NoError(t, err)
	_, err = svc.PublishMobile(ctx, "0.1.1")
	require.NoError(t, err)

	// Upload & publish v0.1.2 with versionCode=3 and force_update=false
	apk3 := fakeAPKBytes("v3")
	_, err = svc.UploadMobile(ctx, "0.1.2", 3, MobileReleaseAndroidAPK, "DoneBot-0.1.2-android.apk", "体验优化", false, bytes.NewReader(apk3), int64(len(apk3)))
	require.NoError(t, err)
	_, err = svc.PublishMobile(ctx, "0.1.2")
	require.NoError(t, err)

	// Device on v0.1.0 (code 1) skipped mandatory v0.1.1 -> must still see force_update=true targeting v0.1.2!
	checkFrom1, err := svc.CheckMobile(ctx, "0.1.0", 1, MobileReleaseAndroidAPK)
	require.NoError(t, err)
	require.True(t, checkFrom1.UpdateAvailable)
	require.True(t, checkFrom1.ForceUpdate)
	require.Equal(t, "0.1.2", checkFrom1.Latest)
	require.Equal(t, 3, checkFrom1.LatestCode)
	require.Equal(t, "体验优化", checkFrom1.Notes)

	// Device on v0.1.1 (code 2) -> optional update to v0.1.2
	checkFrom2, err := svc.CheckMobile(ctx, "0.1.1", 2, MobileReleaseAndroidAPK)
	require.NoError(t, err)
	require.True(t, checkFrom2.UpdateAvailable)
	require.False(t, checkFrom2.ForceUpdate)

	// Device on v0.1.2 (code 3) -> up to date
	checkFrom3, err := svc.CheckMobile(ctx, "0.1.2", 3, MobileReleaseAndroidAPK)
	require.NoError(t, err)
	require.False(t, checkFrom3.UpdateAvailable)

	// Cannot delete latest published v0.1.2, can delete older v0.1.0
	require.Error(t, svc.DeleteMobile(ctx, "0.1.2"))
	require.NoError(t, svc.DeleteMobile(ctx, "0.1.0"))
	list, err := svc.ListMobile(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, "0.1.2", list[0].Version)
	require.Equal(t, "0.1.1", list[1].Version)
}

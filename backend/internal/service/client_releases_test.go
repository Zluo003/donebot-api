package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type clientReleaseSettingsStub struct {
	SettingRepository
	values map[string]string
}

type clientReleaseStorageStub struct {
	files map[string][]byte
}

func (s *clientReleaseStorageStub) Save(_ context.Context, key, _ string, data []byte) (string, error) {
	s.files[key] = append([]byte(nil), data...)
	return key, nil
}

func (s *clientReleaseStorageStub) SaveStream(_ context.Context, key, _ string, source io.Reader, size int64) (string, error) {
	data, err := io.ReadAll(io.LimitReader(source, size+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) != size {
		return "", errors.New("asset size does not match the declared size")
	}
	s.files[key] = data
	return key, nil
}

func (s *clientReleaseStorageStub) Delete(_ context.Context, key string) error {
	delete(s.files, key)
	return nil
}

func (s *clientReleaseStorageStub) Kind() string { return "local" }

func (s *clientReleaseStorageStub) Open(_ context.Context, key string) (io.ReadCloser, error) {
	data, ok := s.files[key]
	if !ok {
		return nil, errors.New("asset not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *clientReleaseSettingsStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (s *clientReleaseSettingsStub) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func newClientReleaseTestService(t *testing.T, records ...clientReleaseRecord) (*ClientReleaseService, *clientReleaseSettingsStub) {
	t.Helper()
	raw, err := json.Marshal(clientReleaseStore{Releases: records})
	require.NoError(t, err)
	repo := &clientReleaseSettingsStub{values: map[string]string{clientReleasesSettingKey: string(raw)}}
	return &ClientReleaseService{settings: repo}, repo
}

func testClientReleaseRecord(version, status string, platforms map[string]clientReleaseArtifactRecord) clientReleaseRecord {
	publishedAt := time.Date(2026, time.November, 16, 0, 0, 0, 0, time.UTC)
	record := clientReleaseRecord{
		Version: version, Status: status, CreatedAt: publishedAt.Add(-time.Hour), Platforms: platforms,
	}
	if status == "published" {
		record.PublishedAt = &publishedAt
	}
	return record
}

func testClientReleasePlatforms() map[string]clientReleaseArtifactRecord {
	return map[string]clientReleaseArtifactRecord{
		ClientReleaseWindowsX64:    {Filename: "DoneBot-Setup.exe", ObjectKey: "client-releases/windows.exe", SizeBytes: 100, SHA256: "windows-hash", SHA512: "windows-sha512"},
		ClientReleaseMacOSArm64:    {Filename: "DoneBot-mac-arm64.zip", ObjectKey: "client-releases/macos.zip", SizeBytes: 200, SHA256: "mac-hash", SHA512: "mac-sha512"},
		ClientReleaseMacOSArm64DMG: {Filename: "DoneBot-mac-arm64.dmg", ObjectKey: "client-releases/macos.dmg", SizeBytes: 300, SHA256: "dmg-hash", SHA512: "dmg-sha512"},
	}
}

func TestClientReleaseLatestAndCheck(t *testing.T) {
	service, _ := newClientReleaseTestService(t,
		testClientReleaseRecord("1.9.0", "published", testClientReleasePlatforms()),
		testClientReleaseRecord("1.10.0", "published", testClientReleasePlatforms()),
		testClientReleaseRecord("2.0.0", "draft", testClientReleasePlatforms()),
	)

	latest, err := service.Latest(context.Background())
	require.NoError(t, err)
	require.Equal(t, "1.10.0", latest.Version)

	check, err := service.Check(context.Background(), "1.9.0", ClientReleaseMacOSArm64)
	require.NoError(t, err)
	require.True(t, check.UpdateAvailable)
	require.Equal(t, "1.10.0", check.Latest)
	require.Equal(t, "/api/v1/client/releases/1.10.0/macos-arm64/DoneBot-mac-arm64.zip", check.DownloadURL)
	require.Equal(t, "mac-hash", check.SHA256)

	check, err = service.Check(context.Background(), "1.10.0", ClientReleaseWindowsX64)
	require.NoError(t, err)
	require.False(t, check.UpdateAvailable)
	require.Empty(t, check.DownloadURL)

	_, err = service.Check(context.Background(), "1.10.0", "linux-x64")
	require.Error(t, err)
}

func TestClientReleaseUploadPublishAndDownload(t *testing.T) {
	repo := &clientReleaseSettingsStub{values: map[string]string{}}
	objects := &clientReleaseStorageStub{files: map[string][]byte{}}
	storage := NewDoneBotStorageService(repo, nil, func(context.Context, DoneBotStoragePolicy) (AssetStorage, error) {
		return objects, nil
	}, nil, nil)
	service := NewClientReleaseService(storage)
	ctx := context.Background()

	windows := []byte("windows installer")
	macOSZip := []byte("macOS update package")
	macOSDMG := []byte("macOS installer image")
	_, err := service.Upload(ctx, "v1.0.0", ClientReleaseWindowsX64, "DoneBot-Setup.exe", "Release notes", bytes.NewReader(windows), int64(len(windows)))
	require.NoError(t, err)
	_, err = service.Upload(ctx, "1.0.0", ClientReleaseMacOSArm64, "DoneBot-mac-arm64.zip", "Release notes", bytes.NewReader(macOSZip), int64(len(macOSZip)))
	require.NoError(t, err)
	_, err = service.Upload(ctx, "1.0.0", ClientReleaseMacOSArm64DMG, "DoneBot-mac-arm64.dmg", "Release notes", bytes.NewReader(macOSDMG), int64(len(macOSDMG)))
	require.NoError(t, err)

	published, err := service.Publish(ctx, "v1.0.0")
	require.NoError(t, err)
	require.Equal(t, "published", published.Status)

	latest, err := service.Latest(ctx)
	require.NoError(t, err)
	require.Equal(t, "1.0.0", latest.Version)
	check, err := service.Check(ctx, "0.9.0", ClientReleaseWindowsX64)
	require.NoError(t, err)
	require.True(t, check.UpdateAvailable)
	require.Equal(t, latest.Platforms[ClientReleaseWindowsX64].DownloadURL, check.DownloadURL)

	artifact, reader, err := service.OpenArtifact(ctx, "1.0.0", ClientReleaseWindowsX64)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	require.Equal(t, "DoneBot-Setup.exe", artifact.Filename)
	downloaded, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, windows, downloaded)

	dmgArtifact, dmgReader, err := service.OpenArtifact(ctx, "1.0.0", ClientReleaseMacOSArm64DMG)
	require.NoError(t, err)
	defer func() { _ = dmgReader.Close() }()
	require.Equal(t, "DoneBot-mac-arm64.dmg", dmgArtifact.Filename)
	downloaded, err = io.ReadAll(dmgReader)
	require.NoError(t, err)
	require.Equal(t, macOSDMG, downloaded)

	feed, err := service.UpdateFeed(ctx, ClientReleaseMacOSArm64)
	require.NoError(t, err)
	require.Contains(t, string(feed), "version: \"1.0.0\"")
	require.Contains(t, string(feed), latest.Platforms[ClientReleaseMacOSArm64].DownloadURL)
	require.NotContains(t, string(feed), latest.Platforms[ClientReleaseMacOSArm64DMG].DownloadURL)
	_, err = service.UpdateFeed(ctx, ClientReleaseMacOSArm64DMG)
	require.Error(t, err)
}

func TestClientReleasePublishRequiresAllArtifactsAndNewerVersion(t *testing.T) {
	platforms := testClientReleasePlatforms()
	draft := testClientReleaseRecord("1.10.0", "draft", map[string]clientReleaseArtifactRecord{
		ClientReleaseWindowsX64: platforms[ClientReleaseWindowsX64],
	})
	service, repo := newClientReleaseTestService(t,
		testClientReleaseRecord("1.9.0", "published", platforms),
		draft,
	)

	_, err := service.Publish(context.Background(), "1.10.0")
	require.Error(t, err)

	draft.Platforms[ClientReleaseMacOSArm64] = platforms[ClientReleaseMacOSArm64]
	raw, err := json.Marshal(clientReleaseStore{Releases: []clientReleaseRecord{
		testClientReleaseRecord("1.9.0", "published", platforms), draft,
	}})
	require.NoError(t, err)
	repo.values[clientReleasesSettingKey] = string(raw)

	_, err = service.Publish(context.Background(), "1.10.0")
	require.Error(t, err)

	draft.Platforms[ClientReleaseMacOSArm64DMG] = platforms[ClientReleaseMacOSArm64DMG]
	raw, err = json.Marshal(clientReleaseStore{Releases: []clientReleaseRecord{
		testClientReleaseRecord("1.9.0", "published", platforms), draft,
	}})
	require.NoError(t, err)
	repo.values[clientReleasesSettingKey] = string(raw)

	published, err := service.Publish(context.Background(), "1.10.0")
	require.NoError(t, err)
	require.Equal(t, "published", published.Status)
	require.NotNil(t, published.PublishedAt)

	store, err := service.load(context.Background())
	require.NoError(t, err)
	store.Releases = append(store.Releases, testClientReleaseRecord("1.9.5", "draft", testClientReleasePlatforms()))
	raw, err = json.Marshal(store)
	require.NoError(t, err)
	repo.values[clientReleasesSettingKey] = string(raw)

	_, err = service.Publish(context.Background(), "1.9.5")
	require.Error(t, err)
}

func TestClientReleaseFilenameRequiresPlatformPackageFormat(t *testing.T) {
	tests := []struct {
		platform string
		filename string
		valid    bool
	}{
		{ClientReleaseWindowsX64, "DoneBot.exe", true},
		{ClientReleaseMacOSArm64, "DoneBot.zip", true},
		{ClientReleaseMacOSArm64DMG, "DoneBot.dmg", true},
		{ClientReleaseMacOSArm64, "DoneBot.dmg", false},
		{ClientReleaseMacOSArm64DMG, "DoneBot.zip", false},
	}
	for _, test := range tests {
		t.Run(test.platform+"/"+test.filename, func(t *testing.T) {
			_, err := sanitizeClientReleaseFilename(test.filename, test.platform)
			require.Equal(t, test.valid, err == nil)
		})
	}
}

func TestClientReleaseRejectsFilesLargerThan500MB(t *testing.T) {
	svc := &ClientReleaseService{}
	_, err := svc.Upload(context.Background(), "0.1.0", ClientReleaseMacOSArm64DMG, "DoneBot.dmg", "", strings.NewReader("fixture"), 500_000_001)
	require.ErrorContains(t, err, "500 MB")
}

func TestClientReleaseLegacyPublishedZipRemainsAvailable(t *testing.T) {
	platforms := testClientReleasePlatforms()
	delete(platforms, ClientReleaseMacOSArm64DMG)
	service, _ := newClientReleaseTestService(t, testClientReleaseRecord("1.0.0", "published", platforms))

	feed, err := service.UpdateFeed(context.Background(), ClientReleaseMacOSArm64)
	require.NoError(t, err)
	require.Contains(t, string(feed), "/macos-arm64/DoneBot-mac-arm64.zip")
	require.NotContains(t, string(feed), ClientReleaseMacOSArm64DMG)
}

func TestCompareClientReleaseVersionsUsesSemverPrecedence(t *testing.T) {
	require.Greater(t, compareClientReleaseVersions("1.10.0", "1.9.0"), 0)
	require.Less(t, compareClientReleaseVersions("1.10.0-rc.2", "1.10.0-rc.10"), 0)
	require.Less(t, compareClientReleaseVersions("1.10.0-rc.1", "1.10.0"), 0)
}

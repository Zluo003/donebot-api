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
	values         map[string]string
	setError       error
	setErrorOnCall int
	setCalls       int
}

type clientReleaseStorageStub struct {
	files             map[string][]byte
	deleteError       error
	deleteErrorOnCall int
	deleteCalls       []string
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
	s.deleteCalls = append(s.deleteCalls, key)
	if s.deleteError != nil && (s.deleteErrorOnCall == 0 || len(s.deleteCalls) == s.deleteErrorOnCall) {
		return s.deleteError
	}
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
	s.setCalls++
	if s.setError != nil && (s.setErrorOnCall == 0 || s.setCalls == s.setErrorOnCall) {
		return s.setError
	}
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

func TestClientReleaseDeleteUsesOriginalStorageAndKeepsLatest(t *testing.T) {
	ctx := context.Background()
	repo := &clientReleaseSettingsStub{values: map[string]string{}}
	stores := map[string]*clientReleaseStorageStub{}
	storage := NewDoneBotStorageService(repo, nil, func(_ context.Context, policy DoneBotStoragePolicy) (AssetStorage, error) {
		if stores[policy.LocalPath] == nil {
			stores[policy.LocalPath] = &clientReleaseStorageStub{files: map[string][]byte{}}
		}
		return stores[policy.LocalPath], nil
	}, nil, nil)
	svc := NewClientReleaseService(storage)
	paths := []string{t.TempDir(), t.TempDir()}
	for i, version := range []string{"0.1.0", "0.1.1"} {
		_, err := svc.UpdateStorageSettings(ctx, ClientReleaseStorageSettings{Driver: "local", LocalPath: paths[i]})
		require.NoError(t, err)
		for platform, artifact := range testClientReleasePlatforms() {
			_, err = svc.Upload(ctx, version, platform, artifact.Filename, "", strings.NewReader(version), int64(len(version)))
			require.NoError(t, err)
		}
		_, err = svc.Publish(ctx, version)
		require.NoError(t, err)
	}
	for _, version := range []string{"0.1.1", "v0.1.1"} {
		require.ErrorContains(t, svc.Delete(ctx, version), "Publish a newer release")
	}
	require.Len(t, stores[paths[1]].files, 3)
	require.Empty(t, stores[paths[1]].deleteCalls)
	require.NoError(t, svc.Delete(ctx, "v0.1.0"))
	require.Empty(t, stores[paths[0]].files)
	require.Len(t, stores[paths[0]].deleteCalls, 3)
	require.Len(t, stores[paths[1]].files, 3)
	require.Empty(t, stores[paths[1]].deleteCalls)
	list, err := svc.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "0.1.1", list[0].Version)
	for _, platform := range []string{ClientReleaseWindowsX64, ClientReleaseMacOSArm64} {
		check, err := svc.Check(ctx, "0.1.0", platform)
		require.NoError(t, err)
		require.True(t, check.UpdateAvailable)
		feed, err := svc.UpdateFeed(ctx, platform)
		require.NoError(t, err)
		require.Contains(t, string(feed), `version: "0.1.1"`)
		_, reader, err := svc.OpenArtifact(ctx, "0.1.1", platform)
		require.NoError(t, err)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, "0.1.1", string(data))
	}
	_, _, err = svc.OpenArtifact(ctx, "0.1.0", ClientReleaseMacOSArm64)
	require.Error(t, err)
}

func TestClientReleaseDeleteFailureCanBeRetriedWithoutRepublishing(t *testing.T) {
	ctx := context.Background()
	policy, err := json.Marshal(DoneBotStoragePolicy{Driver: "local", LocalPath: t.TempDir()})
	require.NoError(t, err)
	platforms := testClientReleasePlatforms()
	objects := &clientReleaseStorageStub{files: map[string][]byte{}}
	for platform, artifact := range platforms {
		artifact.StorageConfig = policy
		platforms[platform] = artifact
		objects.files[artifact.ObjectKey] = []byte("fixture")
	}
	svc, repo := newClientReleaseTestService(t,
		testClientReleaseRecord("0.1.0", "published", platforms),
		testClientReleaseRecord("0.1.1", "published", testClientReleasePlatforms()),
	)
	svc.storage = NewDoneBotStorageService(repo, nil, func(context.Context, DoneBotStoragePolicy) (AssetStorage, error) { return objects, nil }, nil, nil)
	repo.setError = errors.New("fixture persistence failure")
	require.ErrorContains(t, svc.Delete(ctx, "0.1.0"), "fixture persistence failure")
	require.Empty(t, objects.deleteCalls)
	store, err := svc.load(ctx)
	require.NoError(t, err)
	require.Equal(t, "published", findClientRelease(store.Releases, "0.1.0").Status)
	repo.setError = nil
	objects.deleteError = errors.New("fixture object storage failure")
	require.ErrorContains(t, svc.Delete(ctx, "0.1.0"), "retry deleting")
	store, err = svc.load(ctx)
	require.NoError(t, err)
	require.Equal(t, "deleting", findClientRelease(store.Releases, "0.1.0").Status)
	_, err = svc.Publish(ctx, "0.1.0")
	require.ErrorContains(t, err, "being deleted")
	_, err = svc.Upload(ctx, "0.1.0", ClientReleaseMacOSArm64, "DoneBot.zip", "", strings.NewReader("fixture"), 7)
	require.ErrorContains(t, err, "being deleted")
	objects.deleteError = nil
	require.NoError(t, svc.Delete(ctx, "0.1.0"))
	require.Empty(t, objects.files)
	latest, err := svc.Latest(ctx)
	require.NoError(t, err)
	require.Equal(t, "0.1.1", latest.Version)
	require.Error(t, svc.Delete(ctx, "0.1.0"))
	require.Error(t, svc.Delete(ctx, "invalid"))
}

func TestClientReleaseDeleteResumesAfterPartialCleanupOrMetadataFailure(t *testing.T) {
	for _, failure := range []string{"partial storage cleanup", "final metadata save"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			policy, err := json.Marshal(DoneBotStoragePolicy{Driver: "local", LocalPath: t.TempDir()})
			require.NoError(t, err)
			platforms := testClientReleasePlatforms()
			objects := &clientReleaseStorageStub{files: map[string][]byte{}}
			for platform, artifact := range platforms {
				artifact.StorageConfig = policy
				platforms[platform] = artifact
				objects.files[artifact.ObjectKey] = []byte("fixture")
			}
			svc, repo := newClientReleaseTestService(t,
				testClientReleaseRecord("0.1.0", "published", platforms),
				testClientReleaseRecord("0.1.1", "published", testClientReleasePlatforms()),
			)
			svc.storage = NewDoneBotStorageService(repo, nil, func(context.Context, DoneBotStoragePolicy) (AssetStorage, error) { return objects, nil }, nil, nil)
			if failure == "partial storage cleanup" {
				objects.deleteError = errors.New("fixture storage failure")
				objects.deleteErrorOnCall = 2
			} else {
				repo.setError = errors.New("fixture metadata failure")
				repo.setErrorOnCall = 2
			}
			require.Error(t, svc.Delete(ctx, "0.1.0"))
			store, err := svc.load(ctx)
			require.NoError(t, err)
			require.Equal(t, "deleting", findClientRelease(store.Releases, "0.1.0").Status)
			if failure == "partial storage cleanup" {
				require.Len(t, objects.files, 2)
			} else {
				require.Empty(t, objects.files)
			}
			_, _, err = svc.OpenArtifact(ctx, "0.1.0", ClientReleaseMacOSArm64)
			require.Error(t, err)
			latest, err := svc.Latest(ctx)
			require.NoError(t, err)
			require.Equal(t, "0.1.1", latest.Version)
			require.NoError(t, svc.Delete(ctx, "0.1.0"))
			require.Empty(t, objects.files)
			store, err = svc.load(ctx)
			require.NoError(t, err)
			require.Nil(t, findClientRelease(store.Releases, "0.1.0"))
		})
	}
}

package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	mobileReleasesSettingKey = "donebot_mobile_releases"
	MobileReleaseAndroidAPK  = "android-apk"
	maxMobileVersionCode     = 2_100_000_000
	androidAPKContentType    = "application/vnd.android.package-archive"
)

type MobileRelease struct {
	Version     string                           `json:"version"`
	VersionCode int                              `json:"version_code"`
	Notes       string                           `json:"notes,omitempty"`
	ForceUpdate bool                             `json:"force_update,omitempty"`
	Status      string                           `json:"status"`
	CreatedAt   time.Time                        `json:"created_at"`
	PublishedAt *time.Time                       `json:"published_at,omitempty"`
	Platforms   map[string]ClientReleaseArtifact `json:"platforms"`
}

type MobileUpdateCheck struct {
	Current         string `json:"current"`
	CurrentCode     int    `json:"current_code,omitempty"`
	Latest          string `json:"latest,omitempty"`
	LatestCode      int    `json:"latest_code,omitempty"`
	Platform        string `json:"platform"`
	UpdateAvailable bool   `json:"update_available"`
	ForceUpdate     bool   `json:"force_update,omitempty"`
	Notes           string `json:"notes,omitempty"`
	Filename        string `json:"filename,omitempty"`
	SizeBytes       int64  `json:"size_bytes,omitempty"`
	DownloadURL     string `json:"download_url,omitempty"`
	SHA256          string `json:"sha256,omitempty"`
}

type mobileReleaseRecord struct {
	Version     string                                 `json:"version"`
	VersionCode int                                    `json:"version_code"`
	Notes       string                                 `json:"notes,omitempty"`
	ForceUpdate bool                                   `json:"force_update,omitempty"`
	Status      string                                 `json:"status"`
	CreatedAt   time.Time                              `json:"created_at"`
	PublishedAt *time.Time                             `json:"published_at,omitempty"`
	Platforms   map[string]clientReleaseArtifactRecord `json:"platforms"`
}

type mobileReleaseStore struct {
	Releases []mobileReleaseRecord `json:"releases"`
}

func (s *ClientReleaseService) ListMobile(ctx context.Context) ([]MobileRelease, error) {
	store, err := s.loadMobile(ctx)
	if err != nil {
		return nil, err
	}
	records := append([]mobileReleaseRecord(nil), store.Releases...)
	sort.Slice(records, func(i, j int) bool {
		if cmp := compareClientReleaseVersions(records[i].Version, records[j].Version); cmp != 0 {
			return cmp > 0
		}
		return records[i].VersionCode > records[j].VersionCode
	})
	out := make([]MobileRelease, 0, len(records))
	for _, record := range records {
		out = append(out, mobileReleaseInfo(record))
	}
	return out, nil
}

func (s *ClientReleaseService) LatestMobile(ctx context.Context) (*MobileRelease, error) {
	store, err := s.loadMobile(ctx)
	if err != nil {
		return nil, err
	}
	latest := latestPublishedMobileRelease(store.Releases)
	if latest == nil {
		return nil, infraerrors.NotFound("MOBILE_RELEASE_NOT_FOUND", "No mobile release has been published")
	}
	out := mobileReleaseInfo(*latest)
	return &out, nil
}

func (s *ClientReleaseService) CheckMobile(ctx context.Context, current string, currentCode int, platform string) (MobileUpdateCheck, error) {
	current = strings.TrimPrefix(strings.TrimSpace(current), "v")
	if _, _, _, _, ok := parseClientReleaseVersion(current); !ok {
		return MobileUpdateCheck{}, infraerrors.BadRequest("INVALID_MOBILE_VERSION", "A semantic mobile version is required")
	}
	if currentCode < 0 || currentCode > maxMobileVersionCode {
		return MobileUpdateCheck{}, infraerrors.BadRequest("INVALID_MOBILE_VERSION_CODE", "Invalid mobile version code")
	}
	if strings.TrimSpace(platform) == "" {
		platform = MobileReleaseAndroidAPK
	}
	if !validMobileReleasePlatform(platform) {
		return MobileUpdateCheck{}, infraerrors.BadRequest("INVALID_MOBILE_PLATFORM", "Unsupported mobile platform")
	}
	result := MobileUpdateCheck{Current: current, CurrentCode: currentCode, Platform: platform}
	store, err := s.loadMobile(ctx)
	if err != nil {
		return result, err
	}
	latest := latestPublishedMobileRelease(store.Releases)
	if latest == nil {
		return result, nil
	}
	result.Latest = latest.Version
	result.LatestCode = latest.VersionCode
	if !mobileReleaseIsNewer(*latest, current, currentCode) {
		return result, nil
	}
	artifact, ok := latest.Platforms[platform]
	if !ok {
		return result, nil
	}
	force := latest.ForceUpdate
	if !force {
		for _, release := range store.Releases {
			if release.Status == "published" && release.ForceUpdate && mobileReleaseIsNewer(release, current, currentCode) {
				force = true
				break
			}
		}
	}
	result.UpdateAvailable = true
	result.ForceUpdate = force
	result.Notes = latest.Notes
	result.Filename = artifact.Filename
	result.SizeBytes = artifact.SizeBytes
	result.DownloadURL = mobileReleaseDownloadURL(latest.Version, platform, artifact.Filename)
	result.SHA256 = artifact.SHA256
	return result, nil
}

func (s *ClientReleaseService) UploadMobile(
	ctx context.Context,
	version string,
	versionCode int,
	platform, filename, notes string,
	forceUpdate bool,
	source io.Reader,
	size int64,
) (*MobileRelease, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if _, _, _, _, ok := parseClientReleaseVersion(version); !ok {
		return nil, infraerrors.BadRequest("INVALID_MOBILE_VERSION", "Version must use semantic versioning, such as 0.1.0")
	}
	if versionCode <= 0 || versionCode > maxMobileVersionCode {
		return nil, infraerrors.BadRequest("INVALID_MOBILE_VERSION_CODE", "Version code must be a positive integer")
	}
	if !validMobileReleasePlatform(platform) {
		return nil, infraerrors.BadRequest("INVALID_MOBILE_PLATFORM", "Unsupported mobile platform")
	}
	filename, err := sanitizeMobileReleaseFilename(filename, platform)
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_MOBILE_PACKAGE", err.Error())
	}
	if size <= 4 || size > MaxClientReleaseBytes {
		return nil, infraerrors.BadRequest("INVALID_MOBILE_PACKAGE_SIZE", "Mobile package must be a valid APK up to 500 MB")
	}
	if len(notes) > 12000 {
		return nil, infraerrors.BadRequest("MOBILE_RELEASE_NOTES_TOO_LONG", "Release notes are too long")
	}
	if s.settings == nil || s.storage == nil {
		return nil, infraerrors.ServiceUnavailable("MOBILE_RELEASES_UNAVAILABLE", "Mobile release storage is unavailable")
	}

	var magic [4]byte
	if _, err = io.ReadFull(source, magic[:]); err != nil {
		return nil, infraerrors.BadRequest("INVALID_MOBILE_PACKAGE", "Unable to read Android APK header")
	}
	if magic != [4]byte{'P', 'K', 0x03, 0x04} {
		return nil, infraerrors.BadRequest("INVALID_MOBILE_PACKAGE", "Android package must be a valid .apk archive")
	}
	stream := io.MultiReader(bytes.NewReader(magic[:]), source)

	s.mu.Lock()
	store, err := s.loadMobile(ctx)
	if err == nil {
		if record := findMobileRelease(store.Releases, version); record != nil {
			switch record.Status {
			case "deleting":
				err = infraerrors.Conflict("MOBILE_RELEASE_DELETING", "A release being deleted cannot be changed")
			case "published":
				err = infraerrors.Conflict("MOBILE_RELEASE_ALREADY_PUBLISHED", "Published releases cannot be changed")
			}
		}
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}

	objectID := make([]byte, 16)
	if _, err = rand.Read(objectID); err != nil {
		return nil, err
	}
	objectKey := "mobile-releases/" + version + "/" + platform + "/" + hex.EncodeToString(objectID) + "-" + filename
	sha256Hash, sha512Hash := sha256.New(), sha512.New()
	storageConfig, objectKey, err := s.saveReleaseObject(ctx, objectKey, androidAPKContentType, io.TeeReader(stream, io.MultiWriter(sha256Hash, sha512Hash)), size)
	if err != nil {
		return nil, err
	}
	artifact := clientReleaseArtifactRecord{
		Filename:      filename,
		ObjectKey:     objectKey,
		StorageConfig: storageConfig,
		SizeBytes:     size,
		SHA256:        hex.EncodeToString(sha256Hash.Sum(nil)),
		SHA512:        base64.StdEncoding.EncodeToString(sha512Hash.Sum(nil)),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	store, err = s.loadMobile(ctx)
	if err != nil {
		return nil, err
	}
	record := findMobileRelease(store.Releases, version)
	if record != nil && record.Status == "deleting" {
		return nil, infraerrors.Conflict("MOBILE_RELEASE_DELETING", "A release being deleted cannot be changed")
	}
	if record != nil && record.Status == "published" {
		return nil, infraerrors.Conflict("MOBILE_RELEASE_ALREADY_PUBLISHED", "Published releases cannot be changed")
	}
	if record == nil {
		now := time.Now().UTC()
		record = &mobileReleaseRecord{Version: version, Status: "draft", CreatedAt: now, Platforms: map[string]clientReleaseArtifactRecord{}}
		store.Releases = append(store.Releases, *record)
		record = &store.Releases[len(store.Releases)-1]
	}
	if record.Platforms == nil {
		record.Platforms = map[string]clientReleaseArtifactRecord{}
	}
	var replaced *clientReleaseArtifactRecord
	if prev, ok := record.Platforms[platform]; ok && prev.ObjectKey != "" && prev.ObjectKey != artifact.ObjectKey {
		copyPrev := prev
		replaced = &copyPrev
	}
	record.VersionCode = versionCode
	record.Notes = strings.TrimSpace(notes)
	record.ForceUpdate = forceUpdate
	record.Platforms[platform] = artifact
	if err = s.saveMobile(ctx, store); err != nil {
		return nil, err
	}
	if replaced != nil && s.storage != nil {
		if oldStorage, openErr := s.storage.originalStorage(ctx, &GatewayAsset{StorageConfig: replaced.StorageConfig}); openErr == nil {
			_ = oldStorage.Delete(ctx, replaced.ObjectKey)
		}
	}
	out := mobileReleaseInfo(*record)
	return &out, nil
}

func (s *ClientReleaseService) PublishMobile(ctx context.Context, version string) (*MobileRelease, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if _, _, _, _, ok := parseClientReleaseVersion(version); !ok {
		return nil, infraerrors.BadRequest("INVALID_MOBILE_VERSION", "Version must use semantic versioning, such as 0.1.0")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.loadMobile(ctx)
	if err != nil {
		return nil, err
	}
	record := findMobileRelease(store.Releases, version)
	if record == nil {
		return nil, infraerrors.NotFound("MOBILE_RELEASE_NOT_FOUND", "Mobile release draft was not found")
	}
	if record.Status == "deleting" {
		return nil, infraerrors.Conflict("MOBILE_RELEASE_DELETING", "A release being deleted cannot be published")
	}
	if record.Status == "published" {
		out := mobileReleaseInfo(*record)
		return &out, nil
	}
	if _, ok := record.Platforms[MobileReleaseAndroidAPK]; !ok {
		return nil, infraerrors.BadRequest("MOBILE_RELEASE_INCOMPLETE", "Upload the Android APK before publishing")
	}
	if record.VersionCode <= 0 {
		return nil, infraerrors.BadRequest("INVALID_MOBILE_VERSION_CODE", "Version code must be a positive integer")
	}
	if latest := latestPublishedMobileRelease(store.Releases); latest != nil {
		if compareClientReleaseVersions(version, latest.Version) <= 0 {
			return nil, infraerrors.Conflict("MOBILE_RELEASE_VERSION_NOT_NEWER", "A published mobile release must have a higher version")
		}
		if record.VersionCode <= latest.VersionCode {
			return nil, infraerrors.Conflict("MOBILE_RELEASE_VERSION_CODE_NOT_NEWER", "A published mobile release must have a higher version code")
		}
	}
	now := time.Now().UTC()
	record.Status = "published"
	record.PublishedAt = &now
	if err = s.saveMobile(ctx, store); err != nil {
		return nil, err
	}
	out := mobileReleaseInfo(*record)
	return &out, nil
}

func (s *ClientReleaseService) DeleteMobile(ctx context.Context, version string) error {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if _, _, _, _, ok := parseClientReleaseVersion(version); !ok {
		return infraerrors.BadRequest("INVALID_MOBILE_VERSION", "A semantic mobile version is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.loadMobile(ctx)
	if err != nil {
		return err
	}
	record := findMobileRelease(store.Releases, version)
	if record == nil {
		return infraerrors.NotFound("MOBILE_RELEASE_NOT_FOUND", "Mobile release was not found")
	}
	if latest := latestPublishedMobileRelease(store.Releases); latest != nil && latest.Version == version {
		return infraerrors.Conflict("MOBILE_RELEASE_IS_LATEST", "Publish a newer mobile release before deleting the latest published release")
	}
	if len(record.Platforms) > 0 && s.storage == nil {
		return infraerrors.ServiceUnavailable("MOBILE_RELEASES_UNAVAILABLE", "Mobile release storage is unavailable")
	}
	if record.Status != "deleting" {
		record.Status = "deleting"
		if err = s.saveMobile(ctx, store); err != nil {
			return err
		}
	}
	platforms := make([]string, 0, len(record.Platforms))
	for platform := range record.Platforms {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	for _, platform := range platforms {
		artifact := record.Platforms[platform]
		storage, err := s.storage.originalStorage(ctx, &GatewayAsset{StorageConfig: artifact.StorageConfig})
		if err != nil {
			return err
		}
		if err = storage.Delete(ctx, artifact.ObjectKey); err != nil {
			return infraerrors.ServiceUnavailable("MOBILE_RELEASE_DELETE_FAILED", "Unable to delete a stored mobile package; retry deleting this release")
		}
	}
	remaining := make([]mobileReleaseRecord, 0, len(store.Releases)-1)
	for _, release := range store.Releases {
		if release.Version != version {
			remaining = append(remaining, release)
		}
	}
	store.Releases = remaining
	return s.saveMobile(ctx, store)
}

func (s *ClientReleaseService) OpenMobileArtifact(ctx context.Context, version, platform string) (*ClientReleaseArtifact, io.ReadCloser, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if _, _, _, _, ok := parseClientReleaseVersion(version); !ok || !validMobileReleasePlatform(platform) {
		return nil, nil, infraerrors.NotFound("MOBILE_RELEASE_NOT_FOUND", "Mobile package was not found")
	}
	store, err := s.loadMobile(ctx)
	if err != nil {
		return nil, nil, err
	}
	record := findMobileRelease(store.Releases, version)
	if record == nil || record.Status != "published" {
		return nil, nil, infraerrors.NotFound("MOBILE_RELEASE_NOT_FOUND", "Mobile package was not found")
	}
	artifact, ok := record.Platforms[platform]
	if !ok {
		return nil, nil, infraerrors.NotFound("MOBILE_RELEASE_PACKAGE_NOT_FOUND", "Mobile package was not found")
	}
	reader, err := s.storage.OpenReleaseObject(ctx, artifact.ObjectKey, artifact.StorageConfig)
	if err != nil {
		return nil, nil, err
	}
	info := publicMobileReleaseArtifact(record.Version, platform, artifact)
	return &info, reader, nil
}

func (s *ClientReleaseService) loadMobile(ctx context.Context) (mobileReleaseStore, error) {
	if s.settings == nil {
		return mobileReleaseStore{}, infraerrors.ServiceUnavailable("MOBILE_RELEASES_UNAVAILABLE", "Mobile release settings are unavailable")
	}
	raw, err := s.settings.GetValue(ctx, mobileReleasesSettingKey)
	if err != nil {
		if err == ErrSettingNotFound {
			return mobileReleaseStore{}, nil
		}
		return mobileReleaseStore{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return mobileReleaseStore{}, nil
	}
	var store mobileReleaseStore
	if err = json.Unmarshal([]byte(raw), &store); err != nil {
		return mobileReleaseStore{}, fmt.Errorf("decode mobile releases: %w", err)
	}
	return store, nil
}

func (s *ClientReleaseService) saveMobile(ctx context.Context, store mobileReleaseStore) error {
	raw, err := json.Marshal(store)
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, mobileReleasesSettingKey, string(raw))
}

func mobileReleaseInfo(record mobileReleaseRecord) MobileRelease {
	info := MobileRelease{
		Version:     record.Version,
		VersionCode: record.VersionCode,
		Notes:       record.Notes,
		ForceUpdate: record.ForceUpdate,
		Status:      record.Status,
		CreatedAt:   record.CreatedAt,
		PublishedAt: record.PublishedAt,
		Platforms:   map[string]ClientReleaseArtifact{},
	}
	for platform, artifact := range record.Platforms {
		info.Platforms[platform] = publicMobileReleaseArtifact(record.Version, platform, artifact)
	}
	return info
}

func publicMobileReleaseArtifact(version, platform string, record clientReleaseArtifactRecord) ClientReleaseArtifact {
	return ClientReleaseArtifact{
		Filename:    record.Filename,
		SizeBytes:   record.SizeBytes,
		SHA256:      record.SHA256,
		SHA512:      record.SHA512,
		DownloadURL: mobileReleaseDownloadURL(version, platform, record.Filename),
	}
}

func mobileReleaseDownloadURL(version, platform, filename string) string {
	return "/api/v1/client/mobile-releases/" + version + "/" + platform + "/" + filename
}

func latestPublishedMobileRelease(records []mobileReleaseRecord) *mobileReleaseRecord {
	var latest *mobileReleaseRecord
	for i := range records {
		record := &records[i]
		if record.Status != "published" {
			continue
		}
		if latest == nil {
			latest = record
			continue
		}
		cmp := compareClientReleaseVersions(record.Version, latest.Version)
		if cmp > 0 || (cmp == 0 && record.VersionCode > latest.VersionCode) {
			latest = record
		}
	}
	return latest
}

func mobileReleaseIsNewer(release mobileReleaseRecord, current string, currentCode int) bool {
	if currentCode > 0 && release.VersionCode > 0 {
		return release.VersionCode > currentCode
	}
	return compareClientReleaseVersions(release.Version, current) > 0
}

func findMobileRelease(records []mobileReleaseRecord, version string) *mobileReleaseRecord {
	for i := range records {
		if records[i].Version == version {
			return &records[i]
		}
	}
	return nil
}

func validMobileReleasePlatform(platform string) bool {
	return platform == MobileReleaseAndroidAPK
}

func sanitizeMobileReleaseFilename(value, platform string) (string, error) {
	value = strings.ReplaceAll(value, "\\", "/")
	filename := value[strings.LastIndex(value, "/")+1:]
	filename = clientReleaseFilenamePattern.ReplaceAllString(filename, "_")
	filename = strings.Trim(filename, ".-")
	if len(filename) > 160 {
		filename = filename[len(filename)-160:]
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if filename == "" || platform != MobileReleaseAndroidAPK || ext != ".apk" {
		return "", fmt.Errorf("invalid package extension: Android mobile packages must be .apk")
	}
	return filename, nil
}

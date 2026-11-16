package service

import (
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
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	clientReleasesSettingKey = "donebot_client_releases"
	ClientReleaseWindowsX64  = "windows-x64"
	ClientReleaseMacOSArm64  = "macos-arm64"
	MaxClientReleaseBytes    = int64(1 << 30)
)

var clientReleaseVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)
var clientReleaseFilenamePattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

type ClientReleaseArtifact struct {
	Filename    string `json:"filename"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	SHA512      string `json:"sha512"`
	DownloadURL string `json:"download_url"`
}

type ClientRelease struct {
	Version     string                           `json:"version"`
	Notes       string                           `json:"notes,omitempty"`
	Status      string                           `json:"status"`
	CreatedAt   time.Time                        `json:"created_at"`
	PublishedAt *time.Time                       `json:"published_at,omitempty"`
	Platforms   map[string]ClientReleaseArtifact `json:"platforms"`
}

type ClientUpdateCheck struct {
	Current         string `json:"current"`
	Latest          string `json:"latest,omitempty"`
	Platform        string `json:"platform"`
	UpdateAvailable bool   `json:"update_available"`
	DownloadURL     string `json:"download_url,omitempty"`
	SHA256          string `json:"sha256,omitempty"`
}

type clientReleaseArtifactRecord struct {
	Filename      string          `json:"filename"`
	ObjectKey     string          `json:"object_key"`
	StorageConfig json.RawMessage `json:"storage_config"`
	SizeBytes     int64           `json:"size_bytes"`
	SHA256        string          `json:"sha256"`
	SHA512        string          `json:"sha512"`
}

type clientReleaseRecord struct {
	Version     string                                 `json:"version"`
	Notes       string                                 `json:"notes,omitempty"`
	Status      string                                 `json:"status"`
	CreatedAt   time.Time                              `json:"created_at"`
	PublishedAt *time.Time                             `json:"published_at,omitempty"`
	Platforms   map[string]clientReleaseArtifactRecord `json:"platforms"`
}

type clientReleaseStore struct {
	Releases []clientReleaseRecord `json:"releases"`
}

type ClientReleaseService struct {
	settings SettingRepository
	storage  *DoneBotStorageService
	mu       sync.Mutex
}

func NewClientReleaseService(storage *DoneBotStorageService) *ClientReleaseService {
	if storage == nil {
		return &ClientReleaseService{}
	}
	return &ClientReleaseService{settings: storage.repo, storage: storage}
}

func (s *ClientReleaseService) List(ctx context.Context) ([]ClientRelease, error) {
	store, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	records := append([]clientReleaseRecord(nil), store.Releases...)
	sort.Slice(records, func(i, j int) bool { return compareClientReleaseVersions(records[i].Version, records[j].Version) > 0 })
	out := make([]ClientRelease, 0, len(records))
	for _, record := range records {
		out = append(out, clientReleaseInfo(record))
	}
	return out, nil
}

func (s *ClientReleaseService) Latest(ctx context.Context) (*ClientRelease, error) {
	store, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	latest := latestPublishedClientRelease(store.Releases)
	if latest == nil {
		return nil, infraerrors.NotFound("CLIENT_RELEASE_NOT_FOUND", "No client release has been published")
	}
	out := clientReleaseInfo(*latest)
	return &out, nil
}

func (s *ClientReleaseService) Check(ctx context.Context, current, platform string) (ClientUpdateCheck, error) {
	current = strings.TrimPrefix(strings.TrimSpace(current), "v")
	if _, _, _, _, ok := parseClientReleaseVersion(current); !ok {
		return ClientUpdateCheck{}, infraerrors.BadRequest("INVALID_CLIENT_VERSION", "A semantic client version is required")
	}
	if !validClientReleasePlatform(platform) {
		return ClientUpdateCheck{}, infraerrors.BadRequest("INVALID_CLIENT_PLATFORM", "Unsupported client platform")
	}
	result := ClientUpdateCheck{Current: current, Platform: platform}
	store, err := s.load(ctx)
	if err != nil {
		return result, err
	}
	latest := latestPublishedClientRelease(store.Releases)
	if latest == nil {
		return result, nil
	}
	result.Latest = latest.Version
	if compareClientReleaseVersions(latest.Version, current) <= 0 {
		return result, nil
	}
	artifact, ok := latest.Platforms[platform]
	if !ok {
		return result, nil
	}
	result.UpdateAvailable = true
	result.DownloadURL = clientReleaseDownloadURL(latest.Version, platform, artifact.Filename)
	result.SHA256 = artifact.SHA256
	return result, nil
}

func (s *ClientReleaseService) Upload(ctx context.Context, version, platform, filename, notes string, source io.Reader, size int64) (*ClientRelease, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if _, _, _, _, ok := parseClientReleaseVersion(version); !ok {
		return nil, infraerrors.BadRequest("INVALID_CLIENT_VERSION", "Version must use semantic versioning, such as 1.2.3")
	}
	if !validClientReleasePlatform(platform) {
		return nil, infraerrors.BadRequest("INVALID_CLIENT_PLATFORM", "Unsupported client platform")
	}
	filename, err := sanitizeClientReleaseFilename(filename, platform)
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_CLIENT_PACKAGE", err.Error())
	}
	if size <= 0 || size > MaxClientReleaseBytes {
		return nil, infraerrors.BadRequest("INVALID_CLIENT_PACKAGE_SIZE", "Client package must be between 1 byte and 1 GiB")
	}
	if len(notes) > 12000 {
		return nil, infraerrors.BadRequest("CLIENT_RELEASE_NOTES_TOO_LONG", "Release notes are too long")
	}
	if s.settings == nil || s.storage == nil {
		return nil, infraerrors.ServiceUnavailable("CLIENT_RELEASES_UNAVAILABLE", "Client release storage is unavailable")
	}

	s.mu.Lock()
	store, err := s.load(ctx)
	if err == nil {
		if record := findClientRelease(store.Releases, version); record != nil && record.Status == "published" {
			err = infraerrors.Conflict("CLIENT_RELEASE_ALREADY_PUBLISHED", "Published releases cannot be changed")
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
	objectKey := "client-releases/" + version + "/" + platform + "/" + hex.EncodeToString(objectID) + "-" + filename
	sha256Hash, sha512Hash := sha256.New(), sha512.New()
	contentType := "application/zip"
	if platform == ClientReleaseWindowsX64 {
		contentType = "application/vnd.microsoft.portable-executable"
	}
	storageConfig, err := s.storage.SaveReleaseObject(ctx, objectKey, contentType, io.TeeReader(source, io.MultiWriter(sha256Hash, sha512Hash)), size)
	if err != nil {
		return nil, err
	}
	artifact := clientReleaseArtifactRecord{
		Filename: filename, ObjectKey: objectKey, StorageConfig: storageConfig,
		SizeBytes: size, SHA256: hex.EncodeToString(sha256Hash.Sum(nil)), SHA512: base64.StdEncoding.EncodeToString(sha512Hash.Sum(nil)),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	store, err = s.load(ctx)
	if err != nil {
		return nil, err
	}
	record := findClientRelease(store.Releases, version)
	if record != nil && record.Status == "published" {
		return nil, infraerrors.Conflict("CLIENT_RELEASE_ALREADY_PUBLISHED", "Published releases cannot be changed")
	}
	if record == nil {
		now := time.Now().UTC()
		record = &clientReleaseRecord{Version: version, Status: "draft", CreatedAt: now, Platforms: map[string]clientReleaseArtifactRecord{}}
		store.Releases = append(store.Releases, *record)
		record = &store.Releases[len(store.Releases)-1]
	}
	if record.Platforms == nil {
		record.Platforms = map[string]clientReleaseArtifactRecord{}
	}
	if strings.TrimSpace(notes) != "" {
		record.Notes = strings.TrimSpace(notes)
	}
	record.Platforms[platform] = artifact
	if err = s.save(ctx, store); err != nil {
		return nil, err
	}
	out := clientReleaseInfo(*record)
	return &out, nil
}

func (s *ClientReleaseService) Publish(ctx context.Context, version string) (*ClientRelease, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if _, _, _, _, ok := parseClientReleaseVersion(version); !ok {
		return nil, infraerrors.BadRequest("INVALID_CLIENT_VERSION", "Version must use semantic versioning, such as 1.2.3")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	record := findClientRelease(store.Releases, version)
	if record == nil {
		return nil, infraerrors.NotFound("CLIENT_RELEASE_NOT_FOUND", "Client release draft was not found")
	}
	if record.Status == "published" {
		out := clientReleaseInfo(*record)
		return &out, nil
	}
	if _, ok := record.Platforms[ClientReleaseWindowsX64]; !ok {
		return nil, infraerrors.BadRequest("CLIENT_RELEASE_INCOMPLETE", "Upload the Windows x64 package before publishing")
	}
	if _, ok := record.Platforms[ClientReleaseMacOSArm64]; !ok {
		return nil, infraerrors.BadRequest("CLIENT_RELEASE_INCOMPLETE", "Upload the macOS Apple silicon package before publishing")
	}
	if latest := latestPublishedClientRelease(store.Releases); latest != nil && compareClientReleaseVersions(version, latest.Version) <= 0 {
		return nil, infraerrors.Conflict("CLIENT_RELEASE_VERSION_NOT_NEWER", "A published release must have a higher version")
	}
	now := time.Now().UTC()
	record.Status = "published"
	record.PublishedAt = &now
	if err = s.save(ctx, store); err != nil {
		return nil, err
	}
	out := clientReleaseInfo(*record)
	return &out, nil
}

func (s *ClientReleaseService) OpenArtifact(ctx context.Context, version, platform string) (*ClientReleaseArtifact, io.ReadCloser, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if _, _, _, _, ok := parseClientReleaseVersion(version); !ok || !validClientReleasePlatform(platform) {
		return nil, nil, infraerrors.NotFound("CLIENT_RELEASE_NOT_FOUND", "Client package was not found")
	}
	store, err := s.load(ctx)
	if err != nil {
		return nil, nil, err
	}
	record := findClientRelease(store.Releases, version)
	if record == nil || record.Status != "published" {
		return nil, nil, infraerrors.NotFound("CLIENT_RELEASE_NOT_FOUND", "Client package was not found")
	}
	artifact, ok := record.Platforms[platform]
	if !ok {
		return nil, nil, infraerrors.NotFound("CLIENT_RELEASE_PACKAGE_NOT_FOUND", "Client package was not found")
	}
	reader, err := s.storage.OpenReleaseObject(ctx, artifact.ObjectKey, artifact.StorageConfig)
	if err != nil {
		return nil, nil, err
	}
	info := publicClientReleaseArtifact(record.Version, platform, artifact)
	return &info, reader, nil
}

func (s *ClientReleaseService) UpdateFeed(ctx context.Context, platform string) ([]byte, error) {
	if !validClientReleasePlatform(platform) {
		return nil, infraerrors.NotFound("CLIENT_RELEASE_FEED_NOT_FOUND", "Client update feed was not found")
	}
	latest, err := s.Latest(ctx)
	if err != nil {
		return nil, err
	}
	artifact, ok := latest.Platforms[platform]
	if !ok {
		return nil, infraerrors.NotFound("CLIENT_RELEASE_FEED_NOT_FOUND", "Client update feed was not found")
	}
	url := artifact.DownloadURL
	date := time.Now().UTC()
	if latest.PublishedAt != nil {
		date = latest.PublishedAt.UTC()
	}
	feed := fmt.Sprintf("version: %s\nfiles:\n  - url: %s\n    sha512: %s\n    size: %d\npath: %s\nsha512: %s\nreleaseDate: %s\n",
		yamlString(latest.Version), yamlString(url), yamlString(artifact.SHA512), artifact.SizeBytes,
		yamlString(url), yamlString(artifact.SHA512), yamlString(date.Format(time.RFC3339)))
	return []byte(feed), nil
}

func (s *ClientReleaseService) load(ctx context.Context) (clientReleaseStore, error) {
	if s.settings == nil {
		return clientReleaseStore{}, infraerrors.ServiceUnavailable("CLIENT_RELEASES_UNAVAILABLE", "Client release settings are unavailable")
	}
	raw, err := s.settings.GetValue(ctx, clientReleasesSettingKey)
	if err != nil {
		if err == ErrSettingNotFound {
			return clientReleaseStore{}, nil
		}
		return clientReleaseStore{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return clientReleaseStore{}, nil
	}
	var store clientReleaseStore
	if err = json.Unmarshal([]byte(raw), &store); err != nil {
		return clientReleaseStore{}, fmt.Errorf("decode client releases: %w", err)
	}
	return store, nil
}

func (s *ClientReleaseService) save(ctx context.Context, store clientReleaseStore) error {
	raw, err := json.Marshal(store)
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, clientReleasesSettingKey, string(raw))
}

func clientReleaseInfo(record clientReleaseRecord) ClientRelease {
	info := ClientRelease{Version: record.Version, Notes: record.Notes, Status: record.Status, CreatedAt: record.CreatedAt, PublishedAt: record.PublishedAt, Platforms: map[string]ClientReleaseArtifact{}}
	for platform, artifact := range record.Platforms {
		info.Platforms[platform] = publicClientReleaseArtifact(record.Version, platform, artifact)
	}
	return info
}

func publicClientReleaseArtifact(version, platform string, record clientReleaseArtifactRecord) ClientReleaseArtifact {
	return ClientReleaseArtifact{Filename: record.Filename, SizeBytes: record.SizeBytes, SHA256: record.SHA256, SHA512: record.SHA512, DownloadURL: clientReleaseDownloadURL(version, platform, record.Filename)}
}

func clientReleaseDownloadURL(version, platform, filename string) string {
	return "/api/v1/client/releases/" + version + "/" + platform + "/" + filename
}

func latestPublishedClientRelease(records []clientReleaseRecord) *clientReleaseRecord {
	var latest *clientReleaseRecord
	for i := range records {
		record := &records[i]
		if record.Status != "published" {
			continue
		}
		if latest == nil || compareClientReleaseVersions(record.Version, latest.Version) > 0 {
			latest = record
		}
	}
	return latest
}

func findClientRelease(records []clientReleaseRecord, version string) *clientReleaseRecord {
	for i := range records {
		if records[i].Version == version {
			return &records[i]
		}
	}
	return nil
}

func validClientReleasePlatform(platform string) bool {
	return platform == ClientReleaseWindowsX64 || platform == ClientReleaseMacOSArm64
}

func sanitizeClientReleaseFilename(value, platform string) (string, error) {
	value = strings.ReplaceAll(value, "\\", "/")
	filename := value[strings.LastIndex(value, "/")+1:]
	filename = clientReleaseFilenamePattern.ReplaceAllString(filename, "_")
	filename = strings.Trim(filename, ".-")
	if len(filename) > 160 {
		filename = filename[len(filename)-160:]
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if filename == "" || platform == ClientReleaseWindowsX64 && ext != ".exe" || platform == ClientReleaseMacOSArm64 && ext != ".zip" {
		return "", fmt.Errorf("Windows x64 packages must be .exe and macOS Apple silicon packages must be .zip")
	}
	return filename, nil
}

func parseClientReleaseVersion(value string) (int64, int64, int64, []string, bool) {
	match := clientReleaseVersionPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, 0, 0, nil, false
	}
	major, err1 := strconvParseReleasePart(match[1])
	minor, err2 := strconvParseReleasePart(match[2])
	patch, err3 := strconvParseReleasePart(match[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, nil, false
	}
	var prerelease []string
	if match[4] != "" {
		prerelease = strings.Split(match[4], ".")
		for _, part := range prerelease {
			if part == "" || len(part) > 1 && part[0] == '0' && allDigits(part) {
				return 0, 0, 0, nil, false
			}
		}
	}
	return major, minor, patch, prerelease, true
}

func strconvParseReleasePart(value string) (int64, error) {
	var result int64
	for _, digit := range value {
		result = result*10 + int64(digit-'0')
		if result < 0 {
			return 0, fmt.Errorf("version part overflows")
		}
	}
	return result, nil
}

func allDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}

func compareClientReleaseVersions(left, right string) int {
	lm, ln, lp, lpre, lok := parseClientReleaseVersion(left)
	rm, rn, rp, rpre, rok := parseClientReleaseVersion(right)
	if !lok || !rok {
		return strings.Compare(left, right)
	}
	for _, pair := range [][2]int64{{lm, rm}, {ln, rn}, {lp, rp}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(lpre) == 0 && len(rpre) > 0 {
		return 1
	}
	if len(lpre) > 0 && len(rpre) == 0 {
		return -1
	}
	for i := 0; i < len(lpre) && i < len(rpre); i++ {
		l, r := lpre[i], rpre[i]
		if l == r {
			continue
		}
		lnum, lerr := strconvParseReleasePart(l)
		rnum, rerr := strconvParseReleasePart(r)
		if lerr == nil && rerr == nil {
			if lnum < rnum {
				return -1
			}
			return 1
		}
		if lerr == nil {
			return -1
		}
		if rerr == nil {
			return 1
		}
		return strings.Compare(l, r)
	}
	return compareInt(len(lpre), len(rpre))
}

func compareInt(left, right int) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func yamlString(value string) string {
	quoted, _ := json.Marshal(value)
	return string(quoted)
}

func (s *DoneBotStorageService) SaveReleaseObject(ctx context.Context, key, contentType string, source io.Reader, size int64) (json.RawMessage, error) {
	policy, err := s.Policy(ctx, AssetPurposeUpload)
	if err != nil {
		return nil, err
	}
	storage, err := s.storage(ctx, policy)
	if err != nil {
		return nil, err
	}
	writer, ok := storage.(StreamingAssetStorage)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("CLIENT_RELEASE_STORAGE_UNAVAILABLE", "Configured storage does not support streamed package uploads")
	}
	if _, err = writer.SaveStream(ctx, key, contentType, source, size); err != nil {
		return nil, err
	}
	return s.snapshot(policy)
}

func (s *DoneBotStorageService) OpenReleaseObject(ctx context.Context, key string, storageConfig json.RawMessage) (io.ReadCloser, error) {
	var policy DoneBotStoragePolicy
	if err := json.Unmarshal(storageConfig, &policy); err != nil {
		return nil, err
	}
	if policy.Driver == "s3" && policy.S3.SecretAccessKey != "" {
		if s.encryptor == nil {
			return nil, ErrSecretEncryptionKeyNotConfigured
		}
		secret, err := s.decryptSecret(policy.S3.SecretAccessKey)
		if err != nil {
			return nil, err
		}
		policy.S3.SecretAccessKey = secret
	}
	storage, err := s.storage(ctx, policy)
	if err != nil {
		return nil, err
	}
	reader, ok := storage.(AssetReader)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("CLIENT_RELEASE_STORAGE_UNAVAILABLE", "Configured storage does not support package downloads")
	}
	return reader.Open(ctx, key)
}

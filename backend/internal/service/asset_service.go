package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

var (
	ErrAssetNotFound           = infraerrors.NotFound("ASSET_NOT_FOUND", "asset not found")
	ErrAssetTooLarge           = infraerrors.New(http.StatusRequestEntityTooLarge, "ASSET_TOO_LARGE", "asset exceeds upload limit")
	ErrAssetTokenInvalid       = infraerrors.Forbidden("ASSET_TOKEN_INVALID", "asset download token is invalid or expired")
	ErrAssetStorageUnavailable = infraerrors.ServiceUnavailable("ASSET_STORAGE_UNAVAILABLE", "asset storage driver is unavailable")
)

// GatewayAsset is separate from the release updater's Asset type.
type GatewayAsset struct {
	ID            string          `json:"id"`
	SHA256        string          `json:"sha256"`
	MIME          string          `json:"mime"`
	Size          int64           `json:"size"`
	Storage       string          `json:"-"`
	Purpose       string          `json:"-"`
	StorageConfig json.RawMessage `json:"-"`
	ObjectKey     string          `json:"-"`
	URL           string          `json:"url"`
	OwnerKeyID    int64           `json:"-"`
	CreatedAt     time.Time       `json:"created_at"`
	ExpiresAt     time.Time       `json:"expires_at"`
	Deduplicated  bool            `json:"deduplicated"`
}
type AssetRepository interface {
	WithHashLock(context.Context, string, func(context.Context) error) error
	GetByID(context.Context, string) (*GatewayAsset, error)
	GetBySHA256(context.Context, string) (*GatewayAsset, error)
	Create(context.Context, *GatewayAsset) error
	AddOwner(context.Context, string, int64) error
	HasOwner(context.Context, string, int64) (bool, error)
	RemoveOwner(context.Context, string, int64) (bool, error)
	Delete(context.Context, string) error
	ListExpired(context.Context, time.Time, int) ([]GatewayAsset, error)
}
type assetCleanupContextKey struct{}

type AssetService struct {
	storageSettings *DoneBotStorageService
	repo            AssetRepository
	storage         AssetStorage
	cfg             *config.Config
	now             func() time.Time
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	start           sync.Once
	stop            sync.Once
}

func NewAssetService(repo AssetRepository, storage AssetStorage, cfg *config.Config) *AssetService {
	return &AssetService{repo: repo, storage: storage, cfg: cfg, now: time.Now}
}
func (s *AssetService) Upload(ctx context.Context, owner int64, filename, contentType string, data []byte, baseURL string) (*GatewayAsset, error) {
	return s.upload(ctx, AssetPurposeUpload, owner, filename, contentType, data, baseURL)
}
func (s *AssetService) UploadGenerated(ctx context.Context, owner int64, filename, contentType string, data []byte, baseURL string) (*GatewayAsset, error) {
	return s.upload(ctx, AssetPurposeGenerated, owner, filename, contentType, data, baseURL)
}
func (s *AssetService) GeneratedStoragePolicy(ctx context.Context) (DoneBotStoragePolicy, error) {
	if s.storageSettings != nil {
		return s.storageSettings.Policy(ctx, AssetPurposeGenerated)
	}
	if s.cfg == nil {
		return DoneBotStoragePolicy{}, ErrAssetStorageUnavailable
	}
	return DoneBotStoragePolicy{Enabled: s.cfg.ImageStorage.Enabled, Driver: s.cfg.AssetStorage.Driver, PublicBaseURL: s.cfg.AssetStorage.PublicBaseURL, MaxFileBytes: s.cfg.ImageStorage.MaxDownloadByte}, nil
}
func (s *AssetService) UploadLimit(ctx context.Context) (int64, error) {
	if s.storageSettings != nil {
		p, e := s.storageSettings.Policy(ctx, AssetPurposeUpload)
		return p.MaxFileBytes, e
	}
	return s.cfg.AssetStorage.MaxBytes, nil
}
func (s *AssetService) upload(ctx context.Context, purpose string, owner int64, filename, contentType string, data []byte, baseURL string) (*GatewayAsset, error) {
	if s.storageSettings == nil {
		return s.uploadStored(ctx, purpose, owner, filename, contentType, data, baseURL, nil)
	}
	policy, err := s.storageSettings.Policy(ctx, purpose)
	if err != nil {
		return nil, err
	}
	if !policy.Enabled {
		return nil, ErrAssetStorageUnavailable
	}
	var out *GatewayAsset
	err = s.withPurposeLock(ctx, purpose, func(tx context.Context) error {
		var e error
		out, e = s.uploadStored(context.WithValue(tx, assetCleanupContextKey{}, tx), purpose, owner, filename, contentType, data, baseURL, &policy)
		return e
	})
	return out, err
}
func (s *AssetService) uploadStored(ctx context.Context, purpose string, owner int64, filename, contentType string, data []byte, baseURL string, policy *DoneBotStoragePolicy) (*GatewayAsset, error) {
	if s.cfg == nil || !s.cfg.DoneBot.Enabled {
		return nil, infraerrors.NotFound("ASSET_DISABLED", "assets are unavailable")
	}
	if owner <= 0 {
		return nil, ErrAssetNotFound
	}
	limit := s.cfg.AssetStorage.MaxBytes
	if policy != nil {
		limit = policy.MaxFileBytes
	}
	if limit <= 0 {
		limit = 32 << 20
	}
	if int64(len(data)) > limit {
		return nil, ErrAssetTooLarge
	}
	if len(data) == 0 {
		return nil, infraerrors.BadRequest("ASSET_EMPTY", "asset is empty")
	}
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	var out *GatewayAsset
	err := s.repo.WithHashLock(ctx, purpose+":"+digest, func(tx context.Context) error {
		var existing *GatewayAsset
		var err error
		if r, ok := s.repo.(AssetRetentionRepository); ok {
			existing, err = r.GetByPurposeSHA256(tx, purpose, digest)
		} else {
			existing, err = s.repo.GetBySHA256(tx, digest)
		}
		if err == nil && !existing.ExpiresAt.After(s.now()) {
			deleteCtx := tx
			if outer, _ := ctx.Value(assetCleanupContextKey{}).(context.Context); outer != nil {
				deleteCtx = outer
			}
			storage, storageErr := s.storageForAsset(deleteCtx, existing)
			if storageErr != nil {
				return storageErr
			}
			if err = storage.Delete(deleteCtx, existing.ObjectKey); err != nil {
				return err
			}
			if err = s.repo.Delete(deleteCtx, existing.ID); err != nil {
				return err
			}
			existing = nil
			err = ErrAssetNotFound
		}
		if err == nil {
			if _, storageErr := s.storageForAsset(tx, existing); storageErr != nil {
				return storageErr
			}
			if err = s.repo.AddOwner(tx, existing.ID, owner); err != nil {
				return err
			}
			copy := *existing
			copy.Deduplicated = true
			out = &copy
			return nil
		}
		if !errors.Is(err, ErrAssetNotFound) {
			return err
		}
		if parsed, _, e := mime.ParseMediaType(contentType); e == nil {
			contentType = parsed
		} else {
			contentType = http.DetectContentType(data)
		}
		// The extension comes from verified MIME, never from a user supplied path.
		ext := ""
		switch contentType {
		case "image/png":
			ext = ".png"
		case "image/jpeg":
			ext = ".jpg"
		case "image/webp":
			ext = ".webp"
		case "image/gif":
			ext = ".gif"
		case "application/pdf":
			ext = ".pdf"
		case "text/plain":
			ext = ".txt"
		}
		objectKey := path.Join(purpose, digest[:2], digest+ext)
		if policy != nil && policy.Driver == "s3" {
			objectKey = path.Join(policy.S3.Prefix, objectKey)
		}
		storage := s.storage
		var snapshot json.RawMessage
		if policy != nil {
			cleanupCtx, _ := ctx.Value(assetCleanupContextKey{}).(context.Context)
			if cleanupCtx == nil {
				cleanupCtx = ctx
			}
			if err = s.ensureStorageCapacity(cleanupCtx, purpose, *policy, int64(len(data))); err != nil {
				return err
			}
			storage, err = s.storageSettings.storage(tx, *policy)
			if err != nil {
				return err
			}
			snapshot, err = s.storageSettings.snapshot(*policy)
			if err != nil {
				return err
			}
		}
		storedURL, err := storage.Save(tx, objectKey, contentType, data)
		if err != nil {
			return err
		}
		ttl := s.cfg.AssetStorage.TTLHours
		if policy != nil {
			ttl = policy.TTLHours
		}
		if ttl <= 0 {
			ttl = 168
		}
		now := s.now().UTC()
		out = &GatewayAsset{ID: "ast_" + strings.ReplaceAll(uuid.NewString(), "-", ""), SHA256: digest, MIME: contentType, Size: int64(len(data)), Storage: storage.Kind(), Purpose: purpose, StorageConfig: snapshot, ObjectKey: objectKey, URL: storedURL, OwnerKeyID: owner, CreatedAt: now, ExpiresAt: now.Add(time.Duration(ttl) * time.Hour)}
		if err = s.repo.Create(tx, out); err != nil {
			_ = storage.Delete(context.WithoutCancel(tx), objectKey)
			return err
		}
		if err = s.repo.AddOwner(tx, out.ID, owner); err != nil {
			_ = s.repo.Delete(context.WithoutCancel(tx), out.ID)
			_ = storage.Delete(context.WithoutCancel(tx), objectKey)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.withURL(out, baseURL), nil
}

// Save lets async image results reuse asset retention and signed local URLs.
func (s *AssetService) Save(ctx context.Context, key, contentType string, data []byte) (string, error) {
	owner, _ := ctx.Value(assetOwnerKey{}).(int64)
	asset, err := s.UploadGenerated(ctx, owner, key, contentType, data, "")
	if err != nil {
		return "", err
	}
	return asset.URL, nil
}
func (s *AssetService) Get(ctx context.Context, owner int64, id, baseURL string) (*GatewayAsset, error) {
	a, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !a.ExpiresAt.After(s.now()) {
		return nil, ErrAssetNotFound
	}
	ok, err := s.repo.HasOwner(ctx, id, owner)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrAssetNotFound
	}
	return s.withURL(a, baseURL), nil
}
func (s *AssetService) Delete(ctx context.Context, owner int64, id string) error {
	a, err := s.Get(ctx, owner, id, "")
	if err != nil {
		return err
	}
	return s.withAssetLock(ctx, a.Purpose, a.SHA256, func(tx context.Context) error {
		empty, err := s.repo.RemoveOwner(tx, id, owner)
		if err != nil {
			return err
		}
		if !empty {
			return nil
		}
		storage, storageErr := s.storageForAsset(tx, a)
		if storageErr != nil {
			return errors.Join(storageErr, s.repo.AddOwner(context.WithoutCancel(tx), id, owner))
		}
		if err = storage.Delete(tx, a.ObjectKey); err != nil {
			return errors.Join(err, s.repo.AddOwner(context.WithoutCancel(tx), id, owner))
		}
		if err = s.repo.Delete(tx, id); err != nil {
			return errors.Join(err, s.repo.AddOwner(context.WithoutCancel(tx), id, owner))
		}
		return nil
	})
}
func (s *AssetService) withURL(asset *GatewayAsset, baseURL string) *GatewayAsset {
	a := *asset
	if a.Storage == "local" {
		base := strings.TrimRight(s.cfg.AssetStorage.PublicBaseURL, "/")
		if len(a.StorageConfig) > 0 {
			var policy DoneBotStoragePolicy
			if json.Unmarshal(a.StorageConfig, &policy) == nil {
				base = strings.TrimRight(policy.PublicBaseURL, "/")
			}
		}
		if base == "" {
			base = strings.TrimRight(baseURL, "/")
		}
		a.URL = base + "/assets/" + a.ID + "?token=" + url.QueryEscape(s.sign(a.ID, a.ExpiresAt))
	}
	return &a
}
func (s *AssetService) sign(id string, expires time.Time) string {
	exp := strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(s.cfg.JWT.Secret))
	_, _ = mac.Write([]byte(id + "\n" + exp))
	return exp + "." + hex.EncodeToString(mac.Sum(nil))
}
func (s *AssetService) verify(id, token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || s.cfg == nil || s.cfg.JWT.Secret == "" {
		return ErrAssetTokenInvalid
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || exp <= s.now().Unix() {
		return ErrAssetTokenInvalid
	}
	expected := s.sign(id, time.Unix(exp, 0))
	if !hmac.Equal([]byte(expected), []byte(token)) {
		return ErrAssetTokenInvalid
	}
	return nil
}
func (s *AssetService) Download(ctx context.Context, id, token string) (*GatewayAsset, io.ReadCloser, error) {
	if err := s.verify(id, token); err != nil {
		return nil, nil, err
	}
	a, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if !a.ExpiresAt.After(s.now()) || a.Storage != "local" {
		return nil, nil, ErrAssetTokenInvalid
	}
	storage, err := s.storageForAsset(ctx, a)
	if err != nil {
		return nil, nil, err
	}
	reader, ok := storage.(LocalAssetReader)
	if !ok {
		return nil, nil, fmt.Errorf("local asset reader unavailable")
	}
	body, err := reader.Open(ctx, a.ObjectKey)
	return a, body, err
}
func (s *AssetService) CleanupExpired(ctx context.Context, limit int) (int, error) {
	expired, err := s.repo.ListExpired(ctx, s.now(), limit)
	if err != nil {
		return 0, err
	}
	count := 0
	var failures []error
	for _, a := range expired {
		deleted := false
		err = s.withAssetLock(ctx, a.Purpose, a.SHA256, func(tx context.Context) error {
			current, err := s.repo.GetByID(tx, a.ID)
			if errors.Is(err, ErrAssetNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if current.ExpiresAt.After(s.now()) {
				return nil
			}
			storage, storageErr := s.storageForAsset(tx, current)
			if storageErr != nil {
				return storageErr
			}
			if err = storage.Delete(tx, current.ObjectKey); err != nil {
				return err
			}
			if err = s.repo.Delete(tx, current.ID); err == nil {
				deleted = true
			}
			return err
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("cleanup asset %s: %w", a.ID, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if deleted {
			count++
		}
	}
	return count, errors.Join(failures...)
}

func (s *AssetService) storageFor(kind string) (AssetStorage, error) {
	if kind == s.storage.Kind() {
		return s.storage, nil
	}
	if resolver, ok := s.storage.(AssetStorageResolver); ok {
		if storage, found := resolver.ForKind(kind); found {
			return storage, nil
		}
	}
	return nil, ErrAssetStorageUnavailable
}
func (s *AssetService) Start() {
	s.start.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				if _, err := s.CleanupExpired(ctx, 1000); err != nil && ctx.Err() == nil {
					slog.Warn("asset cleanup will retry", "error", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}
func (s *AssetService) Stop() {
	s.stop.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.wg.Wait()
	})
}

func (s *AssetService) storageForAsset(ctx context.Context, a *GatewayAsset) (AssetStorage, error) {
	if len(a.StorageConfig) > 0 && s.storageSettings != nil {
		return s.storageSettings.originalStorage(ctx, a)
	}
	return s.storageFor(a.Storage)
}
func (s *AssetService) ensureStorageCapacity(ctx context.Context, purpose string, p DoneBotStoragePolicy, incoming int64) error {
	repo, ok := s.repo.(AssetRetentionRepository)
	if !ok {
		return ErrAssetStorageUnavailable
	}
	// Divide before multiplying, keeping arithmetic safe for user supplied capacities.
	threshold := (p.MaxStorageBytes/100)*int64(p.CleanupThresholdPercent) + (p.MaxStorageBytes%100)*int64(p.CleanupThresholdPercent)/100
	if incoming > threshold {
		return infraerrors.New(http.StatusInsufficientStorage, "STORAGE_CAPACITY_EXCEEDED", "file exceeds storage cleanup threshold")
	}
	used, _, err := repo.StorageUsage(ctx, purpose)
	if err != nil {
		return err
	}
	for used > threshold-incoming {
		oldest, err := repo.ListOldest(ctx, purpose, 100)
		if err != nil {
			return err
		}
		if len(oldest) == 0 {
			return ErrAssetStorageUnavailable
		}
		for _, a := range oldest {
			if used <= threshold-incoming {
				break
			}
			storage, e := s.storageForAsset(ctx, &a)
			if e != nil {
				return e
			}
			if e = storage.Delete(ctx, a.ObjectKey); e != nil {
				return e
			}
			if e = s.repo.Delete(ctx, a.ID); e != nil {
				return e
			}
			used -= a.Size
		}
	}
	return nil
}

func (s *AssetService) withAssetLock(ctx context.Context, purpose, hash string, fn func(context.Context) error) error {
	if purpose == "" {
		purpose = AssetPurposeUpload
	}
	if s.storageSettings == nil {
		return s.repo.WithHashLock(ctx, purpose+":"+hash, fn)
	}
	return s.withPurposeLock(ctx, purpose, func(tx context.Context) error { return s.repo.WithHashLock(tx, purpose+":"+hash, fn) })
}

func (s *AssetService) withPurposeLock(ctx context.Context, purpose string, fn func(context.Context) error) error {
	if locker, ok := s.repo.(interface {
		WithPurposeLock(context.Context, string, func(context.Context) error) error
	}); ok {
		return locker.WithPurposeLock(ctx, purpose, fn)
	}
	return s.repo.WithHashLock(ctx, "policy:"+purpose, fn)
}

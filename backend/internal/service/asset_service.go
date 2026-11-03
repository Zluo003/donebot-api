package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	ID           string    `json:"id"`
	SHA256       string    `json:"sha256"`
	MIME         string    `json:"mime"`
	Size         int64     `json:"size"`
	Storage      string    `json:"-"`
	ObjectKey    string    `json:"-"`
	URL          string    `json:"url"`
	OwnerKeyID   int64     `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Deduplicated bool      `json:"deduplicated"`
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
type AssetService struct {
	repo    AssetRepository
	storage AssetStorage
	cfg     *config.Config
	now     func() time.Time
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	start   sync.Once
	stop    sync.Once
}

func NewAssetService(repo AssetRepository, storage AssetStorage, cfg *config.Config) *AssetService {
	return &AssetService{repo: repo, storage: storage, cfg: cfg, now: time.Now}
}
func (s *AssetService) Upload(ctx context.Context, owner int64, filename, contentType string, data []byte, baseURL string) (*GatewayAsset, error) {
	if s.cfg == nil || !s.cfg.DoneBot.Enabled {
		return nil, infraerrors.NotFound("ASSET_DISABLED", "assets are unavailable")
	}
	if owner <= 0 {
		return nil, ErrAssetNotFound
	}
	limit := s.cfg.AssetStorage.MaxBytes
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
	err := s.repo.WithHashLock(ctx, digest, func(tx context.Context) error {
		existing, err := s.repo.GetBySHA256(tx, digest)
		if err == nil && !existing.ExpiresAt.After(s.now()) {
			storage, storageErr := s.storageFor(existing.Storage)
			if storageErr != nil {
				return storageErr
			}
			if err = storage.Delete(tx, existing.ObjectKey); err != nil {
				return err
			}
			if err = s.repo.Delete(tx, existing.ID); err != nil {
				return err
			}
			existing = nil
			err = ErrAssetNotFound
		}
		if err == nil {
			if _, storageErr := s.storageFor(existing.Storage); storageErr != nil {
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
		objectKey := path.Join(digest[:2], digest+ext)
		storedURL, err := s.storage.Save(tx, objectKey, contentType, data)
		if err != nil {
			return err
		}
		ttl := s.cfg.AssetStorage.TTLHours
		if ttl <= 0 {
			ttl = 168
		}
		now := s.now().UTC()
		out = &GatewayAsset{ID: "ast_" + strings.ReplaceAll(uuid.NewString(), "-", ""), SHA256: digest, MIME: contentType, Size: int64(len(data)), Storage: s.storage.Kind(), ObjectKey: objectKey, URL: storedURL, OwnerKeyID: owner, CreatedAt: now, ExpiresAt: now.Add(time.Duration(ttl) * time.Hour)}
		if err = s.repo.Create(tx, out); err != nil {
			return err
		}
		return s.repo.AddOwner(tx, out.ID, owner)
	})
	if err != nil {
		return nil, err
	}
	return s.withURL(out, baseURL), nil
}

// Save lets async image results reuse asset retention and signed local URLs.
func (s *AssetService) Save(ctx context.Context, key, contentType string, data []byte) (string, error) {
	owner, _ := ctx.Value(assetOwnerKey{}).(int64)
	asset, err := s.Upload(ctx, owner, key, contentType, data, "")
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
	return s.repo.WithHashLock(ctx, a.SHA256, func(tx context.Context) error {
		empty, err := s.repo.RemoveOwner(tx, id, owner)
		if err != nil {
			return err
		}
		if !empty {
			return nil
		}
		storage, storageErr := s.storageFor(a.Storage)
		if storageErr != nil {
			return storageErr
		}
		if err = storage.Delete(tx, a.ObjectKey); err != nil {
			return err
		}
		return s.repo.Delete(tx, id)
	})
}
func (s *AssetService) withURL(asset *GatewayAsset, baseURL string) *GatewayAsset {
	a := *asset
	if a.Storage == "local" {
		base := strings.TrimRight(s.cfg.AssetStorage.PublicBaseURL, "/")
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
	storage, err := s.storageFor(a.Storage)
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
		err = s.repo.WithHashLock(ctx, a.SHA256, func(tx context.Context) error {
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
			storage, storageErr := s.storageFor(current.Storage)
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
			ticker := time.NewTicker(time.Hour)
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

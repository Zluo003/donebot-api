package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const settingKeyDoneBotStorage = "donebot_storage_policies"
const AssetPurposeUpload = "upload"
const AssetPurposeGenerated = "generated"

type DoneBotStorageS3 struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	ForcePathStyle  bool   `json:"force_path_style"`
}
type DoneBotStoragePolicy struct {
	Enabled                 bool             `json:"enabled"`
	Driver                  string           `json:"driver"`
	LocalPath               string           `json:"local_path"`
	PublicBaseURL           string           `json:"public_base_url"`
	MaxStorageBytes         int64            `json:"max_storage_bytes"`
	TTLHours                int              `json:"ttl_hours"`
	CleanupThresholdPercent int              `json:"cleanup_threshold_percent"`
	MaxFileBytes            int64            `json:"max_file_bytes"`
	S3                      DoneBotStorageS3 `json:"s3"`
	SecretConfigured        bool             `json:"secret_configured"`
	UsageBytes              int64            `json:"usage_bytes"`
	FileCount               int64            `json:"file_count"`
}
type DoneBotStoragePolicies struct {
	Upload    DoneBotStoragePolicy `json:"upload"`
	Generated DoneBotStoragePolicy `json:"generated"`
}
type DoneBotStorageFactory func(context.Context, DoneBotStoragePolicy) (AssetStorage, error)
type AssetRetentionRepository interface {
	GetByPurposeSHA256(context.Context, string, string) (*GatewayAsset, error)
	ListOldest(context.Context, string, int) ([]GatewayAsset, error)
	StorageUsage(context.Context, string) (int64, int64, error)
}

// Each asset stores its encrypted original policy so switching buckets or local
// directories never redirects reads and deletes of retained objects.
type DoneBotStorageService struct {
	repo      SettingRepository
	encryptor SecretEncryptor
	factory   DoneBotStorageFactory
	cfg       *config.Config
	assets    AssetRepository
	mu        sync.Mutex
}

func NewDoneBotStorageService(repo SettingRepository, encryptor SecretEncryptor, factory DoneBotStorageFactory, cfg *config.Config, assets AssetRepository) *DoneBotStorageService {
	return &DoneBotStorageService{repo: repo, encryptor: encryptor, factory: factory, cfg: cfg, assets: assets}
}
func (s *DoneBotStorageService) defaults() DoneBotStoragePolicies {
	p := DoneBotStoragePolicy{Enabled: true, Driver: "local", LocalPath: "data/assets", MaxStorageBytes: 10 << 30, TTLHours: 168, CleanupThresholdPercent: 90, MaxFileBytes: 32 << 20, S3: DoneBotStorageS3{Region: "auto", Prefix: "uploads/"}}
	if s.cfg != nil {
		c := s.cfg.AssetStorage
		if c.Driver != "" {
			p.Driver = c.Driver
		}
		if c.LocalPath != "" {
			p.LocalPath = c.LocalPath
		}
		p.PublicBaseURL = c.PublicBaseURL
		if c.TTLHours > 0 {
			p.TTLHours = c.TTLHours
		}
		if c.MaxBytes > 0 {
			p.MaxFileBytes = c.MaxBytes
		}
		p.S3 = DoneBotStorageS3{Endpoint: c.S3.Endpoint, Region: c.S3.Region, Bucket: c.S3.Bucket, Prefix: c.S3.Prefix, AccessKeyID: c.S3.AccessKeyID, SecretAccessKey: c.S3.SecretAccessKey, ForcePathStyle: c.S3.ForcePathStyle}
	}
	g := p
	g.MaxStorageBytes = 50 << 30
	g.MaxFileBytes = 256 << 20
	g.LocalPath = filepath.Join(p.LocalPath, "generated")
	g.S3.Prefix = "generated/"
	g.Enabled = s.cfg != nil && s.cfg.ImageStorage.Enabled
	if s.cfg != nil && s.cfg.ImageStorage.Enabled && s.cfg.ImageStorage.Driver != "local" && s.cfg.ImageStorage.IsConfigured() {
		c := s.cfg.ImageStorage
		g.Driver = "s3"
		g.PublicBaseURL = c.PublicBaseURL
		g.S3 = DoneBotStorageS3{Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, Prefix: c.Prefix, AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, ForcePathStyle: c.ForcePathStyle}
	}

	return DoneBotStoragePolicies{Upload: p, Generated: g}
}
func (s *DoneBotStorageService) load(ctx context.Context) (DoneBotStoragePolicies, error) {
	p := s.defaults()
	if s.repo == nil {
		return p, nil
	}
	raw, err := s.repo.GetValue(ctx, settingKeyDoneBotStorage)
	if errors.Is(err, ErrSettingNotFound) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if strings.TrimSpace(raw) == "" {
		return p, nil
	}
	err = json.Unmarshal([]byte(raw), &p)
	return p, err
}
func (s *DoneBotStorageService) Policy(ctx context.Context, purpose string) (DoneBotStoragePolicy, error) {
	p, err := s.load(ctx)
	if err != nil {
		return DoneBotStoragePolicy{}, err
	}
	out := p.Upload
	if purpose == AssetPurposeGenerated {
		out = p.Generated
	} else if purpose != AssetPurposeUpload {
		return out, fmt.Errorf("invalid storage purpose")
	}
	if out.S3.SecretAccessKey != "" && s.encryptor != nil {
		if v, e := s.decryptSecret(out.S3.SecretAccessKey); e == nil {
			out.S3.SecretAccessKey = v
		} else {
			return out, e
		}
	}
	return out, nil
}
func (s *DoneBotStorageService) Get(ctx context.Context) (*DoneBotStoragePolicies, error) {
	p, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	for purpose, policy := range map[string]*DoneBotStoragePolicy{AssetPurposeUpload: &p.Upload, AssetPurposeGenerated: &p.Generated} {
		policy.SecretConfigured = policy.S3.SecretAccessKey != ""
		policy.S3.SecretAccessKey = ""
		if repo, ok := s.assets.(AssetRetentionRepository); ok {
			policy.UsageBytes, policy.FileCount, err = repo.StorageUsage(ctx, purpose)
			if err != nil {
				return nil, err
			}
		}
	}
	return &p, nil
}
func normalizeDoneBotStoragePolicy(p *DoneBotStoragePolicy, purpose string) error {
	p.Driver = strings.ToLower(strings.TrimSpace(p.Driver))
	p.LocalPath = strings.TrimSpace(p.LocalPath)
	p.PublicBaseURL = strings.TrimRight(strings.TrimSpace(p.PublicBaseURL), "/")
	p.S3.Bucket = strings.TrimSpace(p.S3.Bucket)
	p.S3.Endpoint = strings.TrimSpace(p.S3.Endpoint)
	p.S3.Region = strings.TrimSpace(p.S3.Region)
	p.S3.Prefix = strings.Trim(strings.TrimSpace(p.S3.Prefix), "/")
	if p.S3.Region == "" {
		p.S3.Region = "auto"
	}
	if p.S3.Prefix == "" {
		p.S3.Prefix = purpose
	}
	p.S3.Prefix += "/"
	if p.Driver != "local" && p.Driver != "s3" {
		return fmt.Errorf("driver must be local or s3")
	}
	if p.MaxStorageBytes <= 0 || p.MaxFileBytes <= 0 || p.MaxFileBytes > int64(^uint64(0)>>1)-(1<<20) {
		return fmt.Errorf("capacity and max_file_bytes must be positive")
	}
	if p.TTLHours < 1 || p.TTLHours > 8760 {
		return fmt.Errorf("ttl_hours must be between 1 and 8760")
	}
	if p.CleanupThresholdPercent < 1 || p.CleanupThresholdPercent > 100 {
		return fmt.Errorf("cleanup_threshold_percent must be between 1 and 100")
	}
	if p.Driver == "local" && p.LocalPath == "" {
		return fmt.Errorf("local_path is required")
	}
	if p.PublicBaseURL != "" {
		u, e := url.Parse(p.PublicBaseURL)
		if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("public_base_url must be an absolute http(s) URL")
		}
	}
	if p.Enabled && p.Driver == "local" && purpose == AssetPurposeGenerated && p.PublicBaseURL == "" {
		return fmt.Errorf("generated local storage requires public_base_url")
	}
	if p.Enabled && p.Driver == "s3" && p.TTLHours > 168 && p.PublicBaseURL == "" {
		return fmt.Errorf("S3 ttl_hours cannot exceed 168 without public_base_url")
	}
	if p.Enabled && p.Driver == "s3" && (p.S3.Bucket == "" || p.S3.AccessKeyID == "" || p.S3.SecretAccessKey == "") {
		return fmt.Errorf("S3 bucket, access key, and secret are required")
	}
	return nil
}
func (s *DoneBotStorageService) Update(ctx context.Context, in DoneBotStoragePolicies) (*DoneBotStoragePolicies, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	for purpose, pol := range map[string]*DoneBotStoragePolicy{AssetPurposeUpload: &in.Upload, AssetPurposeGenerated: &in.Generated} {
		prev := old.Upload
		if purpose == AssetPurposeGenerated {
			prev = old.Generated
		}
		suppliedSecret := pol.S3.SecretAccessKey != "" && strings.Trim(pol.S3.SecretAccessKey, "*") != ""
		if !suppliedSecret {
			pol.S3.SecretAccessKey = prev.S3.SecretAccessKey
		}
		if pol.S3.SecretAccessKey != "" && (suppliedSecret || !strings.HasPrefix(pol.S3.SecretAccessKey, "sealed:")) {
			if s.encryptor == nil || s.cfg != nil && !s.cfg.Totp.EncryptionKeyConfigured {
				return nil, ErrSecretEncryptionKeyNotConfigured
			}
			pol.S3.SecretAccessKey, err = s.encryptSecret(pol.S3.SecretAccessKey)
			if err != nil {
				return nil, err
			}
		}
		if err = normalizeDoneBotStoragePolicy(pol, purpose); err != nil {
			return nil, infraerrors.BadRequest("INVALID_STORAGE_POLICY", purpose+": "+err.Error())
		}
		pol.SecretConfigured = false
		pol.UsageBytes = 0
		pol.FileCount = 0
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if err = s.repo.Set(ctx, settingKeyDoneBotStorage, string(raw)); err != nil {
		return nil, err
	}
	if retention, ok := s.assets.(interface {
		ApplyStorageRetention(context.Context, string, int) error
	}); ok {
		if err = retention.ApplyStorageRetention(ctx, AssetPurposeUpload, in.Upload.TTLHours); err != nil {
			return nil, err
		}
		if err = retention.ApplyStorageRetention(ctx, AssetPurposeGenerated, in.Generated.TTLHours); err != nil {
			return nil, err
		}
	}

	return s.Get(ctx)
}
func (s *DoneBotStorageService) snapshot(p DoneBotStoragePolicy) (json.RawMessage, error) {
	if p.Driver == "local" {
		p.S3 = DoneBotStorageS3{}
	}
	if p.S3.SecretAccessKey != "" {
		if s.encryptor == nil {
			return nil, ErrSecretEncryptionKeyNotConfigured
		}
		v, err := s.encryptSecret(p.S3.SecretAccessKey)
		if err != nil {
			return nil, err
		}
		p.S3.SecretAccessKey = v
	}
	return json.Marshal(p)
}
func (s *DoneBotStorageService) storage(ctx context.Context, p DoneBotStoragePolicy) (AssetStorage, error) {
	if s.factory == nil {
		return nil, ErrAssetStorageUnavailable
	}
	return s.factory(ctx, p)
}
func (s *DoneBotStorageService) originalStorage(ctx context.Context, a *GatewayAsset) (AssetStorage, error) {
	var p DoneBotStoragePolicy
	if err := json.Unmarshal(a.StorageConfig, &p); err != nil {
		return nil, err
	}
	if p.S3.SecretAccessKey != "" {
		if s.encryptor == nil {
			return nil, ErrSecretEncryptionKeyNotConfigured
		}
		v, e := s.decryptSecret(p.S3.SecretAccessKey)
		if e != nil {
			return nil, e
		}
		p.S3.SecretAccessKey = v
	}
	return s.storage(ctx, p)
}

func (s *DoneBotStorageService) encryptSecret(v string) (string, error) {
	raw, e := s.encryptor.Encrypt(v)
	return "sealed:" + raw, e
}
func (s *DoneBotStorageService) decryptSecret(v string) (string, error) {
	if raw, ok := strings.CutPrefix(v, "sealed:"); ok {
		return s.encryptor.Decrypt(raw)
	}
	return v, nil
}

package repository

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type LocalAssetStorage struct{ root string }

type assetStorageSet struct {
	service.AssetStorage
	drivers map[string]service.AssetStorage
}

func (s *assetStorageSet) ForKind(kind string) (service.AssetStorage, bool) {
	driver, ok := s.drivers[kind]
	return driver, ok
}
func (s *assetStorageSet) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.drivers["local"].(service.LocalAssetReader).Open(ctx, key)
}

func NewLocalAssetStorage(root string) *LocalAssetStorage { return &LocalAssetStorage{root: root} }
func (*LocalAssetStorage) Kind() string                   { return "local" }
func (s *LocalAssetStorage) file(key string) (string, error) {
	if !filepath.IsLocal(key) || strings.Contains(key, "\\") {
		return "", fmt.Errorf("invalid asset object key")
	}
	return filepath.Join(s.root, key), nil
}
func (s *LocalAssetStorage) Save(ctx context.Context, key, contentType string, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	target, err := s.file(key)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".asset-*")
	if err != nil {
		return "", err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(name, target); err != nil {
		return "", err
	}
	return "", nil
}
func (s *LocalAssetStorage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := s.file(key)
	if err != nil {
		return err
	}
	err = os.Remove(file)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
func (s *LocalAssetStorage) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := s.file(key)
	if err != nil {
		return nil, err
	}
	return os.Open(file)
}

func ProvideAssetStorage(cfg *config.Config) (service.AssetStorage, error) {
	if err := cfg.AssetStorage.Validate(); err != nil {
		return nil, err
	}
	drivers := map[string]service.AssetStorage{"local": NewLocalAssetStorage(cfg.AssetStorage.LocalPath)}
	if !cfg.AssetStorage.S3.IsConfigured() {
		return drivers["local"], nil
	}
	s3cfg := cfg.AssetStorage.S3
	s3cfg.PresignExpiry = cfg.AssetStorage.TTLHours
	if s3cfg.PresignExpiry > 168 {
		s3cfg.PresignExpiry = 168
	}
	if cfg.AssetStorage.PublicBaseURL != "" {
		s3cfg.PublicBaseURL = cfg.AssetStorage.PublicBaseURL
	}
	storage, err := NewS3ImageStorage(context.Background(), &s3cfg)
	if err != nil {
		return nil, err
	}
	drivers["s3"] = &S3AssetStorage{storage}
	return &assetStorageSet{AssetStorage: drivers[cfg.AssetStorage.Driver], drivers: drivers}, nil
}

package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func ProvideDoneBotStorageFactory() service.DoneBotStorageFactory {
	return func(ctx context.Context, p service.DoneBotStoragePolicy) (service.AssetStorage, error) {
		if p.Driver == "local" {
			return NewLocalAssetStorage(p.LocalPath), nil
		}
		expiry := p.TTLHours
		if expiry > 168 {
			expiry = 168
		}
		storage, err := NewS3ImageStorage(ctx, &config.ImageStorageConfig{Enabled: true, Endpoint: p.S3.Endpoint, Region: p.S3.Region, Bucket: p.S3.Bucket, Prefix: p.S3.Prefix, AccessKeyID: p.S3.AccessKeyID, SecretAccessKey: p.S3.SecretAccessKey, ForcePathStyle: p.S3.ForcePathStyle, PublicBaseURL: p.PublicBaseURL, PresignExpiry: expiry, MaxDownloadByte: p.MaxFileBytes})
		if err != nil {
			return nil, err
		}
		return &S3AssetStorage{storage}, nil
	}
}

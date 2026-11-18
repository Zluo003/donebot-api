package repository

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

type OSSAssetStorage struct {
	upload   *oss.Client
	download *oss.Client
	bucket   string
	expiry   time.Duration
}

var _ service.AssetStorage = (*OSSAssetStorage)(nil)
var _ service.StreamingAssetStorage = (*OSSAssetStorage)(nil)
var _ service.AssetReader = (*OSSAssetStorage)(nil)
var _ service.StorageConnectivityTester = (*OSSAssetStorage)(nil)

func NewOSSAssetStorage(p service.DoneBotStoragePolicy, options ...func(*oss.Config)) *OSSAssetStorage {
	cfg := p.OSS
	provider := credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey)
	newClient := func(endpoint string) *oss.Client {
		config := oss.LoadDefaultConfig().WithRegion(cfg.Region).WithEndpoint(endpoint).WithCredentialsProvider(provider)
		for _, option := range options {
			option(config)
		}
		return oss.NewClient(config)
	}
	expiry := time.Duration(p.TTLHours) * time.Hour
	if expiry <= 0 {
		expiry = 24 * time.Hour
	}
	if expiry > 168*time.Hour {
		expiry = 168 * time.Hour
	}
	return &OSSAssetStorage{upload: newClient(cfg.Endpoint), download: newClient(cfg.DownloadEndpoint), bucket: cfg.Bucket, expiry: expiry}
}

func (*OSSAssetStorage) Kind() string { return "oss" }
func (s *OSSAssetStorage) Save(ctx context.Context, key, contentType string, data []byte) (string, error) {
	return s.SaveStream(ctx, key, contentType, bytes.NewReader(data), int64(len(data)))
}
func (s *OSSAssetStorage) SaveStream(ctx context.Context, key, contentType string, body io.Reader, size int64) (string, error) {
	_, err := s.upload.PutObject(ctx, &oss.PutObjectRequest{Bucket: oss.Ptr(s.bucket), Key: oss.Ptr(key), Body: body, ContentType: oss.Ptr(contentType), ContentLength: oss.Ptr(size)})
	if err != nil {
		return "", fmt.Errorf("OSS PutObject: %w", err)
	}
	result, err := s.download.Presign(ctx, &oss.GetObjectRequest{Bucket: oss.Ptr(s.bucket), Key: oss.Ptr(key)}, func(o *oss.PresignOptions) { o.Expires = s.expiry })
	if err != nil {
		return "", fmt.Errorf("OSS presign: %w", err)
	}
	return result.URL, nil
}
func (s *OSSAssetStorage) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.download.GetObject(ctx, &oss.GetObjectRequest{Bucket: oss.Ptr(s.bucket), Key: oss.Ptr(key)})
	if err != nil {
		return nil, fmt.Errorf("OSS GetObject: %w", err)
	}
	if out.Body == nil {
		return nil, fmt.Errorf("OSS GetObject returned an empty body")
	}
	return out.Body, nil
}
func (s *OSSAssetStorage) Delete(ctx context.Context, key string) error {
	_, err := s.download.DeleteObject(ctx, &oss.DeleteObjectRequest{Bucket: oss.Ptr(s.bucket), Key: oss.Ptr(key)})
	if err != nil {
		return fmt.Errorf("OSS DeleteObject: %w", err)
	}
	return nil
}
func (s *OSSAssetStorage) TestConnection(ctx context.Context, prefix string) error {
	return service.ProbeObjectStorage(ctx, prefix, func(ctx context.Context, key string, data []byte) (string, error) {
		return s.Save(ctx, key, "text/plain", data)
	}, s.Open, s.Delete)
}

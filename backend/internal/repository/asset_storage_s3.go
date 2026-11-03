package repository

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3AssetStorage reuses the existing S3 client, upload and presign behavior.
type S3AssetStorage struct{ *S3ImageStorage }

func (*S3AssetStorage) Kind() string { return "s3" }
func (s *S3AssetStorage) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return fmt.Errorf("S3 DeleteObject: %w", err)
	}
	return nil
}

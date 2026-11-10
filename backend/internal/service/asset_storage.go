package service

import (
	"context"
	"io"
)

type AssetStorage interface {
	Save(context.Context, string, string, []byte) (string, error)
	Delete(context.Context, string) error
	Kind() string
}

type LocalAssetReader interface {
	Open(context.Context, string) (io.ReadCloser, error)
}

type AssetReader interface {
	Open(context.Context, string) (io.ReadCloser, error)
}

// AssetStorageResolver lets retained objects keep using their original driver
// after the operator changes the default storage for new uploads.
type AssetStorageResolver interface {
	ForKind(string) (AssetStorage, bool)
}

type assetOwnerKey struct{}

func WithAssetOwnerKeyID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, assetOwnerKey{}, id)
}

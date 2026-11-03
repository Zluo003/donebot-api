package repository

import (
	"context"
	"io"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAssetStorageSetOpenRejectsMissingOrNonLocalDriver(t *testing.T) {
	for _, drivers := range []map[string]service.AssetStorage{
		nil,
		{},
		{"local": nil},
		{"local": &S3AssetStorage{}},
	} {
		storage := &assetStorageSet{drivers: drivers}
		require.NotPanics(t, func() {
			reader, err := storage.Open(context.Background(), "ab/asset.png")
			require.Error(t, err)
			require.Nil(t, reader)
		})
	}
}

func TestLocalAssetStorageAtomicWriteReadDeleteAndPathIsolation(t *testing.T) {
	storage := NewLocalAssetStorage(t.TempDir())
	ctx := context.Background()
	_, err := storage.Save(ctx, "ab/abcdef.png", "image/png", []byte("png-data"))
	require.NoError(t, err)
	reader, err := storage.Open(ctx, "ab/abcdef.png")
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "png-data", string(data))
	_, err = storage.Save(ctx, "../outside", "text/plain", []byte("x"))
	require.Error(t, err)
	_, err = storage.Open(ctx, "/etc/passwd")
	require.Error(t, err)
	require.NoError(t, storage.Delete(ctx, "ab/abcdef.png"))
	_, err = storage.Open(ctx, "ab/abcdef.png")
	require.Error(t, err)
}

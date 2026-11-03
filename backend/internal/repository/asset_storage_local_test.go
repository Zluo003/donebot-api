package repository

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

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

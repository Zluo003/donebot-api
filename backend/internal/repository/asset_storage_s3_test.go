package repository

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestS3AssetStorageUsesExistingClientUploadPresignAndDelete(t *testing.T) {
	var mu sync.Mutex
	objects := map[string][]byte{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			data, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			objects[r.URL.Path] = data
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			_, _ = w.Write(objects[r.URL.Path])
		case http.MethodDelete:
			delete(objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer upstream.Close()
	cfg := &config.Config{AssetStorage: config.AssetStorageConfig{Driver: "s3", TTLHours: 24, S3: config.ImageStorageConfig{Endpoint: upstream.URL, Region: "auto", Bucket: "assets", AccessKeyID: "fixture-access", SecretAccessKey: "fixture-secret", ForcePathStyle: true}}}
	storage, err := ProvideAssetStorage(cfg)
	require.NoError(t, err)
	require.Equal(t, "s3", storage.Kind())
	downloadURL, err := storage.Save(context.Background(), "ab/ref.png", "image/png", []byte("test-image"))
	require.NoError(t, err)
	require.True(t, strings.Contains(downloadURL, "X-Amz-Signature="))
	response, err := http.Get(downloadURL)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, "test-image", string(body))
	require.NoError(t, storage.Delete(context.Background(), "ab/ref.png"))
	mu.Lock()
	require.Empty(t, objects)
	mu.Unlock()
}

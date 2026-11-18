//go:build unit

package repository

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/stretchr/testify/require"
)

func TestOSSStorageUploadsToAccelerationAndDownloadsDeletesThroughRegionalEndpoint(t *testing.T) {
	var mu sync.Mutex
	objects := map[string][]byte{}
	calls := []string{}
	const secret = "synthetic-oss-secret"
	handler := func(role string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			require.NotContains(t, r.URL.String(), secret)
			require.NotContains(t, r.Header.Get("Authorization"), secret)
			calls = append(calls, role+":"+r.Method)
			require.True(t, strings.HasPrefix(r.URL.Path, "/fixture-bucket/generated/"), r.URL.Path)
			switch r.Method {
			case "PUT":
				require.Equal(t, "upload", role)
				require.Contains(t, r.Header.Get("Authorization"), "OSS4-HMAC-SHA256")
				data, e := io.ReadAll(r.Body)
				require.NoError(t, e)
				objects[r.URL.Path] = data
				w.WriteHeader(200)
			case "GET":
				require.Equal(t, "regional", role)
				if r.URL.RawQuery != "" {
					require.NotEmpty(t, r.URL.Query().Get("x-oss-signature"))
				} else {
					require.Contains(t, r.Header.Get("Authorization"), "OSS4-HMAC-SHA256")
				}
				data, ok := objects[r.URL.Path]
				if !ok {
					w.WriteHeader(404)
					return
				}
				_, _ = w.Write(data)
			case "DELETE":
				require.Equal(t, "regional", role)
				delete(objects, r.URL.Path)
				w.WriteHeader(204)
			default:
				t.Errorf("unexpected method %s", r.Method)
				w.WriteHeader(405)
			}
		}
	}
	up := httptest.NewServer(handler("upload"))
	defer up.Close()
	down := httptest.NewServer(handler("regional"))
	defer down.Close()
	storage := NewOSSAssetStorage(service.DoneBotStoragePolicy{Driver: "oss", TTLHours: 4, OSS: service.DoneBotStorageS3{Endpoint: up.URL, DownloadEndpoint: down.URL, Region: "cn-hangzhou", Bucket: "fixture-bucket", AccessKeyID: "synthetic-oss-key", SecretAccessKey: secret}}, func(c *oss.Config) { c.WithUsePathStyle(true).WithRetryMaxAttempts(1) })
	require.Equal(t, "oss", storage.Kind())
	require.NoError(t, storage.TestConnection(context.Background(), "generated/"))
	mu.Lock()
	defer mu.Unlock()
	require.Empty(t, objects)
	require.Equal(t, []string{"upload:PUT", "regional:GET", "regional:GET", "regional:DELETE"}, calls)
}

func TestOSSPrivateDownloadSignatureUsesRealRegionalHostWithoutLeakingSecret(t *testing.T) {
	storage := NewOSSAssetStorage(service.DoneBotStoragePolicy{Driver: "oss", TTLHours: 4, OSS: service.DoneBotStorageS3{Endpoint: "https://oss-accelerate.aliyuncs.com", DownloadEndpoint: "https://oss-cn-hangzhou.aliyuncs.com", Region: "cn-hangzhou", Bucket: "fixture-bucket", AccessKeyID: "synthetic-key", SecretAccessKey: "synthetic-secret"}})
	result, err := storage.download.Presign(context.Background(), &oss.GetObjectRequest{Bucket: oss.Ptr(storage.bucket), Key: oss.Ptr("generated/a b.png")}, func(o *oss.PresignOptions) { o.Expires = storage.expiry })
	require.NoError(t, err)
	u, err := url.Parse(result.URL)
	require.NoError(t, err)
	require.Equal(t, "fixture-bucket.oss-cn-hangzhou.aliyuncs.com", u.Host)
	require.Equal(t, "/generated/a b.png", u.Path)
	require.Equal(t, "14400", u.Query().Get("x-oss-expires"))
	require.NotEmpty(t, u.Query().Get("x-oss-signature"))
	require.NotContains(t, result.URL, "oss-accelerate")
	require.NotContains(t, result.URL, "synthetic-secret")
	require.Equal(t, 4*time.Hour, storage.expiry)
}

func TestS3StorageConnectivityUsesActualSDKAndChecksDownloadLink(t *testing.T) {
	var mu sync.Mutex
	objects := map[string][]byte{}
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method)
		require.True(t, strings.HasPrefix(r.URL.Path, "/fixture-bucket/generated/.donebot-connectivity/"))
		switch r.Method {
		case "PUT":
			data, e := io.ReadAll(r.Body)
			require.NoError(t, e)
			objects[r.URL.Path] = data
			w.WriteHeader(200)
		case "GET":
			require.NotEmpty(t, objects[r.URL.Path])
			_, _ = w.Write(objects[r.URL.Path])
		case "DELETE":
			delete(objects, r.URL.Path)
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	}))
	defer server.Close()
	storage, err := NewS3ImageStorage(context.Background(), &config.ImageStorageConfig{Endpoint: server.URL, Region: "auto", Bucket: "fixture-bucket", AccessKeyID: "synthetic-key", SecretAccessKey: "synthetic-secret", ForcePathStyle: true, PresignExpiry: 4})
	require.NoError(t, err)
	require.NoError(t, storage.TestConnection(context.Background(), "generated/"))
	mu.Lock()
	defer mu.Unlock()
	require.Empty(t, objects)
	require.Equal(t, []string{"PUT", "GET", "GET", "DELETE"}, calls)
}

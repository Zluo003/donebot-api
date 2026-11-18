//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/require"
)

func TestProbeObjectStorageReadsDownloadsAndAlwaysCleansItsUniqueObject(t *testing.T) {
	for _, stage := range []string{"success", "write", "read", "nil body", "mismatch", "download", "http", "redirect", "delete", "cancel", "missing bucket", "oss denied"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			const sensitive = "secret-access-key-MUST-NOT-LEAK"
			var key string
			var payload []byte
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Empty(t, r.Header.Get("Authorization"))
				require.Equal(t, "/download", r.URL.Path)
				require.Equal(t, "synthetic-signature", r.URL.Query().Get("Signature"))
				switch stage {
				case "http":
					w.WriteHeader(403)
					return
				case "redirect":
					w.Header().Set("Location", "/other")
					w.WriteHeader(302)
					return
				case "download":
					_, _ = w.Write([]byte("wrong bucket"))
					return
				}
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			err := ProbeObjectStorage(ctx, "/fixture/generated/", func(ctx context.Context, k string, data []byte) (string, error) {
				calls = append(calls, "write")
				key = k
				payload = bytes.Clone(data)
				require.True(t, strings.HasPrefix(k, "fixture/generated/.donebot-connectivity/"))
				require.True(t, strings.HasSuffix(k, ".txt"))
				if stage == "write" {
					return "", errors.New(sensitive)
				}
				if stage == "missing bucket" {
					return "", &smithy.GenericAPIError{Code: "NoSuchBucket", Message: sensitive}
				}
				if stage == "oss denied" {
					return "", &oss.ServiceError{Code: "AccessDenied", Message: sensitive}
				}
				if stage == "cancel" {
					cancel()
					return "", ctx.Err()
				}
				return server.URL + "/download?Signature=synthetic-signature", nil
			}, func(ctx context.Context, k string) (io.ReadCloser, error) {
				calls = append(calls, "read")
				require.Equal(t, key, k)
				if stage == "read" {
					return nil, errors.New(sensitive)
				}
				if stage == "nil body" {
					return nil, nil
				}
				if stage == "mismatch" {
					return io.NopCloser(strings.NewReader("wrong content")), nil
				}
				return io.NopCloser(bytes.NewReader(payload)), nil
			}, func(ctx context.Context, k string) error {
				calls = append(calls, "delete")
				require.NoError(t, ctx.Err())
				require.Equal(t, key, k)
				if stage == "delete" {
					return errors.New(sensitive)
				}
				return nil
			})
			require.Equal(t, "delete", calls[len(calls)-1])
			if stage == "success" {
				require.NoError(t, err)
				require.Equal(t, []string{"write", "read", "delete"}, calls)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), sensitive)
				require.NotContains(t, err.Error(), "synthetic-signature")
				require.NotContains(t, err.Error(), server.URL)
			}
			if stage == "missing bucket" {
				require.Contains(t, err.Error(), "存储桶不存在")
			}
			if stage == "oss denied" {
				require.Contains(t, err.Error(), "访问被拒绝")
			}
			if stage == "delete" {
				require.Contains(t, err.Error(), key)
			}
		})
	}
}

func TestProbeObjectStorageDoesNotReuseTestObjectKeys(t *testing.T) {
	keys := map[string]bool{}
	for i := 0; i < 2; i++ {
		var payload []byte
		err := ProbeObjectStorage(context.Background(), "", func(_ context.Context, key string, data []byte) (string, error) {
			require.False(t, keys[key])
			keys[key] = true
			payload = bytes.Clone(data)
			return "", nil
		}, func(context.Context, string) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(payload)), nil
		}, func(context.Context, string) error { return nil })
		require.NoError(t, err)
	}
	require.Len(t, keys, 2)
}

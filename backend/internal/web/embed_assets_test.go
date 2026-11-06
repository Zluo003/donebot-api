//go:build embed

package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedFrontendBypassesGatewayAssetDownloads(t *testing.T) {
	for _, path := range []string{
		"/assets/ast_0123456789abcdef0123456789abcdef",
		"/assets/ast_ffffffffffffffffffffffffffffffff",
	} {
		require.True(t, shouldBypassEmbeddedFrontend(path), "path=%s", path)
	}
	for _, path := range []string{
		"/assets/ast_",
		"/assets/ast_0123456789abcdef0123456789abcde",
		"/assets/ast_0123456789abcdef0123456789abcdef0",
		"/assets/ast_0123456789abcdef0123456789abcdeg",
		"/assets/ast_0123456789ABCDEF0123456789ABCDEF",
		"/assets/ast_0123456789abcdef0123456789abcdef.js",
		"/assets/ast_0123456789abcdef0123456789abcdef/file",
		"/assets/ast_0123456789abcdef0123456789abcdef/",
		"/assets/index-AbCd1234.js",
		"/assets/index-AbCd1234.css",
		"/assets",
		"/assets-backup/ast_0123456789abcdef0123456789abcdef",
	} {
		require.False(t, shouldBypassEmbeddedFrontend(path), "path=%s", path)
	}
}

func TestEmbeddedFrontendServesSignedGatewayAssets(t *testing.T) {
	for _, frontend := range embeddedAssetMiddlewareFactories() {
		t.Run(frontend.name, func(t *testing.T) {
			for _, test := range []struct {
				name   string
				mutate func(*url.URL, *embeddedAssetRepository)
				status int
				code   string
			}{
				{name: "valid_signature", status: http.StatusOK},
				{name: "missing_signature", status: http.StatusForbidden, code: "ASSET_TOKEN_INVALID", mutate: func(u *url.URL, _ *embeddedAssetRepository) {
					u.RawQuery = ""
				}},
				{name: "forged_signature", status: http.StatusForbidden, code: "ASSET_TOKEN_INVALID", mutate: func(u *url.URL, _ *embeddedAssetRepository) {
					query := u.Query()
					query.Set("token", query.Get("token")+"tampered")
					u.RawQuery = query.Encode()
				}},
				{name: "expired_signature", status: http.StatusForbidden, code: "ASSET_TOKEN_INVALID", mutate: func(u *url.URL, _ *embeddedAssetRepository) {
					query := u.Query()
					_, signature, ok := strings.Cut(query.Get("token"), ".")
					require.True(t, ok)
					query.Set("token", "0."+signature)
					u.RawQuery = query.Encode()
				}},
				{name: "expired_asset", status: http.StatusForbidden, code: "ASSET_TOKEN_INVALID", mutate: func(_ *url.URL, repo *embeddedAssetRepository) {
					repo.asset.ExpiresAt = time.Now().Add(-time.Hour)
				}},
				{name: "signature_rebound_to_other_asset", status: http.StatusForbidden, code: "ASSET_TOKEN_INVALID", mutate: func(u *url.URL, _ *embeddedAssetRepository) {
					u.Path = "/assets/ast_ffffffffffffffffffffffffffffffff"
				}},
				{name: "removed_asset", status: http.StatusNotFound, code: "ASSET_NOT_FOUND", mutate: func(_ *url.URL, repo *embeddedAssetRepository) {
					repo.asset = nil
				}},
			} {
				t.Run(test.name, func(t *testing.T) {
					payload := []byte("\x89PNG\r\n\x1a\nfixture image")
					repo := &embeddedAssetRepository{asset: &service.GatewayAsset{
						ID:        "ast_0123456789abcdef0123456789abcdef",
						MIME:      "image/png",
						Size:      int64(len(payload)),
						Storage:   "local",
						ObjectKey: "generated/fixture.png",
						ExpiresAt: time.Now().Add(time.Hour),
					}}
					storage := &embeddedAssetStorage{data: payload}
					cfg := &config.Config{}
					cfg.JWT.Secret = "embedded-asset-fixture-secret"
					assets := service.NewAssetService(repo, storage, cfg)
					asset, err := assets.Get(context.Background(), 1, repo.asset.ID, "https://gateway.example")
					require.NoError(t, err)
					assetURL, err := url.Parse(asset.URL)
					require.NoError(t, err)
					if test.mutate != nil {
						test.mutate(assetURL, repo)
					}

					router := gin.New()
					router.Use(frontend.new(t))
					router.GET("/assets/:id", handler.NewAssetHandler(assets, cfg).Download)
					response := httptest.NewRecorder()
					router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, assetURL.RequestURI(), nil))

					require.Equal(t, test.status, response.Code)
					if test.status == http.StatusOK {
						require.Equal(t, "image/png", response.Header().Get("Content-Type"))
						require.Equal(t, payload, response.Body.Bytes())
						require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
						require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
						require.Equal(t, 1, storage.opens)
					} else {
						require.Contains(t, response.Header().Get("Content-Type"), "application/json")
						require.Contains(t, response.Body.String(), test.code)
						require.Zero(t, storage.opens)
					}
				})
			}
		})
	}
}

func TestEmbeddedFrontendPreservesBuildAssetsAndSPAFallback(t *testing.T) {
	entries, err := frontendFS.ReadDir("dist/assets")
	require.NoError(t, err)
	for _, frontend := range embeddedAssetMiddlewareFactories() {
		t.Run(frontend.name, func(t *testing.T) {
			router := gin.New()
			router.Use(frontend.new(t))
			router.GET("/assets/:id", func(c *gin.Context) {
				t.Error("frontend resource reached gateway asset handler")
				c.Status(http.StatusInternalServerError)
			})
			for _, extension := range []string{".js", ".css"} {
				t.Run(extension, func(t *testing.T) {
					assetPath := ""
					for _, entry := range entries {
						if !entry.IsDir() && strings.HasSuffix(entry.Name(), extension) {
							assetPath = "assets/" + entry.Name()
							break
						}
					}
					require.NotEmpty(t, assetPath)
					expected, err := frontendFS.ReadFile("dist/" + assetPath)
					require.NoError(t, err)
					response := httptest.NewRecorder()
					router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/"+assetPath, nil))
					require.Equal(t, http.StatusOK, response.Code)
					require.Equal(t, expected, response.Body.Bytes())
					require.NotContains(t, response.Header().Get("Content-Type"), "text/html")
					require.Equal(t, staticAssetsCacheControl, response.Header().Get("Cache-Control"))
				})
			}
			for _, path := range []string{"/dashboard", "/assets/missing-build-file.js", "/assets/ast_0123456789abcdef0123456789abcdef.js"} {
				t.Run(path, func(t *testing.T) {
					response := httptest.NewRecorder()
					router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
					require.Equal(t, http.StatusOK, response.Code)
					require.Contains(t, response.Header().Get("Content-Type"), "text/html")
					require.Contains(t, response.Body.String(), "<!doctype html>")
				})
			}
		})
	}
}

func embeddedAssetMiddlewareFactories() []struct {
	name string
	new  func(*testing.T) gin.HandlerFunc
} {
	return []struct {
		name string
		new  func(*testing.T) gin.HandlerFunc
	}{
		{name: "settings_frontend", new: func(t *testing.T) gin.HandlerFunc {
			server, err := NewFrontendServer(&mockSettingsProvider{settings: map[string]string{}})
			require.NoError(t, err)
			server.overrideDir = ""
			return server.Middleware()
		}},
		{name: "legacy_frontend", new: func(t *testing.T) gin.HandlerFunc { return ServeEmbeddedFrontend() }},
	}
}

type embeddedAssetRepository struct {
	service.AssetRepository
	asset *service.GatewayAsset
}

func (r *embeddedAssetRepository) GetByID(_ context.Context, id string) (*service.GatewayAsset, error) {
	if r.asset == nil || r.asset.ID != id {
		return nil, service.ErrAssetNotFound
	}
	return r.asset, nil
}

func (r *embeddedAssetRepository) HasOwner(context.Context, string, int64) (bool, error) {
	return true, nil
}

type embeddedAssetStorage struct {
	service.AssetStorage
	data  []byte
	opens int
}

func (*embeddedAssetStorage) Kind() string { return "local" }

func (s *embeddedAssetStorage) Open(context.Context, string) (io.ReadCloser, error) {
	s.opens++
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

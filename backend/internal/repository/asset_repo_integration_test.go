//go:build integration

package repository

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAssetRepositoryDeduplicatesConcurrentUploadsAndServesSignedLocalContent(t *testing.T) {
	ctx := context.Background()
	user := &service.User{Email: "assets-" + time.Now().Format("150405.000000000") + "@example.com", PasswordHash: "test", Role: service.RoleUser, Status: service.StatusActive}
	require.NoError(t, NewUserRepository(integrationEntClient, integrationDB).Create(ctx, user))
	key := &service.APIKey{UserID: user.ID, Key: "asset-integration-" + time.Now().Format("150405.000000000"), Name: "asset integration", Status: service.StatusActive}
	require.NoError(t, NewAPIKeyRepository(integrationEntClient, integrationDB).Create(ctx, key))
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`DELETE FROM assets WHERE owner_key_id=$1`, key.ID)
		_, _ = integrationDB.Exec(`DELETE FROM api_keys WHERE id=$1`, key.ID)
		_, _ = integrationDB.Exec(`DELETE FROM users WHERE id=$1`, user.ID)
	})
	cfg := &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}, AssetStorage: config.AssetStorageConfig{Driver: "local", LocalPath: t.TempDir(), TTLHours: 168, MaxBytes: 1024, PublicBaseURL: "https://assets.example.com"}}
	cfg.JWT.Secret = "asset-integration-signing-secret"
	svc := service.NewAssetService(NewAssetRepository(integrationEntClient), NewLocalAssetStorage(cfg.AssetStorage.LocalPath), cfg)
	const count = 4
	results := make([]*service.GatewayAsset, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.Upload(ctx, key.ID, "ref.png", "image/png", []byte("same-file"), "")
		}(i)
	}
	wg.Wait()
	firsts := 0
	for i := range errs {
		require.NoError(t, errs[i])
		require.Equal(t, results[0].ID, results[i].ID)
		if !results[i].Deduplicated {
			firsts++
		}
	}
	require.Equal(t, 1, firsts)
	var stored int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM assets WHERE owner_key_id=$1`, key.ID).Scan(&stored))
	require.Equal(t, 1, stored)
	downloadURL, err := url.Parse(results[0].URL)
	require.NoError(t, err)
	asset, reader, err := svc.Download(ctx, results[0].ID, downloadURL.Query().Get("token"))
	require.NoError(t, err)
	require.Equal(t, results[0].ID, asset.ID)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "same-file", string(body))
	h := handler.NewAssetHandler(svc, cfg)
	router := gin.New()
	router.GET("/assets/:id", h.Download)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, downloadURL.RequestURI(), nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "same-file", response.Body.String())
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/"+results[0].ID+"?token=0.bad", nil))
	require.Equal(t, http.StatusForbidden, response.Code)
	require.NoError(t, svc.Delete(ctx, key.ID, results[0].ID))
	_, _, err = svc.Download(ctx, results[0].ID, downloadURL.Query().Get("token"))
	require.ErrorIs(t, err, service.ErrAssetNotFound)
}

func TestAssetMigrationDownAndUpCanRunWithoutLeavingSchemaBehind(t *testing.T) {
	tx, err := integrationDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	down, err := os.ReadFile("../../migrations/down/251_assets.sql")
	require.NoError(t, err)
	script := strings.ReplaceAll(strings.ReplaceAll(string(down), "BEGIN;", ""), "COMMIT;", "")
	_, err = tx.Exec(script)
	require.NoError(t, err)
	var missing bool
	require.NoError(t, tx.QueryRow(`SELECT to_regclass('public.assets') IS NULL AND to_regclass('public.asset_owners') IS NULL`).Scan(&missing))
	require.True(t, missing)
	up, err := os.ReadFile("../../migrations/251_assets.sql")
	require.NoError(t, err)
	_, err = tx.Exec(string(up))
	require.NoError(t, err)
	var exists bool
	require.NoError(t, tx.QueryRow(`SELECT to_regclass('public.idx_assets_sha256') IS NOT NULL AND to_regclass('public.idx_assets_expires_at') IS NOT NULL AND to_regclass('public.asset_owners') IS NOT NULL`).Scan(&exists))
	require.True(t, exists)
}

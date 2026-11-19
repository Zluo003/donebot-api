//go:build unit

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type installerFixtureSettings struct {
	service.SettingRepository
	values map[string]string
}

func (s *installerFixtureSettings) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (s *installerFixtureSettings) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func TestClientInstallerLargeUploadStorageAndDownloadFixture(t *testing.T) {
	root := t.TempDir()
	repo := &installerFixtureSettings{values: map[string]string{
		"donebot_client_release_storage": `{"driver":"local","local_path":` + string(mustInstallerFixtureJSON(t, filepath.Join(root, "packages"))) + `}`,
	}}
	storage := service.NewDoneBotStorageService(repo, nil, repository.ProvideDoneBotStorageFactory(), nil, nil)
	h := admin.ProvideDoneBotHandler(nil, nil, storage, nil, nil, nil, nil, nil)
	router := gin.New()
	uploadPath := "/api/v1/admin/donebot/releases/upload"
	router.POST(uploadPath, func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer fixture-admin" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if c.GetHeader("X-Fixture-Step-Up") != "verified" {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}, h.UploadClientRelease)
	router.POST("/publish/:version", h.PublishClientRelease)
	router.GET("/api/v1/client/releases/:version/:platform/:filename", h.DownloadClientRelease)
	cfg := ingressTestConfig()
	cfg.Server.MaxRequestBodySize = 256 << 20
	srv := httptest.NewServer(ProvideHTTPServer(cfg, router).Handler)
	defer srv.Close()

	for _, tc := range []struct {
		auth, stepUp string
		status       int
	}{{"", "", http.StatusUnauthorized}, {"Bearer fixture-admin", "", http.StatusForbidden}} {
		req, err := http.NewRequest(http.MethodPost, srv.URL+uploadPath, strings.NewReader("fixture"))
		require.NoError(t, err)
		req.Header.Set("Authorization", tc.auth)
		req.Header.Set("X-Fixture-Step-Up", tc.stepUp)
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, tc.status, resp.StatusCode)
	}
	require.NotContains(t, repo.values, "donebot_client_releases")

	for _, item := range []struct {
		platform, filename string
		size               int64
	}{
		{service.ClientReleaseMacOSArm64DMG, "DoneBot-0.1.0-arm64.dmg", 304 << 20},
		{service.ClientReleaseWindowsX64, "DoneBot-0.1.0-setup.exe", 1024},
		{service.ClientReleaseMacOSArm64, "DoneBot-0.1.0-arm64.zip", 1024},
	} {
		file, err := os.CreateTemp(root, "installer-*")
		require.NoError(t, err)
		require.NoError(t, file.Truncate(item.size))
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField("version", "0.1.0"))
		require.NoError(t, writer.WriteField("platform", item.platform))
		_, err = writer.CreateFormFile("file", item.filename)
		require.NoError(t, err)
		header := append([]byte(nil), body.Bytes()...)
		require.NoError(t, writer.Close())
		footer := body.Bytes()[len(header):]
		req, err := http.NewRequest(http.MethodPost, srv.URL+uploadPath, io.MultiReader(bytes.NewReader(header), file, bytes.NewReader(footer)))
		require.NoError(t, err)
		req.ContentLength = int64(len(header)+len(footer)) + item.size
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", "Bearer fixture-admin")
		req.Header.Set("X-Fixture-Step-Up", "verified")
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		responseBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode, string(responseBody))
		var envelope struct {
			Data service.ClientRelease `json:"data"`
		}
		require.NoError(t, json.Unmarshal(responseBody, &envelope))
		artifact := envelope.Data.Platforms[item.platform]
		require.Equal(t, item.size, artifact.SizeBytes)
		expectedHash := sha256.New()
		_, err = file.Seek(0, io.SeekStart)
		require.NoError(t, err)
		_, err = io.Copy(expectedHash, file)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		require.NoError(t, os.Remove(file.Name()))
		require.Equal(t, hex.EncodeToString(expectedHash.Sum(nil)), artifact.SHA256)
	}

	resp, err := srv.Client().Post(srv.URL+"/publish/0.1.0", "application/json", nil)
	require.NoError(t, err)
	var published struct {
		Data service.ClientRelease `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&published))
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	for _, artifact := range published.Data.Platforms {
		resp, err := srv.Client().Get(srv.URL + artifact.DownloadURL)
		require.NoError(t, err)
		hash := sha256.New()
		size, err := io.Copy(hash, resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, artifact.SizeBytes, size)
		require.Equal(t, artifact.SHA256, hex.EncodeToString(hash.Sum(nil)))
	}
}

func mustInstallerFixtureJSON(t *testing.T, value string) []byte {
	t.Helper()
	out, err := json.Marshal(value)
	require.NoError(t, err)
	return out
}

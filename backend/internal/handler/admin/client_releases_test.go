package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type releaseHTTPSettings struct {
	service.SettingRepository
	values map[string]string
}

func (s *releaseHTTPSettings) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}
func (s *releaseHTTPSettings) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func TestClientReleaseHTTPFixtureStoragePublishAndDownload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := &releaseHTTPSettings{values: map[string]string{"donebot_storage_policies": `{"upload":{"driver":"s3","s3":{"bucket":"media-only"}}}`}}
	storage := service.NewDoneBotStorageService(settings, nil, func(_ context.Context, p service.DoneBotStoragePolicy) (service.AssetStorage, error) {
		return repository.NewLocalAssetStorage(p.LocalPath), nil
	}, nil, nil)
	h := &DoneBotHandler{releases: service.NewClientReleaseService(storage)}
	r := gin.New()
	r.GET("/storage", h.ClientReleaseStorage)
	r.PUT("/storage", h.UpdateClientReleaseStorage)
	r.POST("/upload", h.UploadClientRelease)
	r.POST("/publish/:version", h.PublishClientRelease)
	r.GET("/latest", h.LatestClientRelease)
	r.GET("/check", h.CheckClientRelease)
	r.GET("/api/v1/client/releases/:version/:platform/:filename", h.DownloadClientRelease)
	request := func(method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, body)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	saveStorage := func(path string) {
		t.Helper()
		body, _ := json.Marshal(service.ClientReleaseStorageSettings{Driver: "local", LocalPath: path})
		w := request("PUT", "/storage", bytes.NewReader(body), "application/json")
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
	firstDir := t.TempDir()
	saveStorage(firstDir)
	payload := []byte("fixture installer bytes")
	for _, item := range []struct{ platform, filename string }{{"windows-x64", "DoneBot.exe"}, {"macos-arm64-dmg", "DoneBot.dmg"}, {"macos-arm64", "DoneBot.zip"}} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField("version", "9.8.7"))
		require.NoError(t, writer.WriteField("platform", item.platform))
		file, err := writer.CreateFormFile("file", item.filename)
		require.NoError(t, err)
		_, err = file.Write(payload)
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		w := request("POST", "/upload", &body, writer.FormDataContentType())
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	w := request("POST", "/publish/9.8.7", nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	w = request("GET", "/latest", nil, "")
	require.Equal(t, 200, w.Code)
	var latest struct {
		Data service.ClientRelease `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &latest))
	require.Contains(t, latest.Data.Platforms["macos-arm64-dmg"].DownloadURL, "DoneBot.dmg")
	w = request("GET", "/check?version=9.8.6&platform=macos-arm64", nil, "")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "DoneBot.zip")
	saveStorage(t.TempDir())
	for _, platform := range []string{"windows-x64", "macos-arm64-dmg", "macos-arm64"} {
		w = request("GET", latest.Data.Platforms[platform].DownloadURL, nil, "")
		require.Equal(t, 200, w.Code)
		require.Equal(t, payload, w.Body.Bytes())
	}
	require.Contains(t, settings.values["donebot_storage_policies"], "media-only")
}

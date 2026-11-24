package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMobileReleaseHTTPWorkflow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := &releaseHTTPSettings{values: map[string]string{}}
	stores := map[string]*releaseHTTPStorage{}
	storage := service.NewDoneBotStorageService(settings, nil, func(_ context.Context, p service.DoneBotStoragePolicy) (service.AssetStorage, error) {
		if stores[p.LocalPath] == nil {
			stores[p.LocalPath] = &releaseHTTPStorage{objects: map[string][]byte{}}
		}
		return stores[p.LocalPath], nil
	}, nil, nil)
	h := &DoneBotHandler{releases: service.NewClientReleaseService(storage)}
	r := gin.New()
	r.PUT("/storage", h.UpdateClientReleaseStorage)
	r.GET("/mobile-releases", h.MobileReleases)
	r.POST("/mobile-releases/upload", h.UploadMobileRelease)
	r.POST("/mobile-releases/:version/publish", h.PublishMobileRelease)
	r.DELETE("/mobile-releases/:version", h.DeleteMobileRelease)
	r.GET("/api/v1/client/mobile-releases/latest", h.LatestMobileRelease)
	r.GET("/api/v1/client/mobile-releases/check", h.CheckMobileRelease)
	r.GET("/api/v1/client/mobile-releases/:version/:platform/:filename", h.DownloadMobileRelease)

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

	dir := t.TempDir()
	storageBody, _ := json.Marshal(service.ClientReleaseStorageSettings{Driver: "local", LocalPath: dir})
	require.Equal(t, 200, request("PUT", "/storage", bytes.NewReader(storageBody), "application/json").Code)

	payload := append([]byte{'P', 'K', 0x03, 0x04}, []byte("fixture-android-apk-content")...)
	upload := func(version, versionCode, forceUpdate string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField("version", version))
		require.NoError(t, writer.WriteField("version_code", versionCode))
		require.NoError(t, writer.WriteField("platform", service.MobileReleaseAndroidAPK))
		require.NoError(t, writer.WriteField("notes", "Android 首发"))
		require.NoError(t, writer.WriteField("force_update", forceUpdate))
		part, err := writer.CreateFormFile("file", "DoneBot-"+version+"-android.apk")
		require.NoError(t, err)
		_, err = part.Write(payload)
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		return request("POST", "/mobile-releases/upload", &body, writer.FormDataContentType())
	}

	w := upload("0.1.0", "1", "false")
	require.Equal(t, 200, w.Code, w.Body.String())

	// Unpublished draft is not visible on latest
	require.Equal(t, 404, request("GET", "/api/v1/client/mobile-releases/latest", nil, "").Code)

	// Publish v0.1.0
	w = request("POST", "/mobile-releases/0.1.0/publish", nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())

	// Latest returns artifact with download URL
	w = request("GET", "/api/v1/client/mobile-releases/latest", nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var latest struct {
		Data service.MobileRelease `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &latest))
	require.Equal(t, "0.1.0", latest.Data.Version)
	require.Equal(t, 1, latest.Data.VersionCode)
	apk := latest.Data.Platforms[service.MobileReleaseAndroidAPK]
	require.Equal(t, "/api/v1/client/mobile-releases/0.1.0/android-apk/DoneBot-0.1.0-android.apk", apk.DownloadURL)

	// Download APK returns Android APK MIME type and checksum header
	w = request("GET", apk.DownloadURL, nil, "")
	require.Equal(t, 200, w.Code)
	require.Equal(t, "application/vnd.android.package-archive", w.Header().Get("Content-Type"))
	require.Equal(t, apk.SHA256, w.Header().Get("X-Checksum-Sha256"))
	require.Equal(t, payload, w.Body.Bytes())

	// Check update from older version returns update_available=true
	w = request("GET", "/api/v1/client/mobile-releases/check?version=0.0.9&version_code=0&platform=android-apk", nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"update_available":true`)
	require.Contains(t, w.Body.String(), `"latest_code":1`)

	// Upload and publish v0.2.0, then delete v0.1.0
	require.Equal(t, 200, upload("0.2.0", "2", "true").Code)
	require.Equal(t, 200, request("POST", "/mobile-releases/0.2.0/publish", nil, "").Code)
	require.Equal(t, 409, request("DELETE", "/mobile-releases/0.2.0", nil, "").Code)
	require.Equal(t, 200, request("DELETE", "/mobile-releases/0.1.0", nil, "").Code)
	require.Equal(t, 404, request("GET", apk.DownloadURL, nil, "").Code)
}

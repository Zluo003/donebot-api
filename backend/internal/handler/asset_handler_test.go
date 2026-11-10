package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type assetHandlerStub struct {
	calls int
	owner int64
	data  []byte
}

type doneBotArtifactOpenerStub struct {
	calls      int
	taskID     string
	artifactID string
	asset      *service.GatewayAsset
	reader     io.ReadCloser
	err        error
}

func (s *doneBotArtifactOpenerStub) Open(_ context.Context, taskID, artifactID string) (*service.GatewayAsset, io.ReadCloser, error) {
	s.calls++
	s.taskID, s.artifactID = taskID, artifactID
	return s.asset, s.reader, s.err
}

func (s *assetHandlerStub) Upload(_ context.Context, owner int64, _, _ string, data []byte, _ string) (*service.GatewayAsset, error) {
	s.calls++
	s.owner = owner
	s.data = data
	return &service.GatewayAsset{ID: "ast_test", URL: "https://assets.example.com/assets/ast_test"}, nil
}
func (*assetHandlerStub) Get(context.Context, int64, string, string) (*service.GatewayAsset, error) {
	return nil, service.ErrAssetNotFound
}
func (*assetHandlerStub) Delete(context.Context, int64, string) error { return nil }
func (*assetHandlerStub) Download(context.Context, string, string) (*service.GatewayAsset, io.ReadCloser, error) {
	return nil, nil, service.ErrAssetTokenInvalid
}
func TestAssetHandlerUploadUsesCredentialOwnerAndRejectsOversizedWithoutStorage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &assetHandlerStub{}
	cfg := &config.Config{AssetStorage: config.AssetStorageConfig{MaxBytes: 3}}
	h := &AssetHandler{assets: stub, cfg: cfg}
	router := gin.New()
	router.POST("/v1/assets", func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 8}); c.Next() }, h.Upload)
	request := func(content string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		file, err := writer.CreateFormFile("file", "ref.png")
		require.NoError(t, err)
		_, _ = file.Write([]byte(content))
		require.NoError(t, writer.Close())
		req := httptest.NewRequest(http.MethodPost, "/v1/assets", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	response := request("abc")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, int64(8), stub.owner)
	require.Equal(t, "abc", string(stub.data))
	var result map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, "ast_test", result["id"])
	response = request("abcd")
	require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	require.Equal(t, 1, stub.calls)
}

func TestAssetHandlerDownloadsDoneBotArtifactWithoutCredentialsOrQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reader := io.NopCloser(bytes.NewReader([]byte("artifact-bytes")))
	opener := &doneBotArtifactOpenerStub{asset: &service.GatewayAsset{ID: "ast_private", MIME: "image/png", Size: 14}, reader: reader}
	h := &AssetHandler{artifacts: opener}
	router := gin.New()
	router.GET("/v1/donebot/media/jobs/:job_id/artifacts/:artifact_id", h.DownloadDoneBotArtifact)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/donebot/media/jobs/dbtask_opaque/artifacts/artifact_opaque", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "artifact-bytes", response.Body.String())
	require.Equal(t, "dbtask_opaque", opener.taskID)
	require.Equal(t, "artifact_opaque", opener.artifactID)

	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/donebot/media/jobs/dbtask_opaque/artifacts/artifact_opaque?token=signature", nil))
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Equal(t, 1, opener.calls, "query parameters must be rejected before opening an artifact")
}

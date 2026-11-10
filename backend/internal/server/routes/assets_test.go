package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDoneBotArtifactRouteUsesOpaquePathWithoutAPIKeyOrQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	apiKeyCalls := 0
	auth := middleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		apiKeyCalls++
		c.AbortWithStatus(http.StatusUnauthorized)
	})
	handlers := &handler.Handlers{Asset: handler.NewAssetHandler(nil, &config.Config{})}
	registerAssetRoutes(router, handlers, auth)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/donebot/media/jobs/job-random/artifacts/artifact-random", nil))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Zero(t, apiKeyCalls)

	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/donebot/media/jobs/job-random/artifacts/artifact-random?token=signature", nil))
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Zero(t, apiKeyCalls)
}

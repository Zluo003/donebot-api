package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDoneBotMediaRoutesReachAuthenticationWithLegacySwitchesDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterGatewayRoutes(router, &handler.Handlers{
		Gateway:       &handler.GatewayHandler{},
		OpenAIGateway: &handler.OpenAIGatewayHandler{},
		AsyncImage:    handler.NewAsyncImageHandler(nil, nil),
	}, middleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		c.AbortWithStatus(http.StatusUnauthorized)
	}), nil, nil, nil, nil, nil, &config.Config{
		DoneBot: config.DoneBotConfig{Enabled: true, Features: config.DoneBotFeaturesConfig{Audio: false, Video: false}},
		Gateway: config.GatewayConfig{MaxBodySize: 1024 * 1024, TextMaxBodySize: 1024 * 1024},
	})
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/v1/videos"},
		{http.MethodGet, "/v1/videos/task-1"},
		{http.MethodGet, "/v1/videos/task-1/content"},
		{http.MethodPost, "/videos"},
		{http.MethodGet, "/videos/task-1/content"},
		{http.MethodPost, "/api/v3/contents/generations/tasks"},
		{http.MethodPost, "/v3/contents/generations/tasks"},
		{http.MethodPost, "/v1/contents/generations/tasks"},
		{http.MethodPost, "/contents/generations/tasks"},
		{http.MethodGet, "/api/v3/contents/generations/tasks/task-1"},
		{http.MethodGet, "/v1/donebot/media/jobs/task-1"},
		{http.MethodGet, "/v1/donebot/media/jobs/by-idempotency/key-1"},
		{http.MethodPost, "/v1/music/generations"},
		{http.MethodGet, "/v1/music/tasks/task-1"},
		{http.MethodPost, "/v1/audio/speech"},
		{http.MethodPost, "/v1/tts"},
		{http.MethodPost, "/tts"},
		{http.MethodPost, "/v1/live"},
		{http.MethodPost, "/backend-api/codex/realtime/calls"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			request := httptest.NewRequest(route.method, route.path, strings.NewReader(`{"model":"fixture"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
		})
	}
}

func TestDoneBotGatewayDoesNotExposeIntentPlanningRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterGatewayRoutes(router, &handler.Handlers{Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{}},
		middleware.APIKeyAuthMiddleware(func(c *gin.Context) { c.Next() }), nil, nil, nil, nil, nil, &config.Config{})
	for _, route := range router.Routes() {
		require.NotContains(t, route.Path, "/donebot/media/intent")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/donebot/media/intent", strings.NewReader(`{"intent":{"kind":"image"}}`)))
	require.Equal(t, http.StatusNotFound, recorder.Code)
}

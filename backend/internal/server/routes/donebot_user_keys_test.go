package routes

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUserRoutesHideOrdinaryAPIKeysInDoneBotMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &handler.Handlers{DoneBot: handler.NewDoneBotHandler(nil, nil, &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}})}
	RegisterUserRoutes(router.Group("/api/v1"), h, middleware.JWTAuthMiddleware(mcpRouteNext), middleware.AuditLogMiddleware(mcpRouteNext), nil, nil, &config.Config{DoneBot: config.DoneBotConfig{Enabled: true}})

	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	require.True(t, registered["POST /api/v1/donebot/installations/claim"])
	for _, route := range []string{
		"GET /api/v1/keys",
		"POST /api/v1/keys",
		"GET /api/v1/keys/:id",
		"PUT /api/v1/keys/:id",
		"DELETE /api/v1/keys/:id",
		"GET /api/v1/user/api-keys/:id/usage/daily",
	} {
		require.False(t, registered[route], "ordinary API key route must be hidden in DoneBot mode: %s", route)
	}
}

func TestUserRoutesKeepOrdinaryAPIKeysOutsideDoneBotMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterUserRoutes(router.Group("/api/v1"), &handler.Handlers{APIKey: handler.NewAPIKeyHandler(service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil))}, middleware.JWTAuthMiddleware(mcpRouteNext), middleware.AuditLogMiddleware(mcpRouteNext), nil, nil)

	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	require.True(t, registered["GET /api/v1/keys"])
	require.True(t, registered["POST /api/v1/keys"])
}

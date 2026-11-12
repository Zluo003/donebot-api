package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestCloudNodeAndUserRoutesHaveSeparateAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &handler.Handlers{DoneBot: &handler.DoneBotHandler{}}
	rejectJWT := middleware.JWTAuthMiddleware(func(c *gin.Context) { c.AbortWithStatus(401) })
	RegisterAuthRoutes(r.Group("/api/v1"), h, rejectJWT, middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }), nil, nil, nil)
	RegisterUserRoutes(r.Group("/api/v1"), h, rejectJWT, middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }), nil, nil)
	for _, path := range []string{"/api/v1/donebot/cloud/instances", "/api/v1/donebot/cloud/instances/any/stop", "/api/v1/donebot/cloud/billing/instances/any/purchase"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", path, nil)
		r.ServeHTTP(w, req)
		require.Equal(t, 401, w.Code, path)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/donebot/cloud/nodes/any/claim", nil))
	require.Equal(t, 503, w.Code)
}

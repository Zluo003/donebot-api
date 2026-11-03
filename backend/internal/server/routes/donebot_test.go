package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDoneBotRoutesInheritAuthAndAdminRevokeStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &handler.Handlers{DoneBot: &handler.DoneBotHandler{}, Admin: &handler.AdminHandlers{DoneBot: &admin.DoneBotHandler{}}}
	r := gin.New()
	user := r.Group("/api/v1")
	user.Use(func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
	registerDoneBotUserRoutes(user, h)
	adminGroup := r.Group("/api/v1/admin")
	var auditCalls int
	adminGroup.Use(func(c *gin.Context) { auditCalls++; c.Next() })
	stepUp := middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.AbortWithStatus(http.StatusForbidden) })
	registerDoneBotAdminRoutes(adminGroup, h, stepUp)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodPost, "/api/v1/donebot/installations/claim", http.StatusUnauthorized},
		{http.MethodGet, "/api/v1/donebot/installations", http.StatusUnauthorized},
		{http.MethodGet, "/api/v1/donebot/models", http.StatusUnauthorized},
		{http.MethodGet, "/api/v1/donebot/account", http.StatusUnauthorized},
		{http.MethodGet, "/api/v1/donebot/receipts/receipt", http.StatusUnauthorized},
		{http.MethodPost, "/api/v1/donebot/installations/a35372c0-39ba-41c3-b6b3-632032ba7b04/rotate", http.StatusUnauthorized},
		{http.MethodDelete, "/api/v1/donebot/installations/a35372c0-39ba-41c3-b6b3-632032ba7b04", http.StatusUnauthorized},
		{http.MethodDelete, "/api/v1/admin/donebot/installs/12", http.StatusForbidden},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		require.Equal(t, tc.status, w.Code)
	}
	require.Equal(t, 1, auditCalls)
	var listPresent bool
	for _, route := range r.Routes() {
		if route.Method == http.MethodGet && route.Path == "/api/v1/admin/donebot/installs" {
			listPresent = true
		}
	}
	require.True(t, listPresent)
}

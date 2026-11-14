package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDoneBotAdminWritesRequireStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &handler.Handlers{Admin: &handler.AdminHandlers{DoneBot: adminhandler.NewDoneBotHandler(nil, nil)}}
	calls := 0
	stepUp := middleware.StepUpAuthMiddleware(func(c *gin.Context) { calls++; c.AbortWithStatus(http.StatusForbidden) })
	registerDoneBotAdminRoutes(router.Group("/api/v1/admin"), h, stepUp)
	for _, route := range []struct{ method, path string }{
		{http.MethodPut, "/llm/models"}, {http.MethodPut, "/llm/pricing"},
		{http.MethodPost, "/llm/models/sync"}, {http.MethodPut, "/images/models"},
		{http.MethodPut, "/images/pricing"}, {http.MethodPost, "/images/models/sync"},
		{http.MethodDelete, "/installs/7"},
		{http.MethodPost, "/cloud/nodes"}, {http.MethodPut, "/cloud/nodes/fixture"},
		{http.MethodDelete, "/cloud/nodes/fixture"}, {http.MethodPost, "/cloud/nodes/fixture/credential"},
		{http.MethodPut, "/web-tools"},
		{http.MethodPut, "/cloud/runtime-policy"},
		{http.MethodPut, "/audio/models"}, {http.MethodPut, "/audio/pricing"},
		{http.MethodPost, "/audio/models/sync"},
		{http.MethodPut, "/llm/configuration"}, {http.MethodPut, "/images/configuration"},
		{http.MethodPut, "/video/configuration"}, {http.MethodPut, "/audio/configuration"},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			before := calls
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(route.method, "/api/v1/admin/donebot"+route.path, nil))
			require.Equal(t, http.StatusForbidden, w.Code)
			require.Equal(t, before+1, calls)
		})
	}
}

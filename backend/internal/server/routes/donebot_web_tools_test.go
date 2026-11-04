package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDoneBotWebToolsRoutesInheritAPIKeyAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, status := range []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusTooManyRequests} {
		r := gin.New()
		gateway := r.Group("/v1", func(c *gin.Context) { c.AbortWithStatus(status) })
		registerDoneBotWebToolsRoutes(gateway, &handler.DoneBotHandler{})
		for _, action := range []string{"search", "fetch"} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/tools/"+action, strings.NewReader(`{}`)))
			require.Equal(t, status, w.Code)
			require.NotContains(t, w.Body.String(), "donebot_web_upstream_unavailable")
		}
	}
}

func TestDoneBotWebToolsGatewayRoutesSkipModelSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterGatewayRoutes(r, &handler.Handlers{DoneBot: &handler.DoneBotHandler{}, Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{}, AsyncImage: handler.NewAsyncImageHandler(nil, nil)},
		middleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			id := int64(9)
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{Key: "sk-donebot-fixture", UserID: 7, ID: 17, GroupID: &id, Group: &service.Group{ID: id, Platform: service.PlatformComposite, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"allowed"}}}})
		}), nil, nil, nil, nil, nil, &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1 << 20, TextMaxBodySize: 1 << 20}})
	for _, action := range []string{"search", "fetch"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/tools/"+action, strings.NewReader(`{"model":"forbidden"}`)))
		require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "donebot_web_upstream_unavailable")
	}
}

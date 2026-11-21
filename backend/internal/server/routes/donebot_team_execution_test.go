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

func TestTeamModelGatewayRoutesFailClosedBeforeAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterGatewayRoutes(r, &handler.Handlers{Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{}, AsyncImage: handler.NewAsyncImageHandler(nil, nil), DoneBot: &handler.DoneBotHandler{}}, middleware.APIKeyAuthMiddleware(func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) }), nil, nil, nil, nil, nil, &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1024, TextMaxBodySize: 1024}})
	for _, path := range []string{"/v1/messages", "/v1/responses", "/v1/chat/completions", "/v1beta/models/gemini-3.8-flash:streamGenerateContent?alt=sse"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"fixture"}`))
		request.Header.Set("Authorization", "Bearer dbte_"+strings.Repeat("a", 43))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, request)
		require.Equal(t, http.StatusServiceUnavailable, w.Code, path)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
}

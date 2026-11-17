package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// CodingCredential switches to the user's native key after installation authentication.
// The same standard authenticator checks the derived key's status, ACL, quotas and group.
func (h *DoneBotHandler) CodingCredential(auth middleware.APIKeyAuthMiddleware) gin.HandlerFunc {
	return func(c *gin.Context) {
		engine := c.GetHeader("X-DoneBot-Coding-Engine")
		if engine == "" {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		allowed := c.Request.Method == http.MethodGet && path == "/v1/models" || c.Request.Method == http.MethodPost &&
			(engine == "codex" && path == "/v1/responses" || engine == "claude" && (path == "/v1/messages" || path == "/v1/messages/count_tokens"))
		parent, ok := middleware.GetAPIKeyFromContext(c)
		if !allowed || (engine != "codex" && engine != "claude") || !ok || parent == nil || !strings.HasPrefix(parent.Key, "sk-donebot-") {
			response.Forbidden(c, "A valid DoneBot installation and native coding endpoint are required")
			c.Abort()
			return
		}
		provider, ok := h.installs.(interface {
			CodingCredential(context.Context, *service.APIKey, string, string) (*service.APIKey, error)
		})
		if !ok {
			response.Error(c, 503, "Native coding groups are unavailable")
			c.Abort()
			return
		}
		key, err := provider.CodingCredential(c.Request.Context(), parent, c.GetHeader("X-DoneBot-Installation"), engine)
		if response.ErrorFrom(c, err) {
			c.Abort()
			return
		}
		for _, header := range []string{"X-API-Key", "X-Goog-API-Key", "X-DoneBot-Coding-Engine"} {
			c.Request.Header.Del(header)
		}
		c.Request.Header.Set("Authorization", "Bearer "+key.Key)
		c.Header("X-DoneBot-Coding-Engine", engine)
		c.Set("donebot_coding_discovery", c.Request.Method == http.MethodGet)
		auth(c)
	}
}

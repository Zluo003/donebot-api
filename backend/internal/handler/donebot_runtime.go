package handler

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// RuntimeInstallation permits only the explicitly registered installation read routes.
func (h *DoneBotHandler) RuntimeInstallation() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || key == nil || !strings.HasPrefix(key.Key, "sk-donebot-") || key.GroupID == nil {
			response.Forbidden(c, "A DoneBot installation key is required")
			c.Abort()
			return
		}
		reader, ok := h.installs.(interface {
			InstallationForAPIKey(context.Context, int64, int64, int64) (*service.DoneBotInstall, error)
		})
		if !ok {
			response.Error(c, 503, "Installation validation unavailable")
			c.Abort()
			return
		}
		install, err := reader.InstallationForAPIKey(c.Request.Context(), key.UserID, key.ID, *key.GroupID)
		if response.ErrorFrom(c, err) {
			c.Abort()
			return
		}
		if install == nil || install.UserID != key.UserID || install.APIKeyID != key.ID || install.GroupID != *key.GroupID {
			response.Forbidden(c, "Installation does not match the authenticated key")
			c.Abort()
			return
		}
		if requested := c.GetHeader("X-DoneBot-Installation"); requested != "" && requested != install.InstallationID {
			response.Forbidden(c, "Installation does not match the authenticated key")
			c.Abort()
			return
		}
		c.Request.Header.Set("X-DoneBot-Installation", install.InstallationID)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID})
		c.Next()
	}
}

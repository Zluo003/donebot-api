package admin

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type doneBotWebToolsSettings interface {
	Get(context.Context) (*service.DoneBotWebToolsSettings, error)
	Update(context.Context, service.DoneBotWebToolsSettingsInput) (*service.DoneBotWebToolsSettings, error)
}

func (h *DoneBotHandler) WebTools(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.webTools == nil {
		response.Error(c, 503, "Web tools settings are unavailable")
		return
	}
	settings, err := h.webTools.Get(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, settings)
	}
}

func (h *DoneBotHandler) UpdateWebTools(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.webTools == nil {
		response.Error(c, 503, "Web tools settings are unavailable")
		return
	}
	var input service.DoneBotWebToolsSettingsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid web tools settings")
		return
	}
	settings, err := h.webTools.Update(c.Request.Context(), input)
	if !response.ErrorFrom(c, err) {
		response.Success(c, settings)
	}
}

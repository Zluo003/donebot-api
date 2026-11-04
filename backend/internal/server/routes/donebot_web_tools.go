package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerDoneBotWebToolsRoutes(gateway *gin.RouterGroup, h *handler.DoneBotHandler) {
	if h == nil {
		return
	}
	gateway.POST("/tools/search", h.WebSearch)
	gateway.POST("/tools/fetch", h.WebFetch)
}
